#!/usr/bin/env bun
// Full composed workspace geometry in physical pixels; not a glyph/full-image raster gate.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const output=path.join(root,'build/artifacts/android-native');
const read=name=>JSON.parse(fs.readFileSync(path.join(output,name+'.json'),'utf8'));
const expected=read('web-composer-layout-dpr2.625'),failures=[];
let count=0,maximum=0;
function compare(actual,expected,label) {
 if(actual&&typeof actual==='object') {
  if(!expected||typeof expected!=='object')throw new Error('Missing Web object: '+label);
  for(const key of Object.keys(expected)) {
   if(!Object.hasOwn(actual,key))throw new Error('Missing native '+label+'.'+key);
   compare(actual[key],expected[key],label+'.'+key);
  }
 } else {
  count++;
  if(typeof expected==='number') {
   const difference=Math.abs(actual-expected);
   if(!Number.isFinite(actual)||!Number.isFinite(expected))throw new Error('Nonfinite geometry: '+label);
   maximum=Math.max(maximum,difference);
   if(difference>1)failures.push({label,native:actual,web:expected,difference});
  } else if(actual!==expected)failures.push({label,native:actual,web:expected});
 }
}
for(const api of [26,35]) {
 const actual=read('native-composer-layout-api'+api+'-dpr2.625');
 if(actual.length!==expected.length||expected.length!==22)throw new Error('Incomplete production composer scenario coverage');
 for(const sample of expected) {
  const match=actual.find(item=>item.id===sample.id&&item.theme===sample.theme);
  if(!match)throw new Error('Missing native scene: '+sample.id+'/'+sample.theme);
  compare(match,sample,'api'+api+'/'+sample.id+'/'+sample.theme);
 }
}
const report={count,maximum,failures};
fs.writeFileSync(path.join(output,'composer-layout-check-dpr2.625.json'),JSON.stringify(report,null,2)+'\n');
console.log(JSON.stringify(report,null,2));
if(failures.length)throw new Error('Composed 420dpi composer geometry/semantics differ from original Web (<=1 physical pixel, exact strings/visibility).');
console.log(`Verified ${count} composed composer geometry/semantic values at 420dpi on API26/35; both themes, all three locales and live phone/keyboard/narrow/wide changes. Not physical IME, glyph or full-image raster parity.`);
