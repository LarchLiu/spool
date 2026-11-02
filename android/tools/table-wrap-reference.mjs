#!/usr/bin/env node
// Actual Web MarkdownMessage/CSS table lines and unclipped intrinsic inline widths.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import {renderMarkdown} from './web-markdown-reference.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
if(!path.resolve(process.env.TMPDIR||'').startsWith(path.join(root,'build/tmp')+path.sep))throw new Error('Run through android/tools/run.sh');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__table_wrap__.cjs'));
const {chromium}=require('playwright'),fonts=path.join(root,'build/cache/android-ui-fonts'),output=path.join(root,'build/artifacts/android-native');
await fs.mkdir(output,{recursive:true});
const manifest=JSON.parse(await fs.readFile(path.join(fonts,'manifest.json'),'utf8'));
let fontCSS='';
for(const face of [...manifest.faces.map(face=>({...face,family:'AndroidRoboto'})),...manifest.cjk])fontCSS+=`@font-face{font-family:${face.family};src:url(data:font/ttf;base64,${(await fs.readFile(path.join(fonts,face.file))).toString('base64')});font-weight:${face.weight||400};font-style:${face.italic?'italic':'normal'}}`;
fontCSS+=':root{font-family:AndroidRoboto,AndroidCjkSC,sans-serif}:lang(ja){font-family:AndroidRoboto,AndroidCjkJP,sans-serif}';
const labels=[
 ['boundary','en','A long linked label that wraps across several native text lines while keeping its original selection and link behavior.'],
 ['ordinary','en','Another long linked label with enough words to exercise centered cells next to a wrapped paragraph.'],
 ['bold','en','**A long bold label that wraps across several native text lines while retaining its selection and clickable source.**'],
 ['italic','en','*An italic label checks the original font advances and normal whitespace when a narrow table needs several lines.*'],
 ['mixed','en','A **bold phrase** followed by *italic words* and ordinary content retains every native span while it wraps.'],
 ['accented','en','Crème brûlée et café déjà préparés: un texte sélectionnable conserve ses accents et ses espaces dans chaque ligne.'],
 ['chinese','zh-CN','这里是一段需要在手机表格中自动换行的中文链接，检查原生字体、标点、选区和链接位置是否保持正确。'],
 ['japanese','ja','モバイルの表に表示する長いリンクです。日本語の折り返し、句読点、文字選択とリンクの位置を確認します。'],
];
const fixtures=labels.map(([id,locale,label])=>({id,locale,source:`| Name | Value |\n| --- | ---: |\n| [${label}](https://example.com/table) | 1 |`}));
const css=await fs.readFile(path.join(root,'web/src/style.css'),'utf8'),cases=[];
let browser;
try {
 browser=await chromium.launch({channel:'chrome',headless:true});
 for(const density of [1,2.625,3]) {
  const context=await browser.newContext({viewport:{width:360,height:600},deviceScaleFactor:density});
  try {
   await context.route('**/*',route=>route.abort());const page=await context.newPage();
   for(const fixture of fixtures) {
    await page.setContent(`<!doctype html><html lang="${fixture.locale}"><meta charset="utf-8"><style>${css}</style><style>${fontCSS}#root{padding:25px 18px;width:360px}</style><div id="root" class="assistant-text">${renderMarkdown(fixture.source)}</div></html>`);
    await page.evaluate(()=>document.fonts.ready);
    for(const width of [320,360,412]) {
    await page.setViewportSize({width,height:600});await page.evaluate(width=>document.querySelector('#root').style.width=width+'px',width);
    const cells=await page.evaluate(()=>[...document.querySelectorAll('th,td')].map(cell=>{
     const r=cell.getBoundingClientRect(),lines=[],walker=document.createTreeWalker(cell,NodeFilter.SHOW_TEXT);
     for(let node=walker.nextNode();node;node=walker.nextNode())for(let i=0;i<node.length;i++) {
      const range=document.createRange();range.setStart(node,i);range.setEnd(node,i+1);const box=range.getBoundingClientRect();if(!box.width)continue;
      let line=lines.find(line=>Math.abs(line.y-box.y)<.01);if(!line){line={y:box.y,text:''};lines.push(line);}line.text+=node.textContent[i];
     }
     const clone=cell.cloneNode(true);clone.style.cssText='position:fixed;left:-10000px;top:0;display:inline-block;padding:0;border:0;width:max-content;white-space:nowrap';cell.parentElement.append(clone);
     const range=document.createRange();range.selectNodeContents(clone);const advance=range.getBoundingClientRect().width;clone.remove();
     return {text:cell.textContent,x:r.x,y:r.y,width:r.width,height:r.height,lines:lines.map(line=>line.text.trim()),advance};
    }));
    cases.push({id:fixture.id,locale:fixture.locale,density,width,cells});assert.equal(cells.length,4);
    if(width===360&&density===1&&['boundary','mixed'].includes(fixture.id))await page.screenshot({path:path.join(output,`web-table-wrap-${fixture.id}.png`)});
    }
   }
  } finally {await context.close();}
 }
 const report={scope:'Original Web MarkdownMessage/CSS with byte-matched native test fonts; actual line contents and intrinsic inline widths. Not device/raster equivalence.',browser:browser.version(),fixtures,cases};
 await fs.writeFile(path.join(root,'android/app/src/test/resources/web-table-wrap.json'),JSON.stringify(report,null,2)+'\n');
 await fs.writeFile(path.join(output,'web-table-wrap-reference.json'),JSON.stringify(report,null,2)+'\n');
 console.log(`Recorded ${cases.length} actual Web tables, ${cases.length*4} cell line/width references, including bold/italic/mixed/accents/Chinese/Japanese.`);
} finally {await browser?.close();}
