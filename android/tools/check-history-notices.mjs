#!/usr/bin/env bun
// Full unscaled status frames; geometry success never waives text-raster differences.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__history_raster__.cjs'));
const {PNG}=require('pngjs'), output=path.join(root,'build/artifacts/android-native');
const cases=[];
for(const api of [26,35]) for(const theme of ['light','dark']) {
  const read=name=>PNG.sync.read(fs.readFileSync(path.join(output,name+'.png')));
  const native=read(`native-history-notices-api${api}-${theme}`),web=read(`web-history-notices-${theme}`);
  assert.equal(native.width,360);assert.equal(native.height,800);
  assert.equal(native.width,web.width);assert.equal(native.height,web.height);
  let outsideTolerance=0,maximum=0,at;
  for(let i=0;i<native.data.length;i+=4) {
    let difference=0;for(let c=0;c<4;c++)difference=Math.max(difference,Math.abs(native.data[i+c]-web.data[i+c]));
    if(difference>1)outsideTolerance++;
    if(difference>maximum){maximum=difference;at={x:i/4%native.width,y:Math.floor(i/4/native.width)};}
  }
  cases.push({api,theme,pixels:native.width*native.height,maximum,outsideTolerance,at});
}
fs.writeFileSync(path.join(output,'history-notices-raster-check.json'),JSON.stringify({purpose:'Strict original full-frame Web/native history-status comparison; no crop/rescale/device claim.',cases},null,2)+'\n');
console.log(JSON.stringify(cases,null,2));
assert.ok(cases.every(item=>item.outsideTolerance===0),'Full history-status text raster differs by more than one channel unit; geometry passes do not waive it.');
