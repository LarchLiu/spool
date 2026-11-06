#!/usr/bin/env bun
// Compare measured layout boxes, not raster-font pixels or whole-app visual parity.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const directory=path.join(root,'build/artifacts/android-native');
const nativePrefix=process.argv.find(value=>value.startsWith('--native-prefix='))?.slice('--native-prefix='.length)||'native';
if(!/^[a-z0-9-]+$/.test(nativePrefix))throw new Error('Invalid native artifact prefix');
let count=0;
const failures=[];
function compare(native,web,label,tolerance) {
 if(Array.isArray(native)) {
  if(!Array.isArray(web)||native.length!==web.length)throw new Error(`Different item counts for ${label}`);
  native.forEach((box,index)=>compare(box,web[index],`${label}[${index}]`,tolerance));
  return;
 }
 for(const [key,value] of Object.entries(native)) {
  if(!Object.hasOwn(web,key))throw new Error(`Web reference has no ${label}.${key}`);
  if(typeof value==='object')compare(value,web[key],`${label}.${key}`,tolerance);
  else {
   count++;
   const limit=typeof value==='string'||['color','style','lineCount'].includes(key)?0:tolerance;
   if(typeof value==='string'?value!==web[key]:!Number.isFinite(value)||!Number.isFinite(web[key])||Math.abs(value-web[key])>limit)
    failures.push(`${label}.${key}: native=${value}, Web=${web[key]} (tolerance ${limit})`);
  }
 }
}
const screens=process.argv.find(value=>value.startsWith('--screens='))?.slice('--screens='.length).split(',')||['login','workspace','workspace-1','workspace-3','workspace-6','workspace-overflow','workspace-wrapped','model','permissions','attachments','image','appearance','files','people','people-group','group-picker','online-menu','sidebar','sidebar-empty','sidebar-operators','code-tabs','diff-layout','syntax','messages','markdown-blocks'];
for(const screen of screens)for(const width of [320,360,412])for(const theme of ['light','dark']) {
 const load=side=>{
  const file=path.join(directory,`${side}-${screen}-${width}-${theme}.json`);
  if(!fs.existsSync(file))throw new Error(`Missing ${file}. Render Web references and run Android layout tests first.`);
  return JSON.parse(fs.readFileSync(file,'utf8'));
 };
 // Browsers keep fractional CSS image widths; native views round to physical pixels at mdpi.
 compare(load(nativePrefix),load('web'),`${nativePrefix}/${screen}/${width}/${theme}`,['model','attachments'].includes(screen)?0:1);
}
if(failures.length)throw new Error(failures.slice(0,80).join('\n')+(failures.length>80?`\n... ${failures.length} differences in total.`:''));
console.log(`Verified ${count} mobile UI geometry/semantic values, tab stops, diff highlights and syntax-character positions/colors/styles against actual Web at three widths and both themes (fractional geometry rounding <=1px, semantics/colors/styles exact).`);
