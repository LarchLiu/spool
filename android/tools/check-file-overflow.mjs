#!/usr/bin/env bun
// Full unscaled mobile frames stay independent from passing segment-layout/motion controls.
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__file_overflow__.cjs'));
const {PNG}=require('pngjs'),output=path.join(root,'build/artifacts/android-native'),cases=[];
for(const api of [26,35])for(const theme of ['light','dark']) {
 const read=name=>PNG.sync.read(fs.readFileSync(path.join(output,name+'.png')));
 const actual=read(`native-file-overflow-browser-api${api}-${theme}`),expected=read(`web-file-overflow-${theme}`);
 if(actual.width!==578||actual.height!==2100||actual.width!==expected.width||actual.height!==expected.height)throw new Error('Unexpected full frame dimensions; never crop or rescale');
 let maximum=0,outsideTolerance=0,at;
 for(let i=0;i<actual.data.length;i+=4) {
  let difference=0;for(let channel=0;channel<4;channel++)difference=Math.max(difference,Math.abs(actual.data[i+channel]-expected.data[i+channel]));
  if(difference>maximum){maximum=difference;at={x:i/4%actual.width,y:Math.floor(i/4/actual.width)};}
  if(difference>1)outsideTolerance++;
 }
 cases.push({api,theme,density:2.625,locale:'zh-CN',pixels:actual.width*actual.height,maximum,outsideTolerance,at});
}
fs.writeFileSync(path.join(output,'file-overflow-raster-check.json'),JSON.stringify({purpose:'Strict full FileSidebar frame, not an isolated segment/device claim',cases},null,2)+'\n');
console.log(JSON.stringify(cases,null,2));
if(cases.some(item=>item.outsideTolerance))throw new Error('Full file-sidebar pixels differ from actual Web beyond one channel unit; segment geometry and timing do not waive this failure.');
console.log(`Verified ${cases.reduce((sum,item)=>sum+item.pixels,0)} full file-sidebar pixels at <=1 channel unit.`);
