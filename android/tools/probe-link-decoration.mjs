#!/usr/bin/env bun
// Original CSS + actual MarkdownMessage: record Blink decoration operations, not a painted imitation.
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import {renderMarkdown} from './web-markdown-reference.mjs';
import {linkDecorationCases} from './link-decoration-fixtures.mjs';
import {measureTextOrigins} from './text-origin-reference.mjs';
import {measureTableOrigins} from './table-origin-reference.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
if(!path.resolve(process.env.TMPDIR||'').startsWith(path.join(root,'build/tmp')+path.sep))throw new Error('Run through android/tools/run.sh');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__decoration__.cjs'));
const {chromium}=require('playwright');
const output=path.join(root,'build/artifacts/android-native'),fonts=path.join(root,'build/cache/android-ui-fonts');
const manifest=JSON.parse(await fs.readFile(path.join(fonts,'manifest.json'),'utf8'));
let fontCSS='';
for(const face of [...manifest.faces.map(face=>({...face,family:'AndroidRoboto'})),...manifest.cjk]) {
 const bytes=await fs.readFile(path.join(fonts,face.file));
 fontCSS+=`@font-face{font-family:${face.family};src:url(data:font/ttf;base64,${bytes.toString('base64')});font-weight:${face.weight||400};font-style:${face.italic?'italic':'normal'}}`;
}
const mono=await fs.readFile(path.join(fonts,manifest.mono.file));
fontCSS+=`@font-face{font-family:AndroidMono;src:url(data:font/ttf;base64,${mono.toString('base64')})}:root{font-family:AndroidRoboto,AndroidCjkSC,sans-serif}:lang(ja){font-family:AndroidRoboto,AndroidCjkJP,sans-serif}.markdown code{font-family:AndroidMono,monospace}`;
const css=await fs.readFile(path.join(root,'web/src/style.css'),'utf8');
const rows=linkDecorationCases.map(item=>`<div data-case="${item.id}" lang="${item.locale||'en'}" class="assistant-text" style="margin-bottom:14px">${renderMarkdown(item.text,{localFiles:true})}</div>`).join('');
let browser;
try {
 browser=await chromium.launch({channel:'chrome',headless:true});
 const cases=[];let tableMetrics;
 for(const density of [1,2.625,3])for(const theme of ['light','dark']) {
  const context=await browser.newContext({viewport:{width:360,height:1200},deviceScaleFactor:density});
  try {
   await context.route('**/*',route=>route.abort());
   const page=await context.newPage(),client=await context.newCDPSession(page);let layers=[];
   client.on('LayerTree.layerTreeDidChange',event=>{if(event.layers)layers=event.layers;});await client.send('LayerTree.enable');
   await page.setContent(`<!doctype html><html data-theme="${theme}"><style>${css}</style><style>${fontCSS}#root{padding:25px 18px;height:1200px;background:var(--elevated)}</style><div id="root">${rows}</div></html>`);
   await page.evaluate(()=>document.fonts.ready);
   const geometry=await page.evaluate(()=>[...document.querySelectorAll('[data-case]')].map(row=>{
    const paragraph=row.querySelector('p,h1,h2,h3'),r=row.getBoundingClientRect(),style=getComputedStyle(paragraph);
    const bounds=node=>{const r=node.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height};};
    const links=[...row.querySelectorAll('a,.local-file-link')].map(link=>{
     const s=getComputedStyle(link),range=document.createRange();range.selectNodeContents(link);
     return {text:link.textContent,...bounds(link),font:s.font,color:s.color,offset:s.textUnderlineOffset,thickness:s.textDecorationThickness,skipInk:s.textDecorationSkipInk,rects:[...range.getClientRects()].map(r=>({x:r.x,y:r.y,width:r.width,height:r.height}))};
    });
    const marker=document.createElement('span');marker.style.cssText='display:inline-block;width:0;height:0;vertical-align:baseline';paragraph.prepend(marker);
    const baseline=marker.getBoundingClientRect().bottom;marker.remove();
    const c=document.createElement('canvas').getContext('2d');c.font=style.font;c.letterSpacing=style.letterSpacing;
    const metrics=c.measureText(paragraph.textContent);
    const positions=Array.from({length:paragraph.textContent.length+1},(_,end)=>c.measureText(paragraph.textContent.slice(0,end)).width);
    return {id:row.dataset.case,locale:row.lang,text:paragraph.textContent,renderedText:paragraph.innerText,x:r.x,y:r.y,width:r.width,height:r.height,font:style.font,
     fontSize:parseFloat(style.fontSize),fontWeight:Number(style.fontWeight),lineHeight:parseFloat(style.lineHeight),baseline,
     letterSpacing:style.letterSpacing,advance:metrics.width,positions,
     metrics:{ascent:metrics.fontBoundingBoxAscent,descent:metrics.fontBoundingBoxDescent},links};
   }));
   await page.screenshot({path:path.join(output,`web-link-decoration-dpr${density}-${theme}.png`)});
   const snapshots=[];
   for(const layer of layers.filter(layer=>layer.drawsContent&&layer.width>0&&layer.height>0)) {
    const {snapshotId}=await client.send('LayerTree.makeSnapshot',{layerId:layer.layerId});
    try {const {commandLog}=await client.send('LayerTree.snapshotCommandLog',{snapshotId});snapshots.push({layer,commands:commandLog});}
    finally {await client.send('LayerTree.releaseSnapshot',{snapshotId});}
   }
   if(!snapshots.length)throw new Error('No actual CSS paint snapshot');
   for(const row of geometry)row.paintedBaselines=[...new Set(snapshots.flatMap(snapshot=>snapshot.commands)
    .filter(command=>command.method==='drawTextBlob'&&command.params.y>=row.y&&command.params.y<row.y+row.height)
    .map(command=>command.params.y))].sort((a,b)=>a-b);
   if(!tableMetrics)tableMetrics=await page.evaluate(html=>{
    const host=document.createElement('div');host.className='assistant-text';host.lang='en';
    host.style.cssText='position:fixed;left:-10000px;top:0;width:324px';host.innerHTML=html;document.body.append(host);
    try {
     const cell=host.querySelector('th'),style=getComputedStyle(cell);
     return {rowHeight:cell.getBoundingClientRect().height,fontSize:parseFloat(style.fontSize),lineHeight:parseFloat(style.lineHeight)};
    } finally {host.remove();}
   },renderMarkdown('| Heading |\n| --- |\n| Body |'));
   cases.push({density,theme,geometry,snapshots});
  } finally {await context.close();}
 }
 await fs.writeFile(path.join(output,'web-link-decoration.json'),JSON.stringify({browser:browser.version(),purpose:'Actual Web link-decoration geometry and paint operations; not native/device parity',cases},null,2)+'\n');
 const lineCases=cases[0].geometry.filter(row=>['plain','chinese','japanese','heading-one','heading-two','heading-three'].includes(row.id)).map(row=>({
  id:row.id,locale:row.locale,text:row.text,size:row.fontSize,weight:row.fontWeight,lineHeight:row.lineHeight,
  measuredHeight:row.height,baselineOffset:row.baseline-row.y,metrics:row.metrics,
 }));
 const color='#FF'+cases[0].geometry[0].links[0].color.match(/\d+/g).map(value=>Number(value).toString(16).padStart(2,'0')).join('').toUpperCase();
 const wrappingCases=cases[0].geometry.filter(row=>row.id.startsWith('wrapped')).map(row=>{
  const lines=[];
  for(const rect of cases[0].snapshots.flatMap(snapshot=>snapshot.commands).filter(command=>command.method==='drawRect'&&command.params.paint.color===color)
   .map(command=>command.params.rect).filter(rect=>rect.top>=row.y&&rect.top<row.y+row.height)) {
   let line=lines.find(line=>line.top===rect.top);
   if(line){line.left=Math.min(line.left,rect.left-row.x);line.right=Math.max(line.right,rect.right-row.x);}
   else lines.push({top:rect.top,left:rect.left-row.x,right:rect.right-row.x});
  }
  lines.sort((a,b)=>a.top-b.top);
  const height=cases[0].geometry[0].height;
  return {id:row.id,source:linkDecorationCases.find(item=>item.id===row.id).text,text:row.renderedText,lineCount:Math.round(row.height/height),
   lines:lines.map(({top,left,right})=>({line:Math.round((top-lines[0].top)/height),left,right}))};
 });
 await fs.writeFile(path.join(root,'android/app/src/test/resources/web-line-metrics.json'),JSON.stringify({
  purpose:'Actual Web CSS explicit line-height struts with byte-matched Android test fonts; desktop mobile viewport, not Android Chrome/OEM parity',
  browser:browser.version(),host:process.platform,cases:lineCases,table:tableMetrics,wrappingCases,
 },null,2)+'\n');
 const originSource='[agjpqy](https://example.com)\n[agjpqy](https://example.org)';
 const originCases=await measureTextOrigins(browser,{css,fontCSS,markup:renderMarkdown(originSource)});
 const origins=JSON.stringify({purpose:'Actual Web painted baselines during fractional inset and ancestor layout changes; desktop mobile viewport, not device parity',
  browser:browser.version(),host:process.platform,source:originSource,cases:originCases},null,2)+'\n';
 await fs.writeFile(path.join(output,'web-text-origins.json'),origins);
 await fs.writeFile(path.join(root,'android/app/src/test/resources/web-text-origins.json'),origins);
 const tables=await measureTableOrigins(browser,{css,fontCSS,renderMarkdown,output});
 const tableOrigins=JSON.stringify({purpose:'Actual Web collapsed-table painted baselines during fractional flow and ancestor changes; not Android Chrome/OEM parity',
  browser:browser.version(),host:process.platform,...tables},null,2)+'\n';
 await fs.writeFile(path.join(output,'web-table-origins.json'),tableOrigins);
 await fs.writeFile(path.join(root,'android/app/src/test/resources/web-table-origins.json'),tableOrigins);
 console.log(`Recorded ${cases.length} actual Web link-decoration scenes, ${linkDecorationCases.length} rows each.`);
 console.log(`Recorded ${originCases.length} actual Web text-origin scenes.`);
 console.log(`Recorded ${tables.cases.length} actual Web table-origin scenes.`);
} finally {await browser?.close();}
