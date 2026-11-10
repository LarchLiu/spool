// Opt-in browser check of the production settings UI with an inert native bridge.
// Run through tools/run.sh with CHROME_BIN; no real settings or credentials.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import { join } from "node:path";
import { build } from "esbuild";
import { requireRunTemp } from "../../tools/test-scratch.mjs";

requireRunTemp();
assert.ok(process.env.CHROME_BIN, "Set CHROME_BIN to headless Chrome/Chromium");
const scratch = await mkdtemp(join(process.env.TMPDIR, "web-proxy-browser-"));
let browser;
let report;
const source = `
import React from 'react';
import { createRoot } from 'react-dom/client';
import { SettingsApp } from './src/settings-app';
import './src/style.css';
import './src/settings.css';
window.spoolDesktop = true;
let data = {
  section: 'Service', message: '',
  settings: { web_enabled:true, host:'127.0.0.1', port:8787, public_url:'', web_trusted_proxies:'', idle_minutes:15, history_cache_mib:64, auto_start:false, tsnet_enabled:false, tsnet_hostname:'spool-server', tsnet_port:443, proxy_enabled:false, use_system_proxy:false, proxy_url:'', no_proxy:'' },
  status: {Running:true, RestartRequired:false, PendingChanges:[], Address:'127.0.0.1:8787', Error:'', Online:[]},
  users:[], projects:[], agents:[], updates:{Enabled:false}, preferences:{theme:'light',language:'zh-CN'}, update_preferences:{}, database:'fixture.sqlite',
};
const commands = [];
let release;
let fail = false;
const applyDetected = address => {
  data = {...data,settings:{...data.settings,public_url:address.replace(/\\/$/,''),web_detected_origin:address.replace(/\\/$/,''),web_detected_proxy:'172.18.0.3/32'},status:{...data.status,WebDetecting:false,RestartRequired:false,PendingChanges:[]}};
};
window.mygo = {locale:'zh-CN',on:()=>()=>{}, call:async(method,...args)=>{
  if(method === 'DesktopSettings.Snapshot') return structuredClone(data);
  if(method !== 'DesktopSettings.Execute') throw new Error('Unexpected bridge method: '+method);
  const command = args[0]; commands.push(command);
  if(command.kind === 'save-settings') {
    data = {...data,settings:command.settings,status:{...data.status,WebDetecting:true,RestartRequired:true,PendingChanges:['web']}};
    new Promise(resolve=>{release=resolve}).then(()=>applyDetected(command.settings.public_url));
  } else if(command.kind === 'detect-web-proxy') {
    await new Promise(resolve=>{release=resolve});
    if(fail) throw new Error('Could not verify this address. Check that your tunnel or reverse proxy points to this Spool server.');
    applyDetected(command.address);
  } else throw new Error('Unexpected command: '+command.kind);
  return structuredClone(data);
}};
const pause = ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function until(check) {for(let i=0;i<200;i++){const v=check();if(v)return v;await pause(25)}throw new Error('Timed out: '+check.toString())}
const check=(condition,label)=>{if(!condition)throw new Error(label)};
const detect=()=>document.querySelector('[data-detect-web]');
const address=()=>document.querySelector('input[aria-label="公网地址"]') || document.querySelector('input[placeholder="https://spool.example.com"]');
createRoot(document.getElementById('root')).render(<SettingsApp/>);
(async()=>{
  await until(detect);
  check(detect().textContent==='检测' && detect().disabled,'Detect label and empty-address disabled state');
  check(!document.querySelector('.settings-advanced').open,'Advanced configuration starts collapsed');
  const input=address();input.focus();
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'https://spool.example.test/');
  input.dispatchEvent(new Event('input',{bubbles:true}));
  await until(()=>address().value==='https://spool.example.test/');
  // Merely leave the input: no Detect click is needed.
  document.querySelector('[data-section="Service"]').focus();
  await until(()=>release);
  await until(()=>detect().textContent==='检测中…');
  check(commands.map(c=>c.kind).join(',')==='save-settings','Saving the address starts backend detection without a UI detect command');
  check(detect().textContent==='检测中…' && detect().matches(':disabled') && address().matches(':disabled'),'Detection disables repeated clicks and edits');
  release(); release=undefined;
  await until(()=>document.body.textContent.includes('检测成功，可通过公网地址访问。'));
  check(!detect().matches(':disabled'),'Detect becomes retryable after success');
  check(address().value==='https://spool.example.test','Normalized public address updates in place');
  check(document.querySelector('[data-detected-web-proxy]').textContent.includes('172.18.0.3/32'),'Detected proxy appears in advanced settings');
  check(document.querySelector('input[aria-label="Web 可信代理"]').value==='','Detection preserves manual trust entries');
  check(!document.querySelector('[data-apply-service-change="web"]'),'Success needs no extra Apply action');
  fail=true;detect().click();await until(()=>release);release();release=undefined;
  await until(()=>document.body.textContent.includes('无法验证此地址'));
  check(!document.body.textContent.includes('检测成功，可通过公网地址访问。'),'Failed retry clears old success');
  check(document.querySelector('[data-detected-web-proxy]').textContent.includes('172.18.0.3/32'),'Failed retry preserves verified trust');
  check(!detect().matches(':disabled'),'Failure permits retry');
  await fetch('/report',{method:'POST',body:JSON.stringify({ok:true,checks:13})});
})().catch(async error=>{await fetch('/report',{method:'POST',body:JSON.stringify({error:String(error)})})});
`;
let javascript, css;
const server = createServer(async (request, response) => {
  if (request.url === "/report") {
    let body = "";
    for await (const chunk of request) body += chunk;
    report = JSON.parse(body);
    response.end("ok");
  } else if (request.url === "/app.js") {
    response.setHeader("Content-Type", "text/javascript");
    response.end(javascript);
  } else if (request.url === "/app.css") {
    response.setHeader("Content-Type", "text/css");
    response.end(css);
  } else {
    response.setHeader("Content-Type", "text/html; charset=utf-8");
    response.end(
      '<!doctype html><html lang="zh-CN"><head><link rel="stylesheet" href="/app.css"></head><body><div id="root"></div><script src="/app.js"></script></body></html>',
    );
  }
});
try {
  const bundle = await build({
    stdin: {
      contents: source,
      resolveDir: new URL("..", import.meta.url).pathname,
      loader: "tsx",
    },
    bundle: true,
    write: false,
    outdir: scratch,
    format: "iife",
    loader: { ".woff2": "dataurl" },
  });
  javascript = bundle.outputFiles.find((file) =>
    file.path.endsWith(".js"),
  ).text;
  css = bundle.outputFiles.find((file) => file.path.endsWith(".css")).text;
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  browser = spawn(
    process.env.CHROME_BIN,
    [
      "--headless",
      "--no-first-run",
      "--no-default-browser-check",
      "--disable-background-networking",
      "--disable-component-update",
      "--disable-sync",
      "--disable-extensions",
      "--disable-breakpad",
      "--disable-crash-reporter",
      "--user-data-dir=" + join(scratch, "profile"),
      `http://127.0.0.1:${server.address().port}/`,
    ],
    { stdio: "ignore" },
  );
  const browserDone = once(browser, "close");
  try {
    for (let i = 0; i < 300 && !report; i++)
      await new Promise((resolve) => setTimeout(resolve, 50));
    assert.ok(report, "Browser did not report a result");
    assert.equal(report.error, undefined, report.error);
    assert.equal(report.ok, true);
    console.log(JSON.stringify(report));
  } finally {
    browser.kill("SIGTERM");
    await browserDone;
  }
} finally {
  await new Promise((resolve) => server.close(resolve));
  await rm(scratch, { recursive: true, force: true });
}
