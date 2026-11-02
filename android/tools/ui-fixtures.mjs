// Synthetic, shared Web/native layout and behavioral fixtures; never user data.
export const typographyCases=[12,14,24,28].flatMap(size=>[400,500,600,650,700,750,800].map(weight=>({size,weight,text:'Spool native mobile 0123456789'})));
export const editorCases=[
 {id:'hint',locale:'en',value:''},
 {id:'single',locale:'en',value:'First line'},
 {id:'multi',locale:'en',value:'First line\nSecond line\nThird line'},
 {id:'six',locale:'en',value:'One\nTwo\nThree\nFour\nFive\nSix'},
 {id:'overflow',locale:'en',value:'One\nTwo\nThree\nFour\nFive\nSix\nSeven\nEight'},
 {id:'wrapped',locale:'en',value:'A long message that wraps naturally across several lines in the mobile composer. The history must remain stable while the input grows.'},
 {id:'chinese-hint',locale:'zh-CN',value:''},
 {id:'chinese',locale:'zh-CN',value:'原生输入框保留中文输入。\n中英文可以一起显示 Native input。\n多行文字的基线与移动端一致。'},
 {id:'japanese-hint',locale:'ja',value:''},
 {id:'japanese',locale:'ja',value:'ネイティブの入力欄です。\n日本語と English を入力します。\n行間と文字の位置を確認します。'},
];
export const composerLayoutCases=[
 {id:'empty',width:360,height:800,editor:'hint'},
 {id:'six',width:360,height:800,editor:'six'},
 {id:'keyboard',width:360,height:560,editor:'six'},
 {id:'overflow',width:360,height:560,editor:'overflow'},
 {id:'narrow',width:220,height:500,editor:'wrapped'},
 {id:'wide',width:900,height:500,editor:'multi'},
 {id:'restored',width:360,height:800,editor:'hint'},
 {id:'chinese-hint',width:360,height:800,editor:'chinese-hint'},
 {id:'chinese',width:360,height:560,editor:'chinese'},
 {id:'japanese-hint',width:360,height:800,editor:'japanese-hint'},
 {id:'japanese',width:360,height:560,editor:'japanese'},
].map(({editor,...viewport})=>{
 const sample=editorCases.find(item=>item.id===editor);
 if(!sample)throw new Error('Missing actual editor fixture: '+editor);
 return {...viewport,locale:sample.locale,value:sample.value};
});
export const commandCases=[
 {id:'menu',draft:'/go'},
 {id:'hint',draft:'/goal fix tests'},
 {id:'notice',draft:'',noticeKey:'No goal set.'},
 {id:'tall',draft:'',notice:Array.from({length:24},(_,index)=>`Goal detail ${index+1}: keep the original native session.`).join('\n')},
 {id:'error',draft:'/goal fix tests',errorKey:'Goal commands do not accept attachments'},
];
export const sendButtonCases=[
 {id:'send',active:false,stopping:false,disabled:false},
 {id:'disabled-send',active:false,stopping:false,disabled:true},
 {id:'stop',active:true,stopping:false,disabled:false},
 {id:'readonly-stop',active:true,stopping:false,disabled:true},
 {id:'stopping',active:true,stopping:true,disabled:true},
];
const statePart=(text,id='part-one')=>({role:'assistant',kind:'text',text,id});
const stateChat=(status='running',parts=[statePart('A complete answer')],turns)=>({id:'chat-one',project_id:'project-one',backend:'codex',status,updated_at:20,transcript:{parts},turns:turns||[{id:'turn-one',status,part_start:0,input_end:0,part_end:parts.length,started_at:10}]});
const finishedChat=stateChat('completed');
const pendingTurn={id:'pending-one',user_id:'alice',status:'running',started_at:30,backend:'codex',requested_model:'codex-model',reasoning_effort:'high',permission_mode:'ask'};
export const chatStateCases=[
 {id:'new-send',operation:'optimistic',previous:null,project:'project-one',turn:pendingTurn,input:[{role:'user',kind:'text',text:'Hello'}]},
 {id:'continued-send',operation:'optimistic',previous:finishedChat,project:'project-one',turn:pendingTurn,input:[{role:'user',kind:'image',name:'image.png',text:'[image]'}]},
 {id:'different-chat',previous:finishedChat,next:{...finishedChat,id:'chat-two'}},
 {id:'old-snapshot',previous:finishedChat,next:{...stateChat(),updated_at:10}},
 {id:'same-version-cannot-revive',previous:finishedChat,next:stateChat()},
 {id:'preserve-shorter-text',previous:stateChat(),next:stateChat('running',[statePart('A')])},
 {id:'preserve-phase',previous:stateChat('running',[{...statePart('A complete answer'),phase:'final_answer'}]),next:stateChat()},
 {id:'preserve-cut',previous:stateChat('running',[{...statePart('A complete answer'),cut:true}]),next:stateChat('running',[statePart('A')])},
 {id:'different-part-is-authoritative',previous:stateChat(),next:stateChat('running',[statePart('A','part-two')])},
 {id:'hydrate-empty-parts',previous:stateChat(),next:stateChat('completed',[])},
 {id:'native-completion-authoritative',previous:stateChat(),next:{...stateChat('completed',[statePart('A')]),transcript:{source:'codex-native',parts:[statePart('A')]}}},
 {id:'native-alias',previous:stateChat('running',[statePart('A')],[{id:'live-one',native_turn_id:'native-one',status:'running',part_start:0,input_end:0,part_end:1}]),next:stateChat('completed',[statePart('A complete answer')],[{id:'imported-one',native_turn_id:'native-one',status:'completed',part_start:0,input_end:0,part_end:1}])},
 {id:'missing-running-retained',previous:stateChat(),next:stateChat('running',[],[])},
 {id:'missing-completed-retained',previous:finishedChat,next:stateChat('running',[],[])},
 {id:'native-missing-retired',previous:finishedChat,next:{...stateChat('completed',[],[]),transcript:{source:'codex-native',parts:[]}}},
 {id:'native-error-retains-history',previous:finishedChat,next:{...stateChat('completed',[],[]),history:{error:'Hydration unavailable'},transcript:{source:'codex-native',parts:[]}}},
 {id:'native-pagination-retains-history',previous:finishedChat,next:{...stateChat('completed',[],[]),history:{has_more:true},transcript:{source:'codex-native',parts:[]}}},
 {id:'new-turn-removes-old-running',previous:stateChat(),next:stateChat('running',[statePart('New')],[{id:'turn-two',status:'running',part_start:0,input_end:0,part_end:1}])},
 {id:'new-turn-keeps-completed',previous:finishedChat,next:stateChat('running',[statePart('New')],[{id:'turn-two',status:'running',part_start:0,input_end:0,part_end:1}])},
 {id:'adopt-created-chat',operation:'adopt',previous:null,project:'project-one',turn:pendingTurn,input:[{role:'user',kind:'text',text:'First prompt'}],next:stateChat('idle',[],[])},
];
const permissionModes=(...ids)=>ids.map(id=>({id,available:id!=='dontAsk',...(id==='dontAsk'?{reason:'Permission discovery is unavailable'}:{})}));
export const permissionCases=[
 {id:'codex',backend:'codex',selected:'ask',modes:permissionModes('ask','auto','dontAsk','bypassPermissions')},
 {id:'claude',backend:'claude',selected:'acceptEdits',modes:permissionModes('acceptEdits','plan','manual','bypassPermissions')},
 {id:'loading',backend:'codex',selected:'ask',loading:true},
 {id:'failed',backend:'codex',selected:'ask',failed:true},
 {id:'confirm',backend:'claude',selected:'acceptEdits',confirm:true,modes:permissionModes('acceptEdits','bypassPermissions')},
 {id:'bypass',backend:'codex',selected:'bypassPermissions',modes:permissionModes('ask','bypassPermissions')},
 {id:'chinese',backend:'codex',selected:'auto',locale:'zh-CN',confirm:true,modes:permissionModes('ask','auto','bypassPermissions')},
 {id:'japanese',backend:'claude',selected:'plan',locale:'ja',confirm:true,modes:permissionModes('plan','bypassPermissions')},
];
export const markdownCases=[
 {id:'headings',text:'# First heading\n\nBody after the heading.\n\n## Second heading\n\n### Third heading\n\n#### Fourth heading\n\n##### Fifth heading\n\n###### Sixth heading\n\nLast paragraph.'},
 {id:'quote',text:'Before the quote.\n\n> A quoted paragraph.\n>\n> Another quoted paragraph with enough content to wrap onto the next line.\n>\n> > Nested quote.\n\nAfter the quote.'},
 {id:'code',text:'Before the code.\n\n```text\nFirst line\nSecond line\nThird line\n```\n\nAfter the code.\n\n---\n\nAfter the rule.'},
 {id:'tight-list',text:'Before the list.\n\n- First item\n- Second item with enough content to wrap onto another line.\n  - Nested item\n  - Another nested item\n    - Deep square item\n- Last item\n\nAfter the list.'},
 {id:'loose-list',text:'10. First paragraph.\n\n    A second paragraph in the same item.\n\n11. Another item.\n\n12. Last item.\n\nAfter the list.'},
 {id:'table',text:'Before the table.\n\n| Left | Center | Right |\n| :--- | :---: | ---: |\n| Alpha | Beta | Gamma |\n| Short | Two words | 123 |\n\nAfter the table.'},
 {id:'table-wrapped',text:'| Name | Notes |\n| :--- | ---: |\n| First | A longer cell needs several lines at narrow mobile widths. |\n| Second | Short |\n| Third | More text in another wrapped cell. |\n\nAfter the table.'},
 {id:'zero-list',text:'0. Zero starts an ordered list.\n1. This item continues\n   on the next source line.\n\n> ## A heading inside a quote\n>\n> A paragraph.'},
 {id:'tasks',text:'- [x] Finished item\n- [ ] A pending task that wraps on the same mobile width.\n- Plain item in the same list.\n\nAfter the tasks.'},
 {id:'wide-markers',text:'1000. A large ordered marker must stay on one line.\n1001. The marker stays outside the item content box.'},
 {id:'table-default-alignment',text:'| Name | Value |\n| --- | --- |\n| First | One |\n| Second | Two |'},
 {id:'chinese-blocks',locale:'zh-CN',text:'## 原生组件与移动端一致\n\n正文保留原来的段落间距。\n\n- 标题、列表和引用都使用相同的样式。\n- 中英文可以一起显示。\n\n> 引用中的文字也保留正确的行距。'},
 {id:'japanese-blocks',locale:'ja',text:'## モバイル表示に合わせる\n\n段落と余白を共通化します。\n\n- ネイティブのコンポーネントを再利用します。\n- 見出しとリストを確認します。\n\n> 引用の行間も同じにします。'},
 {id:'literal-links',text:'Web: www.example.com\nMail: person@example.org\nRead https://example.com/a(b).\n[**Linked label**](https://example.org)'},
 {id:'chinese-links',locale:'zh-CN',text:'网站：www.example.com\n邮箱：person@example.org\n[源文件](src/main.tsx#L12C3)'},
 {id:'japanese-links',locale:'ja',text:'サイト：www.example.com\nメール：person@example.org\n[ソース](src/main.tsx#L12C3)'},
];
export const codeTabContents=['\tX','A\tX','AB\tX','ABC\tX','\t\tX','AB\tA\tX',' \tX','A \tX','\tAB\tX','\tABCD\tX','A\t\tX','\tA\t\tX','A'.repeat(31)+'\tX','\t'.repeat(20)+'X'].join('\n');
export const peopleProfiles=[
 {id:'alice',nickname:'Mobile user',avatar:{style:'clay',seed:'Mobile user'}},
 {id:'bob',nickname:'Mina',avatar:{style:'pixel-art',seed:'Mobile user'}},
 {id:'carol',nickname:'Lin',avatar:{style:'croodles',seed:'Mobile user'}},
 {id:'david',nickname:'Kai',avatar:{style:'lorelei',seed:'Mobile user'}},
];
const longUserText=Array.from({length:12},(_,i)=>`Line ${i+1}: keep the original mobile spacing.`).join('\n');
export const messageCases=[
 {id:'short',text:'Hello',locale:'en'},
 {id:'operator',text:'Ready.',locale:'en',operator:peopleProfiles[0],timestamp:1704164645000},
 {id:'wrapped',text:'A longer message should wrap naturally at the same mobile width, without stretching short bubbles or shifting the avatar.',locale:'en'},
 {id:'wrapped-operator',text:'A longer message should wrap naturally at the same mobile width, without stretching short bubbles or shifting the avatar.',locale:'en',operator:peopleProfiles[0],timestamp:1704164645000},
 {id:'chinese',text:'原生界面要和 Web 移动端一致。\n头像、图标、间距都要复用。',locale:'zh-CN',operator:{...peopleProfiles[0],nickname:'移动端成员'},timestamp:1704164645000},
 {id:'japanese',text:'ネイティブの画面を Web のモバイル表示に合わせます。\nアイコンと余白を共通化します。',locale:'ja',operator:{...peopleProfiles[0],nickname:'モバイルユーザー'},timestamp:1704164645000},
 {id:'markdown',text:'**Shared components**\n\nKeep *emphasis* and the original paragraph spacing.',locale:'en',operator:peopleProfiles[0]},
 {id:'long',text:longUserText,locale:'en',operator:peopleProfiles[0]},
 {id:'expanded',text:longUserText,locale:'en',operator:peopleProfiles[0],expanded:true},
 {id:'long-author',text:'Done',locale:'en',operator:{...peopleProfiles[0],nickname:'A very long operator nickname that needs an ellipsis'},timestamp:1704164645000},
 {id:'attachment-only',text:'',locale:'en',operator:peopleProfiles[0],attachment:2},
 {id:'attachment-text',text:'Use this archive.',locale:'en',operator:peopleProfiles[0],attachment:2},
 {id:'assistant',role:'assistant',text:'A **native answer** keeps the same typography.\n\nA second paragraph follows the Web spacing.',locale:'en',timestamp:1704164645000},
];
export const peopleOnline=[{id:'alice',state:'online'},{id:'bob',state:'online'},{id:'carol',state:'away'}];
export const peopleGroups=[{id:'group-mobile',name:'Design room',members:['alice','bob','carol'],created_by:'alice'}];
export const peopleMessages=[
 {id:'hello',request_id:'',from:'bob',to:'alice',text:'The native components are ready.\nCan you check the spacing?',sent_at:1793413440000},
 {id:'reply',request_id:'sent',from:'alice',to:'bob',text:'Yes, the icons and avatars match.',sent_at:1793413500000},
 {id:'more',request_id:'',from:'bob',to:'alice',text:'Great. Keep the same mobile layout.',sent_at:1793413560000},
];
export const sidebarProjects=Array.from({length:7},(_,i)=>({id:`project-${i}`,name:i===0?'Mobile project':i===1?'Read-only design archive':'Project '+(i+1),can_create:i!==1}));
export const sidebarChats=Array.from({length:18},(_,i)=>({
 id:`chat-${i}`,project_id:i<16?'project-0':`project-${i-15}`,title:i===0?'Native mobile workspace':i===1?'A very long conversation title that truncates':i===2?'New chat':i===3?'Desktop-local session':'Layout conversation '+(i+1),
 backend:i%2?'claude':'codex',updated_at:1000-i,status:i===0?'running':'completed',local_session:i===3,
 participants:i===0?peopleProfiles.slice(0,4).map(p=>({id:p.id})):i===1?[{id:'bob'}]:i===2?peopleProfiles.slice(0,2).map(p=>({id:p.id})):[],
})).reverse();
export const fileDirectories={
 '.':{path:'.',entries:[
  {name:'src',path:'src',kind:'directory'},
  {name:'docs',path:'docs',kind:'directory'},
  {name:'.config',path:'.config',kind:'directory'},
  {name:'README.md',path:'README.md',kind:'file'},
  {name:'package.json',path:'package.json',kind:'file'},
  {name:'.gitignore',path:'.gitignore',kind:'file'},
 ],truncated:false},
 'src':{path:'src',entries:[
  {name:'NativeUi.java',path:'src/NativeUi.java',kind:'file'},
  {name:'assets',path:'src/assets',kind:'directory'},
  {name:'mobile.tsx',path:'src/mobile.tsx',kind:'file'},
  {name:'main.go',path:'src/main.go',kind:'file'},
 ],truncated:false},
 'docs':{path:'docs',entries:[{name:'guide.md',path:'docs/guide.md',kind:'file'}],truncated:false},
 'src/assets':{path:'src/assets',entries:[{name:'mobile.png',path:'src/assets/mobile.png',kind:'file'}],truncated:false},
 '.config':{path:'.config',entries:[],truncated:false},
};
export const projectFiles={
 'src/NativeUi.java':{path:'src/NativeUi.java',name:'NativeUi.java',size:384,kind:'text',media_type:'text/plain; charset=utf-8',content:'final class NativeUi {\n    // Shared mobile components.\n    String title = "Project files";\n    boolean nativeViews = true;\n\n    void render() {\n        showTree();\n        showPreview();\n    }\n}\n',truncated:false},
 'README.md':{path:'README.md',name:'README.md',size:2841,kind:'text',media_type:'text/plain',content:'# Mobile workspace\n\nNative **reusable components**, with the same icons and layout.\n\n- Browse project files\n- Preview Markdown and images\n\n[Source component](src/NativeUi.java#L3)\n',truncated:false},
 'src/assets/mobile.png':{path:'src/assets/mobile.png',name:'mobile.png',size:12085,kind:'image',media_type:'image/png',content:null,truncated:false},
 'archive.zip':{path:'archive.zip',name:'archive.zip',size:10240,kind:'binary',media_type:'application/octet-stream',content:null,truncated:false},
};
export const agents = [
  {backend:'codex',enabled:true,installed:true,catalog:{defaultModel:'gpt-6.1-codex',defaultEffort:'high',models:[
    {model:'gpt-6.1-codex',displayName:'GPT-6.1 Codex',defaultReasoningEffort:'medium',supportedReasoningEfforts:[
      {reasoningEffort:'low'},{reasoningEffort:'medium'},{reasoningEffort:'high'},{reasoningEffort:'xhigh'}]},
    {model:'gpt-mini',displayName:'GPT Mini',defaultReasoningEffort:'low',supportedReasoningEfforts:[{reasoningEffort:'low'},{reasoningEffort:'high'}]},
  ]}},
  {backend:'claude',enabled:true,installed:true,catalog:{defaultModel:'claude-sonnet',defaultEffort:'',models:[
    {model:'claude-sonnet',displayName:'Claude Sonnet',defaultReasoningEffort:'',supportedReasoningEfforts:[{reasoningEffort:'low'},{reasoningEffort:'high'}]},
    {model:'claude-fixed',displayName:'Claude Fixed',defaultReasoningEffort:'high',supportedReasoningEfforts:[]},
  ]}},
];
export const attachmentFiles=[
 {id:'image-1',name:'mobile-preview.png',media_type:'image/png',size:1234,preview_url:'/__android-reference-image.png'},
 {id:'code-1',name:'NativeUi.TSX',media_type:'text/plain',size:321,excerpt:'export function AttachmentCard() {\n  return <article className="attachment-card">\n    Shared icons and spacing\n  </article>;\n}\n'},
 {id:'archive-1',name:'project-assets.zip',media_type:'application/zip',size:8765},
 {id:'notes-1',name:'notes.md',media_type:'text/markdown',size:120,excerpt:'# Mobile workspace\n\nThe same attachment components\nare used in drafts and history.\n'},
];
export const tool=(name,text)=>({role:'assistant',kind:'tool_use',name,text});
export const result=text=>({role:'tool',kind:'tool_result',text});
export const message=text=>({role:'assistant',kind:'text',phase:'commentary',text});
export const patch='*** Begin Patch\n*** Update File: src/mobile/NativeUi.java\n@@\n-old spacing\n+shared spacing\n+shared icons\n*** End Patch';
export const activityParts=[
  {role:'assistant',kind:'thinking',text:'## Native components\n\nKeep **shared spacing** and `vector icons`.\n\n- Read the current styles\n- Preserve the transport'},
  tool('reasoning','Check the **mobile layout** before editing.'),
  tool('Read',JSON.stringify({file_path:'web/src/style.css'})),result('The shared mobile styles.'),
  tool('Read',JSON.stringify({file_path:'android/NativeUi.java'})),result('The native UI components.'),
  message('The icons already share their original **SVG geometry**.'),
  tool('functions.apply_patch',patch),result('Success. Updated the following files:\nM src/mobile/NativeUi.java'),
  tool('exec_command','{"cmd":"go test ./..."}'),result('ok   spool/internal/workspace'),
];
export const activityCases=[
  {name:'live-groups',backend:'codex',parts:activityParts},
  {name:'orphan-and-progress',backend:'claude',parts:[tool('Read','{"file_path":"a.txt"}'),message('Still working'),result('unpaired output'),message(' \n')]},
  {name:'editing-priority',backend:'claude',parts:[
    tool('Write','{"file_path":"pong.html","content":"controls.find cat rg websearch"}'),
    tool('Edit','{"path":"/a/b/c/d/file with spaces.txt","content":"find cat"}'),
    tool('apply_patch','*** Begin Patch\n*** Add File: folder/new game.html\n+first\n+second\n*** End Patch'),
    tool('functions.apply_patch','*** Begin Patch\n*** Delete File: folder/old game.html\n*** End Patch'),
    tool('apply_patch','*** Begin Patch\n*** Add File: a.txt\n+first\n*** Update File: b.txt\n-old\n+new\n*** End Patch'),
  ]},
  {name:'commands-and-fields',backend:'codex',parts:[
    tool('exec_command','{"cmd":"ls -la"}'),tool('exec_command','{"cmd":"rg -n foo src"}'),
    tool('exec_command','{"cmd":"cat README.md"}'),tool('exec_command','{"cmd":"go test ./..."}'),
    tool('Grep','{"pattern":"native\\ncomponents","path":"src"}'),
    tool('WebSearch','{"query":"Android SVG geometry"}'),tool('custom_tool','result summary'),
    tool('Read','{"file_path":42,"filename":"folder/file.txt"}'),tool('Write','changed file +12 -4'),
  ]},
  {name:'result-stats',backend:'codex',parts:[tool('Edit','{"path":"a.txt"}'),result('Output:\ndiff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1,2 @@\n-old\n+new\n+extra\n')]},
  {name:'thoughts-are-markdown',backend:'claude',parts:[{role:'assistant',kind:'thinking',text:'**Full thought** '.repeat(40)+'\n\n[Link](https://example.com)\n\n```java\nNativeUi ui;\n```'}]},
];

const unified=(name='a.txt')=>`diff --git a/${name} b/${name}\n--- a/${name}\n+++ b/${name}\n@@ -1 +1,2 @@\n-old\n+new\n+extra\n`;
export const patchCases=[
 {name:'update',text:patch},
 {name:'multiple-contexts-and-move',text:'*** Begin Patch\r\n*** Update File: folder/old "game".html\r\n*** Move to: folder/new "game".html\r\n@@ function first()\r\n context\r\n-old\r\n+new\r\n@@ function last()\r\n-last\r\n+last edited\r\n\r\n*** End of File\r\n*** End Patch'},
 {name:'add-empty-delete-implicit',text:'*** Begin Patch\n*** Add File: a/file with spaces.txt\n+first\n+\n+last\n*** Add File: empty.txt\n*** Delete File: deleted.txt\n*** Update File: edited.txt\n-before\n+after\n*** End Patch'},
 {name:'git-multiple',text:unified()+unified('b.txt')},
 {name:'unified-multiple',text:(unified()+unified('b.txt')).replace(/^diff --git .*\n/gm,'')},
 {name:'numbered-context',text:'Output:\ndiff --git a/src/view.java b/src/view.java\n--- a/src/view.java\n+++ b/src/view.java\n@@ -40,3 +40,4 @@ render()\n context\n-old\n+new\n+another\n \n'},
 {name:'no-final-newline',text:'diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n'},
 {name:'quoted-utf8-path',text:'diff --git "a/folder/game \\303\\251.txt" "b/folder/game \\303\\251.txt"\n--- "a/folder/game \\303\\251.txt"\n+++ "b/folder/game \\303\\251.txt"\n@@ -1 +1 @@\n-old\n+new\n'},
 {name:'git-new-file',text:'diff --git a/new.txt b/new.txt\nnew file mode 100644\n--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1,2 @@\n+one\n+two\n'},
 {name:'git-delete-file',text:'diff --git a/deleted.txt b/deleted.txt\ndeleted file mode 100644\n--- a/deleted.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-one\n-two\n'},
 ...[
  patch.replace('*** End Patch',''),patch.replace('*** Begin Patch',''),patch.replace('+shared spacing','unmarked'),
  '*** Begin Patch\n*** Add File: a.txt\n+first\nnot an addition\n*** End Patch',
  '*** Begin Patch\n*** Delete File: a.txt\n-old\n*** End Patch',
  '*** Begin Patch\n*** Update File: a.txt\n@@\n@@\n-old\n+new\n*** End Patch',
  '*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** End of File\n+extra\n*** End Patch',
  '*** Begin Patch\n*** Update File: a.txt\n@@\n context only\n*** End Patch',
  patch+'\n*** Begin Patch\n*** Add File: unfinished.txt\n+not complete',
  'diff --git a/a.js b/a.js\n--- a/a.js\n+++ b/a.js\n@@ -2,3 +2,3 @@\n-partial\n',
  unified()+'diff --git invalid header\n@@ -1 +1 @@\n-old\n+new\n',
  '--- a/a.js\n+++ b/a.js\n@@ not a hunk @@\n-old\n+new\n',
  unified().replace('@@ -1 +1,2 @@','@@ -1 +1 @@'),
  'Success. Updated the following files:\nA pong.html',
 ].map((text,index)=>({name:`malformed-${index}`,text})),
];

export const diffLayoutContents = 'diff --git a/sample.txt b/sample.txt\n@@ -1,4 +1,5 @@\n'+
 '-first old + stable + end;\n-\treturn old;\n-second old + stable + old;\n-same();\n'+
 '+// prologue\n+first next + stable + done;\n+\treturn next;\n+second next + stable + next;\n+same();\n';

export const syntaxSourceContents = '// Shared native syntax\nexport function greet(name: string) {\n\t/* multiline\n\t   comment */\n\treturn `Hello ${name}`;\n}\n';
export const syntaxNextContents = syntaxSourceContents.replace('greet', 'welcome').replace('Hello', 'Welcome');
export const syntaxPatchContents = 'diff --git a/render.ts b/render.ts\n--- a/render.ts\n+++ b/render.ts\n@@ -1,6 +1,6 @@\n' +
 syntaxSourceContents.trimEnd().split('\n').map(line => '-' + line + '\n').join('') +
 syntaxNextContents.trimEnd().split('\n').map(line => '+' + line + '\n').join('');
