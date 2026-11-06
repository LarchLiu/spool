#!/usr/bin/env bun
// Actual textarea UA-shadow ranges vs production EditText layout; no mirror or glyph-raster claim.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {editorCases} from './ui-fixtures.mjs';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const output=path.join(root,'build/artifacts/android-native');
const read=name=>JSON.parse(fs.readFileSync(path.join(output,name),'utf8'));
const web=read('web-editor-text.json'),failures=[];
let count=0,maximumBaseline=0,maximumAdvance=0;
function compare(actual,expected,label,tolerance=1) {
 count++;
 if(typeof expected!=='number'?actual!==expected:!Number.isFinite(actual)||Math.abs(actual-expected)>tolerance)
  failures.push(`${label}: native=${JSON.stringify(actual)}, Web=${JSON.stringify(expected)} (limit ${typeof expected==='number'?tolerance:'exact'})`);
}
function webLines(sample) {
 const groups=new Map();
 for(const node of sample.texts.filter(node=>node.visible)) {
  for(const rect of node.rects.filter(rect=>rect.height>0)) {
   const key=rect.y.toFixed(4),line=groups.get(key)||{left:Infinity,right:-Infinity,y:rect.y,text:''};
   line.left=Math.min(line.left,rect.x);line.right=Math.max(line.right,rect.x+rect.width);groups.set(key,line);
  }
  node.chars.forEach((rect,index)=>{const line=groups.get(rect.y.toFixed(4));if(line)line.text+=node.text[index];});
 }
 return [...groups.values()].sort((a,b)=>a.y-b.y).map(line=>({text:line.text,x:line.left-sample.editor.x,width:line.right-line.left,baseline:line.y-sample.editor.y+sample.editor.ascent}));
}
const expectedCount=4*editorCases.length;
if(web.length!==expectedCount)throw new Error('Incomplete Web editor samples');
for(const api of [26,35]) {
 const native=read(`native-editor-text-api${api}.json`);
 if(native.length!==expectedCount)throw new Error('Incomplete native editor samples');
 for(const width of [320,360,412,900])for(const fixture of editorCases) {
  const label=`API${api}/${width}/${fixture.id}`,expected=web.find(sample=>sample.width===width&&sample.id===fixture.id),actual=native.find(sample=>sample.width===width&&sample.id===fixture.id);
  if(!expected||!actual)throw new Error('Missing '+label);
  for(const key of ['locale','value']) {compare(actual[key],fixture[key],label+'.'+key);compare(expected[key],fixture[key],'Web/'+label+'.'+key);}
  for(const key of ['width','height','paddingTop','paddingBottom','paddingLeft','scrollTop'])compare(actual.editor[key],expected.editor[key],label+'.'+key);
  compare(actual.editor.fontSize,parseFloat(expected.editor.font),label+'.fontSize',0);
  compare(expected.editor.weight,'400','Web/'+label+'.weight');
  const lines=webLines(expected);
  compare(actual.lines.length,lines.length,label+'.lineCount',0);
  if(!lines.length)throw new Error('Empty UA-shadow text ranges: '+label);
  for(let i=0;i<Math.min(lines.length,actual.lines.length);i++) {
   for(const key of ['text','x','width','baseline'])compare(actual.lines[i][key],lines[i][key],label+`.line${i}.`+key);
   maximumBaseline=Math.max(maximumBaseline,Math.abs(actual.lines[i].baseline-lines[i].baseline));
   maximumAdvance=Math.max(maximumAdvance,Math.abs(actual.lines[i].width-lines[i].width));
  }
  if(!fixture.value) {
   compare(actual.editor.hint,lines.map(line=>line.text).join(''),label+'.localizedHint');
   compare(actual.editor.baseline,lines[0].baseline,label+'.emptyEditorBaseline');
  }
 }
}
const report={count,samples:web.length,nativeAPIs:[26,35],maximumBaseline,maximumAdvance,failures};
fs.writeFileSync(path.join(output,'editor-text-check.json'),JSON.stringify(report,null,2)+'\n');
if(failures.length)throw new Error(failures.join('\n'));
console.log(`Verified ${count} actual textarea/EditText layout and semantic values (${web.length} cases, API26/35, four widths, English/Chinese/Japanese); baseline max ${maximumBaseline.toFixed(4)}px, advance max ${maximumAdvance.toFixed(4)}px, geometry <=1px and text exact. Not glyph-raster or physical IME parity.`);
