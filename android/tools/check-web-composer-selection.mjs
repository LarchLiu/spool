#!/usr/bin/env node
// Execute the actual Web selection effect's synchronous reset block, not a rewritten predicate.
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(root,'web/package.json'));
const {transformSync}=require('esbuild');
const source=fs.readFileSync(path.join(root,'web/src/main.tsx'),'utf8');
const start=source.indexOf('    setPicker(false);\n    setPermissionOpen(false);\n    setPreview(null);');
const end=source.indexOf('    historyAnchor.current = null;',start);
if(start<0||end<start)throw new Error('Actual Web selection-effect reset block not found');
const effect=source.slice(start,end),cases=[];
for(const [id,chatID,adopted,reset] of [
 ['other-chat','chat-two','',true],['return-to-chat','chat-one','',true],
 ['new-chat','','',true],['fresh-new-draft','','',true],
 ['first-send-adoption','created-one','created-one',false],['fork-adoption','forked-one','forked-one',false],
 ['different-adoption','chat-two','created-one',true],['empty-adoption','','',true],
]) {
 const state={draft:'  Exact draft\n',files:['attachment-one'],chat:{id:'previous'},error:'Previous error',released:[],picker:true,permissionOpen:true,preview:'previous',notice:'previous',commandDismissed:true};
 const adoptedChatID={current:adopted};
 vm.runInNewContext(effect,{chatID,adoptedChatID,
  setPicker:value=>state.picker=value,setPermissionOpen:value=>state.permissionOpen=value,
  setPreview:value=>state.preview=value,setCommandNotice:value=>state.notice=value,
  setCommandDismissed:value=>state.commandDismissed=value,setError:value=>state.error=value,
  setChat:value=>state.chat=value,setDraft:value=>state.draft=value,
  setFiles:updater=>state.files=updater(state.files),releaseAttachments:files=>state.released.push(...files),
 });
 if(state.draft!==(reset?'':'  Exact draft\n')||state.files.length!==(reset?0:1)
  ||state.released.length!==(reset?1:0)||adoptedChatID.current!==''
  ||state.picker||state.permissionOpen||state.preview!==null||state.notice!==null||state.commandDismissed)
  throw new Error('Actual Web composer selection behavior changed: '+id);
 cases.push({id,chatID,adopted,reset,state});
}
const attachmentStart=source.indexOf('  async function addFiles(selected: File[]) {');
const attachmentEnd=source.indexOf('  async function logout()',attachmentStart);
if(attachmentStart<0||attachmentEnd<attachmentStart)throw new Error('Actual Web attachment preparation function not found');
const attachmentCode=transformSync('export '+source.slice(attachmentStart,attachmentEnd).trim(),{loader:'ts',format:'cjs'}).code;
const preparations=[];
for(const [id,change,accepted] of [
 ['unchanged',()=>{},true],
 ['other-chat',context=>context.activeID.current=context.selection.current.chatID='chat-two',false],
 ['returned-chat',context=>{context.activeID.current='chat-two';context.activeID.current='chat-one';},true],
 ['fresh-new-draft',context=>context.selection.current.draftVersion++,false],
 ['other-project',context=>context.selection.current.project='project-two',false],
 ['revoked-session',context=>context.activeUser.current='bob',false],
 ['first-chat-id-adopted',context=>context.activeID.current=context.selection.current.chatID='created-one',false],
]) {
 let release;
 const pending=new Promise(resolve=>release=resolve),state={files:[],released:[],preparing:false,error:''};
 const context={module:{exports:{}},preparingFiles:false,posting:false,composerWritable:true,
  project:'project-one',chatID:'chat-one',draftVersion:0,user:{id:'alice'},filesRef:{current:[]},
  selection:{current:{project:'project-one',chatID:'chat-one',draftVersion:0}},activeID:{current:'chat-one'},activeUser:{current:'alice'},
  validateAttachments:()=>'',prepareAttachments:()=>pending,setPreparingFiles:value=>state.preparing=value,
  setError:value=>state.error=value,setFiles:updater=>state.files=updater(state.files),
  releaseAttachments:files=>state.released.push(...files),fail:error=>state.error=String(error),t:key=>key,
 };
 vm.runInNewContext(attachmentCode,context);
 const preparing=context.module.exports.addFiles([{name:'note.txt'}]);
 if(!state.preparing)throw new Error('Actual Web attachment preparation did not start');
 change(context);release(['attachment-one']);await preparing;
 if(state.files.length!==(accepted?1:0)||state.released.length!==(accepted?0:1)||state.preparing||state.error)
  throw new Error('Actual Web attachment selection guard changed: '+id);
 preparations.push({id,accepted,state});
}
const output=path.join(root,'build/artifacts/android-native/web-composer-selection.json');
fs.writeFileSync(output,JSON.stringify({source:'web/src/main.tsx',scope:'Actual synchronous selection-effect block and async addFiles function with stubbed file preparation; not mounted React, file-decoder or device tests',cases,preparations},null,2)+'\n');
console.log('Verified 8 actual-Web selection-effect reset/adoption paths and 7 actual async attachment selection guards, exact draft whitespace and attachment release. Unchanged-selection effect behavior is checked separately in native Activity tests.');
