#!/usr/bin/env node
// Raw diagnostic crops only. Full geometry/raster gates never read this contact sheet.
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__links__.cjs'));
const {PNG}=require('pngjs'),directory=path.join(root,'build/artifacts/android-native');
const fixtures=JSON.parse(fs.readFileSync(path.join(root,'android/app/src/test/resources/web-behavior.json'),'utf8')).markdownCases;
const first=fixtures.findIndex(item=>item.id==='literal-links');
if(first<0||fixtures.length-first!==3)throw new Error('Expected the three shared link layout cases');
const rows=[];
for(const theme of ['light','dark']) {
 const images=['web','native'].map(side=>{
  const {cases}=JSON.parse(fs.readFileSync(path.join(directory,`${side}-markdown-blocks-360-${theme}.json`),'utf8'));
  if(cases.length!==fixtures.length)throw new Error('Stale Markdown layout artifacts');
  const image=PNG.sync.read(fs.readFileSync(path.join(directory,`${side==='native'?'':'web-'}markdown-blocks-360-${theme}.png`)));
  const top=Math.round(25+cases.slice(0,first).reduce((sum,item)=>sum+item[0].height+22,0));
  if(image.width!==360||top<0||top>=image.height)throw new Error('Unexpected original layout; never rescale');
  return {image,top,height:image.height-top};
 });
 rows.push({images,height:Math.max(...images.map(item=>item.height))});
}
const result=new PNG({width:720,height:rows.reduce((sum,row)=>sum+row.height,0)});
let top=0;
for(const row of rows) {
 for(const [column,{image,top:sourceY,height}] of row.images.entries())
  PNG.bitblt(image,result,0,sourceY,360,height,column*360,top);
 top+=row.height;
}
fs.writeFileSync(path.join(directory,'markdown-links-comparison.png'),PNG.sync.write(result));
console.log('Wrote markdown-links-comparison.png: actual Web left / production native right, light above dark. Unscaled diagnostic only, not a pixel-parity gate.');
