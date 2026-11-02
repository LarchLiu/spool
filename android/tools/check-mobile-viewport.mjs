#!/usr/bin/env node
// Whole composed shell geometry plus real CSS/native drawer transition samples, not raster parity.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const output=path.join(root,'build/artifacts/android-native');
const motionOnly=process.argv.includes('--motion-only');
const scenes=process.argv.find(value=>value.startsWith('--scenes='))?.slice('--scenes='.length).split(',')||['full','keyboard','split','narrow','wide'];
for(const scene of scenes)if(!['full','keyboard','split','narrow','wide'].includes(scene))throw new Error('Unknown viewport scene');
const read=name=>JSON.parse(fs.readFileSync(path.join(output,name+'.json'),'utf8'));
const failures=[];
let count=0,motionCount=0;
function compare(actual,expected,label,motion=false) {
 for(const [key,value] of Object.entries(actual)) {
  if(!Object.hasOwn(expected,key))throw new Error(`Missing Web ${label}.${key}`);
  if(value!==null&&typeof value==='object')compare(value,expected[key],`${label}.${key}`,motion);
  else {
   count++;if(motion)motionCount++;
   const exact=typeof value!=='number'||key==='scrimColor';
   if(exact?value!==expected[key]:!Number.isFinite(value)||!Number.isFinite(expected[key])||Math.abs(value-expected[key])>1)
    failures.push(`${label}.${key}: native=${value}, Web=${expected[key]} (limit ${exact?'exact':'1px'})`);
  }
 }
}
if(!motionOnly)for(const scene of scenes)for(const side of ['workspace','left','right'])for(const theme of ['light','dark']) {
 const name=`viewport-${scene}-${side}-${theme}`;
 compare(read('native-'+name),read('web-'+name),name);
}
const motion=read('web-viewport-motion');
for(const api of [26,29,35])compare(read('native-viewport-motion-api'+api),motion,'motion/api'+api,true);
const suffix=motionOnly?'-motion':scenes.join(',')==='full,keyboard,split,narrow,wide'?'':'-'+scenes.join('-');
fs.writeFileSync(path.join(output,`viewport-check${suffix}.json`),JSON.stringify({count,motionCount,scenes:motionOnly?[]:scenes,failures},null,2)+'\n');
if(failures.length)throw new Error(failures.join('\n'));
console.log(`Verified ${count} composed viewport geometry/semantic values (${motionCount} CSS/native motion samples on API26/29/35); geometry <=1px, semantic visibility and scrim colors exact. Not physical IME, gestures or full-app raster parity.`);
