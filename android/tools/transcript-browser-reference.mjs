#!/usr/bin/env node
// Execute actual main.tsx keyed turn JSX, latest callback/scroll handler and original components.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const require = createRequire(path.join(root, 'web/package.json'));
const {createServer} = await import(require.resolve('vite'));
const {default:reactPlugin} = await import(require.resolve('@vitejs/plugin-react'));
const {chromium} = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root,'web/node_modules'),'__transcript__.cjs'))('playwright');
const output = path.join(root,'build/artifacts/android-native');
const main = await fs.readFile(path.join(root,'web/src/main.tsx'),'utf8');
const section = (start,end) => { const at=main.indexOf(start), stop=main.indexOf(end,at); assert.ok(at>=0&&stop>at,`Inspect moved Web source: ${start}`);return main.slice(at,stop); };
const turns = section('chat.turns.map((turn) => {','\n              })') + '\n              })';
const latest = section('  function latest() {','  function fail(');
const scroll = section('          onScroll={() => {','\n        >');
const button = section('          {showLatest && (','          {error && (');
const summary = section('function worked(','function makeInputTranscriptParts(');
const fonts = path.join(root,'build/cache/android-ui-fonts');
const manifest = JSON.parse(await fs.readFile(path.join(fonts,'manifest.json'),'utf8'));
const fontFiles = new Set([...manifest.faces,...manifest.cjk].map(face=>face.file));
const fontCSS = manifest.faces.map(face=>`@font-face{font-family:AndroidRoboto;src:url(/__fonts/${face.file});font-weight:${face.weight};font-style:${face.italic?'italic':'normal'}}`).join('')
  + manifest.cjk.map(face=>`@font-face{font-family:${face.family};src:url(/__fonts/${face.file});font-weight:400}`).join('')
  + ':root{font-family:AndroidRoboto,AndroidCjkSC,sans-serif}:lang(ja){font-family:AndroidRoboto,AndroidCjkJP,sans-serif}';
const script = `
import React,{useRef,useState} from 'react';
import {createRoot} from 'react-dom/client';
import {flushSync} from 'react-dom';
import {ArrowDown} from 'lucide-react';
import {TurnResponse} from '/src/tool-activity.tsx';
import {turnExchanges,exchangeFiles} from '/src/turn-exchanges.ts';
import {UserMessage} from '/src/user-message.tsx';
import {MessageFooter} from '/src/message-footer.tsx';
import {TurnOperatorAvatar,TurnActions} from '/src/turn-identity.tsx';
import {turnOperator} from '/src/chat-state.ts';
import {AttachmentCards} from '/src/attachments.tsx';
import {ApprovalCard} from '/src/permission-picker.tsx';
import {translator} from '/src/i18n.ts';
import '/src/style.css';
${summary}
const params=new URL(location.href).searchParams,locale=params.get('locale')||'en',t=translator(locale),clock=21000,effortLabel=v=>v;
document.documentElement.lang=locale; document.documentElement.dataset.theme=params.get('theme')||'light';
const initial={id:'chat-one',user_id:'alice',can_control:true,transcript:{parts:[
 {role:'user',kind:'text',text:'Long prompt\\n'.repeat(16)},
 {role:'assistant',kind:'text',phase:'final_answer',text:'Selectable answer 0'},
 {role:'user',kind:'text',text:'Next prompt'},
 {role:'assistant',kind:'text',phase:'final_answer',text:'Selectable answer 1'}]},turns:[
 {id:'turn-0',user_id:'alice',backend:'codex',status:'completed',started_at:1000,finished_at:2000,part_start:0,input_end:1,part_end:2},
 {id:'turn-1',user_id:'alice',backend:'codex',status:'running',started_at:1000,part_start:2,input_end:3,part_end:4}]};
let displayUser={id:'alice',nickname:'Original',avatar:{style:'clay',seed:'one'}},resolveProfile=()=>displayUser;
const setPreview=()=>{},fail=()=>{};
function Scene(){
 const [chat,setChat]=useState(initial),[showLatest,setShowLatest]=useState(false),history=useRef(null),follow=useRef(true);
 ${latest}
 window.fixture={chat,follow,showLatest,update:value=>flushSync(()=>setChat(value)),profile:person=>{displayUser=person;flushSync(()=>setChat({...chat}));},show:()=>flushSync(()=>setShowLatest(true)),latest,history};
 return <main className="workspace" style={{height:'100vh'}}><div className="history" ref={history} ${scroll}>
 <div className="history-inner">{${turns}}</div></div><footer className="composer-wrap" style={{height:108}}>${button}<textarea aria-label="Draft" defaultValue="Draft"/></footer></main>;
}
createRoot(document.getElementById('root')).render(<Scene/>);
`;
let server,browser,apiRequests=0;
const plugin={name:'transcript-reference',resolveId(id){if(id==='/__transcript.tsx')return id;},load(id){if(id==='/__transcript.tsx')return script;},configureServer(instance){instance.middlewares.use(async(req,res,next)=>{
  if(req.url?.startsWith('/api/')){apiRequests++;res.statusCode=500;res.end('No API in local fixture');return;}
  if(req.url?.startsWith('/__fonts/')){const file=req.url.slice('/__fonts/'.length);if(!fontFiles.has(file)){res.statusCode=404;res.end();return;}res.end(await fs.readFile(path.join(fonts,file)));return;}
  if(req.url?.startsWith('/__transcript.tsx')){const transformed=await instance.transformRequest('/__transcript.tsx');res.setHeader('Content-Type','application/javascript');res.end(transformed.code);return;}
  if(req.url?.startsWith('/__transcript')){res.setHeader('Content-Type','text/html; charset=utf-8');res.end(await instance.transformIndexHtml(req.url,'<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="/__transcript.tsx"></script>'));return;}
  next();
});}};
try {
  server=await createServer({configFile:false,root:path.join(root,'web'),plugins:[plugin,reactPlugin()],server:{host:'127.0.0.1',port:0,fs:{allow:[root]}},logLevel:'error'});
  await server.listen(); browser=await chromium.launch({channel:'chrome',headless:true});
  const base=`http://127.0.0.1:${server.httpServer.address().port}/__transcript`,scenes=[];
  for(const density of [1,2.625,3])for(const locale of ['en','zh-CN','ja'])for(const theme of ['light','dark']){
    const context=await browser.newContext({viewport:{width:360,height:800},deviceScaleFactor:density,isMobile:true});
    try{
      const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(String(e)));
      await page.goto(base+`?locale=${locale}&theme=${theme}`);await page.waitForFunction(()=>window.fixture);await page.addStyleTag({content:fontCSS});await page.evaluate(()=>document.fonts.ready);
      for(const width of [320,360,412])for(const size of [{height:800,footer:108},{height:600,footer:180}]){
        await page.setViewportSize({width,height:size.height});
        await page.evaluate(footer=>{document.querySelector('footer').style.height=footer+'px';window.fixture.show();},size.footer);
        const geometry=await page.evaluate(()=>{const button=document.querySelector('.scroll-latest'),arrow=button.querySelector('svg'),footer=document.querySelector('footer');
          const box=e=>{const b=e.getBoundingClientRect();return {x:b.x,y:b.y,width:b.width,height:b.height};};const style=getComputedStyle(button);
          return {button:box(button),arrow:box(arrow),footer:box(footer),label:button.getAttribute('aria-label'),border:style.borderWidth,color:style.color,background:style.backgroundColor,shadow:style.boxShadow};});
        scenes.push({density,locale,theme,width,height:size.height,footerHeight:size.footer,...geometry});
      }
      assert.deepEqual(errors,[]);
    }finally{await context.close();}
  }
  const page=await browser.newPage({viewport:{width:360,height:800}});await page.goto(base);await page.waitForFunction(()=>window.fixture);await page.addStyleTag({content:fontCSS});await page.evaluate(()=>document.fonts.ready);
  await page.evaluate(()=>{Object.defineProperty(navigator,'clipboard',{value:{writeText:async()=>{}}});});
  await page.locator('.turn').first().getByRole('button',{name:'Show more',exact:true}).click();
  await page.locator('.turn').first().locator('.message-footer-user button').click();
  await page.waitForFunction(()=>document.querySelector('.message-footer-user button').getAttribute('aria-label')==='Message copied');
  const reuse=await page.evaluate(()=>{
    const article=document.querySelector('.turn'),bubble=article.querySelector('.user-bubble'),footer=article.querySelector('.message-footer-user'),answer=article.querySelector('.assistant-text p'),avatar=article.querySelector('.user-avatar');
    const original=window.fixture.chat,copy=()=>JSON.parse(JSON.stringify(original));
    const range=document.createRange(),selected=answer.firstChild;range.setStart(selected,2);range.setEnd(selected,6);document.getSelection().removeAllRanges();document.getSelection().addRange(range);
    const checks=()=>({article:article===document.querySelectorAll('.turn')[document.querySelectorAll('.turn').length-2],bubble:bubble.isConnected,footer:footer.isConnected,answer:answer.isConnected,avatar:avatar.isConnected,expanded:bubble.querySelector('[data-expanded]').dataset.expanded==='true',copy:footer.querySelector('button').getAttribute('aria-label')==='Message copied',selection:document.getSelection().anchorNode===selected&&document.getSelection().anchorOffset===2&&document.getSelection().focusOffset===6});
    const streaming=copy();streaming.transcript.parts[3].text='New streaming answer';window.fixture.update(streaming);const live=checks();
    const prepended=JSON.parse(JSON.stringify(streaming));prepended.transcript.parts.unshift({role:'user',kind:'text',text:'Old'},{role:'assistant',kind:'text',text:'Older answer'});
    prepended.turns.forEach(t=>{t.part_start+=2;t.input_end+=2;t.part_end+=2;});prepended.turns.unshift({id:'older',backend:'codex',status:'completed',part_start:0,input_end:1,part_end:2});window.fixture.update(prepended);const history=checks();
    window.fixture.profile({id:'alice',nickname:'Renamed',avatar:{style:'pixel-art',seed:'two'}});const profile={...checks(),name:footer.querySelector('.message-author').textContent};
    const changed=JSON.parse(JSON.stringify(window.fixture.chat));changed.transcript.parts[3].text='Changed first answer';window.fixture.update(changed);
    return {live,history,profile,changedAnswerElementRetained:answer.isConnected};
  });
  for(const key of ['live','history','profile'])for(const [name,value]of Object.entries(reuse[key]))if(name!=='name')assert.equal(value,true,`${key}/${name}`);
  assert.equal(reuse.profile.name,'Renamed');assert.equal(reuse.changedAnswerElementRetained,true);
  const thresholds=[];
  await page.evaluate(()=>{document.querySelector('.history-inner').style.minHeight='2000px';});
  for(const gap of [91,90,89,0]){
    await page.evaluate(async gap=>{const view=window.fixture.history.current;view.scrollTop=view.scrollHeight-view.clientHeight-gap;view.dispatchEvent(new Event('scroll'));await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));},gap);
    const state=await page.evaluate(()=>({following:window.fixture.follow.current,shown:window.fixture.showLatest}));
    assert.equal(state.following,gap<90);assert.equal(state.shown,gap>=90);thresholds.push({gap,...state});
  }
  const latestTrace=await page.evaluate(()=>{
    const view=window.fixture.history.current,steps=[];const original=view.scrollTo.bind(view);view.scrollTo=options=>steps.push(options);
    window.fixture.latest();view.scrollTo=original;return {following:window.fixture.follow.current,calls:steps};
  });
  assert.equal(latestTrace.following,true);assert.equal(latestTrace.calls[0].behavior,'smooth');
  await page.evaluate(()=>{document.querySelector('footer').style.height='108px';window.fixture.show();});
  await page.screenshot({path:path.join(output,'web-transcript-latest-light.png')});
  assert.equal(apiRequests,0);
  const result={scope:'Original main.tsx keyed JSX/scroll callback/latest button and original React components, synthetic local Chrome. Not real server/device/IME or raster/compositor equality.',browser:browser.version(),scenes,reuse,latestTrace,thresholds,apiRequests};
  await fs.writeFile(path.join(root,'android/app/src/test/resources/web-transcript.json'),JSON.stringify(result,null,2)+'\n');
  await fs.writeFile(path.join(output,'web-transcript-reference.json'),JSON.stringify(result,null,2)+'\n');
  console.log(`Actual Web: ${scenes.length} latest-button scenes, keyed streaming/history/profile reuse, smooth-scroll call; zero API requests.`);
}finally{await browser?.close();await server?.close();}
