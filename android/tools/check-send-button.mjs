#!/usr/bin/env bun
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {sendButtonCases} from './ui-fixtures.mjs';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const directory=path.join(root,'build/artifacts/android-native'),failures=[];
let count=0;
function compare(actual,expected,label,key='') {
 if(expected!==null&&typeof expected==='object') {
  if(actual===null||typeof actual!=='object'||Object.keys(actual).length!==Object.keys(expected).length)throw new Error('Missing fields: '+label);
  for(const [key,value] of Object.entries(expected))compare(actual[key],value,label+'.'+key,key);
  return;
 }
 count++;
 const exact=typeof expected!=='number'||['background','time'].includes(key),tolerance=key==='angle'?.01:1;
 const difference=key==='angle'?Math.abs(((actual-expected+540)%360)-180):Math.abs(actual-expected);
 if(exact?actual!==expected:!Number.isFinite(actual)||difference>tolerance)
  failures.push(`${label}: native=${actual}, Web=${expected} (limit ${exact?'exact':tolerance})`);
}
for(const width of [320,360,412])for(const theme of ['light','dark'])for(const locale of ['en','zh-CN','ja']) {
 const read=prefix=>JSON.parse(fs.readFileSync(path.join(directory,`${prefix}-send-button-${width}-${theme}-${locale}.json`),'utf8'));
 const expected=read('web');
 if(expected.length!==4||expected.some(item=>item.states.length!==sendButtonCases.length))throw new Error('Incomplete Web spin samples');
 for(const api of [26,35])compare(read('native-api'+api),expected,`API${api}/${width}/${theme}/${locale}`);
}
fs.writeFileSync(path.join(directory,'send-button-check.json'),JSON.stringify({count,failures},null,2)+'\n');
if(failures.length)throw new Error(failures.slice(0,80).join('\n')+`\n${failures.length} differences in total.`);
console.log(`Verified ${count} production send/stop button geometry, label, enabled/color and CSS/native 800ms spin values on API26/35, three widths, both themes and three locales (geometry <=1px, angle <=.01deg, semantics/colors exact). Not full-raster or device scheduling parity.`);
