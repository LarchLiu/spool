#!/usr/bin/env node
// Share the complete installed Shiki grammars and normalized Pierre themes, not keyword lists.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const installed=path.join(root,'web/node_modules');
const load=file=>import(pathToFileURL(path.join(installed,file)).href);
const {syntaxSourceContents,syntaxNextContents,syntaxPatchContents}=await import('./ui-fixtures.mjs');
const {bundledLanguagesInfo}=await load('shiki/dist/langs.mjs');
const {getSharedHighlighter}=await load('@pierre/diffs/dist/highlighter/shared_highlighter.js');
const {EXTENSION_TO_FILE_FORMAT,getFiletypeFromFileName}=await load('@pierre/diffs/dist/utils/getFiletypeFromFileName.js');
const check=process.argv.includes('--check'),grammars=new Map(),languages={},injections={},bundles={};
let assets=0;
function write(relative,value) {
 const target=path.join(root,relative),contents=typeof value==='string'?value:JSON.stringify(value)+'\n';
 if(check) {
  if(!fs.existsSync(target)||fs.readFileSync(target,'utf8')!==contents)throw new Error(`Web/native syntax drift: ${relative}`);
 }else{fs.mkdirSync(path.dirname(target),{recursive:true});fs.writeFileSync(target,contents);}
 assets++;
}
for(const item of bundledLanguagesInfo) {
 const data=(await item.import()).default;
 for(const name of [item.id,...item.aliases||[]])bundles[name]=data.map(grammar=>grammar.scopeName);
 for(const grammar of data) {
  const previous=grammars.get(grammar.scopeName);
  if(previous&&JSON.stringify(previous)!==JSON.stringify(grammar))throw new Error(`Conflicting Shiki scope: ${grammar.scopeName}`);
  grammars.set(grammar.scopeName,grammar);
  for(const name of [grammar.name,...grammar.aliases||[]])languages[name]=grammar.scopeName;
  for(const target of grammar.injectTo||[]) {
   const list=injections[target]||=[];if(!list.includes(grammar.scopeName))list.push(grammar.scopeName);
  }
 }
}
const scopes={};
for(const [index,scope] of [...grammars.keys()].sort().entries()) {
 const file=`grammars/${index}.json`;scopes[scope]=file;
 write(`android/app/src/main/assets/syntax/${file}`,grammars.get(scope));
}
const highlighter=await getSharedHighlighter({themes:['pierre-light','pierre-dark'],langs:[]});
for(const name of ['pierre-light','pierre-dark'])write(`android/app/src/main/assets/syntax/${name}.json`,highlighter.getTheme(name).textmate);
write('android/app/src/main/assets/syntax/manifest.json',{
 shiki:JSON.parse(fs.readFileSync(path.join(installed,'shiki/package.json'),'utf8')).version,
 pierre:JSON.parse(fs.readFileSync(path.join(installed,'@pierre/diffs/package.json'),'utf8')).version,
 languages,scopes,injections,bundles,files:EXTENSION_TO_FILE_FORMAT,
});
const cases=[
 ['src/main.ts','// Unicode 😀 and tabs\nconst text: string = `你好 ${42}`;\n/* multi\nline */\nexport function run(v: number) { return /a+/giu.test(text); }\n'],
 ['src/view.tsx','import React from "react";\nexport const UI = () => <div className="mobile">Hello {42}</div>;\n'],
 ['main.go','package main\nimport "fmt"\n// 你好 😀\nfunc main() {\n\tfmt.Println(`raw\nstring`, "hello", 42)\n}\n'],
 ['lib.rs','// Rust\n#[derive(Debug)]\nstruct User<\'a> { name: &\'a str }\nfn main() { let s = r#"你好 😀"#; println!("{}", s); }\n'],
 ['app.py','@decorator\ndef greet(name: str) -> str:\n\t"""A docstring\n\twith 😀 Unicode."""\n\treturn f"Hello {name!r}" # comment\n'],
 ['Main.java','package sample;\npublic class Main {\n\t/* comment\n\t   continued */\n\tstatic final String S = "你好 😀";\n\tint n = 42;\n}\n'],
 ['main.c','#include <stdio.h>\nint main(void) {\n\tprintf("hello %d\\n", 42);\n\treturn 0;\n}\n'],
 ['main.cpp','template<typename T> struct Box { T value; };\nauto s = R"tag(你好 😀)tag";\nstd::vector<int> list = {1, 2, 3};\n'],
 ['header.h','#import <Foundation/Foundation.h>\n@interface Mobile : NSObject\n@property NSString *name;\n@end\n'],
 ['Main.cs','using System;\nclass Main { public string Name => $"Hello {42}"; }\n'],
 ['page.html','<!doctype html>\n<html><style>body { color: #ff0000; }</style>\n<script>const n = 42; console.log(`hi ${n}`);</script>\n<div title="你好 😀">&amp; Hello</div></html>\n'],
 ['style.css','/* CSS */\n:root { --text: #123456; }\n.mobile:hover { color: var(--text); padding: 2px 4px; }\n@media (width < 600px) { body { display: grid; } }\n'],
 ['package.json','{\n  "name": "你好 😀",\n  "enabled": true,\n  "value": 42,\n  "items": [null, false]\n}\n'],
 ['config.yaml','# Settings\nname: "mobile 😀"\nitems:\n  - enabled: true\n    value: 42\ntext: |\n  Hello\n  World\n'],
 ['run.sh','#!/bin/sh\n# Shell\nNAME="你好 😀"\necho "${NAME:-default}"\nif [ -n "$NAME" ]; then printf \'%s\\n\' "$NAME"; fi\n'],
 ['README.md','# Title\n\n**bold** and *italic* with `code`\n\n```typescript\nconst n = 42;\n```\n\n[link](https://example.test)\n'],
 ['schema.sql','-- SQL\nSELECT id, name FROM users WHERE active = true AND age >= 42;\n'],
 ['config.toml','# TOML\n[server]\nname = "mobile"\nport = 8080\nenabled = true\n'],
 ['Main.swift','import Foundation\nstruct Mobile { let title = "Hello \\(42)" }\n'],
 ['Main.kt','package sample\ndata class Mobile(val name: String = "Hello ${42}")\n'],
 ['Dockerfile','FROM alpine:3.22\nRUN echo "hello" && apk add --no-cache curl\nENV NAME=mobile\n'],
 ['View.vue','<template><div>{{ name }}</div></template>\n<script setup lang="ts">\nconst name: string = "mobile";\n</script>\n<style scoped>.mobile { color: red }</style>\n'],
 ['View.svelte','<script lang="ts">let name: string = "mobile";</script>\n<div class:active={true}>{name}</div>\n<style>div { color: red; }</style>\n'],
 ['long.ts','/* start\n'+'x'.repeat(1001)+'\nend */\nconst n = 42;\n'],
 ['unicode.ts','const a = "😀"; const b = 42;\nconst 😀 = /🤖+/g; // deliberately invalid source still highlights\n'],
 ['crlf.ts','/* comment\r\n still comment */\r\nconst n = 42;\r\n'],
 ['unknown.unknown','No inferred syntax\nconst n = 42;\n'],
 ['render.ts',syntaxSourceContents],
 ['render.ts',syntaxNextContents],
];
const fixtures=[];
for(const [file,code] of cases) {
 const lang=getFiletypeFromFileName(file);
 if(lang!=='text')await highlighter.loadLanguages([lang]);
 const themes={};
 for(const theme of ['pierre-light','pierre-dark'])themes[theme]=highlighter.codeToTokens(code,{lang,theme,tokenizeMaxLineLength:1000,tokenizeTimeLimit:0}).tokens;
 fixtures.push({file,code,lang,themes});
}
const names=['a.ts','A.TS','src/a.component.ts','a.component.html','CMakeLists.txt','src/CMakeLists.txt','Dockerfile','path\\Dockerfile','CODEOWNERS','some.h','a.sh','foo.vue','a.d.ts','a.unknown','src/noextension','src/a.vine.ts'];
write('android/app/src/test/resources/web-syntax.json',{fixtures,syntaxPatchContents,foreground:Object.fromEntries(['pierre-light','pierre-dark'].map(name=>[name,highlighter.getTheme(name).fg])),filenames:names.map(file=>({file,lang:getFiletypeFromFileName(file)}))});
console.log(`${check?'Verified':'Synchronized'} ${assets} code-syntax assets, ${bundledLanguagesInfo.length} code grammars, ${grammars.size} scopes and ${fixtures.length} dual-theme actual-Web fixtures. UI locales remain Chinese/English/Japanese.`);
