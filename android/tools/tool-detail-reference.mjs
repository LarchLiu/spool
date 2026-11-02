#!/usr/bin/env node
// Exercise the production React disclosure and live loader, without session data.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
if (!path.resolve(process.env.TMPDIR || "").startsWith(path.join(root, "build/tmp") + path.sep)) throw new Error("Run through android/tools/run.sh");
const require = createRequire(path.join(root, "web/package.json"));
const { createServer } = await import(require.resolve("vite"));
const { default: react } = await import(require.resolve("@vitejs/plugin-react"));
const { chromium } = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__tool_detail__.cjs"))("playwright");
const fixture = `
import React from 'react';
import {createRoot} from 'react-dom/client';
import {flushSync} from 'react-dom';
import {ToolActivityList} from '/src/tool-activity.tsx';
import {setToolDetailSource,toolDetailCache} from '/src/tool-detail.ts';
const t=(message,values={})=>message.replace(/\\{(\\w+)\\}/g,(_,key)=>values[key]??key);
let setParts,timer,mode,body='',version=1;
window.calls=[];window.aborts=0;
setToolDetailSource((href,signal)=>new Promise((resolve,reject)=>{
 const url=new URL(href,location.origin),offset=Number(url.searchParams.get('offset')||0);
 window.calls.push({offset,version});
 const wait=setTimeout(()=>{
  const text=mode==='live'?'First thought '+'.'.repeat(version):body;
  const end=Math.min(text.length,offset+32768);
  resolve({ref:mode,version:'v'+version,text:text.slice(offset,end),offset,next:end,total:text.length,prefix:'hash'});
 },mode==='live'?300:10);
 signal.addEventListener('abort',()=>{clearTimeout(wait);window.aborts++;reject(new DOMException('Aborted','AbortError'));},{once:true});
}));
function parts(){return [{role:'assistant',kind:mode==='live'?'thinking':'tool_use',name:'exec',text:'',tool:{ref:mode,href:'/chats/chat/tool-detail?ref='+mode,version:'v'+version,bytes:mode==='live'?500:body.length,kind:mode==='live'?'thinking':'shell',preview:'**Preview**'}}]}
function App(){const [value,update]=React.useState([]);setParts=update;return <ToolActivityList parts={value} backend='codex' t={t} live summary='Working'/>}
createRoot(document.getElementById('root')).render(<App/>);
window.mount=(next)=>{clearInterval(timer);mode=next;version=1;body='x'.repeat(70000);window.calls=[];window.aborts=0;toolDetailCache.clear();flushSync(()=>setParts(parts()));if(mode==='live')timer=setInterval(()=>{version++;setParts(parts())},20)};
window.stop=()=>clearInterval(timer);
`;
let server, browser;
try {
  const plugin = {
    name: "tool-detail-fixture",
    configureServer(server) {
      server.middlewares.use("/__tool-detail", async (_request, response) => {
        response.setHeader("Content-Type", "text/html");
        response.end(await server.transformIndexHtml("/__tool-detail", '<div id="root"></div><script type="module" src="/tool-detail-fixture.jsx"></script>'));
      });
    },
    resolveId(id) { if (id === "/tool-detail-fixture.jsx") return id; },
    load(id) { if (id === "/tool-detail-fixture.jsx") return fixture; },
  };
  server = await createServer({ configFile: false, root: path.join(root, "web"), plugins: [plugin, react()], server: { host: "127.0.0.1", port: 0 } });
  await server.listen(); browser = await chromium.launch({ channel: "chrome", headless: true });
  const page = await browser.newPage({ viewport: { width: 360, height: 800 } });
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__tool-detail`);
  await page.waitForFunction(() => typeof window.mount === "function");
  await page.evaluate(() => window.mount("live"));
  await page.waitForTimeout(180);
  assert.equal(await page.evaluate(() => window.calls.length), 0, "folded tool fetched a body");
  await page.locator(".tool-activity-item-summary").click();
  await page.waitForFunction(() => document.querySelector(".tool-detail")?.textContent.includes("First thought"), undefined, { timeout: 3000 });
  const live = await page.evaluate(() => ({ calls: window.calls.length, aborts: window.aborts }));
  assert.equal(live.aborts, 0, "stream versions cancelled the initial request");
  assert.ok(live.calls <= 3, "live revisions were not coalesced");
  await page.locator(".tool-activity-item-summary").click();
  const closed = await page.evaluate(() => window.calls.length);
  await page.waitForTimeout(500);
  assert.equal(await page.evaluate(() => window.calls.length), closed, "closed body kept fetching");
  await page.evaluate(() => { window.stop(); window.mount("large"); });
  await page.locator(".tool-activity-item-summary").click();
  await page.waitForSelector(".tool-detail-pages");
  assert.equal((await page.locator(".tool-detail pre").textContent()).length, 32768);
  await page.getByRole("button", { name: "Load next section" }).click();
  await page.waitForFunction(() => window.calls.some((call) => call.offset === 32768));
  await page.waitForFunction(() => document.querySelector(".tool-detail-pages")?.textContent.includes("32768–65536"));
  assert.equal((await page.locator(".tool-detail pre").textContent()).length, 32768);
  await page.evaluate(() => window.stop());
  const result = { scope: "Production React tool details in Chrome; synthetic data only", assertions: 8, liveRequestsBeforeFirstPaint: live.calls, cancelledByContinuousVersions: live.aborts, maximumRenderedPageBytes: 32768 };
  await fs.writeFile(path.join(root, "build/artifacts/android-native/tool-detail-browser.json"), JSON.stringify(result, null, 2) + "\n");
  console.log(JSON.stringify(result));
} finally { await browser?.close(); await server?.close(); }
