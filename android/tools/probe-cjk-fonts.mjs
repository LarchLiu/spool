#!/usr/bin/env bun
// Observe the actual Web renderer's available font faces with font-synthesis:none.
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import {renderMarkdown} from './web-markdown-reference.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
if(!path.resolve(process.env.TMPDIR||'').startsWith(path.join(root,'build/tmp')+path.sep))throw new Error('Run through android/tools/run.sh');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__cjk_fonts__.cjs'));
const {chromium}=require('playwright'),{PNG}=require('pngjs');
const output=path.join(root,'build/artifacts/android-native'),fonts=path.join(root,'build/cache/android-ui-fonts');
const manifest=JSON.parse(await fs.readFile(path.join(fonts,'manifest.json'),'utf8'));
let fontCSS='';
for(const face of [...manifest.faces.map(face=>({...face,family:'AndroidRoboto'})),...manifest.cjk]) {
 const bytes=await fs.readFile(path.join(fonts,face.file));
 if(crypto.createHash('sha256').update(bytes).digest('hex')!==face.sha256)throw new Error('Reference font drift: '+face.file);
 fontCSS+=`@font-face{font-family:${face.family};src:url(data:font/ttf;base64,${bytes.toString('base64')});font-weight:${face.weight||400};font-style:${face.italic?'italic':'normal'}}`;
}
const css=await fs.readFile(path.join(root,'web/src/style.css'),'utf8'),cases=[];
let browser;
try {
 browser=await chromium.launch({channel:'chrome',headless:true});
 for(const density of [1,2.625,3])for(const locale of ['zh-CN','ja']) {
  const context=await browser.newContext({viewport:{width:360,height:320},deviceScaleFactor:density});
  try {
   await context.route('**/*',route=>route.abort());
   const page=await context.newPage(),client=await context.newCDPSession(page);
   await client.send('DOM.enable');await client.send('CSS.enable');
   const family=locale==='ja'?'AndroidCjkJP':'AndroidCjkSC';
   const source=locale==='ja'?'| 名前と数値 | **共有の項目** |\n| --- | --- |\n| **日本語**と*仮名* | Latin **Bold** |':'| 名称与数值 | **共享项目** |\n| --- | --- |\n| **中文**与*汉字* | Latin **Bold** |';
   await page.setContent(`<!doctype html><html lang="${locale}" data-theme="light"><style>${css}</style><style>${fontCSS}:root{font-family:AndroidRoboto,${family},sans-serif}#root{padding:25px 18px;height:320px}</style><div id="root" class="assistant-text">${renderMarkdown(source)}</div></html>`);
   await page.evaluate(()=>document.fonts.ready);
   const {root:{nodeId}}=await client.send('DOM.getDocument');
   const records=[];
   for(const selector of ['th:first-child','th strong','td strong','td em','td:last-child strong']) {
    const style=await page.locator(selector).first().evaluate(node=>{
     const s=getComputedStyle(node);return {text:node.textContent,weight:s.fontWeight,style:s.fontStyle,synthesis:s.fontSynthesis};
    });
    const {nodeId:target}=await client.send('DOM.querySelector',{nodeId,selector});
    const {fonts:used}=await client.send('CSS.getPlatformFontsForNode',{nodeId:target});
    if(style.synthesis!=='none'||used.some(font=>!font.isCustomFont))throw new Error('Uncontrolled font-synthesis/reference font');
    records.push({selector,...style,fonts:used});
   }
   const original=await page.screenshot();
   await fs.writeFile(path.join(output,`web-cjk-synthesis-${locale}-dpr${density}.png`),original);
   await page.addStyleTag({content:':root{font-synthesis:weight style}'});
   const control=await page.screenshot();
   await fs.writeFile(path.join(output,`web-cjk-synthesis-${locale}-dpr${density}-synthetic-control.png`),control);
   const a=PNG.sync.read(original),b=PNG.sync.read(control);let maximum=0,different=0;
   if(a.width!==b.width||a.height!==b.height)throw new Error('Control changed viewport dimensions');
   for(let i=0;i<a.data.length;i+=4) {
    let delta=0;for(let c=0;c<4;c++)delta=Math.max(delta,Math.abs(a.data[i+c]-b.data[i+c]));
    maximum=Math.max(maximum,delta);if(delta>1)different++;
   }
   if(!different)throw new Error('The actual CJK synthesis control did not change any paint');
   cases.push({locale,density,source,records,syntheticControl:{maximum,different}});
  } finally {await context.close();}
 }
 const report={browser:browser.version(),purpose:'Actual Web font-synthesis:none face discovery with an explicitly separate synthetic control; not native/device raster parity',cases};
 await fs.writeFile(path.join(output,'web-cjk-synthesis.json'),JSON.stringify(report,null,2)+'\n');
 await fs.writeFile(path.join(root,'android/app/src/test/resources/web-cjk-fonts.json'),JSON.stringify(report,null,2)+'\n');
 console.log(JSON.stringify(report,null,2));
} finally {await browser?.close();}
