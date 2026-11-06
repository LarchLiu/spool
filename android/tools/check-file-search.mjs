#!/usr/bin/env bun
// Complete production frames, independent of the search geometry/interaction checks.
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__file_search__.cjs'));
const {PNG}=require('pngjs'),directory=path.join(root,'build/artifacts/android-native'),cases=[];
for(const api of [26,35])for(const theme of ['light','dark']) {
 const read=name=>PNG.sync.read(fs.readFileSync(path.join(directory,name+'.png')));
 const native=read(`native-file-search-api${api}-${theme}`),web=read(`web-file-search-zh-CN-${theme}-220-dpr2.625-typed`);
 if(native.width!==578||native.height!==2100||native.width!==web.width||native.height!==web.height)throw new Error('Full search frame dimensions differ; never crop or scale');
 let maximum=0,outsideTolerance=0,at;
 for(let i=0;i<native.data.length;i+=4) {
  let delta=0;for(let c=0;c<4;c++)delta=Math.max(delta,Math.abs(native.data[i+c]-web.data[i+c]));
  if(delta>maximum){maximum=delta;at={x:i/4%native.width,y:Math.floor(i/4/native.width)};}
  if(delta>1)outsideTolerance++;
 }
 cases.push({api,theme,pixels:native.width*native.height,maximum,outsideTolerance,at});
}
fs.writeFileSync(path.join(directory,'file-search-raster-check.json'),JSON.stringify({purpose:'Strict full-frame search screenshot comparison, not a component/device claim',cases},null,2)+'\n');
console.log(JSON.stringify(cases,null,2));
if(cases.some(item=>item.outsideTolerance))throw new Error('Full FileSidebar search raster differs from actual Web by more than one channel unit; geometry/interaction passes do not waive it.');
console.log(`Verified ${cases.reduce((sum,item)=>sum+item.pixels,0)} full search-frame pixels at <=1 channel unit.`);
