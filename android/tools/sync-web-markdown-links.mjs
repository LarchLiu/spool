#!/usr/bin/env node
// Execute the actual Web renderer; these are not handwritten expected link ranges.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {renderMarkdown,parseFragment} from './web-markdown-reference.mjs';
import {linkDecorationCases} from './link-decoration-fixtures.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const inputs=[
 ['http','Visit https://example.com and http://example.org/a.'],
 ['www','Visit www.example.com and WWW.EXAMPLE.ORG/docs.'],
 ['email','Contact person+tag@example.com or other@example.org.'],
 ['balanced','Read https://example.com/wiki/Function_(math).'],
 ['extra-parens','(https://example.com/a(b))) and www.example.org/a(b).'],
 ['query','https://example.com/a%2Fb?q=a%26b&c=d#part'],
 ['punctuation','https://example.com/a, www.example.org/b! https://example.com/c?'],
 ['quotes','"https://example.com/a" and \'www.example.org/b\'.'],
 ['port','http://localhost:8080/path and https://example.org:443/a'],
 ['prefix','xhttps://example.com 1https://example.org xwww.example.net'],
 ['unicode-prefix','中文https://example.com 中文www.example.org'],
 ['underscore','https://a_b.example.com https://example.a_b.com www.a_b.example.org'],
 ['no-domain','http:// https://. www. person@localhost'],
 ['email-punctuation','(a+b@example.com), a@example.org! /skip@example.com'],
 ['email-invalid','a@example.c0 a@example.c_ a@example.c- a@domain a@.org'],
 ['explicit','[Website](https://example.com/a%2Fb?q=a%26b "Title")'],
 ['explicit-nested','[**https://label.example.com**](https://destination.example.org)'],
 ['explicit-www','[www.label.example.com](https://destination.example.org)'],
 ['image','![https://label.example.com](https://image.example.org/a.png)'],
 ['image-no-alt','![](https://image.example.org/a.png)'],
 ['local','[Source](src/main.tsx#L12C3) and [File](file:///project/README.md)'],
 ['mail-link','[Email](mailto:person@example.com) and <person@example.org>'],
 ['protocols','[IRC](irc://irc.example.org/channel) [Secure](ircs://irc.example.org/channel) [Chat](xmpp:person@example.com)'],
 ['blocked','[unsafe](javascript:alert%281%29) [data](data:text/html,test) [tel](tel:123)'],
 ['encoded-unsafe','[bad](javascript%3Aalert%281%29) [data](%64ata:text/html,test)'],
 ['encoded-controls','[bad](https://example.com/%0a) [badmail](mailto:a@example.com?body=%0aInjected)'],
 ['inline-code','`https://example.com` and `www.example.org` and `a@example.com`'],
 ['emphasis','**https://example.com** and *www.example.org* and ~~a@example.net~~'],
 ['html','Before <b>https://example.com</b> after'],
 ['escaped','https://example.com/a\\) and www.example.org/a\\*'],
 ['entities','https://example.com/a&amp; and www.example.org/b&copy;'],
 ['supplementary','😀 https://example.com/😀 and 日本語 www.example.org/a'],
 ['fragment','[Section](#section)'],
 ['uppercase','HTTPS://EXAMPLE.COM/a and [Mail](MAILTO:a@example.org)'],
 ['idn','https://例子.测试/页面 and www.日本語.jp/案内'],
 ['explicit-unicode','[Unicode](https://example.com/页面?q=😀)'],
 ['ipv6','[Loopback](http://[::1]:8080/a)'],
 ['tail','https://example.com/a*** https://example.org/a___ https://example.net/a~~~'],
 ['symbols','Visit https://example.com/a}> and www.example.org/a]: done'],
 ['credentials','[Login](https://user:secret@example.org/a)'],
 ['bad-utf8','[Invalid](https://example.com/%FF)'],
 ['separated','a@example.com/http://example.org https://example.com/a@example.net'],
];
const cases=inputs.map(([id,source])=>{
 const fragment=parseFragment(renderMarkdown(source));
 let text='';const links=[],anchors=[];
 const visit=node=>{
  const start=text.length;
  if(node.nodeName==='#text')text+=node.value;
  for(const child of node.childNodes||[])visit(child);
  if(node.tagName==='a'||node.tagName==='button') {
   const attrs=Object.fromEntries((node.attrs||[]).map(a=>[a.name,a.value]));
   if(node.tagName==='a'||attrs.class==='local-file-link')links.push({start,end:text.length,target:attrs.href||attrs.title});
   if(node.tagName==='a')anchors.push({start,end:text.length,href:attrs.href,target:attrs.target||'',rel:attrs.rel||''});
  }
 };
 visit(fragment);
 links.sort((a,b)=>a.start-b.start||a.end-b.end);
 anchors.sort((a,b)=>a.start-b.start||a.end-b.end);
 // Web permits encoded controls/credentials in hrefs; Android never dispatches those to another app.
 const actionable=links.filter(link=>!/%(?:0[0-9a-f]|1[0-9a-f]|7f)/i.test(link.target)&&!/^https?:\/\/[^/]*@/i.test(link.target));
 return {id,source,text,links,actionable,anchors};
});
const target=path.join(root,'android/app/src/test/resources/web-markdown-links.json');
const contents=JSON.stringify({purpose:'Actual Web MarkdownMessage inline text/link ranges; actionable additionally excludes encoded control characters and credential-bearing URLs before native Intent dispatch',cases,decorationCases:linkDecorationCases},null,2)+'\n';
if(process.argv.includes('--check')) {
 if(!fs.existsSync(target)||fs.readFileSync(target,'utf8')!==contents)throw new Error('Web/native Markdown link vectors drifted');
 console.log(`Verified ${cases.length} actual Web Markdown link vectors.`);
} else {fs.writeFileSync(target,contents);console.log(`Synchronized ${cases.length} actual Web Markdown link vectors.`);}
