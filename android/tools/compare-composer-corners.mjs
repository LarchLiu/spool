#!/usr/bin/env node
// Juxtapose unchanged production-render pixels; never rescale or feed crops into raster gates.
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__surface__.cjs'));
const {PNG}=require('pngjs'),output=path.join(root,'build/artifacts/android-native');
const densityArgument=process.argv.find(value=>value.startsWith('--density='))?.slice('--density='.length);
if(densityArgument!==undefined) {
 if(densityArgument!=='2.625')throw new Error('The before/after density diagnostic is available at DPR 2.625');
 const density=2.625,cornerWidth=Math.ceil(36*density),cropHeight=Math.ceil(44*density);
 const result=new PNG({width:cornerWidth*6,height:cropHeight*2});
 const reference=JSON.parse(fs.readFileSync(path.join(output,'css-paint-commands.json'),'utf8'));
 for(const [row,theme] of ['light','dark'].entries()) {
  const fixture=reference.cases.find(item=>item.density===density&&item.theme===theme&&item.shadow);
  if(!fixture||fixture.box.radius!==19)throw new Error('Unexpected original CSS composer geometry');
  const sourceY=Math.floor((Math.round(fixture.box.bottom)-22)*density);
  const names=[`web-css-paint-dpr${density}-${theme}-shadow`,`composer-density-before-${theme}`,`native-api35-composer-css-paint-dpr${density}-${theme}-shadow`];
  for(const [column,name] of names.entries()) {
   const image=PNG.sync.read(fs.readFileSync(path.join(output,name+'.png')));
   if(image.width!==945||image.height!==630)throw new Error('Unexpected dense fixture viewport; never rescale');
   for(const side of [0,1])PNG.bitblt(image,result,side===0?0:image.width-cornerWidth,sourceY,cornerWidth,cropHeight,(column*2+side)*cornerWidth,row*cropHeight);
  }
 }
 fs.writeFileSync(path.join(output,'composer-density-corners-comparison.png'),PNG.sync.write(result));
 console.log('Wrote composer-density-corners-comparison.png: original Web / native before / native after; each pair contains both bottom corners; light above dark. Raw unscaled diagnostic pixels, not a raster gate.');
 process.exit(0);
}
const rows=[];
for(const theme of ['light','dark']) {
 const images=[];
 for(const side of ['web','native']) {
  const name=`${side}-workspace-360-${theme}`,image=PNG.sync.read(fs.readFileSync(path.join(output,`${side==='native'?'':'web-'}workspace-360-${theme}.png`)));
  const {composer}=JSON.parse(fs.readFileSync(path.join(output,name+'.json'),'utf8'));
  if(image.width!==360||composer.width!==336||composer.height!==99)throw new Error('Unexpected composer comparison geometry');
  const top=Math.floor(composer.y)-28;
  images.push({image,top,height:image.height-top});
 }
 if(images[0].height!==images[1].height)throw new Error('Different crop heights; do not rescale');
 rows.push(images);
}
const result=new PNG({width:720,height:rows.reduce((sum,row)=>sum+row[0].height,0)});
let top=0;
for(const row of rows) {
 row.forEach(({image,top:sourceY,height},column)=>PNG.bitblt(image,result,0,sourceY,360,height,column*360,top));
 top+=row[0].height;
}
fs.writeFileSync(path.join(output,'composer-corners-comparison.png'),PNG.sync.write(result));
console.log('Wrote composer-corners-comparison.png: actual Web left / production native right; light above dark; raw pixels, no resizing. Diagnostic only, not a raster-gate substitute.');
