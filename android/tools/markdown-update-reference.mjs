#!/usr/bin/env bun
// Observe original ReactMarkdown/TurnResponse updates, not reconstructed HTML.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
if(!path.resolve(process.env.TMPDIR||'').startsWith(path.join(root,'build/tmp')+path.sep))throw new Error('Run through android/tools/run.sh');
const require=createRequire(path.join(root,'web/package.json'));
const {createServer}=await import(require.resolve('vite'));
const {default:react}=await import(require.resolve('@vitejs/plugin-react'));
const {chromium}=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__markdown_update__.cjs'))('playwright');
const out=path.join(root,'build/artifacts/android-native');await fs.mkdir(out,{recursive:true});
const target=(tag,index=0,selector=tag)=>({tag,index,selector});
const fixtures=[
 {id:'changed-text',select:'first',selectionReset:true,targets:{first:target('p')},sources:['Streaming answer.','Streaming answer grows.']},
 {id:'paragraphs',select:'first',targets:{first:target('p'),second:target('p',1),third:target('p',2)},sources:[
  'Keep selectable introduction.\n\nStreaming answer.',
  'Keep selectable introduction.\n\nStreaming answer grows.',
  'Keep selectable introduction.\n\nStreaming answer grows.\n\nNew third paragraph.',
  'Keep selectable introduction.\n\nStreaming answer grows.']},
 {id:'tag-keys',select:'first',selectionMoves:[1],targets:{first:target('p'),second:target('p',1),heading:target('h2'),replacement:target('h3')},sources:[
  'Keep first paragraph.\n\nKeep second paragraph.',
  '## Inserted heading\n\nKeep first paragraph.\n\nKeep second paragraph.',
  '## Changed heading\n\nKeep first paragraph.\n\nKeep second paragraph.',
  '### New heading type\n\nKeep first paragraph.\n\nKeep second paragraph.']},
 {id:'quote',select:'first',targets:{quote:target('blockquote'),first:target('p'),second:target('p',1)},sources:[
  '> Keep quote selection.\n>\n> Streaming quote.',
  '> Keep quote selection.\n>\n> Streaming quote grows.',
  '> Keep quote selection.\n>\n> Streaming quote grows.\n>\n> Third quote paragraph.']},
 {id:'list',select:'first',targets:{list:target('ul'),row:target('li'),first:target('p'),second:target('p',1)},sources:[
  '- Keep list selection.\n\n- Streaming list item.',
  '- Keep list selection.\n\n- Streaming list item grows.',
  '- Keep list selection.\n\n- Streaming list item grows.\n\n- Added item.']},
 {id:'tasks',targets:{list:target('ul'),row:target('li'),check:target('input'),secondCheck:target('input',1)},sources:[
  '- [ ] First task.\n- [x] Second task.',
  '- [x] First task.\n- [x] Second task.',
  '- [x] First task.\n- [ ] Second task.\n- [ ] Third task.']},
 {id:'table',select:'first',targets:{table:target('table'),row:target('tr',1),first:target('td'),second:target('td',2),head:target('th')},sources:[
  '| Name | Value |\n| --- | ---: |\n| Keep cell selection. | 1 |\n| Streaming cell. | 2 |',
  '| Name | Value |\n| --- | ---: |\n| Keep cell selection. | 1 |\n| Streaming cell grows. | 2 |',
  '| Name | Value |\n| :---: | ---: |\n| Keep cell selection. | 1 |\n| Streaming cell grows. | 2 |\n| New row. | 3 |']},
 {id:'code',select:'first',targets:{first:target('p'),code:target('pre')},sources:[
  'Keep code introduction.\n\n```js\nlet value = 1;',
  'Keep code introduction.\n\n```js\nlet value = 1;\nvalue++;',
  'Keep code introduction.\n\n```ts\nlet value = 1;\nvalue++;\n```']},
 {id:'reference',select:'first',targets:{first:target('p'),second:target('p',1)},sources:[
  '[Selectable label][ref]\n\nKeep tail.\n\n[ref]: https://example.com/old',
  '[Selectable label][ref]\n\nKeep tail.\n\n[ref]: https://example.com/new']},
 {id:'inline-format',select:'second',targets:{first:target('p'),second:target('p',1)},sources:[
  '**Formatting sample**\n\nKeep stable selection.',
  '*Formatting sample*\n\nKeep stable selection.',
  '~~Formatting sample~~\n\nKeep stable selection.',
  '`Formatting sample`\n\nKeep stable selection.',
  '[Formatting sample](https://example.com/format)\n\nKeep stable selection.']},
 {id:'chinese-format',select:'second',targets:{first:target('p'),second:target('p',1)},sources:[
  '**需要实时更新的中文样式。**\n\n保留这段中文文字选区。',
  '*需要实时更新的中文样式。*\n\n保留这段中文文字选区。',
  '[需要实时更新的中文样式。](https://example.com/zh)\n\n保留这段中文文字选区。']},
 {id:'japanese-format',select:'second',targets:{first:target('p'),second:target('p',1)},sources:[
  '**更新する日本語の書式です。**\n\nこちらの文字選択を保持します。',
  '*更新する日本語の書式です。*\n\nこちらの文字選択を保持します。',
  '~~更新する日本語の書式です。~~\n\nこちらの文字選択を保持します。']},
 {id:'reparse',select:'first',targets:{first:target('p'),second:target('p',1),table:target('table')},sources:[
  'Keep introduction.\n\n| Name | Value |',
  'Keep introduction.\n\n| Name | Value |\n| --- | --- |\n| Cell | 1 |',
  'Keep introduction.\n\nPlain paragraph again.']},
];
const script=`
import React,{useState} from 'react';import {createRoot} from 'react-dom/client';import {flushSync} from 'react-dom';
import {TurnResponse} from '/src/tool-activity.tsx';import {translator} from '/src/i18n.ts';import '/src/style.css';
const t=translator('en');
function Scene(){const [source,setSource]=useState('Initial'),[epoch,setEpoch]=useState(0);window.bind=value=>flushSync(()=>setSource(value));window.reset=()=>flushSync(()=>setEpoch(value=>value+1));return <div key={epoch} className="assistant-text"><TurnResponse parts={[{role:'assistant',kind:'text',phase:'final_answer',text:source}]} backend="codex" t={t} running={false} summary="Completed" locale="en"/></div>}
createRoot(document.getElementById('root')).render(<Scene/>);`;
let server,browser,requests=0;
const plugin={name:'markdown-update-reference',resolveId:id=>id==='/__markdown-update.tsx'?id:undefined,load:id=>id==='/__markdown-update.tsx'?script:undefined,configureServer(instance){instance.middlewares.use(async(req,res,next)=>{
 if(req.url?.startsWith('/api/')){requests++;res.statusCode=500;res.end('No API in this local fixture');return;}
 if(req.url==='/__markdown-update'){res.setHeader('Content-Type','text/html; charset=utf-8');res.end(await instance.transformIndexHtml(req.url,'<!doctype html><meta charset="utf-8"><div id="root"></div><script type="module" src="/__markdown-update.tsx"></script>'));return;}next();
});}};
try{
 server=await createServer({configFile:false,root:path.join(root,'web'),plugins:[plugin,react()],server:{host:'127.0.0.1',port:0}});await server.listen();
 const address=server.httpServer.address();browser=await chromium.launch({channel:'chrome',headless:true});const page=await browser.newPage({viewport:{width:360,height:800}});
 await page.goto(`http://127.0.0.1:${address.port}/__markdown-update`);await page.waitForFunction(()=>typeof window.bind==='function');
 await page.evaluate(()=>{window.copies=[];Object.defineProperty(navigator,'clipboard',{value:{writeText:async value=>window.copies.push(value)},configurable:true});});
 const cases=[];
 for(const fixture of fixtures){
  const snapshots=await page.evaluate(async fixture=>{
   const root=document.querySelector('.markdown'),previous={};let footer;const snapshots=[];
   for(let step=0;step<fixture.sources.length;step++){
    window.bind(fixture.sources[step]);await Promise.resolve();
    const markdown=document.querySelector('.markdown'),nodes={};
    for(const [name,target] of Object.entries(fixture.targets)){
     const node=markdown.querySelectorAll(target.selector)[target.index],old=previous[name];
     nodes[name]={present:!!node,retained:!!node&&node===old,text:node?.textContent??'',checked:node?.tagName==='INPUT'?node.checked:null};previous[name]=node;
    }
    const nextFooter=document.querySelector('.message-footer');
    const selection=document.getSelection(),selected=fixture.select?previous[fixture.select]:null;
    const selectionState={inside:!!selected&&selected.contains(selection.anchorNode),start:selection.anchorOffset,end:selection.focusOffset,text:selection.toString()};
    const copiedLabel=nextFooter.querySelector('button').getAttribute('aria-label');
    nextFooter.querySelector('button').click();await new Promise(resolve=>setTimeout(resolve,0));
    snapshots.push({nodes,selection:selectionState,footerRetained:step>0&&footer===nextFooter,copyFeedbackRetained:step>0&&copiedLabel==='Message copied',copied:window.copies.at(-1)});footer=nextFooter;
    if(selected){const walker=document.createTreeWalker(selected,NodeFilter.SHOW_TEXT),text=walker.nextNode();const range=document.createRange();range.setStart(text,1);range.setEnd(text,6);selection.removeAllRanges();selection.addRange(range);}
   }
   document.getSelection().removeAllRanges();return snapshots;
  },fixture);
  await fs.writeFile(path.join(out,`web-markdown-update-${fixture.id}.json`),JSON.stringify(snapshots,null,2)+'\n');
  for(let i=1;i<snapshots.length;i++){assert.equal(snapshots[i].footerRetained,true,fixture.id);assert.equal(snapshots[i].copyFeedbackRetained,true,fixture.id);if(fixture.select){const moved=fixture.selectionMoves?.includes(i),reset=fixture.selectionReset||moved;assert.equal(snapshots[i].selection.inside,!moved,fixture.id);assert.equal(snapshots[i].selection.start,reset?0:1,fixture.id);assert.equal(snapshots[i].selection.end,reset?0:6,fixture.id);}assert.equal(snapshots[i].copied,fixture.sources[i]);}
  cases.push({...fixture,snapshots});
 }
 const copyFeedback=await page.evaluate(async()=>{
  window.reset();window.bind('First copied source');const labels=[],wait=ms=>new Promise(resolve=>setTimeout(resolve,ms)),label=()=>document.querySelector('.message-copy').getAttribute('aria-label');
  document.querySelector('.message-copy').click();await wait(0);labels.push(label());await wait(1000);
  window.bind('Latest copied source');document.querySelector('.message-copy').click();await wait(0);labels.push(label());await wait(1100);labels.push(label());
  return {labels,latestCopied:window.copies.at(-1),elapsedMs:2100,scope:'Repeated same-state copy retains the original effect deadline; source rebind does not reset it.'};
 });
 assert.deepEqual(copyFeedback.labels,['Message copied','Message copied','Copy message']);assert.equal(copyFeedback.latestCopied,'Latest copied source');
 assert.equal(requests,0);const report={scope:'Actual original TurnResponse/MarkdownMessage DOM identity, selection and copy through source updates. Chrome local fixture, not device/raster/tsnet parity.',browser:browser.version(),apiRequests:requests,cases,copyFeedback};
 await fs.writeFile(path.join(root,'android/app/src/test/resources/web-markdown-updates.json'),JSON.stringify(report,null,2)+'\n');await fs.writeFile(path.join(out,'web-markdown-updates.json'),JSON.stringify(report,null,2)+'\n');
 console.log(`Recorded ${cases.length} actual Web Markdown update sequences, ${cases.reduce((n,f)=>n+f.sources.length,0)} snapshots; unchanged selections, tag keys, reference destinations and footer copy feedback.`);
}finally{await browser?.close();await server?.close();}
