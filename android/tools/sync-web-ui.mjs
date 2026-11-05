#!/usr/bin/env node
// Keep native assets reproducible from the same packages and CSS as the Web UI.
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';
import { labMix } from './css-lab.mjs';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const require = createRequire(path.join(root, 'web/package.json'));
const React = require('react');
const { renderToStaticMarkup } = require('react-dom/server');
const lucide = require('lucide-react');
const postcss = require('postcss');
const check = process.argv.includes('--check');
const generated = [];
function write(relative, contents) {
  const target = path.join(root, relative);
  generated.push(relative);
  if (check) {
    if (!fs.existsSync(target) || fs.readFileSync(target, 'utf8') !== contents)
      throw new Error(`Web/native asset drift: ${relative}. Run node android/tools/sync-web-ui.mjs`);
  } else {
    fs.mkdirSync(path.dirname(target), { recursive: true });
    if (!fs.existsSync(target) || fs.readFileSync(target, 'utf8') !== contents)
      fs.writeFileSync(target, contents);
  }
}
const icons = {
  menu:'Menu', close:'X', back:'ArrowLeft', send:'ArrowUp', stop:'Square', arrow_down:'ArrowDown',
  files_close:'PanelRightClose', file:'File', info:'Info', folder_git:'FolderGit2', git_branch:'GitBranch', git_commit:'GitCommitHorizontal',
  plus:'Plus', folder:'Folder', folder_open:'FolderOpen', files:'PanelRightOpen',
  project_folder:'Folder', project_folder_open:'FolderOpen',
  people:'Users', user_plus:'UserPlus', attach:'Paperclip', settings:'Settings2', file_text:'FileText',
  file_code:'FileCode2', chevron_down:'ChevronDown', chevron_up:'ChevronUp',
  chevron_right:'ChevronRight', download:'Download', log_out:'LogOut',
  monitor:'Monitor', sun:'Sun', moon:'Moon', check:'Check', command:'Command',
  copy:'Copy', circle_alert:'CircleAlert', shield_check:'ShieldCheck', shield_question:'ShieldQuestion',
  message:'MessageCircle', search:'Search', refresh:'RefreshCw', spinner:'LoaderCircle',
  brain:'Brain', wrench:'Wrench', terminal:'Terminal', globe:'Globe2', list:'List',
  book_open:'BookOpen', file_pen:'FilePenLine', file_plus:'FilePlus2', file_x:'FileX2', crosshair:'Crosshair',
};
function attrs(source) {
  return Object.fromEntries([...source.matchAll(/([\w:-]+)="([^"]*)"/g)].map(m => [m[1], m[2]]));
}
function geometry(tag, a) {
  const n = (key, fallback = 0) => +(a[key] ?? fallback);
  if (tag === 'path') return a.d;
  if (tag === 'line') return `M${n('x1')} ${n('y1')}L${n('x2')} ${n('y2')}`;
  if (tag === 'circle' || tag === 'ellipse') {
    const x = n('cx'), y = n('cy'), rx = n('rx', n('r')), ry = n('ry', n('r'));
    return `M${x-rx} ${y}a${rx} ${ry} 0 1 0 ${rx*2} 0a${rx} ${ry} 0 1 0 ${-rx*2} 0Z`;
  }
  if (tag === 'polyline' || tag === 'polygon') return `M${a.points.trim().replace(/\s+/g, ' ')}${tag === 'polygon' ? 'Z' : ''}`;
  if (tag === 'rect') {
    const x=n('x'), y=n('y'), w=n('width'), h=n('height'), r=n('rx');
    return `M${x+r} ${y}h${w-r*2}a${r} ${r} 0 0 1 ${r} ${r}v${h-r*2}a${r} ${r} 0 0 1 ${-r} ${r}h${r*2-w}a${r} ${r} 0 0 1 ${-r} ${-r}v${r*2-h}a${r} ${r} 0 0 1 ${r} ${-r}Z`;
  }
  throw new Error(`Unsupported SVG geometry: ${tag}`);
}
const escape = v => String(v).replaceAll('&','&amp;').replaceAll('"','&quot;').replaceAll('<','&lt;');
// SVG permits adjacent arc flags (e.g. 013.04); Android's parser requires separators.
function normalizePath(source) {
  const counts={m:2,l:2,h:1,v:1,c:6,s:4,q:4,t:2,a:7,z:0};
  let at=0, command='', index=0;
  const result=[];
  while(at<source.length) {
    if(/[\s,]/.test(source[at])) { at++; continue; }
    if(/[a-df-z]/i.test(source[at])) {
      command=source[at++]; index=0; result.push(command); continue;
    }
    if(!(command.toLowerCase() in counts) || counts[command.toLowerCase()]===0)
      throw new Error(`Invalid SVG path at ${at}`);
    if(command.toLowerCase()==='a' && (index===3 || index===4)) {
      if(!/[01]/.test(source[at]))throw new Error('Invalid SVG arc flag');
      result.push(source[at++]);
    } else {
      const number=source.slice(at).match(/^[+-]?(?:\d+\.?\d*|\.\d+)(?:e[+-]?\d+)?/i);
      if(!number)throw new Error(`Invalid SVG number at ${at}`);
      result.push(number[0]); at+=number[0].length;
    }
    index=(index+1)%counts[command.toLowerCase()];
  }
  return result.join(' ');
}
function vector(svg, filled=false, stroked=!filled) {
  const [svgTag] = svg.match(/<svg[^>]*>/) || [];
  if (!svgTag) throw new Error('Missing SVG root');
  const parent=attrs(svgTag);
  const paths=[...svg.matchAll(/<(path|circle|ellipse|line|polyline|polygon|rect)\b([^>]*?)(?:\/?>)/g)].map(m => {
    const a=attrs(m[2]);
    if (a.transform) throw new Error('SVG transforms must be explicitly converted');
    return `    <path android:pathData="${escape(normalizePath(geometry(m[1],a)))}" android:fillColor="${filled ? '#FF000000' : '@android:color/transparent'}"${a.opacity ? ` android:fillAlpha="${a.opacity}"` : ''} android:fillType="${parent['fill-rule']==='evenodd' || a['fill-rule']==='evenodd' ? 'evenOdd' : 'nonZero'}"${!stroked ? '' : ` android:strokeColor="#FF000000" android:strokeWidth="${parent['stroke-width'] || 2}" android:strokeLineCap="round" android:strokeLineJoin="round"`} />`;
  });
  if (!paths.length) throw new Error('Missing SVG geometry');
  const box=(parent.viewBox||'0 0 24 24').split(/\s+/);
  return `<?xml version="1.0" encoding="utf-8"?>\n<!-- Generated by android/tools/sync-web-ui.mjs. Do not approximate SVG paths. -->\n<vector xmlns:android="http://schemas.android.com/apk/res/android" android:width="24dp" android:height="24dp" android:viewportWidth="${box[2]}" android:viewportHeight="${box[3]}">\n${paths.join('\n')}\n</vector>\n`;
}
for (const [key,name] of Object.entries(icons))
  write(`android/app/src/main/res/drawable/web_icon_${key}.xml`, vector(renderToStaticMarkup(React.createElement(lucide[name],
    key.startsWith('project_') ? {strokeWidth:1.4} : ['brain','wrench','terminal','globe','list','book_open','file_pen','file_plus','file_x'].includes(key) ? {strokeWidth:1.6} : {})), key==='stop', true));
for (const name of ['Codex','Claude']) {
  const source=path.join(root,`web/node_modules/@lobehub/icons/es/${name}/components`);
  const module=fs.existsSync(path.join(source,'Mono.mjs')) ? 'Mono.mjs' : 'Mono.js';
  const { default: Icon }=await import(pathToFileURL(path.join(source,module)).href);
  write(`android/app/src/main/res/drawable/web_icon_${name.toLowerCase()}.xml`,vector(renderToStaticMarkup(React.createElement(Icon)),true));
  icons[name.toLowerCase()]=name;
}
const {SVGSpriteSheet}=await import(pathToFileURL(path.join(root,'web/node_modules/@pierre/diffs/dist/sprite.js')).href);
for(const name of ['symbol-added','symbol-deleted','symbol-modified','symbol-moved','arrow-right-short']) {
 const symbol=[...SVGSpriteSheet.matchAll(/<symbol\b([^>]*)>([\s\S]*?)<\/symbol>/g)].find(m=>attrs(m[1]).id===`diffs-icon-${name}`);
 if(!symbol)throw new Error(`Missing Pierre icon: ${name}`);
 const key='diff_'+name.replaceAll('-','_');
 write(`android/app/src/main/res/drawable/web_icon_${key}.xml`,vector(`<svg viewBox="${attrs(symbol[1]).viewBox}">${symbol[2]}</svg>`,true));
 icons[key]=name;
}
const treeSource=fs.readFileSync(path.join(root,'web/node_modules/@pierre/trees/dist/builtInIcons.js'),'utf8');
const tree=await import('data:text/javascript;base64,'+Buffer.from(treeSource+'\nexport { BUILT_IN_FILE_NAME_TOKENS, BUILT_IN_FILE_EXTENSION_TOKENS, STANDARD_TIER_TOKENS };').toString('base64'));
for(const symbol of tree.getBuiltInSpriteSheet('standard').matchAll(/<symbol\b([^>]*)>([\s\S]*?)<\/symbol>/g)) {
 const attributes=attrs(symbol[1]),key='tree_'+attributes.id.replace(/^file-tree-(?:builtin-|icon-)/,'').replaceAll('-','_');
 write(`android/app/src/main/res/drawable/web_icon_${key}.xml`,vector(`<svg viewBox="${attributes.viewBox}">${symbol[2]}</svg>`,true));
 icons[key]=key;
}
const treeMap=map=>Object.entries(map).filter(([,token])=>tree.STANDARD_TIER_TOKENS.has(token));
const javaCases=map=>treeMap(map).map(([key,token])=>`            case ${JSON.stringify(key.toLowerCase())}: return "tree-${token}";`).join('\n');
write('android/app/src/main/java/com/cloudgeek/spool/android/NativeTreeIcons.java',`package com.cloudgeek.spool.android;\n\n/** Generated from Pierre Trees' standard icon resolver and exact SVG sprite. */\nfinal class NativeTreeIcons {\n    static String file(String path) {\n        String name = path.substring(path.lastIndexOf('/') + 1).toLowerCase(java.util.Locale.ROOT);\n        switch (name) {\n${javaCases(tree.BUILT_IN_FILE_NAME_TOKENS)}\n            default: break;\n        }\n        for (int dot = name.indexOf('.'); dot >= 0; dot = name.indexOf('.', dot + 1)) {\n            switch (name.substring(dot + 1)) {\n${javaCases(tree.BUILT_IN_FILE_EXTENSION_TOKENS)}\n                default: break;\n            }\n        }\n        return "tree-default";\n    }\n}\n`);
write('android/app/src/main/java/com/cloudgeek/spool/android/NativeIcons.java', `package com.cloudgeek.spool.android;\n\nimport android.content.Context;\nimport android.graphics.drawable.Drawable;\n\n/** Generated from the Web client's Lucide and LobeHub SVGs; see android/THIRD_PARTY_NOTICES. */\nfinal class NativeIcons {\n    static Drawable drawable(Context context, String name, int color, int size) {\n        int resource;\n        switch (name.replace('-', '_')) {\n${Object.keys(icons).map(key => `            case "${key}": resource = R.drawable.web_icon_${key}; break;`).join('\n')}\n            default: throw new IllegalArgumentException("Unknown Web icon: " + name);\n        }\n        Drawable icon = context.getDrawable(resource).mutate();\n        icon.setTint(color);\n        icon.setBounds(0, 0, size, size);\n        return icon;\n    }\n}\n`);
const css=postcss.parse(fs.readFileSync(path.join(root,'web/src/style.css'),'utf8'));
function palette(selector) {
  const result={};
  css.nodes.filter(n=>n.type==='rule'&&n.selector===selector).forEach(n=>n.nodes.filter(d=>d.type==='decl'&&/^#[a-f\d]{3,8}$/i.test(d.value)).forEach(d=>{result[d.prop]=d.value;}));
  return result;
}
const light=palette(':root'), dark={...light,...palette(':root[data-theme="dark"]')};
const colors={text:'--text',muted:'--muted',subtle:'--subtle',line:'--line',green:'--green',primary:'--color-primary',primaryHover:'--color-primary-hover',surface:'--surface',sidebar:'--sidebar',elevated:'--elevated',soft:'--soft',hover:'--hover',controlHover:'--control-hover',controlActive:'--control-active',selected:'--selected',input:'--input',header:'--header',atmosphere:'--atmosphere',borderStrong:'--border-strong',error:'--error-text',errorBg:'--error-bg',errorBorder:'--error-border',noticeBg:'--notice-bg',noticeText:'--notice-text',shadow:'--shadow',track:'--track',thumb:'--thumb'};
function argb(value) {
  let s=value.slice(1);
  if(s.length===3||s.length===4)s=[...s].map(c=>c+c).join('');
  return `0x${s.length===8?s.slice(6)+s.slice(0,6):'ff'+s}`;
}
write('android/app/src/main/java/com/cloudgeek/spool/android/WebPalette.java',`package com.cloudgeek.spool.android;\n\n/** Generated from web/src/style.css. Regenerate with node android/tools/sync-web-ui.mjs. */\nclass WebPalette {\n${Object.keys(colors).map(key=>`    final int ${key};`).join('\n')}\n    WebPalette(boolean dark) {\n${Object.entries(colors).map(([key,v])=>`        ${key} = dark ? ${argb(dark[v])} : ${argb(light[v])};`).join('\n')}\n    }\n}\n`);
const expandHex=color=>color.length===4?'#'+[...color.slice(1)].map(c=>c+c).join(''):color;
const srgbMix=(first,second,weight)=>'#'+[0,1,2].map(i=>Math.round(parseInt(expandHex(first).slice(1+i*2,3+i*2),16)*weight+parseInt(expandHex(second).slice(1+i*2,3+i*2),16)*(1-weight)).toString(16).padStart(2,'0')).join('');
const peoplePalette=palette=>({selection:srgbMix(palette['--color-primary'],palette['--elevated'],.09),own:srgbMix(palette['--color-primary'],palette['--elevated'],.11),border:palette['--color-primary']+'2e'});
const peopleLight=peoplePalette(light),peopleDark=peoplePalette(dark);
write('android/app/src/main/java/com/cloudgeek/spool/android/NativePeoplePalette.java',`package com.cloudgeek.spool.android;\n\n/** Generated from Web people-chat sRGB color mixes. */\nfinal class NativePeoplePalette {\n${Object.keys(peopleLight).map(key=>`    final int ${key};`).join('\n')}\n    NativePeoplePalette(boolean dark) {\n${Object.keys(peopleLight).map(key=>`        ${key} = dark ? ${argb(peopleDark[key])} : ${argb(peopleLight[key])};`).join('\n')}\n    }\n}\n`);
function diffPalette(dark) {
 const theme=JSON.parse(fs.readFileSync(path.join(root,`web/node_modules/@pierre/theme/themes/pierre-${dark?'dark':'light'}.json`),'utf8')).colors;
 const bg=theme['editor.background'],fg=theme['editor.foreground'],added=theme['gitDecoration.addedResourceForeground'],deleted=theme['gitDecoration.deletedResourceForeground'];
 const mixer=dark?'#ffffff':'#000000';
 const additionBg=labMix(bg,added,dark?.8:.88),deletionBg=labMix(bg,deleted,dark?.8:.88);
 const over=(fg,bg,alpha)=>'#'+[0,1,2].map(i=>Math.round(parseInt(fg.slice(1+i*2,3+i*2),16)*alpha+parseInt(bg.slice(1+i*2,3+i*2),16)*(1-alpha)).toString(16).padStart(2,'0')).join('');
 return {bg,fg,added,deleted,modified:theme['gitDecoration.modifiedResourceForeground'],number:labMix(fg,bg,.65),
  additionBg,deletionBg,additionEmphasis:over(added,additionBg,dark?.2:.15),deletionEmphasis:over(deleted,deletionBg,dark?.2:.15),
  additionGutter:labMix(bg,added,dark?.85:.91),deletionGutter:labMix(bg,deleted,dark?.85:.91),
  buffer:labMix(bg,mixer,.92),separator:labMix(bg,mixer,dark?.85:.96)};
}
const diffLight=diffPalette(false),diffDark=diffPalette(true);
const sourcePalette=dark=>{
 const css=dark?palette(':root[data-theme="dark"]'):palette(':root');
 const colors=diffPalette(dark),bg=css['--elevated'],primary=css['--color-primary'];
 return {foreground:colors.fg,selection:labMix(bg,primary,dark?.75:.82),gutterSelection:labMix(bg,primary,dark?.6:.75),selectedNumber:labMix(colors.modified,dark?'#ffffff':'#000000',dark?.75:.65)};
};
const sourceLight=sourcePalette(false),sourceDark=sourcePalette(true);
write('android/app/src/main/java/com/cloudgeek/spool/android/NativeSourcePalette.java',`package com.cloudgeek.spool.android;\n\n/** Generated from Web FileSource overrides and Pierre CSS Lab selection mixes. */\nfinal class NativeSourcePalette {\n${Object.keys(sourceLight).map(key=>`    final int ${key};`).join('\n')}\n    NativeSourcePalette(boolean dark) {\n${Object.keys(sourceLight).map(key=>`        ${key} = dark ? ${argb(sourceDark[key])} : ${argb(sourceLight[key])};`).join('\n')}\n    }\n}\n`);
write('android/app/src/main/java/com/cloudgeek/spool/android/NativeDiffPalette.java',`package com.cloudgeek.spool.android;\n\n/** Generated from Pierre's pinned Web themes and CSS Lab color mixes. */\nfinal class NativeDiffPalette {\n${Object.keys(diffLight).map(key=>`    final int ${key};`).join('\n')}\n    NativeDiffPalette(boolean dark) {\n${Object.keys(diffLight).map(key=>`        ${key} = dark ? ${argb(diffDark[key])} : ${argb(diffLight[key])};`).join('\n')}\n    }\n}\n`);
const english=JSON.parse(fs.readFileSync(path.join(root,'locales/en.json'),'utf8'));
const chinese=JSON.parse(fs.readFileSync(path.join(root,'locales/zh-CN.json'),'utf8'));
const japanese=JSON.parse(fs.readFileSync(path.join(root,'locales/ja.json'),'utf8'));
const nativeSource=path.join(root,'android/app/src/main/java/com/cloudgeek/spool/android');
const textKeys=new Set(['Default','Less','More','The next message creates a new chat with the selected agent and converted context.','Only sending with another agent creates a new chat. The original chat stays unchanged.']);
for(const file of fs.readdirSync(nativeSource).filter(file=>file.endsWith('.java')&&file!=='NativeText.java')) {
 const source=fs.readFileSync(path.join(nativeSource,file),'utf8');
 for(const match of source.matchAll(/\bui\.t\("((?:[^"\\]|\\.)*)"/g))textKeys.add(JSON.parse('"'+match[1]+'"'));
 // Legacy bilingual labels still share Web's Japanese translations when that locale is selected.
 for(const match of source.matchAll(/"((?:[^"\\]|\\.)*)"/g)) {
  let key;try{key=JSON.parse('"'+match[1]+'"');}catch{continue;}
  if(key in english&&key in chinese&&key in japanese)textKeys.add(key);
 }
}
for(const key of textKeys)if(!(key in english)||!(key in chinese)||!(key in japanese))throw new Error(`Missing shared UI translation: ${key}`);
const translations=locale=>[...textKeys].sort().map(key=>`            case ${JSON.stringify(key)}: value = ${JSON.stringify(locale[key])}; break;`).join('\n');
write('android/app/src/main/java/com/cloudgeek/spool/android/NativeText.java',`package com.cloudgeek.spool.android;\n\n/** Generated from the Web locale catalogs for native components. */\nfinal class NativeText {\n    static String translate(String key, String locale, Object... pairs) {\n        String value = key;\n        if (locale.equals("zh-CN")) switch (key) {\n${translations(chinese)}\n            default: break;\n        }\n        else if (locale.equals("ja")) switch (key) {\n${translations(japanese)}\n            default: break;\n        }\n        for (int i = 0; i + 1 < pairs.length; i += 2) value = value.replace("{" + pairs[i] + "}", String.valueOf(pairs[i + 1]));\n        return value;\n    }\n}\n`);
// Golden vectors verify the official Go renderer against the exact Web versions.
const avatarNames=['clay','pixel-art','croodles','lorelei','notionists','toon-head','voxel-art','thumbs'];
const avatarDefinitions=Object.fromEntries(avatarNames.map(name=>[name,JSON.parse(fs.readFileSync(require.resolve(`@dicebear/styles/${name}.json`),'utf8'))]));
write('web/src/avatar-meta.ts',`// Generated from the pinned DiceBear styles by android/tools/sync-web-ui.mjs.\nexport const avatarMetadata = ${JSON.stringify(Object.fromEntries(avatarNames.map(name=>[name,avatarDefinitions[name].meta])),null,2)} as const;\n`);
const avatarLabels=['Clay','Pixel Art','Croodles','Lorelei','Notionists','Toon Head','Voxel Art','Thumbs'];
write('android/app/src/main/java/com/cloudgeek/spool/android/NativeAvatarCatalog.java',`package com.cloudgeek.spool.android;\n\n/** Generated from the same pinned art definitions as Web avatar metadata. */\nfinal class NativeAvatarCatalog {\n    static final String[] STYLES = {${avatarNames.map(JSON.stringify).join(', ')}};\n    static final String[] LABELS = {${avatarLabels.map(JSON.stringify).join(', ')}};\n    static final String[][] CREDITS = {\n${avatarNames.map(name=>`        {${['source','creator','license'].flatMap(key=>[avatarDefinitions[name].meta[key].name,avatarDefinitions[name].meta[key].url]).map(JSON.stringify).join(', ')}}`).join(',\n')}\n    };\n    static int index(String style) {\n        for (int i = 0; i < STYLES.length; i++) if (STYLES[i].equals(style)) return i;\n        return -1;\n    }\n}\n`);
const {Avatar,Style}=await import(pathToFileURL(require.resolve('@dicebear/core')).href);
const avatarFixtures=[];
for(const name of avatarNames)for(const seed of ['Mobile user','spool:1','\u539f\u751f\ud83e\udd16']) {
 const svg=new Avatar(new Style(avatarDefinitions[name]),{seed,size:96,backgroundColor:['dbe8d2','f2d5c8','e8dfb4','cce2e5','d8dded']}).toString();
 avatarFixtures.push({name,seed,sha256:createHash('sha256').update(svg).digest('hex'),svg});
}
write('internal/avatar/testdata/web-fixtures.json',JSON.stringify(avatarFixtures,null,2)+'\n');
const notices = `Native vectors are generated from the Web client's installed dependencies; avatars are generated locally with the bundled Go DiceBear engine.\n\nLucide (lucide-react)\n\n${fs.readFileSync(require.resolve('lucide-react/package.json').replace('package.json','LICENSE'),'utf8')}\n\nLobeHub icons (Codex, Claude)\n\n${fs.readFileSync(path.join(root,'web/node_modules/@lobehub/icons/LICENSE'),'utf8')}\n\nDiceBear core (MIT)\n\n${fs.readFileSync(path.join(path.dirname(require.resolve('@dicebear/core')),'../LICENSE'),'utf8')}\n\nAvatar art attribution (unmodified definitions):\n${avatarNames.map(name=>`${name}: ${avatarDefinitions[name].meta.license.text}`).join('\n')}\n\nCommonMark Java: BSD 2-Clause, https://github.com/commonmark/commonmark-java\n\n${fs.readFileSync(path.join(root,'android/licenses/CommonMark.txt'),'utf8')}\nAndroidSVG 1.4 by Paul LeBeau: Apache 2.0, https://github.com/BigBadaboom/androidsvg\nAndroidX ExifInterface 1.4.1 by The Android Open Source Project: Apache 2.0, https://developer.android.com/jetpack/androidx/releases/exifinterface\nAndroidX Core 1.17.0 (NestedScrollView) by The Android Open Source Project: Apache 2.0, https://developer.android.com/jetpack/androidx/releases/core\n\n${fs.readFileSync(path.join(root,'android/licenses/Apache-2.0.txt'),'utf8')}\n`;
let allNotices=notices+`\nPierre file-diff and file-tree icons and themes: @pierre/diffs, @pierre/trees and @pierre/theme, Apache 2.0.\nNativePatch and NativePatchView are native adaptations of the Web display-only patch parser and file-diff presentation, not the JavaScript renderer. NativeTreeIcons uses the original standard-tier file-tree sprite and filename/extension mappings. NativeMiddleTruncate adapts Pierre Trees OverflowText extension splitting and CSS segment/marker behavior in Java; NativeTreeRow adapts the original row highlight and focus ink. NativeFileOrder adapts Pierre Trees natural ASCII-digit token ordering and UTF-16/case tie breaks in Java. No JavaScript renderer is included.\n\n${fs.readFileSync(path.join(root,'web/node_modules/@pierre/diffs/LICENSE.md'),'utf8')}\n\nPierre Trees license:\n\n${fs.readFileSync(path.join(root,'web/node_modules/@pierre/trees/LICENSE.md'),'utf8')}\n`;
allNotices += `\nChromium/Blink search-cancel PNG resources (unmodified): normal/pressed, light/dark. Source paths and SHA256/git-blob identities are recorded in android/licenses/chromium-search-cancel.json. Chromium Authors, BSD 3-Clause.\n\n${fs.readFileSync(path.join(root,'android/licenses/Chromium.txt'),'utf8')}\n`;
allNotices += `\njsdiff (diff package): BSD 3-Clause. NativeWordDiff adapts the word-with-space tokenizer and Myers path tie-breaking; NativeDiffLayout adapts Pierre's bounded similarity alignment. These are Java implementations, not bundled JavaScript runtimes.\n\n${fs.readFileSync(path.join(root,'web/node_modules/diff/LICENSE'),'utf8')}\n`;
allNotices += `\nNative syntax uses the original installed Shiki TextMate grammars and Pierre themes, not a JavaScript runtime. Shiki/@shikijs/langs 4.5.0: MIT.\n\n${fs.readFileSync(path.join(root,'web/node_modules/@shikijs/langs/LICENSE'),'utf8')}\nTextMate upstream attribution (Microsoft):\n\n${fs.readFileSync(path.join(root,'web/node_modules/@shikijs/vscode-textmate/LICENSE.md'),'utf8')}\nEclipse TM4E core 0.14.1: EPL-2.0 with original MIT adaptations. Modified logging/regex adapters and rebuild recipe are distributed in android/textmate and android/tools/prepare-textmate.py. Original source: https://github.com/eclipse-tm4e/tm4e/tree/0.14.1\n\n${fs.readFileSync(path.join(root,'android/licenses/TM4E.txt'),'utf8')}\n${fs.readFileSync(path.join(root,'android/licenses/EPL-2.0.txt'),'utf8')}\nOniguruma 6.9.10: BSD 2-Clause, statically linked with the Spool JNI bridge.\n\n${fs.readFileSync(path.join(root,'android/licenses/Oniguruma.txt'),'utf8')}\nGson 2.13.1 (Google Inc.) and SnakeYAML Engine 2.9 (SnakeYAML authors): Apache 2.0; full Apache text is included above.\n`;
write('android/THIRD_PARTY_NOTICES',allNotices);
write('android/app/src/main/assets/third-party-notices.txt',allNotices);
const logo='android/app/src/main/res/drawable-nodpi/spool_logo.png';
if (check) {
  if(!fs.readFileSync(path.join(root,logo)).equals(fs.readFileSync(path.join(root,'web/public/logo.png'))))throw new Error('Web/native logo drift');
} else fs.copyFileSync(path.join(root,'web/public/logo.png'),path.join(root,logo));
console.log(`${check?'Verified':'Synchronized'} ${generated.length} Web/native assets.`);
