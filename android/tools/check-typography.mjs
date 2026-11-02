#!/usr/bin/env node
// Check subpixel text advances using the exact Robolectric fonts served to the browser.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {typographyCases} from './ui-fixtures.mjs';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const artifacts=path.join(root,'build/artifacts/android-native');
const read=name=>JSON.parse(fs.readFileSync(path.join(artifacts,name),'utf8'));
const manifest=read('font-reference.json');
for(const face of [...manifest.faces,manifest.mono,...manifest.cjk]) {
 const bytes=fs.readFileSync(path.join(root,'build/cache/android-ui-fonts',face.file));
 if(crypto.createHash('sha256').update(bytes).digest('hex')!==face.sha256)throw new Error('Reference font drift: '+face.file);
}
const web=read('web-fonts.json'),failures=[];
const quantized=process.argv.includes('--quantized');
if(web.length!==typographyCases.length)throw new Error('Incomplete browser font audit');
let count=0,maximum=0,rawMaximum=0;
for(const api of [26,35]) {
 const native=read(`native-fonts-api${api}.json`);
 const fonts=read(`native-fonts-source-api${api}.json`);
 for(const face of [...manifest.faces,manifest.mono])if(fonts[face.file]!==face.sha256)throw new Error('Native/browser font byte mismatch: '+face.file);
 for(const face of manifest.cjk)if(fonts[face.sourceFile]!==face.sourceSHA256)throw new Error('Native/browser CJK collection mismatch');
 if(native.length!==web.length)throw new Error('Incomplete native font audit for API '+api);
 for(let i=0;i<web.length;i++) {
  for(const key of ['size','weight','text'])if(web[i][key]!==typographyCases[i][key]||native[i][key]!==web[i][key])throw new Error('Different typography cases');
  for(const key of ['selectedRun','advance']) {
   // Minikin quantizes glyph advances to 1/256px; keep raw comparison available separately.
   const positions=web[i].positions;
   const expected=quantized?positions.slice(1).reduce((sum,position,index)=>sum+Math.floor((position-positions[index])*256+1e-6)/256,0):web[i].advance;
   const delta=Math.abs(native[i][key]-expected);count++;maximum=Math.max(maximum,delta);
   rawMaximum=Math.max(rawMaximum,Math.abs(native[i][key]-web[i].advance));
   if(!Number.isFinite(delta)||delta>.01)failures.push(`API ${api}/${web[i].size}px/${web[i].weight}/${key}: native=${native[i][key]}, expected=${expected}, raw Web=${web[i].advance}`);
  }
 }
}
if(failures.length)throw new Error(failures.join('\n'));
console.log(`Verified ${count} ${quantized?'quantization-aware':'raw'} font advances at 12/14/24/28px and seven CSS weights on API 26/35; maximum residual ${maximum.toFixed(6)}px (limit 0.01px); raw difference ${rawMaximum.toFixed(6)}px. Not a glyph-raster parity claim.`);
