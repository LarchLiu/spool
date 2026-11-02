#!/usr/bin/env node
// Evaluate the actual Web TypeScript in memory, without a second handwritten oracle.
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import {createRequire} from 'node:module';
import {fileURLToPath,pathToFileURL} from 'node:url';
import {agents,activityCases,patchCases,attachmentFiles,fileDirectories,projectFiles,peopleProfiles,peopleOnline,peopleGroups,peopleMessages,sidebarProjects,sidebarChats,codeTabContents,messageCases,typographyCases,markdownCases,permissionCases,editorCases,composerLayoutCases,commandCases,chatStateCases,sendButtonCases} from './ui-fixtures.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(root,'web/package.json'));
const {buildSync}=require('esbuild');
const imports=new Map();
const pierreRoot=path.join(root,'web/node_modules/@pierre/diffs');
const pierre=JSON.parse(fs.readFileSync(path.join(pierreRoot,'package.json'),'utf8'));
for(const [key,specifier] of [['.','@pierre/diffs'],['./react','@pierre/diffs/react']])
 imports.set(specifier,{...await import(pathToFileURL(path.join(pierreRoot,pierre.exports[key].import)).href),__esModule:true});
function web(file, globals={}) {
 const built=buildSync({entryPoints:[path.join(root,'web/src',file)],bundle:true,write:false,format:'cjs',platform:'node',packages:'external',logLevel:'error'});
 const module={exports:{}};
 vm.runInNewContext(built.outputFiles[0].text,{module,exports:module.exports,require:id=>imports.get(id)||require(id),console,crypto:globalThis.crypto,URL:globalThis.URL,...globals},{filename:file});
 return module.exports;
}
const model=web('model-selection.ts'),activity=web('tool-activity.tsx'),diff=web('tool-diff.ts');
const attachments=web('attachment-selection.ts');
const people=web('direct-messages.ts'),floating=web('floating-window.ts');
const sidebar=web('sidebar-state.ts');
const slash=web('slash-commands.ts');
const chatState=web('chat-state.ts'),turnControl=web('turn-control.ts');
const chatStates=chatStateCases.map(item=>{
 const optimistic=item.operation==='optimistic'||item.operation==='adopt'?chatState.optimisticChat(item.previous,item.project,item.turn,item.input):null;
 const expected=item.operation==='optimistic'?optimistic:item.operation==='adopt'?chatState.adoptCreatedChat(optimistic,item.next):chatState.reconcile(item.previous,item.next);
 return {...item,expected,running:turnControl.runningTurn(expected)||null,turnRunning:turnControl.hasRunningTurn(expected)};
});
const mainSource=fs.readFileSync(path.join(root,'web/src/main.tsx'),'utf8');
const inputSource=mainSource.slice(mainSource.indexOf('function makeInputTranscriptParts('),mainSource.indexOf('interface BeforeInstallPromptEvent'));
if(!inputSource.startsWith('function makeInputTranscriptParts('))throw new Error('Input transcript helper not found');
const inputModule={exports:{}};
vm.runInNewContext(buildSync({stdin:{contents:'export '+inputSource,loader:'ts'},write:false,format:'cjs'}).outputFiles[0].text,{module:inputModule,exports:inputModule.exports});
const inputTranscripts=[[' Hello \n',[]],['\u3000Text\ufeff',[]],['\u00a0',attachmentFiles],['',attachmentFiles]].map(([message,attachments])=>({message,attachments,expected:inputModule.exports.makeInputTranscriptParts(message,attachments)}));
const slashInputs=['','/','/g','/go','/goa','/goal','/goals','/goal/path',' /goal','/goal ','/Goal','`/goal`','explain /goal','/unknown',
 '/goal clear','/goal stop','/goal off','/goal reset','/goal none','/goal cancel','/goal CLEAR','/goal clear the tests',
 '/goal\u3000修好测试\nand verify','\n/goal\t','/\n','/goal\r\n',...['\u00a0','\u1680','\u2007','\u2028','\u2029','\u202f','\u205f','\u3000','\ufeff','\u0085','\u180e'].map(space=>space+'/goal'+space),
 '/goal '+ 'x'.repeat(4000),'/goal '+ 'x'.repeat(4001),'/goal '+ '😀'.repeat(4000),'/goal '+ '😀'.repeat(4001)];
const slashCommands=slashInputs.map(text=>({text,suggestion:slash.goalSuggestion(text),command:slash.parseGoalCommand(text)}));
const peopleInbox=people.createDirectInbox(),peopleCases=[];
const recordPeople=operation=>{const s=peopleInbox.snapshot();peopleCases.push({operation,expected:{active:s.active,messages:s.messages.map(m=>m.id),people:s.people.map(p=>p.id),drafts:Object.fromEntries(Object.entries(s.drafts).map(([id,d])=>[id,d.text])),sending:Object.keys(s.sending),unread:Object.fromEntries(Object.entries(s.unread).filter(([,n])=>n)),groups:s.groups.map(g=>g.id),names:s.groupNames,conversations:people.peopleConversationIDs(s,'alice',peopleOnline.map(p=>p.id))}});};
recordPeople({type:'initial'});
peopleInbox.groups(peopleGroups);recordPeople({type:'groups',groups:peopleGroups});
peopleInbox.open('bob',peopleProfiles);recordPeople({type:'open',id:'bob'});
peopleInbox.draft('bob',' Original draft  \n');recordPeople({type:'draft',id:'bob',text:' Original draft  \n'});
let acknowledge;
const sending=peopleInbox.send('bob',' Original draft  \n',()=>new Promise(resolve=>{acknowledge=resolve;}));recordPeople({type:'send',id:'bob',request:'sent'});
peopleInbox.draft('bob','Newer edit');recordPeople({type:'draft',id:'bob',text:'Newer edit'});
acknowledge(peopleMessages[1]);await sending;peopleInbox.receive(peopleMessages[1],'alice',true,peopleProfiles);recordPeople({type:'ack',message:peopleMessages[1]});
peopleInbox.receive(peopleMessages[0],'alice',true,peopleProfiles);recordPeople({type:'receive',message:peopleMessages[0],visible:true});
peopleInbox.receive(peopleMessages[0],'alice',false,peopleProfiles);recordPeople({type:'receive',message:peopleMessages[0],visible:false});
peopleInbox.close();recordPeople({type:'close'});
peopleInbox.receive(peopleMessages[2],'alice',false,peopleProfiles);recordPeople({type:'receive',message:peopleMessages[2],visible:false});
peopleInbox.open('bob',peopleProfiles);recordPeople({type:'open',id:'bob'});
peopleInbox.groups([]);recordPeople({type:'groups',groups:[]});
peopleInbox.reset();recordPeople({type:'reset'});
const floatingCases=[{width:320,height:800},{width:360,height:800},{width:412,height:800},{width:270,height:300},{width:800,height:450}].flatMap(viewport=>{
 const v={x:0,y:0,...viewport},initial=floating.fitFloatingWindow(null,v);
 return [{viewport,operation:'fit',expected:initial},...['move',...floating.floatingWindowEdges].map(operation=>({viewport,operation,initial,dx:-75,dy:95,expected:operation==='move'?floating.moveFloatingWindow(initial,-75,95,v):floating.resizeFloatingWindow(initial,-75,95,v,operation)}))];
});
const panel=web('panel-layout.ts'),filePreview=web('file-preview.tsx'),links=web('local-file-links.ts');
const {FileTree,createFileTreeIconResolver}=await import(pathToFileURL(path.join(root,'web/node_modules/@pierre/trees/dist/index.js')).href);
const fileTree=new FileTree({paths:Object.values(fileDirectories).flatMap(listing=>listing.entries.map(entry=>entry.path+(entry.kind==='directory'?'/':''))),initialExpansion:'closed',flattenEmptyDirectories:false,icons:{set:'standard',colored:true},search:false});
const treeCases=[];
const recordTree=operation=>treeCases.push({operation,expected:fileTree.getVisibleRows(0,fileTree.getVisibleCount()).map(row=>({path:row.path.replace(/\/$/,''),depth:row.depth,expanded:row.isExpanded}))});
recordTree({type:'initial'});
fileTree.getItem('src/').expand();recordTree({type:'expand',path:'src'});
fileTree.openSearch();fileTree.setSearch('MOBILE');recordTree({type:'search',query:'MOBILE'});
fileTree.setSearch('missing-entry');recordTree({type:'search',query:'missing-entry'});
fileTree.closeSearch();recordTree({type:'close-search'});
fileTree.getItem('src/').collapse();recordTree({type:'collapse',path:'src'});
const iconResolver=createFileTreeIconResolver({set:'standard',colored:true});
const preferences=web('preferences.ts'),select=web('custom-select-layout.ts');
const selectCases=[
 {anchor:{left:25,top:240,right:297,bottom:278,width:272},viewport:{x:0,y:0,width:360,height:800},count:4},
 {anchor:{left:25,top:700,right:297,bottom:738,width:272},viewport:{x:0,y:0,width:360,height:800},count:4},
 {anchor:{left:280,top:240,right:308,bottom:278,width:28},viewport:{x:0,y:0,width:320,height:800},count:15},
 {anchor:{left:6,top:16,right:250,bottom:54,width:244},viewport:{x:0,y:0,width:280,height:90},count:4},
 {anchor:{left:70,top:500,right:342,bottom:538,width:272},viewport:{x:40,y:300,width:320,height:300},count:4},
 {anchor:{left:25,top:240,right:297,bottom:278,width:272},viewport:{x:0,y:0,width:360,height:800},count:0},
];
const languageCases=[
 {preference:'system',preferred:['de-DE','ja-JP','en-US']},
 {preference:'system',preferred:['fr','zh_Hant_TW','en']},
 {preference:'system',preferred:['de','fr']},
 {preference:'system',preferred:['en-GB','zh-CN']},
 {preference:'system',preferred:[]},
 {preference:'zh-CN',preferred:['ja-JP']},
 {preference:'ja',preferred:['en-US']},
 {preference:'en',preferred:['zh']},
];
let messageNow=Date.parse('2026-10-31T12:34:56Z');
class MessageDate extends Date {
 constructor(...args){super(...(args.length?args:[messageNow]));}
 static now(){return messageNow;}
}
const footer=web('message-footer.tsx',{Date:MessageDate});
const messageTimes=[];
const savedTimezone=process.env.TZ;
try {
 for(const timezone of ['UTC','Asia/Shanghai','America/New_York']) {
  process.env.TZ=timezone;
  for(const now of ['2026-10-31T12:34:56Z','2027-01-01T00:15:00Z'])for(const locale of ['en','zh-CN','ja'])
   for(const timestamp of [0,-1,8640000000000001,...['2026-10-31T12:00:01Z','2026-10-30T16:05:03Z','2026-01-02T03:04:05Z','2025-01-02T03:04:05Z','2027-01-01T00:15:00Z'].map(Date.parse)]) {
    messageNow=Date.parse(now);
    messageTimes.push({timezone,now:messageNow,locale,timestamp,expected:footer.messageTime(timestamp,locale)});
   }
 }
}finally {if(savedTimezone===undefined)delete process.env.TZ;else process.env.TZ=savedTimezone;}
const draftInputs=[
 {name:'NativeUi.TSX',mediaType:'text/plain',text:'export function NativeUi() { return "mobile"; }'},
 {name:'settings.JSON',mediaType:'application/octet-stream',text:'\ufeff{"native":true}'},
 {name:'empty-type.md',mediaType:'',text:'# Native attachments\n'},
 {name:'large.txt',mediaType:'text/plain',text:'x'.repeat(4094)+'\ud83e\udd16'},
 {name:'binary.zip',mediaType:'application/octet-stream',text:'no automatic text excerpt'},
];
const draftCases=[];
for(const input of draftInputs) {
 const bytes=Buffer.from(input.text,'utf8');
 const [draft]=await attachments.prepareAttachments([new File([bytes],input.name,{type:input.mediaType})]);
 draftCases.push({name:input.name,mediaType:input.mediaType,data:bytes.toString('base64'),expected:{mediaType:draft.media_type,excerpt:draft.excerpt||''}});
 attachments.releaseAttachments([draft]);
}
const english=JSON.parse(fs.readFileSync(path.join(root,'locales/en.json'),'utf8'));
const chinese=JSON.parse(fs.readFileSync(path.join(root,'locales/zh-CN.json'),'utf8'));
const translations=locale=>(key,values={})=>(locale==='zh-CN'?chinese[key]||english[key]:english[key]||key).replace(/\{([^}]+)\}/g,(match,key)=>values[key]===undefined?match:String(values[key]));
const modelCases=[];
for(const agent of agents)for(const id of [...agent.catalog.models.map(item=>item.model),'imported-custom'])
 modelCases.push({agent,model:id,expected:model.chooseModel(agent,id),levels:model.effortChoices(agent.backend,agent.catalog.models.find(item=>item.model===id)).map(item=>item.reasoningEffort)});
const value={
 slashCommands,commandCases,chatStates,inputTranscripts,sendButtonCases,composerLayoutCases,
 permissionCases,
 messageTimes,
 codeTabContents,
 sidebarProjects,sidebarChats,
 sidebarSorts:[null,'project-0','project-1','missing'].map(project=>({project,expected:sidebar.sortedSidebarChats(sidebarChats.filter(chat=>project===null||chat.project_id===project)).map(chat=>chat.id)})),
 sidebarLimits:[0,1,5,6,14,15,16,26,38].flatMap(total=>[5,15,25,35].map(limit=>({total,limit,expected:sidebar.nextSidebarChatLimit(total,limit)}))),
 sidebarReveals:[['project-0','chat-10',5,5],['project-6','missing',5,5],['missing','missing',5,5],['project-0','chat-3',10,15]].map(([project,chat,projectLimit,chatLimit])=>({project,chat,projectLimit,chatLimit,expectedProject:sidebar.revealSidebarProject(sidebarProjects,project,projectLimit),expectedChat:sidebar.revealSidebarChat(sidebar.sortedSidebarChats(sidebarChats.filter(item=>item.project_id===project)),chat,chatLimit)})),
 peopleProfiles,peopleOnline,peopleGroups,peopleMessages,peopleCases,floatingCases,messageCases,typographyCases,markdownCases,editorCases,
 fileDirectories,projectFiles,fileTrees:treeCases,
 fileIcons:['package.json','.gitignore','LICENSE','a.mdx.tsx','UPPER.TSX','a.unknown','a.tar.gz','hello.go','CLAUDE.md'].map(path=>({path,expected:iconResolver.resolveIcon('file-tree-icon-file',path).name.replace('file-tree-builtin-','tree-')})),
 filePreviews:Object.values(projectFiles).concat([{kind:'text',name:'page.HTML',media_type:'text/plain'},{kind:'text',name:'page',media_type:' Text/HTML; charset=UTF-8 '},{kind:'text',name:'guide.mdown',media_type:'text/plain'}]).map(file=>({file,expected:filePreview.filePreviewKind(file)||''})),
 fileLinks:[{href:'../README.md#L3',base:'src/NativeUi.java'},{href:'src/main.go:4:2',base:''},{href:'./image%20one.png',base:'docs/guide.md'}].map(input=>({...input,expected:links.localFileTarget(input.href,input.base)})),
 fileSplits:[0,100,248,400,750,1000].flatMap(height=>[-1,.15,.32,.6,2].map(value=>({height,value,expected:panel.previewTreeFraction(value,height),limits:panel.previewSplitLimits(height)}))),
 selectLayouts:selectCases.map(input=>({...input,expected:select.selectPopupLayout(input.anchor,input.viewport,input.count)})),
 languages:languageCases.map(input=>({...input,expected:preferences.resolveLanguage(input.preference,input.preferred)})),
 attachmentFiles,
 attachmentDrafts:draftCases,
 attachmentPaths:['chat/id','chat?one','chat with spaces'].map((chatID,index)=>({chatID,fileID:['file?id','file/one','file with spaces'][index],expected:attachments.attachmentURL(chatID,{id:['file?id','file/one','file with spaces'][index]})})),
 models:modelCases,
 chats:[{backend:'claude',model:'imported-custom',reasoning_effort:'high'},{backend:'codex',model:'',reasoning_effort:''}].map(chat=>({agents,chat,expected:model.chatSelection(agents,chat)})),
 activities:['en','zh-CN'].flatMap(locale=>activityCases.map(input=>{
  const items=activity.activitiesFromParts(input.parts,input.backend,translations(locale));
  return {...input,locale,expected:items.map(item=>({...item,changeStats:activity.changeStats([item])})),stats:activity.changeStats(items)};
 })),
 patches:patchCases.map(input=>({...input,expected:diff.toolResultDiffs(input.text).map(file=>({
  name:file.name,prevName:file.prevName||'',type:file.type,
  lineNumbersUnavailable:!!file.lineNumbersUnavailable,contentsUnavailable:!!file.contentsUnavailable,
  additionLines:file.additionLines,deletionLines:file.deletionLines,
  hunks:file.hunks.map(h=>({additionStart:h.additionStart,additionCount:h.additionCount,deletionStart:h.deletionStart,deletionCount:h.deletionCount,
   hunkContext:h.hunkContext||'',hunkSpecs:h.hunkSpecs||'',noEOFCRAdditions:h.noEOFCRAdditions,noEOFCRDeletions:h.noEOFCRDeletions,
   additionLines:h.additionLines,deletionLines:h.deletionLines})),
 }))})),
};
const target=path.join(root,'android/app/src/test/resources/web-behavior.json');
const contents=JSON.stringify(value,null,2)+'\n';
if(process.argv.includes('--check')) {
 if(!fs.existsSync(target)||fs.readFileSync(target,'utf8')!==contents)throw new Error('Web/native behavior drift. Run node android/tools/sync-web-behavior.mjs');
 console.log('Verified Web model/activity behavior vectors.');
}else{
 fs.mkdirSync(path.dirname(target),{recursive:true});
 fs.writeFileSync(target,contents);
 console.log('Synchronized Web model/activity behavior vectors.');
}
