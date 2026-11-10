#!/usr/bin/env bun
import { requireRunTemp } from "../../tools/test-scratch.mjs";
requireRunTemp();
// Actual main.tsx status JSX/CSS and history-loading.ts in local Chrome; no real workspace.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import vm from 'node:vm';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const require = createRequire(path.join(root, 'web/package.json'));
const runtime = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, 'web/node_modules'), '__history__.cjs'));
const {buildSync} = require('esbuild'), React = require('react'), {renderToStaticMarkup} = require('react-dom/server'), {chromium} = runtime('playwright');
const output = path.join(root, 'build/artifacts/android-native');
const fontRoot = path.join(root, 'build/cache/android-ui-fonts');
const manifest = JSON.parse(fs.readFileSync(path.join(fontRoot, 'manifest.json'), 'utf8'));
const fontFiles = new Set([...manifest.faces, ...manifest.cjk].map(face => face.file));
const fontCSS = manifest.faces.map(face => `@font-face{font-family:AndroidRoboto;src:url(/fonts/${face.file});font-weight:${face.weight};font-style:${face.italic ? 'italic' : 'normal'}}`).join('')
  + manifest.cjk.map(face => `@font-face{font-family:${face.family};src:url(/fonts/${face.file});font-weight:400}`).join('')
  + ':root{font-family:AndroidRoboto,AndroidCjkSC,sans-serif}:lang(ja){font-family:AndroidRoboto,AndroidCjkJP,sans-serif}';
const main = fs.readFileSync(path.join(root, 'web/src/main.tsx'), 'utf8');
const start = main.indexOf('            {chat?.history?.error && (');
const end = main.indexOf('            {!chat?.turns.length ?', start);
assert.ok(start > 0 && end > start, 'Inspect moved actual history JSX');
const jsx = main.slice(start, end);
const built = buildSync({stdin: {contents: `import React from 'react'; export function status(chat,historyLoadError,t){const setHistoryLoadError=()=>{};return <>${jsx}</>}`, loader: 'tsx'}, format: 'cjs', write: false}).outputFiles[0].text;
const module = {exports: {}}; vm.runInNewContext(built, {module, exports: module.exports, require});
const functions = buildSync({entryPoints: [path.join(root, 'web/src/history-loading.ts')], bundle: true, platform: 'browser', format: 'iife', globalName: 'historyFns', write: false}).outputFiles[0].text;
const fixtures = [
  {id: 'unavailable', error: true, more: false, failed: false},
  {id: 'loading', error: false, more: true, failed: false},
  {id: 'retry', error: false, more: true, failed: true},
  {id: 'unavailable-loading', error: true, more: true, failed: false},
  {id: 'unavailable-retry', error: true, more: true, failed: true},
];
const server = http.createServer((request, response) => {
  const url = new URL(request.url, 'http://fixture.invalid');
  if (url.pathname === '/style.css') { response.setHeader('Content-Type', 'text/css'); response.end(fs.readFileSync(path.join(root, 'web/src/style.css'))); return; }
  if (url.pathname.startsWith('/fonts/')) {
    const file = url.pathname.slice('/fonts/'.length);
    if (!fontFiles.has(file)) { response.writeHead(404); response.end(); return; }
    response.end(fs.readFileSync(path.join(fontRoot, file))); return;
  }
  const locale = url.searchParams.get('locale') || 'en', theme = url.searchParams.get('theme') || 'light';
  if (!['en','zh-CN','ja'].includes(locale) || !['light','dark'].includes(theme)) { response.writeHead(400); response.end(); return; }
  const translations = JSON.parse(fs.readFileSync(path.join(root, 'locales', locale + '.json'), 'utf8'));
  const rows = fixtures.map(item => `<section data-case="${item.id}" style="margin-bottom:20px"><div class="history-inner">${renderToStaticMarkup(module.exports.status({history: {error: item.error ? 'unavailable' : '', has_more: item.more}}, item.failed, key => translations[key] || key))}<div class="sentinel" style="height:1px"></div></div></section>`).join('');
  response.setHeader('Content-Type', 'text/html; charset=utf-8');
  response.end(`<!doctype html><html lang="${locale}" data-theme="${theme}"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/style.css"><style>${fontCSS}</style><main style="padding:25px 18px">${rows}</main><script>${functions}</script>`);
});
const sockets = new Set(); server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
let browser;
try {
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  browser = await chromium.launch({channel: 'chrome', headless: true});
  const base = `http://127.0.0.1:${server.address().port}`, cases = [];
  for (const density of [1,2.625,3]) for (const locale of ['en','zh-CN','ja']) for (const theme of ['light','dark']) {
    const context = await browser.newContext({viewport: {width:360,height:800}, deviceScaleFactor: density, isMobile:true});
    try {
      const page = await context.newPage(); const errors = []; page.on('pageerror', error => errors.push(String(error)));
      await page.goto(`${base}/?locale=${locale}&theme=${theme}`); await page.evaluate(() => document.fonts.ready);
      for (const width of density === 1 ? [320,360,412] : [360]) {
        await page.setViewportSize({width,height:800});
        const values = await page.evaluate(() => [...document.querySelectorAll('[data-case]')].map(section => {
          const inner = section.querySelector('.history-inner'), base = inner.getBoundingClientRect();
          const children = [...inner.children].filter(child => !child.classList.contains('sentinel')).map(element => {
            const box = element.getBoundingClientRect(), style = getComputedStyle(element);
            return {text:element.textContent, x:box.x-base.x, y:box.y-base.y, width:box.width, height:box.height, fontSize:parseFloat(style.fontSize), color:style.color,
              paddingLeft:parseFloat(style.paddingLeft), paddingTop:parseFloat(style.paddingTop), marginBottom:parseFloat(style.marginBottom), tag:element.tagName, role:element.getAttribute('role')};
          });
          return {id:section.dataset.case,width:base.width,height:base.height-1,children};
        }));
        cases.push(...values.map(value => ({density,locale,theme,viewport:width,...value})));
        assert.ok(values.every(value => value.children.every(child => child.fontSize > 0 && child.width > 0 && child.height > 0)), 'Mobile quiet rule cannot hide history retry');
        if (width === 360 && density === 1 && locale === 'en') await page.screenshot({path:path.join(output, `web-history-notices-${theme}.png`)});
      }
      assert.deepEqual(errors, []);
    } finally { await context.close(); }
  }
  const page = await browser.newPage(); await page.goto(base);
  const lifecycle = await page.evaluate(async () => {
    const trace = []; let frame = 0;
    const tick = () => { frame++; requestAnimationFrame(tick); }; requestAnimationFrame(tick);
    let current = {id:'chat-one', history:{before:'first',has_more:true}};
    for (const next of [{id:'chat-one',history:{before:'last',has_more:true}}, {id:'chat-one',history:{has_more:false}}]) {
      const before = frame;
      current = await historyFns.fetchOlderHistory(current, async path => { trace.push({path,before,frame}); return next; }, new AbortController().signal);
    }
    const controller = new AbortController(); let canceledRequests = 0;
    const pending = historyFns.fetchOlderHistory({id:'chat-one',history:{before:'cancel',has_more:true}}, async () => { canceledRequests++; }, controller.signal);
    controller.abort(); const canceled = await pending;
    const element = document.createElement('div'); element.style.cssText='height:200px;overflow-y:auto;overflow-anchor:none';
    const body = document.createElement('div'); body.style.height='1200px'; element.append(body); document.body.append(element);
    element.scrollTop=150; const anchor={id:'chat-one',height:element.scrollHeight,top:element.scrollTop}; body.style.height='3400px';
    historyFns.restoreHistoryScroll(element,false,anchor); const restored=element.scrollTop;
    historyFns.restoreHistoryScroll(element,true,anchor); const followed=element.scrollTop;
    return {trace,canceledRequests,canceled,anchor,restored,followed,scrollHeight:element.scrollHeight,clientHeight:element.clientHeight};
  });
  assert.ok(lifecycle.trace.every(item => item.frame > item.before), 'Each page must yield to a new animation frame');
  assert.equal(lifecycle.canceledRequests,0); assert.equal(lifecycle.canceled,null); assert.equal(lifecycle.restored,2350); assert.equal(lifecycle.followed,3200);
  const report = {purpose:'Original main.tsx history JSX/CSS with byte-matched Android test fonts and actual history functions in Chrome; not real API/tsnet/device parity.',browser:browser.version(),cases,lifecycle};
  fs.writeFileSync(path.join(output,'web-history-notices.json'),JSON.stringify(report,null,2)+'\n');
  fs.writeFileSync(path.join(root,'android/app/src/test/resources/web-history-notices.json'),JSON.stringify(report,null,2)+'\n');
  console.log(`Verified ${cases.length} actual-Web history notice scenes and real Chrome frame-yield/cancel/scroll behavior.`);
} finally {
  await browser?.close(); server.closeAllConnections(); for (const socket of sockets) socket.destroy();
  await new Promise(resolve => server.close(resolve));
}
