#!/usr/bin/env node
// Original Web ToolActivityList conditional mounts, keys and source state through live updates.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
if(!path.resolve(process.env.TMPDIR||'').startsWith(path.join(root,'build/tmp')+path.sep))throw new Error('Run through android/tools/run.sh');
const require=createRequire(path.join(root,'web/package.json')),{createServer}=await import(require.resolve('vite')),{default:react}=await import(require.resolve('@vitejs/plugin-react'));
const {chromium}=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__activity_updates__.cjs'))('playwright');
const output=path.join(root,'build/artifacts/android-native');await fs.mkdir(output,{recursive:true});
const use=(id,name,text)=>({role:'assistant',kind:'tool_use',id,name,text});
const result=(id,text)=>({role:'tool',kind:'tool_result',id,text});
const thought=text=>({role:'assistant',kind:'thinking',id:'thought',text});
const message=(id,text)=>({role:'assistant',kind:'text',id,text});
const read=id=>use('read-'+id,'read_file',JSON.stringify({file_path:`src/file${id}.ts`}));
const shell=use('shell','exec','echo hello'),patch='*** Begin Patch\n*** Update File: src/a.ts\n@@\n-old\n+new\n*** End Patch',patch2=patch.replace('+new','+new\n+second');
const tools=[read(1),result('result1','Stable output line.\n'.repeat(24))];
const targetRoles=['entry','group','item','item-toggle','detail','input','input-text','output','output-text','raw-toggle-input','raw-input','raw-toggle-output','raw-output'];
const targets=['work','list',...Array.from({length:4},(_,g)=>targetRoles.flatMap(role=>role==='entry'||role==='group'?[`${role}:${g}`]:[`${role}:${g}:0`,`${role}:${g}:1`])).flat()];
const fixtures=[
 {id:'result-arrival',steps:[{parts:[read(1)],live:true},{click:'item:0:0'},{select:'input-text:0:0'},
  {parts:tools},{parts:[read(1),result('result1','Stable output line.\n'.repeat(24)+'New tail.')]},
  {parts:[...tools,shell]},{parts:[...tools,{...shell,name:'bash'}]}]},
 {id:'thinking-growth',steps:[{parts:[thought('Stable thought paragraph.\n\nGrowing thought.'),shell],live:true},
  {click:'item:0:0'},{select:'input-text:0:0'},{parts:[thought('Stable thought paragraph.\n\nGrowing thought expands.'),shell]},
  {parts:[thought('Stable thought paragraph.\n\nGrowing thought expands.'),{...thought('Second adjacent thought.'),id:'thought2'},shell]},
  {click:'item:0:1'},{parts:[thought('Stable thought paragraph.\n\nGrowing thought expands.'),{...thought('Second adjacent thought grows.'),id:'thought2'},shell]}]},
 {id:'group-growth',steps:[{parts:[...tools,read(2),result('result2','Second output.'),shell],live:true},
  {click:'item:0:0'},{select:'input-text:0:0'},
  {parts:[...tools,read(2),result('result2','Second output.'),read(3),shell]},
  {click:'group:0'},{parts:[...tools,read(2),read(3),read(4),shell]},
  {parts:[...tools,read(2),read(3),read(4),shell,message('progress','A new progress paragraph.')]},
  {click:'group:0'},{click:'group:0'}]},
 {id:'raw-lifetimes',steps:[{parts:[use('edit','apply_patch',patch),shell],live:true},{click:'item:0:0'},
  {click:'raw-toggle-input:0:0'},{select:'raw-input:0:0'},
  {parts:[use('edit','apply_patch',patch),shell,result('shell-result','Completed command.')]},
  {click:'raw-toggle-input:0:0'},{click:'raw-toggle-input:0:0'},
  {click:'work'},{parts:[use('edit','apply_patch',patch),shell,result('shell-result','Updated command.')]},{click:'work'},
  {click:'raw-toggle-input:0:0'},{click:'item:0:0'},{click:'item:0:0'},
  {live:false},{click:'work'},{live:true}]},
 {id:'diff-key',steps:[{parts:[use('edit','apply_patch',patch),result('edit-result',patch)],live:true},
  {click:'item:0:0'},{click:'raw-toggle-input:0:0'},{click:'raw-toggle-output:0:0'},{select:'raw-output:0:0'},
  {parts:[use('edit','apply_patch',patch2),result('edit-result',patch)]},
  {parts:[use('edit','apply_patch',patch2),result('edit-result',patch2)]},
  {parts:[use('edit','apply_patch','plain input'),result('edit-result',patch2)]},
  {parts:[use('edit','apply_patch','plain input grows'),result('edit-result',patch2)]}]},
 {id:'same-key-kind',steps:[{parts:[use('one','custom','plain selectable input'),result('result','Stable result.')],live:true},{click:'item:0:0'},
  {select:'output-text:0:0'},{parts:[use('one','read_file','plain selectable input'),result('result','Stable result.')]},
  {parts:[{...thought('plain selectable input'),id:'one'},result('result','Stable result.')]},
  {parts:[use('one','custom',''),result('result','Stable result.')]},
  {parts:[use('one','custom','Restored input.'),result('result','Stable result.')]},
  {parts:[use('one','custom','Restored input.')]},{parts:[]},{parts:[use('one','custom','Restored input.')]},
  {click:'work'}]},
 {id:'kind-source-lifetime',steps:[{parts:[use('one','apply_patch',patch)],live:true},{click:'item:0:0'},
  {click:'raw-toggle-input:0:0'},{select:'raw-input:0:0'},
  {parts:[{...thought(patch),id:'one'}]},{parts:[use('one','apply_patch',patch)]},
  {click:'raw-toggle-input:0:0'},{parts:[use('one','custom','Plain replacement input.')]},
  {parts:[use('one','apply_patch',patch)]}]},
 {id:'progress-keys',steps:[{parts:[message('p1','Stable progress paragraph.\n\nGrowing progress.'),shell],live:true},
  {select:'entry:0'},{parts:[message('p1','Stable progress paragraph.\n\nGrowing progress expands.'),shell]},
  {parts:[message('p2','Stable progress paragraph.\n\nGrowing progress expands.'),shell]},
  {parts:[message('p2','Stable progress paragraph.\n\nGrowing progress expands.'),message('p3','Second progress.'),shell]},
  {parts:[message('p3','Second progress.'),shell]}]},
 {id:'terminal-groups',steps:[{parts:[...tools,read(2),shell],live:true},{click:'item:0:0'},{click:'group:0'},
  {live:false},{click:'work'},{click:'group:0'},{select:'input-text:0:0'},
  {parts:[...tools,read(2),shell,result('done','Done.')]},{parts:[...tools,read(2),shell,result('done','Done.'),message('p','Terminal progress.')]},
  {live:true}]},
];
const script=`import React,{useState} from 'react';import {createRoot} from 'react-dom/client';import {flushSync} from 'react-dom';
import {ToolActivityList} from '/src/tool-activity.tsx';import {translator} from '/src/i18n.ts';import '/src/style.css';
const t=translator('en');function Scene(){const [value,setValue]=useState({parts:[],live:true}),[epoch,setEpoch]=useState(0);
window.bind=next=>flushSync(()=>setValue(next));window.reset=()=>flushSync(()=>setEpoch(value=>value+1));
return <div style={{padding:'25px 18px',minHeight:'100vh',background:'var(--elevated)'}}><ToolActivityList key={epoch} parts={value.parts} live={value.live} backend="codex" t={t} summary="Working for 5s"/></div>}
createRoot(document.getElementById('root')).render(<Scene/>);`;
let server,browser,apiRequests=0;
const plugin={name:'activity-update-reference',resolveId:id=>id==='/__activity-updates.tsx'?id:undefined,load:id=>id==='/__activity-updates.tsx'?script:undefined,configureServer(instance){instance.middlewares.use(async(req,res,next)=>{
 if(req.url?.startsWith('/api/')){apiRequests++;res.statusCode=500;res.end('No API in local fixture');return;}
 if(req.url==='/__activity-updates'){res.setHeader('Content-Type','text/html; charset=utf-8');res.end(await instance.transformIndexHtml(req.url,'<!doctype html><meta charset="utf-8"><div id="root"></div><script type="module" src="/__activity-updates.tsx"></script>'));return;}next();
});}};
try{
 server=await createServer({configFile:false,root:path.join(root,'web'),plugins:[plugin,react()],server:{host:'127.0.0.1',port:0}});await server.listen();browser=await chromium.launch({channel:'chrome',headless:true});
 const page=await browser.newPage({viewport:{width:360,height:1000}});await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__activity-updates`);await page.waitForFunction(()=>typeof window.bind==='function');
 const cases=[];
 for(const fixture of fixtures){
  await page.evaluate(()=>{document.documentElement.dataset.theme='light';});
  const snapshots=await page.evaluate(async({fixture,targets})=>{
   window.reset();let parts=[],live=true,selectedPath;const previous={},snapshots=[],settle=()=>new Promise(resolve=>setTimeout(resolve,0));
   const entry=index=>document.querySelector('.tool-activity-list')?.children[index];
   const item=(g,i)=>{const group=entry(g);return group?.classList.contains('tool-activity-item')?(Number(i)===0?group:null):group?.querySelector('.tool-activity-items')?.children[i];};
   const resolve=key=>{
    const [role,g,i]=key.split(':');if(role==='work')return document.querySelector('.work-label');if(role==='list')return document.querySelector('.tool-activity-list');
    const group=entry(g);if(role==='entry')return group;if(role==='group')return group?.classList.contains('tool-activity')?group.querySelector('.tool-activity-summary'):null;
    const value=item(g,i);if(role==='item')return value;if(role==='item-toggle')return value?.querySelector('.tool-activity-item-summary');
    const detail=value?.querySelector('.tool-detail');if(role==='detail')return detail;
    const block=[...detail?.querySelectorAll(':scope > .tool-detail-block')||[]].find(block=>{const label=block.querySelector('.tool-detail-label')?.textContent;return role.includes('output')?label==='Result':label!=='Result';});
    if(role==='input'||role==='output')return block?.querySelector(':scope > .markdown,:scope > pre,:scope > .tool-diff');
    if(role.endsWith('-text'))return block?.querySelector(':scope > pre,:scope > .markdown p');
    if(role.startsWith('raw-toggle'))return block?.querySelector('.tool-result-source > summary');
    if(role.startsWith('raw-'))return block?.querySelector('.tool-result-source > pre');
   };
   const firstText=node=>node?.tagName==='PRE'?node.firstChild:document.createTreeWalker(node||document.createElement('div'),NodeFilter.SHOW_TEXT).nextNode();
   for(let step=0;step<fixture.steps.length;step++){
    const action=fixture.steps[step];if(action.parts)parts=action.parts;if(action.live!==undefined)live=action.live;
    if(action.parts||action.live!==undefined){window.bind({parts,live});await settle();}
    if(action.click){const node=resolve(action.click);if(!node)throw new Error('Missing Web click '+fixture.id+'/'+step+'/'+action.click);(node.matches('button,summary')?node:node.querySelector('button')).click();await settle();}
    if(action.select){selectedPath=action.select;const node=resolve(selectedPath),text=firstText(node);if(!text||text.length<8)throw new Error('Missing selectable text '+selectedPath);const range=document.createRange();range.setStart(text,1);range.setEnd(text,6);document.getSelection().removeAllRanges();document.getSelection().addRange(range);}
    const nodes={};for(const key of targets){const node=resolve(key),role=key.split(':')[0];nodes[key]={present:!!node,retained:!!node&&node===previous[key]};
     if(node&&(role.endsWith('-text')||role.startsWith('raw-')&&!role.startsWith('raw-toggle')))nodes[key].text=node.textContent;
     if(node&&(role==='group'||role==='item-toggle'||role==='work')){nodes[key].label=node.textContent.replace(/\s+/g,' ').trim();nodes[key].expanded=node.getAttribute('aria-expanded');}
     if(node&&role.startsWith('raw-toggle'))nodes[key].expanded=node.parentElement.open;
     if(node&&(role==='input'||role==='output'))nodes[key].type=node.classList.contains('markdown')?'markdown':node.tagName==='PRE'?'raw':'diff';previous[key]=node;
    }
    const node=selectedPath?resolve(selectedPath):null,selection=document.getSelection();
    snapshots.push({nodes,selection:selectedPath?{path:selectedPath,present:!!node,inside:!!node&&node.contains(selection.anchorNode),start:selection.anchorOffset,end:selection.focusOffset,text:selection.toString()}:null});
   }
   document.getSelection().removeAllRanges();return snapshots;
  },{fixture,targets});
  await fs.writeFile(path.join(output,`web-activity-update-${fixture.id}.json`),JSON.stringify(snapshots,null,2)+'\n');cases.push({...fixture,snapshots});
  if(['group-growth','diff-key'].includes(fixture.id))for(const theme of ['light','dark']){
   await page.evaluate(theme=>{document.documentElement.dataset.theme=theme;},theme);
   await page.screenshot({path:path.join(output,`web-activity-update-${fixture.id}-${theme}.png`)});
  }
 }
 assert.equal(apiRequests,0);
 const raw=cases.find(c=>c.id==='raw-lifetimes').snapshots;
 assert.equal(raw[4].nodes['input:0:0'].retained,true);assert.equal(raw[5].nodes['raw-input:0:0'].retained,true);assert.equal(raw[6].nodes['raw-input:0:0'].retained,true);
 assert.equal(raw[9].nodes['raw-toggle-input:0:0'].expanded,false);assert.equal(raw[12].nodes['raw-toggle-input:0:0'].expanded,false);
 const growth=cases.find(c=>c.id==='group-growth').snapshots;assert.equal(growth[3].nodes['input:0:0'].retained,true);assert.equal(growth[5].nodes['group:0'].expanded,'false');assert.equal(growth[6].nodes['group:0'].expanded,'true');
 const diff=cases.find(c=>c.id==='diff-key').snapshots;assert.equal(diff[5].nodes['input:0:0'].retained,false);assert.equal(diff[5].nodes['output:0:0'].retained,true);assert.equal(diff[5].nodes['raw-toggle-output:0:0'].expanded,true);
 const report={scope:'Actual original ToolActivityList/ActivityDetail/ToolPatch mounted DOM updates and conditional source lifetimes. Chrome synthetic local fixture, not Android/device/tsnet or raster parity.',browser:browser.version(),apiRequests,targets,cases};
 await fs.writeFile(path.join(root,'android/app/src/test/resources/web-activity-updates.json'),JSON.stringify(report,null,2)+'\n');await fs.writeFile(path.join(output,'web-activity-updates.json'),JSON.stringify(report,null,2)+'\n');
 console.log(`Recorded ${cases.length} actual Web tool-update sequences, ${cases.reduce((n,c)=>n+c.steps.length,0)} snapshots and ${targets.length} tracked node roles each; zero API requests.`);
}finally{await browser?.close();await server?.close();}
