#!/usr/bin/env bun
import { requireRunTemp } from "../../tools/test-scratch.mjs";
// Diagnose borderline native/Web wraps from actual DOM styles and byte-matched fonts.
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import {renderMarkdown} from './web-markdown-reference.mjs';
import {tableOriginFixtures} from './table-origin-reference.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
requireRunTemp();
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__table_wrap__.cjs'));
const {chromium}=require('playwright'),fonts=path.join(root,'build/cache/android-ui-fonts');
const manifest=JSON.parse(await fs.readFile(path.join(fonts,'manifest.json'),'utf8'));
let fontCSS='';
for(const face of manifest.faces)fontCSS+=`@font-face{font-family:AndroidRoboto;src:url(data:font/ttf;base64,${(await fs.readFile(path.join(fonts,face.file))).toString('base64')});font-weight:${face.weight};font-style:${face.italic?'italic':'normal'}}`;
const css=await fs.readFile(path.join(root,'web/src/style.css'),'utf8'),cases=[];
let browser;
try {
 browser=await chromium.launch({channel:'chrome',headless:true});
 for(const density of [1,2.625,3]) {
  const context=await browser.newContext({viewport:{width:360,height:600},deviceScaleFactor:density});
  try {
   await context.route('**/*',route=>route.abort());
   const page=await context.newPage();
   await page.setContent(`<!doctype html><style>${css}</style><style>${fontCSS}:root{font-family:AndroidRoboto}#root{padding:25px 18px;width:360px}</style><div id="root" class="assistant-text">${renderMarkdown(tableOriginFixtures.find(item=>item.id==='wrapped').source)}</div>`);
   await page.evaluate(()=>document.fonts.ready);
   cases.push(await page.evaluate(density=>{
    const cell=document.querySelector('td'),style=getComputedStyle(cell),rect=cell.getBoundingClientRect();
    const c=document.createElement('canvas').getContext('2d');c.font=style.font;
    const phrases=['A long linked label that wraps','across several native text lines while','keeping its original selection and'];
    const rendered=[];
    const walker=document.createTreeWalker(cell,NodeFilter.SHOW_TEXT);
    for(let node=walker.nextNode();node;node=walker.nextNode())for(let i=0;i<node.length;i++) {
     const range=document.createRange();range.setStart(node,i);range.setEnd(node,i+1);
     const r=range.getBoundingClientRect();rendered.push({text:node.textContent[i],x:r.x,y:r.y,width:r.width});
    }
    const sizes=[12.875,12.88,13].map(size=>{
     c.font=style.font.replace(style.fontSize,size+'px');
     const text=phrases[1];
     return {size,font:c.font,text,advance:c.measureText(text).width,positions:Array.from({length:text.length+1},(_,i)=>c.measureText(text.slice(0,i)).width)};
    });
    c.font=style.font;
    return {density,cell:{x:rect.x,width:rect.width,clientWidth:cell.clientWidth},styles:Object.fromEntries(['font','fontSize','fontWeight','fontKerning','letterSpacing','paddingLeft','paddingRight','borderLeftWidth','borderRightWidth','width','boxSizing','textRendering'].map(key=>[key,style[key]])),rendered,sizes,
     phrases:phrases.map(text=>({text,advance:c.measureText(text).width,positions:Array.from({length:text.length+1},(_,i)=>c.measureText(text.slice(0,i)).width)}))};
   },density));
  } finally {await context.close();}
 }
 const report={browser:browser.version(),purpose:'Actual Web CSS/content widths and fractional glyph advances; diagnostic only, no tolerance relaxation',cases};
 await fs.writeFile(path.join(root,'build/artifacts/android-native/web-table-wrap-probe.json'),JSON.stringify(report,null,2)+'\n');
 console.log(JSON.stringify(report,null,2));
} finally {await browser?.close();}
