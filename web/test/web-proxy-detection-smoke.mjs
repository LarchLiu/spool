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
let generation = 0;
const startCheck = preflight => {
  const current = ++generation;
  const check = {ID:String(current),PublicURL:data.settings.public_url,Host:data.settings.host,Port:data.settings.port,Preflight:preflight,Reused:preflight,Succeeded:false,Proxy:'',Error:''};
  data = {...data,status:{...data.status,WebDetecting:true,WebError:'',WebProxyCheck:check}};
  release = () => {
    if(current !== generation) return;
    const error = fail ? 'Could not verify this address. Check that your tunnel or reverse proxy points to this Spool server.' : '';
    data = {...data,status:{...data.status,WebDetecting:false,WebError:error,WebProxyCheck:{...check,Succeeded:!fail,Proxy:fail?'':'172.18.0.3/32',Error:error}}};
    if(!preflight && !fail) data = {...data,settings:{...data.settings,web_detected_origin:check.PublicURL,web_detected_proxy:'172.18.0.3/32'}};
  };
};
window.mygo = {locale:'zh-CN',on:()=>()=>{}, call:async(method,...args)=>{
  if(method === 'DesktopSettings.Snapshot') return structuredClone(data);
  if(method === 'DesktopSettings.CancelWebProxyDetection') {
    if(data.status.WebProxyCheck?.ID !== args[0]) return true;
    generation++; release=undefined;
    data = {...data,status:{...data.status,WebDetecting:false,WebProxyCheck:undefined,WebError:''}};
    return true;
  }
  if(method !== 'DesktopSettings.Execute') throw new Error('Unexpected bridge method: '+method);
  const command = args[0]; commands.push(command);
  if(command.kind === 'save-settings') {
    data = {...data,settings:{...command.settings,public_url:command.settings.public_url.replace(/\\/$/,'')},status:{...data.status,RestartRequired:true,PendingChanges:['web']}};
    startCheck(true);
  } else if(command.kind === 'detect-web-proxy') {
    startCheck(data.status.PendingChanges.includes('web'));
  } else if(command.kind === 'apply-service-changes') {
    data = {...data,status:{...data.status,RestartRequired:false,PendingChanges:[],Address:data.settings.host+':'+data.settings.port}};
    startCheck(false);
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
  check(detect().textContent==='检测中…' && detect().matches(':disabled') && !address().matches(':disabled'),'Detection disables repeat checks while allowing edits');
  release(); release=undefined;
  await until(()=>document.body.textContent.includes('预检成功，点击应用更改后生效。'));
  check(!detect().matches(':disabled'),'Detect becomes retryable after success');
  check(address().value==='https://spool.example.test','Normalized public address updates in place');
  check(document.querySelector('[data-preflight-web-proxy]').textContent.includes('172.18.0.3/32'),'Preflight proxy appears separately from formal trust');
  check(document.querySelector('input[aria-label="Web 可信代理"]').value==='','Detection preserves manual trust entries');
  check(document.querySelector('[data-apply-service-change="web"]'),'Preflight success retains Apply action');
  check(!data.settings.web_detected_proxy && data.status.Address==='127.0.0.1:8787','Preflight does not apply trust or change the real entry');
  fail=true;detect().click();await until(()=>release);release();release=undefined;
  await until(()=>document.body.textContent.includes('无法验证此地址'));
  check(!document.body.textContent.includes('检测成功，可通过公网地址访问。'),'Failed retry clears old success');
  check(address().value==='https://spool.example.test' && document.querySelector('[data-apply-service-change="web"]'),'Failed preflight preserves user input and pending changes');
  check(!detect().matches(':disabled'),'Failure permits retry');
  document.querySelector('[data-apply-service-change="web"]').click();
  await until(()=>release && data.status.WebProxyCheck?.Preflight===false);
  release(); release=undefined;
  await until(()=>!data.status.WebDetecting && document.body.textContent.includes('无法验证此地址'));
  check(address().value==='https://spool.example.test' && !document.querySelector('[data-apply-service-change="web"]'),'Failed real-entry check keeps applied URL and clears pending changes');
  fail=false;detect().click();await until(()=>release);release();release=undefined;
  await until(()=>document.body.textContent.includes('检测成功，可通过公网地址访问。'));
  check(document.querySelector('[data-detected-web-proxy]').textContent.includes('172.18.0.3/32'),'Real-entry success updates formal trust');
  const port=()=>document.querySelector('input[aria-label="HTTP 端口"]');
  port().focus();
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(port(),'8788');
  port().dispatchEvent(new Event('input',{bubbles:true}));
  await until(()=>port().value==='8788');
  document.querySelector('[data-section="Service"]').focus();
  await until(()=>release && data.status.WebProxyCheck?.Port===8788);
  release();release=undefined;
  await until(()=>document.body.textContent.includes('预检成功，点击应用更改后生效。'));
  check(data.status.Address==='127.0.0.1:8787' && document.querySelector('[data-apply-service-change="web"]'),'Port preflight retains real listener and Apply action');
  detect().click();await until(()=>release);
  const oldRelease=release;
  address().focus();
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(address(),'https://new.example.test');
  address().dispatchEvent(new Event('input',{bubbles:true}));
  await until(()=>!data.status.WebDetecting);
  oldRelease();
  check(data.settings.public_url==='https://spool.example.test' && !data.status.WebProxyCheck,'An edit cancels old detection without changing the saved input');
  await fetch('/report',{method:'POST',body:JSON.stringify({ok:true,checks:21})});
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
    for (let i = 0; i < 1200 && !report; i++)
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
