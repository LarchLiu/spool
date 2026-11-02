#!/usr/bin/env node
// Exercise the actual Web effect and profile cache; do not invent a second loading oracle.
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const require=createRequire(path.join(root,'web/package.json')),{buildSync}=require('esbuild');
function web(file) {
 const module={exports:{}};
 const result=buildSync({entryPoints:[path.join(root,'web/src',file)],bundle:true,write:false,format:'cjs',platform:'node',packages:'external',logLevel:'silent'});
 vm.runInNewContext(result.outputFiles[0].text,{module,exports:module.exports,require,console,crypto:globalThis.crypto});return module.exports;
}
const {createProfileCache}=web('user-profiles.ts'),{mergeNavigationChats}=web('workspace-stream.ts');
const source=fs.readFileSync(path.join(root,'web/src/main.tsx'),'utf8');
const start=source.lastIndexOf('let cancelled = false;',source.indexOf('let realtimeConnected = false;'));
const end=source.indexOf('    loadNavigation(true);',start);
if(start<0||end<0)throw new Error('Actual Web loading effect moved; inspect it before regenerating');
const effect=buildSync({stdin:{contents:`export function effect(){${source.slice(start,end)};return {load:loadNavigation,navigation:receiveNavigation,profiles:receiveProfiles};}`,loader:'ts'},write:false,format:'cjs'}).outputFiles[0].text;
const missingStart=source.indexOf('const missingProfileIDs = useMemo(() => {')+'const missingProfileIDs = useMemo(() => {'.length;
const missingEnd=source.indexOf('}, [profiles, chats, chat, online]);',missingStart);
if(missingStart<40||missingEnd<0)throw new Error('Actual Web missing-profile calculation moved');
const missing=new Function('profiles','chats','chat','online',source.slice(missingStart,missingEnd));
if(!source.includes('if (!realtimeConnected) loadNavigation();\n    }, 15000);')||!source.includes('}, 50);'))throw new Error('Web loading/debounce timings changed');
const project=id=>({id,name:id}),chat=(id,p='one',updated=1)=>({id,project_id:p,updated_at:updated,title:id,can_control:true,participants:[]});
const navigation=(projects,chats)=>({projects:projects.map(project),chats});
function harness(initial={}) {
 const state={project:'one',chatID:'',chat:null,chats:[],projects:[],...initial},requests=[],failures=[],profileCache=createProfileCache();
 let navigationCount=0,agentCount=0;
 const selection={current:state},module={exports:{}};
 const setter=key=>value=>{state[key]=typeof value==='function'?value(state[key]):value;};
 vm.runInNewContext(effect,{module,exports:module.exports,AbortController,profileCache,mergeNavigationChats,
  api:path=>new Promise((resolve,reject)=>requests.push({path,resolve,reject,settled:false})),
  selection,activeID:{current:''},notificationChat:{current:''},setChat:setter('chat'),
  setProject:setter('project'),setChatID:setter('chatID'),setChats:setter('chats'),
  setProjects:value=>{navigationCount++;state.projects=value;},setAgents:()=>agentCount++,
  chooseModel:()=>({backend:'codex',model:'model',effort:''}),setBackend:()=>{},setModel:()=>{},setEffort:()=>{},
  fail:error=>failures.push(error.message),openChat:()=>{throw new Error('Unexpected notification navigation');}});
 const methods=module.exports.effect();
 return {state,requests,methods,profileCache,snapshot:()=>({navigationCount,agentCount,failures:[...failures],profiles:profileCache.snapshot()}),
  reply:async(path,value,error=false)=>{const request=requests.find(request=>request.path===path&&!request.settled);if(!request)throw new Error('Missing request '+path);request.settled=true;error?request.reject(new Error(value)):request.resolve(value);for(let i=0;i<10;i++)await Promise.resolve();}};
}
const navigationCases=[];
for(const item of [
 {id:'first-project',initial:{project:'',chatID:''},steps:[navigation(['one'],[chat('latest'),chat('older')])]},
 {id:'existing-project',steps:[navigation(['one'],[chat('latest')])]},
 {id:'new-chat-stays-blank',steps:[navigation(['one'],[]),navigation(['one'],[chat('later')])]},
 {id:'deleted-current-chat',initial:{chatID:'latest'},steps:[navigation(['one'],[chat('latest')]),navigation(['one'],[chat('other')])]},
 {id:'removed-project',initial:{project:'gone',chatID:'old'},steps:[navigation(['two'],[chat('new','two')])]},
 {id:'empty-workspace',initial:{chatID:'old'},steps:[navigation([],[])]},
 {id:'preserve-newer-metadata-not-membership',initial:{chatID:'kept',chat:chat('kept','one',20),chats:[chat('kept','one',20),chat('removed','one',30)]},steps:[navigation(['one'],[{...chat('kept','one',10),can_control:false}])]},
]) {
 const h=harness(item.initial);const steps=[];
 for(const value of item.steps){h.methods.navigation(value);steps.push({navigation:value,expected:JSON.parse(JSON.stringify(h.state))});}
 navigationCases.push({id:item.id,initial:{project:'one',chatID:'',chat:null,chats:[],...item.initial},steps});
}
const loadingCases=[];
for(const mode of ['batch','navigation-push','profile-push','profile-save','navigation-failure','agents-failure']) {
 const h=harness();h.methods.load(true);h.methods.load(false);
 const restNavigation=navigation(['one'],[chat('rest')]),pushNavigation=navigation(['one'],[chat('push')]);
 const profilePush={revision:4,people:[{id:'alice',nickname:'Latest'},{id:'new',nickname:'New'}]};
 const profileSave={revision:4,people:[{id:'alice',nickname:'Saved'},{id:'revoked',nickname:'Revoked'}]};
 const restProfiles={revision:4,people:[{id:'alice',nickname:'Stale'},{id:'revoked',nickname:'Revoked'}]};
 const requests=h.requests.map(({path})=>path);
 await h.reply('/navigation',restNavigation,mode==='navigation-failure');
 const beforeAgents=h.snapshot();
 if(mode==='navigation-push')h.methods.navigation(pushNavigation);
 if(mode==='profile-push'||mode==='profile-save') {
  h.methods.profiles(profilePush);
  if(mode==='profile-save')h.profileCache.acceptSaved(profileSave,'alice');
 }
 await h.reply('/agents',[],mode==='agents-failure');
 await h.reply('/profiles',restProfiles);
 loadingCases.push({mode,requests,restNavigation,pushNavigation,profilePush,profileSave,restProfiles,beforeAgents,expected:h.snapshot()});
}
const missingCases=[
 {profiles:{revision:0,people:[]},chats:[{participants:[{id:'bob'},{id:'spool-bot'}]}],chat:{user_id:'carol',participants:[{id:'alice'}],turns:[{actions:[{user_id:'dave'}]}]},online:[{id:'eve'},{id:'bob'}]},
 {profiles:{revision:1,people:[{id:'alice'},{id:'bob'}]},chats:[{participants:[{id:'alice'},{id:'bob'}]}],chat:null,online:[{id:'spool-bot'}]},
 {profiles:{revision:0,people:[]},chats:[],chat:{user_id:'owner',participants:[],turns:[{user_id:'operator',actions:[{user_id:''},{user_id:'action'}]}]},online:[]},
].map(item=>({...item,expected:missing(item.profiles,item.chats,item.chat,item.online)}));
const result={purpose:'Actual Web main.tsx loading/navigation/missing-profile effect and user-profiles.ts cache, executed without a browser or network',navigationCases,loadingCases,missingCases,timings:{fallback:15000,missing:50}};
const target=path.join(root,'android/app/src/test/resources/web-workspace-data.json'),contents=JSON.stringify(result,null,2)+'\n';
if(process.argv.includes('--check')){if(fs.readFileSync(target,'utf8')!==contents)throw new Error('Web workspace-data behavior changed; regenerate and inspect');}
else fs.writeFileSync(target,contents);
console.log(`Verified actual Web workspace-data reference: ${navigationCases.length} navigation sequences, ${loadingCases.length} asynchronous loading races, ${missingCases.length} missing-identity vectors.`);
