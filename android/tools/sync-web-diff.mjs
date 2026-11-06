#!/usr/bin/env bun
// Execute Pierre/jsdiff itself: these fixtures are not another implementation of its rules.
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {diffLayoutContents} from './ui-fixtures.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(root,'web/package.json'));
const pierre=path.join(root,'web/node_modules/@pierre/diffs/dist');
const {processFile}=await import(pathToFileURL(path.join(pierre,'index.js')).href);
const {iterateOverDiff}=await import(pathToFileURL(path.join(pierre,'utils/iterateOverDiff.js')).href);
const sourceFile=path.join(pierre,'utils/renderDiffWithHighlighter.js');
const source=fs.readFileSync(sourceFile,'utf8').replace(/(from |import )"([^"]+)"/g,(_,prefix,id)=>
 `${prefix}${JSON.stringify(id.startsWith('.')?new URL(id,pathToFileURL(sourceFile)).href:pathToFileURL(require.resolve(id)).href)}`);
const {computeLineDiffDecorations}=await import('data:text/javascript;base64,'+Buffer.from(source+'\nexport {computeLineDiffDecorations};').toString('base64'));
function ranges(old,next) {
 const a=[],b=[];
 computeLineDiffDecorations({deletionLine:old,additionLine:next,deletionLineIndex:0,additionLineIndex:0,
  deletionDecorations:a,additionDecorations:b,lineDiffType:'word-alt',maxLineDiffLength:1000});
 const list=values=>values.map(value=>[value.start.character,value.end.character]);
 return {old:list(a),next:list(b)};
}
const pairs=[
 ['const old = before + same + end;','const next = after + same + done;'],
 ['const first = old; const second = old;','const first = new; const second = new;'],
 ['ab X cd','ab Y cd'],['a b c','a x b y c'],['aaa aa aaa','aa aaa aa'],
 ['one.two(three, four);','one.two(five, four);'],['\told\tstable\tend','\tnew\tstable\tlast'],
 ['hello 😀 world 🤖!','hello 🤖 world 😀!'],['你好原生界面','您好原生页面'],['café bientôt Ångström','café demain Ångstrom'],
 ['a\u00a0b\u2003c\ufeffd','a b\u202fc\tD'],['a\u0085b\u180ec','a b c'],
 ['foo\nbar\r\nbaz\r','foo\nbee\r\nbaz\r'],['x\r\n','y\r\n'],['',''],['','a'],['a',''],[' ','\t'],
 ['x'.repeat(1000),'y'.repeat(1000)],['x'.repeat(1001),'y'.repeat(1001)],
 ['\n',''],['😀'.repeat(500),'🤖'.repeat(500)],['😀'.repeat(501),'🤖'.repeat(501)],
];
let seed=0x51a17;
const random=limit=>{seed=(Math.imul(seed,1664525)+1013904223)>>>0;return seed%limit;};
const words=['foo','bar','42',' ','  ','\t','(',')','.',',','_','é','你好','😀','\u00a0','\u200b'];
for(let i=0;i<240;i++) {
 const a=Array.from({length:random(25)},()=>words[random(words.length)]),b=[...a];
 for(let edit=0,n=1+random(6);edit<n;edit++)b.splice(random(b.length+1),random(3),words[random(words.length)]);
 pairs.push([a.join(''),b.join('')]);
}
function patch(old,next,suffix='') {
 return `diff --git a/sample.txt b/sample.txt\n@@ -1,${old.length} +1,${next.length} @@\n`+
  old.map(line=>'-'+line+'\n').join('')+next.map(line=>'+'+line+'\n').join('')+suffix;
}
const inputs=[
 {name:'added-prologue',text:patch(['const old = 42;','return old;'],['// A new prologue','const next = 42;','return next;'])},
 {name:'deleted-prologue',text:patch(['// remove me','const old = 42;','return old;'],['const next = 42;','return next;'])},
 {name:'surplus-before-and-after',text:patch(['return left;'],['unrelated();','return right;','trailer();'])},
 {name:'weak-similarity-keeps-position',text:patch(['alpha'],['zeta','beta'])},
 {name:'whitespace-stripped-alignment',text:patch(['\treturn first;','\treturn second;'],['// prologue',' return FIRST;','\u00a0return second;'])},
 {name:'unicode-similarity',text:patch(['😀 return first;','你好 second;'],['unrelated();','🤖 return first;','您好 second;'])},
 {name:'context-is-a-barrier',text:'diff --git a/sample.txt b/sample.txt\n@@ -1,4 +1,6 @@\n-a\n+new();\n+a\n middle\n-b\n+other();\n+b\n end\n'},
 {name:'multiple-hunks',text:patch(['return first;'],['extra();','return next;'])+'@@ -20,1 +21,2 @@\n-return last;\n+another();\n+return latest;\n'},
 {name:'at-comparison-cap',text:patch(Array.from({length:64},(_,i)=>`return old${i};`),['unrelated();',...Array.from({length:64},(_,i)=>`return new${i};`),...Array.from({length:62},()=> 'extra();')])},
 {name:'above-comparison-cap',text:patch(Array.from({length:64},(_,i)=>`return old${i};`),['unrelated();',...Array.from({length:64},(_,i)=>`return new${i};`),...Array.from({length:63},()=> 'extra();')])},
];
for(let i=0;i<60;i++) {
 const n=1+random(8),m=1+random(8),offset=random(Math.abs(n-m)+1);
 const a=Array.from({length:n},(_,j)=>j<offset&&n>m?'unrelated();':`value(${j-(n>m?offset:0)}, old);`);
 const b=Array.from({length:m},(_,j)=>j<offset&&m>n?'prologue();':`value(${j-(m>n?offset:0)}, next);`);
 inputs.push({name:`seeded-${i}`,text:patch(a,b)});
}
const layouts=inputs.map(input=>{
 const file=processFile(input.text,{throwOnError:true});if(!file)throw new Error(input.name);
 const expected=[];
 iterateOverDiff({diff:file,diffStyle:'split',expandedHunks:true,callback:({additionLine,deletionLine,type})=>{
  const cell=(line,add)=>line==null?null:{text:(add?file.additionLines:file.deletionLines)[line.lineIndex],number:line.lineNumber,kind:type==='context'?' ':add?'+':'-'};
  expected.push({old:cell(deletionLine,false),next:cell(additionLine,true)});
 }});
 return {...input,expected};
});
const value={diffLayoutContents,pierre:JSON.parse(fs.readFileSync(path.join(pierre,'../package.json'),'utf8')).version,
 jsdiff:JSON.parse(fs.readFileSync(path.join(root,'web/node_modules/diff/package.json'),'utf8')).version,
 words:pairs.map(([old,next])=>({old,next,expected:ranges(old,next)})),layouts};
const target=path.join(root,'android/app/src/test/resources/web-diff.json'),contents=JSON.stringify(value,null,2)+'\n';
if(process.argv.includes('--check')) {
 if(!fs.existsSync(target)||fs.readFileSync(target,'utf8')!==contents)throw new Error('Web/native diff drift. Run bun android/tools/sync-web-diff.mjs');
}else fs.writeFileSync(target,contents);
console.log(`${process.argv.includes('--check')?'Verified':'Synchronized'} ${value.words.length} word-alt pairs and ${layouts.length} actual Pierre split layouts.`);
