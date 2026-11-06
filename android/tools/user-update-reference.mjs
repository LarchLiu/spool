#!/usr/bin/env node
// Original main.tsx user JSX, UserMessage and AttachmentCards through mounted updates.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
if(!path.resolve(process.env.TMPDIR||'').startsWith(path.join(root,'build/tmp')+path.sep))throw new Error('Run through android/tools/run.sh');
const require=createRequire(path.join(root,'web/package.json'));
const {createServer}=await import(require.resolve('vite')),{default:react}=await import(require.resolve('@vitejs/plugin-react'));
const {chromium}=createRequire(path.join(process.env.SPOOL_NODE_MODULES||path.join(root,'web/node_modules'),'__user_updates__.cjs'))('playwright');
const out=path.join(root,'build/artifacts/android-native'),main=await fs.readFile(path.join(root,'web/src/main.tsx'),'utf8');
const section=(start,end)=>{const a=main.indexOf(start),b=main.indexOf(end,a);assert.ok(a>=0&&b>a,'Inspect moved Web source '+start);return main.slice(a,b);};
const turns=section('chat.turns.map((turn) => {','\n              })')+'\n              })',summary=section('function worked(','function makeInputTranscriptParts(');
const pixel='data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=';
const file=(id,name,excerpt='')=>({id,name,media_type:'text/plain',size:12,excerpt});
const photo=(id,name='pixel.png')=>({id,name,media_type:'image/png',size:68,preview_url:pixel});
const long='Stable selectable introduction.\n\n'+'Long prompt line.\n'.repeat(16);
const targets=['row','message','bubble','clip','markdown','paragraph','toggle','footer','copy','author','time','avatar','attachments',...['a','b','c'].flatMap(id=>['card','open','image','placeholder','excerpt','filename','symbol'].map(role=>role+':'+id))];
const fixtures=[
 {id:'text-growth',steps:[{text:long},{click:'toggle'},{select:'paragraph'},{click:'copy'},
  {text:long+'Growing tail.'},{text:'Stable selectable introduction.\n\nShort tail.'},{text:''},{text:long},{click:'copy'}]},
 {id:'overflow-lifetime',steps:[{text:long},{click:'toggle'},{text:'Short nonempty prompt.'},{text:long},{click:'toggle'},
  {text:'   '},{text:long},{text:''},{text:long}]},
 {id:'keyed-files',steps:[{text:'Stable selectable introduction.',files:[file('a','notes.txt','First excerpt.'),file('b','module.ts')]},
  {select:'paragraph'},{files:[file('a','renamed.go','Changed excerpt.'),file('b','module.ts','New excerpt.')]},
  {files:[file('b','module.ts','New excerpt.'),file('a','renamed.go','Changed excerpt.')]},
  {files:[file('b','module.txt'),photo('a')]},{files:[file('b','module.txt'),{...photo('a','renamed.png'),preview_url:pixel+'#updated'}]},
  {files:[file('b','module.txt'),file('a','plain.txt','Restored text.')]},
  {files:[file('b','module.txt')]},{files:[file('b','module.txt'),file('a','plain.txt','Restored text.')]}]},
 {id:'attachment-container',steps:[{text:long,files:[]},{click:'toggle'},{select:'paragraph'},
  {files:[photo('a'),file('c','readme.md')]},{files:[]},{files:[file('c','readme.md')]},{files:[]},{text:long+'\nFinal tail.'}]},
 {id:'metadata',steps:[{text:'Stable selectable introduction.',files:[file('a','note.txt','Stable excerpt.')]},
  {select:'paragraph'},{click:'copy'},{operator:{id:'alice',nickname:'Renamed',avatar:{style:'pixel-art',seed:'two'}}},
  {timestamp:1735787045000},{timestamp:0},{timestamp:1735787045000},{text:'Latest copied prompt.'},{click:'copy'}]},
];
const fonts=path.join(root,'build/cache/android-ui-fonts'),manifest=JSON.parse(await fs.readFile(path.join(fonts,'manifest.json'),'utf8'));
const fontFiles=new Set([...manifest.faces,...manifest.cjk].map(face=>face.file));
const fontCSS=manifest.faces.map(face=>`@font-face{font-family:AndroidRoboto;src:url(/__fonts/${face.file});font-weight:${face.weight};font-style:${face.italic?'italic':'normal'}}`).join('')+manifest.cjk.map(face=>`@font-face{font-family:${face.family};src:url(/__fonts/${face.file});font-weight:400}`).join('')+':root{font-family:AndroidRoboto,AndroidCjkSC,sans-serif}';
const script=`import React,{useState} from 'react';import {createRoot} from 'react-dom/client';import {flushSync} from 'react-dom';
import {TurnResponse} from '/src/tool-activity.tsx';import {UserMessage} from '/src/user-message.tsx';import {MessageFooter} from '/src/message-footer.tsx';
import {turnExchanges,exchangeFiles} from '/src/turn-exchanges.ts';
import {TurnOperatorAvatar,TurnActions} from '/src/turn-identity.tsx';import {turnOperator} from '/src/chat-state.ts';import {AttachmentCards} from '/src/attachments.tsx';import {ApprovalCard} from '/src/permission-picker.tsx';import {translator} from '/src/i18n.ts';import '/src/style.css';
${summary}
const t=translator('en'),locale='en',clock=21000,effortLabel=v=>v,fail=()=>{},setPreview=()=>{};
function Scene(){const [value,setValue]=useState({text:'',files:[],operator:null,timestamp:0}),[epoch,setEpoch]=useState(0);
window.bind=next=>flushSync(()=>setValue(next));window.reset=()=>flushSync(()=>setEpoch(v=>v+1));
const displayUser=value.operator,resolveProfile=()=>displayUser,chat={id:'chat-one',can_control:true,user_id:'alice',transcript:{parts:[{role:'user',kind:'text',text:value.text}]},turns:[{id:'turn-one',user_id:'alice',backend:'codex',status:'completed',started_at:value.timestamp,part_start:0,input_end:1,part_end:1,attachments:value.files}]};
return <main key={epoch} style={{padding:'25px 18px',minHeight:'100vh',background:'var(--elevated)'}}><div className="history-inner" style={{padding:0}}>{${turns}}</div></main>}
createRoot(document.getElementById('root')).render(<Scene/>);`;
let server,browser,apiRequests=0;
const plugin={name:'user-update-reference',resolveId:id=>id==='/__user-updates.tsx'?id:undefined,load:id=>id==='/__user-updates.tsx'?script:undefined,configureServer(instance){instance.middlewares.use(async(req,res,next)=>{
 if(req.url?.startsWith('/api/')){apiRequests++;res.statusCode=500;res.end('No API in synthetic fixture');return;}
 if(req.url?.startsWith('/__fonts/')){const file=req.url.slice('/__fonts/'.length);if(!fontFiles.has(file)){res.statusCode=404;res.end();return;}res.end(await fs.readFile(path.join(fonts,file)));return;}
 if(req.url==='/__user-updates'){res.setHeader('Content-Type','text/html; charset=utf-8');res.end(await instance.transformIndexHtml(req.url,'<!doctype html><html lang="en" data-theme="light"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="/__user-updates.tsx"></script>'));return;}next();
});}};
try{
 server=await createServer({configFile:false,root:path.join(root,'web'),plugins:[plugin,react()],server:{host:'127.0.0.1',port:0},logLevel:'error'});await server.listen();browser=await chromium.launch({channel:'chrome',headless:true});
 const page=await browser.newPage({viewport:{width:360,height:1000},timezoneId:'UTC'});const errors=[];page.on('pageerror',e=>errors.push(String(e)));
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__user-updates`);await page.waitForFunction(()=>window.bind);await page.addStyleTag({content:fontCSS});await page.evaluate(()=>document.fonts.ready);
 await page.evaluate(()=>{window.copied=[];Object.defineProperty(navigator,'clipboard',{value:{writeText:async text=>window.copied.push(text)}});});
 const cases=[];
 for(const fixture of fixtures){
  await page.evaluate(()=>{document.documentElement.dataset.theme='light';});
  const snapshots=await page.evaluate(async({fixture,targets})=>{
   window.reset();let value={text:'',files:[],operator:{id:'alice',nickname:'Original',avatar:{style:'clay',seed:'one'}},timestamp:0},selectedPath;const previous={},snapshots=[];
   const settle=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
   const resolve=key=>{const [role,id]=key.split(':'),row=document.querySelector('.user-row'),message=row?.querySelector('.user-message');
    if(role==='row')return row;if(role==='message')return message;
    const selectors={bubble:'.user-bubble',clip:'.user-bubble-content',markdown:'.user-bubble .markdown',paragraph:'.user-bubble .markdown p',toggle:'.user-message-toggle',footer:'.message-footer-user',copy:'.message-footer-user button',author:'.message-author',time:'time',avatar:'.user-avatar',attachments:'.attachment-cards'};
    if(selectors[role])return row?.querySelector(selectors[role]);
    const index=value.files.findIndex(file=>file.id===id),card=message?.querySelector('.attachment-cards')?.children[index];if(role==='card')return card;
    return card?.querySelector({open:'.attachment-open',image:'img',placeholder:'.file-placeholder,.file-excerpt',excerpt:'pre',filename:'.attachment-name > span',symbol:'.attachment-name > svg'}[role]);
   };
   for(const action of fixture.steps){const {click,select,...patch}=action;value={...value,...patch};window.bind(value);await settle();
    if(click){const node=resolve(click);if(!node)throw new Error('Missing Web click '+click);node.click();await settle();}
    if(select){selectedPath=select;const node=resolve(select),text=document.createTreeWalker(node,NodeFilter.SHOW_TEXT).nextNode(),range=document.createRange();range.setStart(text,1);range.setEnd(text,6);document.getSelection().removeAllRanges();document.getSelection().addRange(range);}
    const nodes={};for(const key of targets){const node=resolve(key),role=key.split(':')[0];nodes[key]={present:!!node,retained:!!node&&node===previous[key]};
     if(node&&['paragraph','excerpt','filename','author','toggle'].includes(role))nodes[key].text=node.textContent;
     if(node&&role==='clip')nodes[key].expanded=node.dataset.expanded==='true';
     if(node&&role==='copy'){nodes[key].label=node.getAttribute('aria-label');nodes[key].disabled=node.disabled;}
     if(node&&role==='card')nodes[key].image=node.classList.contains('image-card');
     if(node&&role==='open'){nodes[key].type=node.tagName==='BUTTON'?'image':'file';nodes[key].name=node.getAttribute('aria-label')||node.title;}
     previous[key]=node;
    }
    const node=selectedPath?resolve(selectedPath):null,selection=document.getSelection();snapshots.push({nodes,selection:selectedPath?{path:selectedPath,present:!!node,inside:!!node&&node.contains(selection.anchorNode),start:selection.anchorOffset,end:selection.focusOffset}:null,copied:window.copied.at(-1)||null});
   }
   document.getSelection().removeAllRanges();return snapshots;
  },{fixture,targets});cases.push({...fixture,snapshots});
  if(['keyed-files','overflow-lifetime'].includes(fixture.id))for(const theme of ['light','dark']){await page.evaluate(theme=>{document.documentElement.dataset.theme=theme;},theme);await page.screenshot({path:path.join(out,`web-user-update-${fixture.id}-${theme}.png`)});}
 }
 assert.deepEqual(errors,[]);assert.equal(apiRequests,0);
 const growth=cases[0].snapshots;assert.equal(growth[4].nodes.bubble.retained,true);assert.equal(growth[4].nodes.footer.retained,true);assert.equal(growth[6].nodes.bubble.present,false);assert.equal(growth[7].nodes.clip.expanded,false);assert.equal(growth[8].copied,long);
 const files=cases[2].snapshots;assert.equal(files[3].nodes['card:a'].retained,true);assert.equal(files[4].nodes['card:a'].retained,true);assert.equal(files[4].nodes['open:a'].retained,false);assert.equal(files[5].nodes['image:a'].retained,true);
 const report={scope:'Actual original main.tsx user JSX/UserMessage/AttachmentCards DOM identity and conditional state; synthetic local Chrome, not Android/device/IME/tsnet/raster parity.',browser:browser.version(),apiRequests,targets,cases};
 await fs.writeFile(path.join(root,'android/app/src/test/resources/web-user-updates.json'),JSON.stringify(report,null,2)+'\n');await fs.writeFile(path.join(out,'web-user-updates.json'),JSON.stringify(report,null,2)+'\n');
 console.log(`Recorded ${cases.length} original Web user/attachment sequences, ${cases.reduce((n,c)=>n+c.steps.length,0)} snapshots and ${targets.length} node roles each; zero API requests.`);
}finally{await browser?.close();await server?.close();}
