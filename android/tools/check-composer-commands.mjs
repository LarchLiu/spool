#!/usr/bin/env node
// Actual SlashCommandMenu/CSS fixtures vs production native views, not whole-app raster parity.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {commandCases} from './ui-fixtures.mjs';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const directory=path.join(root,'build/artifacts/android-native');
const prefix=process.argv.find(value=>value.startsWith('--native-prefix='))?.slice('--native-prefix='.length)||'native';
if(!/^[a-z0-9-]+$/.test(prefix))throw new Error('Invalid native artifact prefix');
const failures=[];
let count=0;
function compare(actual,expected,label,key='') {
 if(expected!==null&&typeof expected==='object') {
  if(actual===null||typeof actual!=='object')throw new Error('Missing '+label);
  if(Object.keys(actual).join(',')!==Object.keys(expected).join(',')) {
   const keys=Object.keys(expected);
   if(keys.length!==Object.keys(actual).length||keys.some(key=>!Object.hasOwn(actual,key)))throw new Error('Different fields for '+label);
  }
  for(const [key,value] of Object.entries(expected))compare(actual[key],value,label+'.'+key,key);
  return;
 }
 count++;
 const exact=typeof expected!=='number'||key==='color';
 if(exact?actual!==expected:!Number.isFinite(actual)||Math.abs(actual-expected)>1)
  failures.push(`${label}: native=${JSON.stringify(actual)}, Web=${JSON.stringify(expected)} (limit ${exact?'exact':'1px'})`);
}
for(const width of [320,360,412])for(const theme of ['light','dark'])for(const locale of ['en','zh-CN','ja']) {
 const read=side=>JSON.parse(fs.readFileSync(path.join(directory,`${side}-commands-${width}-${theme}-${locale}.json`),'utf8'));
 const actual=read(prefix),expected=read('web');
 for(const [side,values] of [['native',actual],['Web',expected]])
  if(values.length!==commandCases.length||values.some((item,index)=>item.id!==commandCases[index].id))throw new Error('Incomplete '+side+' command fixtures');
 compare(actual,expected,`${width}/${theme}/${locale}`);
}
fs.writeFileSync(path.join(directory,`command-geometry-check${prefix==='native'?'':'-'+prefix}.json`),JSON.stringify({prefix,count,failures},null,2)+'\n');
if(failures.length)throw new Error(failures.slice(0,100).join('\n')+`\n${failures.length} mismatches in total.`);
console.log(`Verified ${count} command-menu/composer/notice/error geometry and semantic values across three widths, both themes and English/Chinese/Japanese (geometry <=1px; text/colors exact). Not full-raster or physical-IME parity.`);
