#!/usr/bin/env node
// Keep original-CSS screenshot and actual-command software replay comparisons separate.
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__surface__.cjs'));
const {PNG}=require('pngjs');
const output=path.join(root,'build/artifacts/android-native'),cases=[];
const mode=process.argv.includes('--replay')?'replay':'screenshot';
const borderOnly=process.argv.includes('--border-only');
const composer=process.argv.includes('--composer');
if(composer&&borderOnly)throw new Error('The production composer always includes its CSS shadow');
const reference=JSON.parse(fs.readFileSync(path.join(output,'css-paint-commands.json'),'utf8'));
for(const api of [26,35])for(const density of composer?[1,2.625,3]:[1,3])for(const theme of ['light','dark'])for(const profile of composer?['shadow']:['border','shadow']) {
 const read=name=>PNG.sync.read(fs.readFileSync(path.join(output,`${name}-dpr${density}-${theme}-${profile}.png`)));
 const name=`native-api${api}-${composer?'composer-':''}css-paint`;
 const actual=read(name),screenshot=read('web-css-paint'),replay=read('css-paint-replay');
 let geometry;
 if(composer) {
  const actual=JSON.parse(fs.readFileSync(path.join(output,`${name}-dpr${density}-${theme}-${profile}.json`),'utf8'));
  const fixture=reference.cases.find(item=>item.density===density&&item.theme===theme&&item.shadow);
  if(!fixture)throw new Error(`Missing original CSS composer reference for DPR${density}/${theme}`);
  const css=fixture.box;
  const expected={...Object.fromEntries(['left','top','right','bottom'].map(key=>[key,Math.round(css[key])*density])),radius:css.radius*density};
  for(const key of Object.keys(expected))if(!Number.isFinite(actual[key])||!Number.isFinite(expected[key]))throw new Error('Nonfinite composer paint bound: '+key);
  geometry={actual,expected,maximum:Math.max(...Object.keys(expected).map(key=>Math.abs(actual[key]-expected[key])))};
 }
 const compare=expected=>{
  if(actual.width!==expected.width||actual.height!==expected.height)throw new Error('Mismatched paint fixture dimensions');
  let maximum=0,outsideTolerance=0,at;
  for(let i=0;i<actual.data.length;i+=4) {
   let difference=0;for(let c=0;c<4;c++)difference=Math.max(difference,Math.abs(actual.data[i+c]-expected.data[i+c]));
   if(difference>maximum){maximum=difference;at={x:i/4%actual.width,y:Math.floor(i/4/actual.width),native:[...actual.data.subarray(i,i+4)],expected:[...expected.data.subarray(i,i+4)]};}
   if(difference>1)outsideTolerance++;
  }
  return {maximum,outsideTolerance,at};
 };
 cases.push({api,density,theme,profile,pixels:actual.width*actual.height,geometry,screenshot:compare(screenshot),replay:compare(replay)});
}
fs.writeFileSync(path.join(output,`${composer?'composer-':''}css-paint-check.json`),JSON.stringify(cases,null,2)+'\n');
console.log(JSON.stringify(cases,null,2));
const selected=cases.filter(item=>!borderOnly||item.profile==='border');
if(selected.some(item=>item.geometry?.maximum>.0001))throw new Error('Production composer paint bounds differ from original CSS; no tolerance was widened.');
if(selected.some(item=>item[mode].outsideTolerance))throw new Error(`Native vs actual CSS ${mode}${borderOnly?' border-only':''} differs by more than one channel unit; no tolerance was widened.`);
console.log(`Verified ${selected.reduce((sum,item)=>sum+item.pixels,0)} pixels against actual CSS ${mode}${borderOnly?' border-only':''}; all other modes remain independent. Not hardware/device or full-app parity.`);
