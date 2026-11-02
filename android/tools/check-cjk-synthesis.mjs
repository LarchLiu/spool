#!/usr/bin/env node
// Keep full Web/native raster failure visible, separate from real-face/native-paint controls.
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__cjk_raster__.cjs'));
const {PNG}=require('pngjs'),output=path.join(root,'build/artifacts/android-native');
const reference=JSON.parse(fs.readFileSync(path.join(output,'web-cjk-synthesis.json'),'utf8')),cases=[];
const read=name=>PNG.sync.read(fs.readFileSync(path.join(output,name+'.png')));
for(const sample of reference.cases)for(const api of [26,35]) {
 const web=read(`web-cjk-synthesis-${sample.locale}-dpr${sample.density}`);
 const compare=control=>{
  const actual=read(`native-api${api}-cjk-synthesis-${sample.locale}-dpr${sample.density}${control?'-missing-config-control':''}`);
  if(actual.width!==web.width||actual.height!==web.height)throw new Error('Do not crop or rescale CJK synthesis fixtures');
  let maximum=0,outsideTolerance=0,error=0;
  for(let i=0;i<actual.data.length;i+=4) {
   let delta=0;for(let c=0;c<4;c++)delta=Math.max(delta,Math.abs(actual.data[i+c]-web.data[i+c]));
   error+=delta;maximum=Math.max(maximum,delta);if(delta>1)outsideTolerance++;
  }
  return {maximum,outsideTolerance,error};
 };
 cases.push({api,locale:sample.locale,density:sample.density,pixels:web.width*web.height,actual:compare(false),missingConfigControl:compare(true)});
}
const report={purpose:'Full actual-Web/native CJK raster with independent missing-config diagnostic, not a device-parity waiver',cases};
fs.writeFileSync(path.join(output,'cjk-synthesis-raster-check.json'),JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify(report,null,2));
if(cases.some(sample=>sample.actual.outsideTolerance))throw new Error('Full CJK Web/native raster differs by more than one channel unit; real-face controls do not waive this failure.');
console.log('All full-frame CJK fixtures pass the unchanged one-channel-unit threshold; not a hardware/OEM claim.');
