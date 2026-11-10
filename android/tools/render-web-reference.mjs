#!/usr/bin/env bun
import { requireRunTemp } from "../../tools/test-scratch.mjs";
requireRunTemp();
// Original Web CSS/components with synthetic fixtures; never connects to a server.
import fs from 'node:fs/promises';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import {renderViewportReferences} from './viewport-reference.mjs';
import {renderEditorReferences} from './editor-reference.mjs';
import {renderCommandReferences} from './command-reference.mjs';
import {renderSendButtonReferences} from './send-button-reference.mjs';
import {renderComposerLayoutReferences} from './composer-layout-reference.mjs';
import {fileFontFixtures,renderFileFontReferences} from './file-font-reference.mjs';
import {renderFileOverflowReferences} from './file-overflow-reference.mjs';
import {renderFileSearchReferences} from './file-search-reference.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(root,'web/package.json'));
const {createServer}=await import(require.resolve('vite'));
const {chromium}=process.env.SPOOL_NODE_MODULES?createRequire(path.join(process.env.SPOOL_NODE_MODULES,'__reference__.cjs'))('playwright'):require('playwright');
const output=path.join(root,'build/artifacts/android-native');
await fs.mkdir(output,{recursive:true});
execFileSync('bun',[path.join(root,'android/tools/prepare-ui-fonts.mjs')],{cwd:root,stdio:'inherit'});
const fontDirectory=path.join(root,'build/cache/android-ui-fonts');
const fontManifest=JSON.parse(await fs.readFile(path.join(fontDirectory,'manifest.json'),'utf8'));
await fs.writeFile(path.join(output,'font-reference.json'),JSON.stringify(fontManifest,null,2));
const fontFiles=new Set([...fontManifest.faces.map(face=>face.file),fontManifest.mono.file,...fontManifest.cjk.map(face=>face.file)]);
const referenceFontCSS=fontManifest.faces.map(face=>`@font-face{font-family:AndroidRoboto;src:url(/__android-fonts/${face.file});font-weight:${face.weight};font-style:${face.italic?'italic':'normal'}}`).join('')
 +fontManifest.cjk.map(face=>`@font-face{font-family:${face.family};src:url(/__android-fonts/${face.file});font-weight:400}`).join('')
 +':root{font-family:AndroidRoboto,AndroidCjkSC,sans-serif}:lang(ja){font-family:AndroidRoboto,AndroidCjkJP,sans-serif}';
const avatarImageAudit={imageAPIRequests:0,frames:[]};
const script=`
import React from 'react';
import {createRoot} from 'react-dom/client';
import {Menu,Users,PanelRightOpen,Plus,ShieldQuestion,ChevronDown,ArrowUp,Folder,Command,Settings2,X} from 'lucide-react';
import {LoginStory} from '/src/login-story.tsx';
import {AutosizeTextarea} from '/src/autosize-textarea.tsx';
import {SlashCommandMenu} from '/src/slash-command-menu.tsx';
import {ComposerSendButton} from '/src/composer-send-button.tsx';
import {AgentIcon} from '/src/agent-icon.tsx';
import {AvatarDialog} from '/src/avatar-dialog.tsx';
import {AppearanceDialog} from '/src/appearance-dialog.tsx';
import {PermissionPicker} from '/src/permission-picker.tsx';
import {FileSidebar} from '/src/file-sidebar.tsx';
import {FileSource} from '/src/file-source.tsx';
import {MarkdownMessage} from '/src/markdown-message.tsx';
import {UserMessage} from '/src/user-message.tsx';
import {MessageFooter} from '/src/message-footer.tsx';
import {TurnOperatorAvatar} from '/src/turn-identity.tsx';
import {FileDiff} from '@pierre/diffs/react';
import {toolResultDiffs} from '/src/tool-diff.ts';
import {DirectMessagePanel} from '/src/direct-message-panel.tsx';
import {PeopleGroupPicker} from '/src/people-group-picker.tsx';
import {OnlinePeopleMenu} from '/src/online-people-menu.tsx';
import {AvatarStack} from '/src/user-avatar.tsx';
import {Sidebar} from '/src/sidebar.tsx';
import {AgentModelMenu} from '/src/agent-model-menu.tsx';
import {ToolActivityList} from '/src/tool-activity.tsx';
import {AttachmentCards,ImagePreview} from '/src/attachments.tsx';
import {translator} from '/src/i18n.ts';
import {agents,activityParts,attachmentFiles,peopleProfiles,peopleOnline,peopleGroups,peopleMessages,sidebarProjects,sidebarChats,codeTabContents,diffLayoutContents,syntaxSourceContents,syntaxPatchContents,messageCases,markdownCases,permissionCases} from '/@fs${root}/android/tools/ui-fixtures.mjs';
import {commandCases,sendButtonCases} from '/@fs${root}/android/tools/ui-fixtures.mjs';
import '/src/style.css';
const h=React.createElement,t=translator(new URL(location.href).searchParams.get('locale')||'en');
const screen=new URL(location.href).searchParams.get('screen');
const rows=(...values)=>values.join(String.fromCharCode(10));
const composerValues={'workspace':'','workspace-1':'First line','workspace-3':rows('First line','Second line','Third line'),'workspace-6':rows('One','Two','Three','Four','Five','Six'),'workspace-overflow':rows('One','Two','Three','Four','Five','Six','Seven','Eight'),'workspace-wrapped':'A long message that wraps naturally across several lines in the mobile composer. The history must remain stable while the input grows.'};
const icon=(Icon,size=18,cls='icon')=>h('button',{className:cls},h(Icon,{size}));
const login=h('main',{className:'login'},icon(Settings2,20,'icon login-preferences'),h(LoginStory,{t}),
 h('section',{className:'login-panel'},h('form',null,
 h('span',{className:'eyebrow'},t('Welcome back')),h('h2',null,t('Sign in to your workspace')),
 h('p',null,t('Use the account created by your desktop administrator.')),
 h('label',null,t('Username'),h('input',{name:'username'})),h('label',null,t('Password'),h('input',{name:'password',type:'password'})),
 h('button',{className:'primary'},t('Enter workspace'),h(ArrowUp,{size:17})),h('small',null,t('Need an account? Contact your desktop administrator.')))));
const workspace=h('div',{className:'app left-collapsed right-collapsed'},h('main',{className:'workspace'},
 h('header',null,h('div',{className:'heading'},icon(Menu),h('div',{className:'heading-copy'},h('div',{className:'chat-heading'},h('strong',null,'New chat')),h('span',null,'Mobile project'))),h('div',{className:'header-actions'},h('details',{className:'online'},h('summary',null,h(Users,{size:14}))),icon(PanelRightOpen))),
 h('div',{className:'history'},h('div',{className:'history-inner'},h('section',{className:'welcome'},h('img',{className:'welcome-mark',src:'/logo.png'}),h('span',{className:'eyebrow'},t('A little space for big ideas')),h('h1',null,t('What shall we build today?')),h('p',null,t('Describe your idea. Your agent takes it from here.')),h('div',{className:'welcome-notes'},h('span',null,h(Folder,{size:14}),'Mobile project'),h('span',null,h(Command,{size:13}),t('Run locally. Pick up anytime.')))))),
 h('footer',{className:'composer-wrap'},h('form',{className:'composer'},h(AutosizeTextarea,{placeholder:'Message Codex',value:composerValues[screen]||'',onChange:()=>{}}),h('div',{className:'composer-bottom'},
 h('div',{className:'composer-tools'},icon(Plus,20,'icon attachment-add'),h('div',{className:'permission-picker'},h('button',{className:'permission-summary'},h(ShieldQuestion,{size:15}),h('span',null,'Ask for approval'),h(ChevronDown,{size:11})))),
 h('div',{className:'composer-actions'},h('div',{className:'model-picker'},h('button',{className:'picker-summary'},h(AgentIcon,{backend:'codex',size:14}),h('span',null,'gpt-6.1-codex'),h(ChevronDown,{size:12}))),icon(ArrowUp,20,'send')))))));
const commandCase=commandCases.find(item=>item.id===new URL(location.href).searchParams.get('state'))||commandCases[0];
const baseComposer=workspace.props.children.props.children[2].props.children;
const sendScene=h('section',{className:'send-button-scene',style:{padding:24,background:'var(--elevated)'}},sendButtonCases.map(item=>h('div',{'data-case':item.id,key:item.id,style:{height:56,display:'flex',alignItems:'center'}},h(ComposerSendButton,{...item,t,onStop:()=>{}}))));
const commandNotice=commandCase.noticeKey?t(commandCase.noticeKey):commandCase.notice||'';
const commandScene=h('main',{className:'workspace command-scene',style:{height:800,justifyContent:'flex-end'}},
 h('footer',{className:'composer-wrap'},
 commandCase.errorKey&&h('div',{className:'error dismissible-error',role:'alert'},h('span',null,t(commandCase.errorKey)),h('button',{type:'button',className:'icon','aria-label':t('Dismiss')},h(X,{size:15}))),
 commandNotice&&h('div',{className:'goal-command-notice',role:'status'},h('pre',null,commandNotice),h('button',{className:'icon','aria-label':t('Dismiss')},h(X,{size:15}))),
 React.cloneElement(baseComposer,{},commandCase.id==='menu'&&h(SlashCommandMenu,{t,onSelect:()=>{}}),
 React.cloneElement(baseComposer.props.children[0],{value:commandCase.draft}),
 commandCase.draft.startsWith('/goal')&&h('div',{className:'goal-command-hint'},t('/goal <condition> · /goal to view · /goal clear to remove')),
 baseComposer.props.children[1])));
const tabFence='\`\`\`text\\n'+codeTabContents+'\\n\`\`\`';
const tabDiff=toolResultDiffs('*** Begin Patch\\n*** Add File: tabs.txt\\n'+codeTabContents.split('\\n').map(line=>'+'+line).join('\\n')+'\\n*** End Patch')[0];
const codeTabs=h('div',{className:'code-tabs'},h(FileSource,{path:'tabs.txt',contents:codeTabContents,theme:new URL(location.href).searchParams.get('theme')||'light'}),h('div',{className:'assistant-text'},h(MarkdownMessage,null,tabFence)),h('div',{className:'tool-detail'},h(MarkdownMessage,null,tabFence)),h('div',{className:'file-preview-scroll'},h(MarkdownMessage,null,tabFence)),h('div',{className:'tool-diff code-tabs-diff'},h(FileDiff,{fileDiff:tabDiff,options:{disableLineNumbers:true,hunkSeparators:'simple'},disableWorkerPool:true})));
const diffLayout=h('div',{className:'tool-diff diff-layout'},h(FileDiff,{fileDiff:toolResultDiffs(diffLayoutContents)[0],disableWorkerPool:true}));
const syntax=h('div',{className:'syntax-scene'},h(FileSource,{path:'render.ts',contents:syntaxSourceContents,theme:new URL(location.href).searchParams.get('theme')||'light'}),h('div',{className:'tool-diff syntax-diff'},h(FileDiff,{fileDiff:toolResultDiffs(syntaxPatchContents)[0],disableWorkerPool:true})));
const peopleID=screen==='people-group'?'group-mobile':'bob';
const peopleIdentity=id=>peopleProfiles.find(p=>p.id===id)||{id,nickname:id==='online-room'?'Online group chat':peopleGroups.find(g=>g.id===id)?.name||id};
const people=h(DirectMessagePanel,{user:'alice',self:peopleProfiles[0],peer:peopleIdentity(peopleID),people:['online-room','group-mobile','bob','carol','david'].map(peopleIdentity),groups:peopleGroups,resolvePerson:peopleIdentity,presence:Object.fromEntries(peopleOnline.map(p=>[p.id,p.state])),unread:{'online-room':12,carol:2},messages:peopleMessages.map(m=>screen==='people-group'?{...m,to:peopleID,group:true}:m),connected:true,status:'online',onlineCount:peopleOnline.length,group:screen==='people-group'?peopleGroups[0]:undefined,draft:'Check the mobile layout',sending:false,locale:'en-US',t,onSend:()=>new Promise(()=>{}),onDraftChange:()=>{},onPeer:()=>{},onClose:()=>{},onInvite:()=>{},onNewGroup:()=>{}});
const groupPicker=h(PeopleGroupPicker,{people:peopleProfiles.filter(p=>p.id!=='alice'&&peopleOnline.some(o=>o.id===p.id)),t,onSubmit:()=>new Promise(()=>{}),onClose:()=>{},onDone:()=>{}});
const sidebar=h('div',{className:'app left-collapsed right-collapsed'},h(Sidebar,{user:peopleProfiles[0],resolveProfile:peopleIdentity,projects:screen==='sidebar-empty'?[]:sidebarProjects,chats:screen==='sidebar-empty'?[]:sidebarChats,project:screen==='sidebar-empty'?'':'project-0',chatID:screen==='sidebar-empty'?'':'chat-0',activeBackend:'codex',open:true,active:true,posting:false,width:298,t,onClose:()=>{},onResize:()=>{},onChat:()=>{},onNew:()=>{},onSettings:()=>{},onAvatar:()=>{},onLogout:()=>{}}));
const onlineMenu=h('div',{className:'app left-collapsed right-collapsed'},h('main',{className:'workspace'},h('header',null,h('div',{className:'heading'},icon(Menu),h('div',{className:'heading-copy'},h('div',{className:'chat-heading'},h('strong',null,'New chat')),h('span',null,'Mobile project'))),h('div',{className:'header-actions'},h('details',{className:'online',open:true},h('summary',null,h(AvatarStack,{people:peopleOnline.map(p=>peopleIdentity(p.id))}),h('span',{className:'online-unread','aria-hidden':true},'9+')),h(OnlinePeopleMenu,{self:'alice',people:peopleOnline.map(p=>({...peopleIdentity(p.id),state:p.state})),groups:peopleGroups,unread:{'online-room':12,carol:2},unreadMessages:14,resolvePerson:peopleIdentity,t,onInbox:()=>{},onPeer:()=>{},onNewGroup:()=>{}})),icon(PanelRightOpen)))));
const appearance=h(AppearanceDialog,{preferences:{theme:new URL(location.href).searchParams.get('theme')||'light',language:'system'},saving:false,version:'0.2.0-native',t,onChange:()=>{},onClose:()=>{}});
const permissions=h('section',{className:'permissions-scene',style:{padding:24,background:'var(--elevated)'}},permissionCases.map(item=>h('div',{key:item.id,'data-case':item.id,lang:item.locale||'en',style:{marginBottom:24}},h(PermissionPicker,{backend:item.backend,open:true,disabled:false,onOpen:()=>{},t:translator(item.locale||'en'),value:{mode:item.selected,catalog:item.modes?{modes:item.modes}:undefined,loading:!!item.loading,failed:!!item.failed,choose:()=>{},retry:()=>{}}}))));
const fileFonts=screen==='files-fonts',fileLocale=new URL(location.href).searchParams.get('locale')||'en';
const files=h('div',{className:'app left-collapsed right-collapsed'},h(FileSidebar,{project:{id:fileFonts?'font-'+fileLocale:'mobile',name:'Mobile project'},active:true,mobileOpen:true,width:360,selection:fileFonts?null:{target:{path:screen==='files-markdown'?'README.md':'src/NativeUi.java',...(screen==='files-markdown'?{}:{line:3})},request:1},t,theme:new URL(location.href).searchParams.get('theme')||'light',onOpen:()=>{},onClose:()=>{},onResize:()=>{},onClear:()=>{}}));
const model=h('div',{className:'model-scene'},h(AgentModelMenu,{agents,selection:{backend:'codex',model:'gpt-6.1-codex',effort:'high'},currentBackend:'codex',t,onChange:()=>{}}));
const activity=h('div',{className:'activity-scene',style:{padding:'25px 18px',background:'var(--elevated)'}},h(ToolActivityList,{parts:activityParts,backend:'codex',t,live:true,summary:'Working for 5s'}));
const attachments=h('div',{className:'attachment-scene',style:{padding:'25px 18px',background:'var(--elevated)'}},h('div',{className:'draft-fixture'},h(AttachmentCards,{files:attachmentFiles,t,onRemove:()=>{},onPreview:()=>{}})),h('div',{style:{height:24}}),h('div',{className:'user-row'},h('div',{className:'user-message'},h(AttachmentCards,{files:attachmentFiles,chatID:'chat',t,onPreview:()=>{}}))));
const messages=h('section',{className:'messages-scene',style:{padding:'25px 18px',background:'var(--elevated)'}},messageCases.map(item=>{
 const tr=translator(item.locale),mine=item.role!=='assistant';
 const footer=h(MessageFooter,{role:mine?'user':'assistant',author:item.operator?.nickname,text:item.text,timestamp:item.timestamp,locale:item.locale,t:tr});
 return mine?h('div',{className:'user-row','data-case':item.id,key:item.id,lang:item.locale},h('div',{className:'user-message'},item.attachment!==undefined&&h(AttachmentCards,{files:[attachmentFiles[item.attachment]],chatID:'chat',t:tr,onPreview:()=>{}}),item.text&&h(UserMessage,{text:item.text,t:tr}),footer),h(TurnOperatorAvatar,{person:item.operator})):h('div',{'data-case':item.id,key:item.id,lang:item.locale},h('div',{className:'assistant-text'},h(MarkdownMessage,null,item.text)),footer);
}));
const markdownBlocks=h('section',{className:'markdown-blocks-scene',style:{padding:'25px 18px',background:'var(--elevated)'}},markdownCases.map(item=>h('div',{key:item.id,'data-case':item.id,lang:item.locale||'en',className:'assistant-text',style:{marginBottom:22}},h(MarkdownMessage,null,item.text))));
const composerSurface=h('section',{className:'composer-surface-scene',style:{padding:'64px 0',background:'var(--elevated)'}},[99,227].map((height,index)=>h('div',{key:height,className:'composer-wrap',style:{marginBottom:index===0?64:0}},h('div',{className:'composer',style:{height}}))));
const floatingSurface=h('section',{className:'floating-surface-scene',style:{height:800,background:'var(--surface)'}},h('dialog',{className:'direct-message-panel',open:true,style:{left:12,top:80,width:'calc(100% - 24px)',height:520}}));
const popupSurface=h('section',{className:'popup-surface-scene',style:{position:'relative',height:800,background:'var(--elevated)'}},['model-popover','permission-popover','online-list','custom-select-menu'].map((className,index)=>h('div',{key:className,className,style:{position:'absolute',left:12,right:'auto',top:64+index*160,bottom:'auto',width:'calc(100% - 24px)',height:80,maxWidth:'none',maxHeight:'none'}})));
const modalClass={'modal-appearance-surface':'appearance-dialog','modal-avatar-surface':'avatar-dialog','modal-group-surface':'people-group-picker','modal-image-surface':'image-preview'}[screen];
// A focused descendant avoids the empty dialog's UA outline; only the surface is visible.
const modalSurface=h('section',{className:'modal-surface-scene',style:{height:800,background:'var(--elevated)'}},h('dialog',{className:modalClass,style:{height:238,padding:0},ref:element=>{if(element&&!element.open)element.showModal();}},h('button',{autoFocus:true,'aria-label':'Surface fixture focus',style:{position:'absolute',left:'50%',top:'50%',opacity:0}})));
const viewportShell=h('div',{className:'app left-collapsed right-collapsed'},React.cloneElement(sidebar.props.children,{open:false}),workspace.props.children,React.cloneElement(files.props.children,{mobileOpen:false}));
createRoot(document.getElementById('root')).render(screen==='send-button'?sendScene:screen==='commands'?commandScene:screen==='viewport'?viewportShell:modalClass?modalSurface:screen==='permissions'?permissions:screen==='popup-surface'?popupSurface:screen==='floating-surface'?floatingSurface:screen==='composer-surface'?composerSurface:screen.startsWith('background-')?h('section',{className:screen.startsWith('background-login')?'login-story':'workspace',style:{height:screen.startsWith('background-login')?270:800}}):screen==='markdown-blocks'?markdownBlocks:screen==='messages'?messages:screen==='syntax'?syntax:screen==='diff-layout'?diffLayout:screen==='code-tabs'?codeTabs:screen==='login'?login:screen.startsWith('sidebar')?sidebar:screen==='online-menu'?onlineMenu:screen.startsWith('people')?people:screen==='group-picker'?groupPicker:screen.startsWith('files')?files:screen==='appearance'?appearance:screen==='avatar'?h(AvatarDialog,{user:{id:'alice',nickname:'Mobile user'},t,onSave:async()=>{},onClose:()=>{}}):screen==='model'?model:screen==='activity'?activity:screen==='attachments'?attachments:screen==='image'?h(ImagePreview,{file:attachmentFiles[0],url:attachmentFiles[0].preview_url,t,onClose:()=>{}}):workspace);
`;
const {default:reactPlugin}=await import(require.resolve('@vitejs/plugin-react'));
let server;
const {fileDirectories,projectFiles,typographyCases}=await import('./ui-fixtures.mjs');
const referencePlugin={name:'android-reference',resolveId(id){if(id==='/__android-reference.mjs')return id;},load(id){if(id==='/__android-reference.mjs')return script;},configureServer(instance){instance.middlewares.use(async(req,res,next)=>{
 if(req.url==='/favicon.ico'){res.statusCode=204;res.end();return;}
 if(req.url?.startsWith('/api/projects/font-')) {
  const url=new URL(req.url,'http://localhost'),locale=url.pathname.match(/^\/api\/projects\/font-(zh-CN|ja)\/files$/)?.[1];
  const fixture=fileFontFixtures.find(item=>item.locale===locale);
  const value=fixture&&url.searchParams.get('path')==='.'?{path:'.',entries:[{name:fixture.name,path:fixture.name,kind:'text'},{name:'plain.txt',path:'plain.txt',kind:'text'}]}:null;
  res.setHeader('Content-Type','application/json');res.statusCode=value?200:404;res.end(JSON.stringify(value||{error:'Fixture file unavailable'}));return;
 }
 if(req.url?.startsWith('/api/projects/mobile/')) {
  const url=new URL(req.url,'http://localhost'),target=url.searchParams.get('path');
  const value=(url.pathname.endsWith('/files')?fileDirectories:projectFiles)[target];
  res.setHeader('Content-Type','application/json');res.statusCode=value?200:404;res.end(JSON.stringify(value||{error:'Fixture file unavailable'}));return;
 }
 if(req.url==='/__android-reference-image.png'){res.setHeader('Content-Type','image/png');res.end(await fs.readFile(path.join(output,'attachment-fixture.png')));return;}
 // Avatar images must be local data URIs, never a mock image API response.
 if(req.url?.startsWith('/api/avatars.svg')){avatarImageAudit.imageAPIRequests++;res.statusCode=500;res.end('Avatar image API must not be used');return;}
 if(req.url?.startsWith('/__android-reference.mjs')){const transformed=await instance.transformRequest('/__android-reference.mjs');res.setHeader('Content-Type','application/javascript');res.end(transformed.code);return;}
 if(req.url?.startsWith('/__android-fonts/')){const file=req.url.slice('/__android-fonts/'.length);if(!fontFiles.has(file)){res.statusCode=404;res.end();return;}res.setHeader('Content-Type','font/ttf');res.end(await fs.readFile(path.join(fontDirectory,file)));return;}
 if(req.url==='/__android-reference-mono.ttf'){res.setHeader('Content-Type','font/ttf');res.end(await fs.readFile(path.join(fontDirectory,fontManifest.mono.file)));return;}
 if(req.url?.startsWith('/__android-reference')){res.setHeader('Content-Type','text/html');res.end(await server.transformIndexHtml(req.url,'<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="/__android-reference.mjs"></script>'));return;}
 next();
});}};
server=await createServer({cacheDir:path.join(requireRunTemp(),"vite-cache"),configFile:false,root:path.join(root,'web'),plugins:[referencePlugin,reactPlugin()],optimizeDeps:{include:['react','react-dom/client','lucide-react']},server:{host:'127.0.0.1',port:0,fs:{allow:[root]}},logLevel:'error'});
let browser;
try{
 await server.listen();
 const port=server.httpServer.address().port;
 browser=await chromium.launch({channel:'chrome',headless:true});
 const screens=process.argv.find(value=>value.startsWith('--screens='))?.slice('--screens='.length).split(',')||['login','workspace','workspace-1','workspace-3','workspace-6','workspace-overflow','workspace-wrapped','composer-surface','floating-surface','popup-surface','modal-appearance-surface','modal-avatar-surface','modal-group-surface','modal-image-surface','permissions','background-login','background-workspace','avatar','model','activity','attachments','image','appearance','files','files-markdown','people','people-group','group-picker','online-menu','sidebar','sidebar-empty','sidebar-operators','code-tabs','diff-layout','syntax','messages','markdown-blocks'];
 if(screens.includes('viewport'))await renderViewportReferences({browser,url:`http://127.0.0.1:${port}/__android-reference`,output,fontCSS:referenceFontCSS});
 if(screens.includes('editor'))await renderEditorReferences({browser,url:`http://127.0.0.1:${port}/__android-reference`,output,fontCSS:referenceFontCSS});
 if(screens.includes('commands'))await renderCommandReferences({browser,url:`http://127.0.0.1:${port}/__android-reference`,output,fontCSS:referenceFontCSS});
 if(screens.includes('send-button'))await renderSendButtonReferences({browser,url:`http://127.0.0.1:${port}/__android-reference`,output});
 if(screens.includes('composer-layout'))await renderComposerLayoutReferences({browser,url:`http://127.0.0.1:${port}/__android-reference`,output,fontCSS:referenceFontCSS});
 if(screens.includes('files-fonts'))await renderFileFontReferences({browser,url:`http://127.0.0.1:${port}/__android-reference`,output,fontCSS:referenceFontCSS});
 if(screens.includes('files-overflow'))await renderFileOverflowReferences({browser,url:`http://127.0.0.1:${port}/__android-reference`,output,fontCSS:referenceFontCSS});
 if(screens.includes('files-search'))await renderFileSearchReferences({browser,url:`http://127.0.0.1:${port}/__android-reference`,output,fontCSS:referenceFontCSS,probe:process.argv.includes('--search-probe')});
 for(const width of [320,360,412])for(const theme of ['light','dark'])for(const screen of screens.filter(name=>!['viewport','editor','commands','send-button','composer-layout','files-fonts','files-overflow','files-search'].includes(name))){
  const context=await browser.newContext({viewport:{width,height:800},deviceScaleFactor:1,isMobile:true,hasTouch:true,timezoneId:'Asia/Shanghai'});
  const page=await context.newPage();const errors=[];page.on('pageerror',e=>{errors.push(String(e));console.error(e);});
  page.on('console',message=>{if(message.type()==='error')console.error(message.text());});
  await page.goto(`http://127.0.0.1:${port}/__android-reference?screen=${screen}&theme=${theme}`);
  if(screen==='model')await page.addStyleTag({content:'.model-scene .model-popover{position:relative;inset:auto}'});
  if(screen==='permissions')await page.addStyleTag({content:'.permissions-scene .permission-summary{display:none}.permissions-scene .permission-popover{position:relative;inset:auto;max-height:none;box-shadow:none}'});
  await page.waitForSelector(screen.startsWith('modal-')?'.modal-surface-scene dialog[open]':screen==='permissions'?'.permissions-scene': ['floating-surface','popup-surface'].includes(screen)?`.${screen}-scene`:screen.startsWith('background-')?'#root > section':screen==='markdown-blocks'?'.markdown-blocks-scene':screen==='messages'?'.messages-scene':screen==='syntax'?'.syntax-scene .file-code-preview':screen==='diff-layout'?'.diff-layout diffs-container':screen==='code-tabs'?'.code-tabs .file-code-preview':screen==='login'?'.login-panel':screen.startsWith('sidebar')?'.sidebar.open':screen==='online-menu'?'.online-list':screen.startsWith('people')?'.direct-message-panel[open]':screen==='group-picker'?'.people-group-picker[open]':screen.startsWith('files')?'.file-preview-meta':screen==='appearance'?'.appearance-dialog[open]':screen==='avatar'?'.avatar-dialog[open]':screen==='model'?'.model-popover':screen==='activity'?'.tool-activity-list':screen==='attachments'?'.attachment-scene':screen==='image'?'.image-preview[open]':'.composer');
  await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
  if(screen==='background-login-surface')await page.addStyleTag({content:'.login-story::after{display:none}'});
  if(screen==='diff-layout'||screen==='syntax')await page.emulateMedia({colorScheme:theme});
  await page.addStyleTag({content:referenceFontCSS});
  if(screen==='attachments')await page.addStyleTag({content:'@font-face{font-family:AndroidMono;src:url(/__android-reference-mono.ttf)}.file-excerpt pre{font-family:AndroidMono,monospace}'});
  if(screen.startsWith('files'))await page.addStyleTag({content:'@font-face{font-family:AndroidMono;src:url(/__android-reference-mono.ttf)}file-tree-container{--trees-font-family-override:AndroidRoboto}.file-code-preview{--diffs-font-family:AndroidMono}.file-source{font-family:AndroidMono}'});
  if(screen==='code-tabs')await page.addStyleTag({content:'@font-face{font-family:AndroidMono;src:url(/__android-reference-mono.ttf)}.file-code-preview,.code-tabs-diff diffs-container{--diffs-font-family:AndroidMono}.markdown code{font-family:AndroidMono}'});
  if(screen==='diff-layout')await page.addStyleTag({content:'@font-face{font-family:AndroidMono;src:url(/__android-reference-mono.ttf)}.diff-layout diffs-container{--diffs-font-family:AndroidMono}'});
  if(screen==='syntax')await page.addStyleTag({content:'@font-face{font-family:AndroidMono;src:url(/__android-reference-mono.ttf)}.syntax-scene diffs-container{--diffs-font-family:AndroidMono}'});
  if(screen==='markdown-blocks')await page.addStyleTag({content:'@font-face{font-family:AndroidMono;src:url(/__android-reference-mono.ttf)}.markdown-blocks-scene .markdown code{font-family:AndroidMono,monospace}'});
  await page.evaluate(()=>document.fonts.ready);await page.waitForTimeout(500);
  const localAvatarImages=await page.locator('img.user-avatar').evaluateAll(async images=>{
   await Promise.all(images.map(image=>image.decode()));
   return images.map(image=>({local:image.src.startsWith('data:image/svg+xml;')||new URL(image.src).pathname==='/logo.png',width:image.naturalWidth,height:image.naturalHeight}));
  });
  if(localAvatarImages.some(image=>!image.local||!image.width||!image.height))throw new Error('Avatar is not a decoded local image');
  if(localAvatarImages.length)avatarImageAudit.frames.push({screen,width,theme,images:localAvatarImages.length});
  if(width===360&&theme==='light'&&screen===screens[0]) {
   const fonts=await page.evaluate(async cases=>{
    const canvas=document.createElement('canvas'),context=canvas.getContext('2d');
    const samples=[];
    for(const item of cases) {
     const font=item.weight+' '+item.size+'px AndroidRoboto';
     await document.fonts.load(font,item.text);context.font=font;
     const metrics=context.measureText(item.text);
     const positions=Array.from({length:item.text.length+1},(_,index)=>context.measureText(item.text.slice(0,index)).width);
     samples.push({...item,advance:metrics.width,positions,ascent:metrics.fontBoundingBoxAscent,descent:metrics.fontBoundingBoxDescent});
    }
    return samples;
   },typographyCases);
   await fs.writeFile(path.join(output,'web-fonts.json'),JSON.stringify(fonts,null,2));
  }
  if(errors.length)throw new Error(errors.join('\n'));
  if(screen==='permissions') {
   for(const id of ['confirm','chinese','japanese']){await page.locator('[data-case="'+id+'"] .permission-option').last().click();await page.locator('[data-case="'+id+'"] h3').tap();}
   await page.locator('.permissions-scene').screenshot({path:path.join(output,`web-permissions-${width}-${theme}.png`)});
   const metrics=await page.locator('.permissions-scene').evaluate(scene=>[...scene.querySelectorAll('[data-case]')].map(item=>{
    const panel=item.querySelector('.permission-popover'),origin=panel.getBoundingClientRect();
    const box=e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};};
    const text=e=>({text:e.textContent,...box(e),color:getComputedStyle(e).color});
    const confirmation=panel.querySelector('.bypass-confirmation'),scope=panel.querySelector('.permission-scope'),status=panel.querySelector(':scope > p:not(.permission-scope)'),retry=status?.querySelector('button');
    return{id:item.dataset.case,panel:box(panel),heading:box(panel.querySelector('h3')),...(status?{status:box(status),statusText:status.textContent}:{}),...(retry?{retry:text(retry)}:{}),options:[...panel.querySelectorAll('.permission-option')].map(row=>({box:box(row),title:text(row.querySelector('strong')),detail:text(row.querySelector('small')),selected:row.getAttribute('aria-pressed')==='true'?'true':'false',enabled:row.disabled?'false':'true',checks:row.querySelectorAll('svg').length,...(row.querySelector('svg')?{check:{...box(row.querySelector('svg')),color:getComputedStyle(row.querySelector('svg')).color}}:{})})),...(confirmation?{confirmation:box(confirmation),warning:text(confirmation.querySelector('p')),actions:[...confirmation.querySelectorAll('button')].map(text)}:{}),...(scope?{scope:text(scope)}:{})};
   }));
   await fs.writeFile(path.join(output,`web-permissions-${width}-${theme}.json`),JSON.stringify(metrics,null,2));
  }
  else if(screen.startsWith('modal-')) {
   const panel=await page.locator('.modal-surface-scene dialog').evaluate(element=>{
    const rect=element.getBoundingClientRect();
    return {left:rect.left,top:rect.top,width:rect.width,height:rect.height,radius:parseFloat(getComputedStyle(element).borderRadius)};
   });
   await page.screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
   await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(panel,null,2)+'\n');
  }
  else if(['composer-surface','floating-surface','popup-surface'].includes(screen)) {
   await page.locator(`.${screen}-scene`).screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
  }
  else if(screen.startsWith('background-')) {
   const image=await page.locator('#root > section').screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
   const samples=await page.evaluate(async bytes=>{
    const image=await createImageBitmap(new Blob([new Uint8Array(bytes)],{type:'image/png'}));
    const canvas=document.createElement('canvas');canvas.width=image.width;canvas.height=image.height;
    const context=canvas.getContext('2d');context.drawImage(image,0,0);
    const data=context.getImageData(0,0,image.width,image.height).data,samples=[];
    for(let row=0;row<=20;row++)for(let column=0;column<=20;column++){
     const x=Math.floor((image.width-1)*column/20),y=Math.floor((image.height-1)*row/20),at=(y*image.width+x)*4;
     samples.push({x,y,red:data[at],green:data[at+1],blue:data[at+2],alpha:data[at+3]});
    }
    image.close();return samples;
   },[...image]);
   await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(samples,null,2));
  }
  else if(screen==='markdown-blocks') {
   const geometry=await page.locator('.markdown-blocks-scene').evaluate(scene=>({cases:[...scene.querySelectorAll('[data-case]')].map(row=>{
    const root=row.querySelector('.markdown'),origin=root.getBoundingClientRect();
    const box=e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};};
    const nodes=[root,...root.querySelectorAll('p,h1,h2,h3,h4,h5,h6,blockquote,ul,ol,li,pre,.markdown-table,table,tr,th,td,hr,input')];
    return nodes.map(e=>({type:e===root?'root':e.classList.contains('markdown-table')?'table-scroll':e.tagName.toLowerCase(),...box(e),...(e.tagName==='LI'?{marker:getComputedStyle(e).listStyleType}:{}),...(['TH','TD'].includes(e.tagName)?{align:getComputedStyle(e).textAlign.replace('start','left').replace('end','right')}:{})}));
   })}));
   await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
   await page.locator('.markdown-blocks-scene').screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
  }
  else if(screen==='messages') {
   await page.addStyleTag({content:'@font-face{font-family:AndroidMono;src:url(/__android-reference-mono.ttf)}.messages-scene .markdown code{font-family:AndroidMono,monospace}'});
   await page.locator('[data-case=expanded] .user-message-toggle').click();
   const geometry=await page.locator('.messages-scene').evaluate(scene=>({rows:[...scene.querySelectorAll('[data-case]')].map(row=>{
    const origin=row.getBoundingClientRect();const box=e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};};
    const optional=selector=>[...row.querySelectorAll(selector)].map(box);
    const mine=row.classList.contains('user-row');
    const paragraphs=[...row.querySelectorAll('.markdown p')].map(p=>{const range=document.createRange();range.selectNodeContents(p);const tops=[...new Set([...range.getClientRects()].filter(r=>r.width>0).map(r=>Math.round(r.top*100)/100))].sort((a,b)=>a-b);return{...box(p),lineCount:tops.length,lines:tops.map(top=>({offset:top-tops[0]}))};});
    return{row:{x:0,y:0,width:origin.width,height:origin.height},content:box(mine?row.querySelector('.user-message'):row),bubble:optional(mine?'.user-bubble':'.assistant-text'),clip:optional(mine?'.user-bubble-content':'.assistant-text'),markdown:optional('.markdown'),paragraphs,footer:box(row.querySelector('.message-footer')),author:optional('.message-author'),time:optional('time'),copy:box(row.querySelector('.message-copy')),toggle:optional('.user-message-toggle'),avatar:optional('.turn-operator-avatar .user-avatar'),attachments:optional('.attachment-card')};
   })}));
   await fs.writeFile(path.join(output,`web-messages-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
   await page.locator('.messages-scene').screenshot({path:path.join(output,`web-messages-${width}-${theme}.png`)});
  }
  else if(screen==='login'||screen.startsWith('workspace')) {
   const geometry=await page.evaluate(screen=>{
    const box=e=>{const r=e.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height};};
    const read=selector=>box(document.querySelector(selector));
    if(screen==='login')return{story:read('.login-story'),brand:read('.login-story > .brand'),headline:read('.login-story h1'),panel:read('.login-panel'),form:read('.login-panel form'),heading:read('.login-panel h2'),description:read('.login-panel form > p'),username:read('input[name=username]'),password:read('input[name=password]'),submit:read('.login-panel .primary'),preferences:read('.login-preferences')};
    return{header:read('.workspace > header'),menu:read('.heading > button'),title:read('.heading strong'),subtitle:read('.heading-copy > span'),online:read('.online summary'),files:read('.header-actions > button'),welcome:read('.welcome'),mark:read('.welcome-mark'),eyebrow:read('.welcome > .eyebrow'),titleWelcome:read('.welcome h1'),description:read('.welcome p'),notes:read('.welcome-notes'),noteItems:[...document.querySelectorAll('.welcome-notes > span')].map(box),composer:read('.composer'),input:read('.composer textarea'),bottom:read('.composer-bottom'),add:read('.attachment-add'),permission:read('.permission-summary'),model:read('.picker-summary'),send:read('.send')};
   },screen);
   await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
   await page.screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
  }
  else if(screen==='syntax') {
   await page.waitForFunction(()=>[...document.querySelectorAll('.syntax-scene diffs-container')].every(host=>host.shadowRoot?.querySelector('[data-line] span[style]')));
   const geometry=await page.locator('.syntax-scene').evaluate(scene=>{
    const read=(host,selector)=>[...host.shadowRoot.querySelectorAll(selector)].map(line=>{
     const bounds=line.getBoundingClientRect(),base=bounds.x+parseFloat(getComputedStyle(line).paddingLeft),cells=[];
     const walker=document.createTreeWalker(line,NodeFilter.SHOW_TEXT);let node;
     while(node=walker.nextNode()) {
      const style=getComputedStyle(node.parentElement),rgb=style.color.match(/[\d.]+/g).map(Number);
      const color=(0xff000000|(rgb[0]<<16)|(rgb[1]<<8)|rgb[2])>>>0;
      const flags=(style.fontStyle==='italic'?1:0)|(Number(style.fontWeight)>=600?2:0)|(style.textDecorationLine.includes('underline')?4:0)|(style.textDecorationLine.includes('line-through')?8:0);
      for(let i=0;i<node.textContent.length;i++)if(node.textContent[i]!=='\n') {
       const range=document.createRange();range.setStart(node,i);range.setEnd(node,i+1);const box=range.getBoundingClientRect();
       cells.push({color,style:flags,x:box.x-base,end:box.right-base});
      }
     }
     return cells;
    });
    const source=scene.querySelector('.file-code-preview'),diff=scene.querySelector('.syntax-diff diffs-container');
    return{source:read(source,'[data-line]'),old:read(diff,'[data-deletions] [data-line]'),next:read(diff,'[data-additions] [data-line]')};
   });
   await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
   await page.locator('.syntax-scene').screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
  }
  else if(screen==='diff-layout') {
   await page.waitForFunction(()=>document.querySelector('.diff-layout diffs-container')?.shadowRoot?.querySelector('[data-diff-span]'));
   const geometry=await page.locator('.diff-layout diffs-container').evaluate(host=>{
    const side=selector=>[...host.shadowRoot.querySelectorAll(selector+' [data-line]')].map(line=>{
     const r=line.getBoundingClientRect(),style=getComputedStyle(line),left=r.x+parseFloat(style.paddingLeft);
     return [...line.querySelectorAll('[data-diff-span]')].map(span=>{const s=span.getBoundingClientRect();return{x:s.x-left,y:s.y-r.y,width:s.width,height:s.height};});
    });
    return{old:side('[data-deletions]'),next:side('[data-additions]')};
   });
   await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
   await page.locator('.diff-layout').screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
  }
  else if(screen==='code-tabs') {
   await page.waitForFunction(()=>document.querySelector('.file-code-preview')?.shadowRoot?.querySelector('[data-line]'));
   await page.waitForFunction(()=>document.querySelector('.code-tabs-diff diffs-container')?.shadowRoot?.querySelector('[data-line]'));
   const geometry=await page.evaluate(()=>{
    const positions=root=>{const result=[];const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);let node;while(node=walker.nextNode()){for(let i=0;i<node.textContent.length;i++)if(node.textContent[i]==='\t'){const range=document.createRange();range.setStart(node,i+1);range.collapse(true);const line=node.parentElement.closest('[data-line]')||root;const style=getComputedStyle(line);const left=line.getBoundingClientRect().x+parseFloat(style.paddingLeft);result.push({after:range.getBoundingClientRect().x-left});}}return result;};
    const source=document.querySelector('.file-code-preview').shadowRoot;
    const markdown=document.querySelector('.assistant-text .markdown pre code'),detail=document.querySelector('.tool-detail .markdown pre code'),preview=document.querySelector('.file-preview-scroll .markdown pre code');
    const diff=document.querySelector('.code-tabs-diff diffs-container').shadowRoot;
    return{source:[...source.querySelectorAll('[data-line]')].flatMap(positions),markdown:positions(markdown),detail:positions(detail),preview:positions(preview),diff:[...diff.querySelectorAll('[data-line]')].flatMap(positions)};
   });
   await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
  }
  else if(screen.startsWith('sidebar')) {
   await page.addStyleTag({content:'.task-spinner{animation:none}.sidebar{transition:none}'});
   await page.locator('.sidebar').screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
   const geometry=await page.locator('.sidebar').evaluate(panel=>{const origin=panel.getBoundingClientRect();const box=e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};};return{panel:box(panel),brand:box(panel.querySelector('.brand')),scroll:box(panel.querySelector('.sidebar-scroll')),footer:box(panel.querySelector('.side-foot')),headings:[...panel.querySelectorAll('.nav-section h2')].map(box),projects:[...panel.querySelectorAll('.project-row')].map(box),chats:[...panel.querySelectorAll('.nav-chat')].map(box),more:[...panel.querySelectorAll('.show-more')].map(box),footerItems:[...panel.querySelector('.side-foot').children].map(box),operators:[...panel.querySelectorAll('.chat-operators')].map(box),open:[...panel.querySelectorAll('.nav-chat-open')].map(box),titles:[...panel.querySelectorAll('.nav-chat-title')].map(box),agents:[...panel.querySelectorAll('.nav-chat-agent')].map(box),locals:[...panel.querySelectorAll('.nav-chat-local')].map(box),spinners:[...panel.querySelectorAll('.task-spinner')].map(box),projectToggle:[...panel.querySelectorAll('.project-toggle')].map(box),projectNew:[...panel.querySelectorAll('.project-new')].map(box)};});
   if(screen==='sidebar-operators') {
    await page.locator('.chat-operators').first().tap();await page.locator('.chat-operator-popover').waitFor();
    await page.locator('.chat-operator-popover').screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
    const tooltip=await page.locator('.chat-operator-popover').evaluate(panel=>{const origin=panel.getBoundingClientRect();const box=e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};};return{panel:box(panel),rows:[...panel.querySelectorAll('li')].map(box)};});
    await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(tooltip,null,2));
   }else await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
  }
  else if(screen==='online-menu') {
   await page.locator('.online-list').screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
   const geometry=await page.locator('.online-list').evaluate(panel=>{const origin=panel.getBoundingClientRect();const box=e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};};return{panel:box(panel),rows:[...panel.querySelectorAll(':scope > div')].map(box),options:[...panel.querySelectorAll('button')].map(box)};});
   await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
  }
  else if(screen.startsWith('people')||screen==='group-picker') {
   if(screen==='group-picker'){await page.getByRole('checkbox',{name:'Mina'}).check();await page.getByRole('checkbox',{name:'Lin'}).check();await page.locator('h2').tap();}
   const selector=screen==='group-picker'?'.people-group-picker':'.direct-message-panel';
   await page.locator(selector).screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
   if(screen.startsWith('people')) {
    const geometry=await page.locator(selector).evaluate(panel=>{const origin=panel.getBoundingClientRect();const box=e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};};return{panel:box(panel),sidebar:box(panel.querySelector('.direct-message-sidebar')),toolbar:box(panel.querySelector('.direct-message-toolbar')),history:box(panel.querySelector('.direct-message-log')),composer:box(panel.querySelector('.direct-message-composer')),editor:box(panel.querySelector('textarea')),send:box(panel.querySelector('.direct-message-composer button')),rows:[...panel.querySelectorAll('.direct-message-conversations button')].map(box),messages:[...panel.querySelectorAll('.direct-message')].map(box),edges:[...panel.querySelectorAll('.direct-message-edge')].map(box)};});
    await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
   }
   else {
    const geometry=await page.locator(selector).evaluate(panel=>{const origin=panel.getBoundingClientRect();const box=e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};};return{panel:box(panel),heading:box(panel.querySelector('.dialog-heading')),name:box(panel.querySelector('.people-group-name input')),notice:box(panel.querySelector('.people-group-notice')),members:box(panel.querySelector('.people-group-members')),rows:[...panel.querySelectorAll('.people-group-members label')].map(box),actions:box(panel.querySelector('.people-group-actions')),count:box(panel.querySelector('.people-group-actions small')),submit:box(panel.querySelector('.people-group-actions button'))};});
    await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
   }
  }
  else if(screen.startsWith('files')) {
   if(screen==='files-markdown')await page.getByRole('button',{name:'Preview',exact:true}).click();
   await page.waitForTimeout(800);
   await page.locator('.file-sidebar').screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
   if(screen==='files') {
    const geometry=await page.locator('.file-sidebar').evaluate(panel=>{
     const origin=panel.getBoundingClientRect();const box=e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};};
     const tree=panel.querySelector('file-tree-container').shadowRoot;
     return {panel:box(panel),header:box(panel.querySelector('.file-sidebar-header')),split:box(panel.querySelector('.file-sidebar-content')),tree:box(panel.querySelector('.file-tree-pane')),handle:box(panel.querySelector('.file-preview-resize-handle')),preview:box(panel.querySelector('.file-preview')),previewHeader:box(panel.querySelector('.file-preview-header')),meta:box(panel.querySelector('.file-preview-meta')),previewScroll:box(panel.querySelector('.file-preview-scroll')),rows:[...tree.querySelectorAll('[data-type="item"]')].filter(e=>e.getBoundingClientRect().top<panel.querySelector('.file-tree-pane').getBoundingClientRect().bottom).map(box)};
    });
    await fs.writeFile(path.join(output,`web-files-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
    if(width===360)await fs.writeFile(path.join(output,`web-file-source-${theme}.json`),JSON.stringify(await page.locator('.file-code-preview').evaluate(host=>[...host.shadowRoot.querySelectorAll('pre,[data-line],[data-column-number],[data-code]')].slice(0,12).map(e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return{attributes:Object.fromEntries([...e.attributes].map(a=>[a.name,a.value])),text:e.textContent,rect:{x:r.x,y:r.y,width:r.width,height:r.height},font:s.font,lineHeight:s.lineHeight,padding:s.padding,color:s.color,background:s.backgroundColor};})),null,2));
   }
  }
  else if(screen==='appearance') {
   await page.locator('#appearance-title').tap();
   await page.locator('.appearance-dialog').screenshot({path:path.join(output,`web-appearance-${width}-${theme}.png`)});
   const geometry=await page.locator('.appearance-dialog').evaluate(panel=>{
    const origin=panel.getBoundingClientRect();const box=e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};};
    const fieldset=panel.querySelector('fieldset'),language=panel.querySelector('.language-choice');
    const start=fieldset.getBoundingClientRect(),end=language.getBoundingClientRect();
    return{panel:box(panel),heading:box(panel.querySelector('.dialog-heading')),appearance:{x:start.x-origin.x,y:start.y-origin.y,width:start.width,height:end.bottom-start.top},themes:[...panel.querySelectorAll('.theme-options button')].map(box),language:box(panel.querySelector('.custom-select-trigger')),version:box(panel.querySelector(':scope > p'))};
   });
   await fs.writeFile(path.join(output,`web-appearance-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
  }
  else if(screen==='avatar')await page.locator('.avatar-dialog').screenshot({path:path.join(output,`web-avatar-picker-${width}-${theme}.png`)});
  else if(screen==='attachments') {
   await page.locator('.attachment-scene').screenshot({path:path.join(output,`web-attachments-${width}-${theme}.png`)});
   const geometry=await page.locator('.attachment-scene').evaluate(scene=>{
    const origin=scene.getBoundingClientRect();
    const boxes=selector=>[...scene.querySelectorAll(selector)].map(e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};});
    return {draft:boxes('.draft-fixture .attachment-card'),history:boxes('.user-message .attachment-card')};
   });
   await fs.writeFile(path.join(output,`web-attachments-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
  }
  else if(screen==='image') {
   await page.locator('.image-preview > img').evaluate(async image=>{if(!image.complete)await new Promise(resolve=>image.addEventListener('load',resolve,{once:true}));});
   await page.locator('.image-preview > img').tap();
   await page.locator('.image-preview').screenshot({path:path.join(output,`web-image-preview-${width}-${theme}.png`)});
   const geometry=await page.locator('.image-preview').evaluate(panel=>{
    const origin=panel.getBoundingClientRect();const box=e=>{const r=e.getBoundingClientRect();return{x:r.x-origin.x,y:r.y-origin.y,width:r.width,height:r.height};};
    return {panel:box(panel),toolbar:box(panel.querySelector('.image-preview-toolbar')),image:box(panel.querySelector('img'))};
   });
   await fs.writeFile(path.join(output,`web-image-${width}-${theme}.json`),JSON.stringify(geometry,null,2));
  }
  else if(screen==='model')await page.locator('.model-popover').screenshot({path:path.join(output,`web-model-menu-${width}-${theme}.png`)});
  else if(screen==='activity'){
   await page.locator('.activity-scene').screenshot({path:path.join(output,`web-activity-${width}-${theme}.png`)});
   await page.getByRole('button',{name:'Read web/src/style.css',exact:true}).click();
   await page.locator('.activity-scene').screenshot({path:path.join(output,`web-activity-detail-${width}-${theme}.png`)});
   await page.getByRole('button',{name:'Read web/src/style.css',exact:true}).click();
   await page.getByRole('button',{name:/^Edited src\/mobile\/NativeUi.java/}).click();
   await page.locator('.tool-diff').waitFor();
   await page.waitForTimeout(700);
   await page.locator('.activity-scene').screenshot({path:path.join(output,`web-activity-patch-${width}-${theme}.png`)});
   if(width===360)await fs.writeFile(path.join(output,`web-diff-${theme}.json`),JSON.stringify(await page.locator('.tool-diff diffs-container').evaluate(host=>{
    const root=host.shadowRoot;return [...root.querySelectorAll('[data-diffs-header], [data-diff], [data-code], [data-line], [data-column-number], [data-separator]')].map(e=>{
     const r=e.getBoundingClientRect(),s=getComputedStyle(e);return {tag:e.tagName,attributes:Object.fromEntries([...e.attributes].map(a=>[a.name,a.value])),text:e.textContent,rect:{x:r.x,y:r.y,width:r.width,height:r.height},font:s.font,color:s.color,background:s.backgroundColor,padding:s.padding};
    });
   }),null,2));
  }
  else await page.screenshot({path:path.join(output,`web-${screen}-${width}-${theme}.png`)});
  const metrics=await page.evaluate(()=>Object.fromEntries(['.welcome-mark','.welcome > .eyebrow','.welcome h1','.welcome p','.welcome-notes','.composer','.composer textarea','.composer-bottom','.login-story h1','.login-panel h2','.login-panel input','.primary','.avatar-dialog','.dialog-heading','.avatar-dialog-body','.avatar-preview','.avatar-styles','.avatar-automatic','.avatar-grid','.avatar-dialog-actions','.model-popover','.agent-model-groups','.model-effort','.effort-heading','.effort-track','.effort-caption','.model-popover p:first-of-type','.model-popover p:last-of-type','.tool-activity-list','.tool-activity-summary','.tool-activity-item-summary','.tool-detail','.tool-progress-message'].map(selector=>{const e=document.querySelector(selector);if(!e)return[selector,null];const r=e.getBoundingClientRect();return[selector,{x:r.x,y:r.y,width:r.width,height:r.height,font:getComputedStyle(e).font}];})));
  if(!screen.startsWith('modal-')&&!screen.startsWith('background-')&&!screen.startsWith('sidebar')&&!screen.startsWith('files')&&!screen.startsWith('people')&&!screen.startsWith('workspace')&&!['login','composer-surface','floating-surface','popup-surface','permissions','attachments','image','appearance','group-picker','online-menu','code-tabs','diff-layout','syntax','messages','markdown-blocks'].includes(screen))await fs.writeFile(path.join(output,`web-${screen}-${width}-${theme}.json`),JSON.stringify(metrics,null,2));
  await context.close();
 }
 await fs.writeFile(path.join(output,'avatar-image-audit.json'),JSON.stringify(avatarImageAudit,null,2)+'\n');
 if(avatarImageAudit.imageAPIRequests)throw new Error('Local avatars unexpectedly requested the image API');
 console.log(`Decoded local avatars in ${avatarImageAudit.frames.length} frames; zero avatar image API requests.`);
 console.log('Rendered Web mobile references using original CSS and components.');
}finally{await browser?.close();await server.close();}
