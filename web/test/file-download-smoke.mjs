import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

const scratch = requireRunTemp();
const root = fileURLToPath(new URL("../", import.meta.url));
const chrome = await readFile(new URL("../../cmd/desktop/window_chrome.go", import.meta.url), "utf8");
const preload = chrome.match(/const desktopWindowPreload = `([\s\S]*?)`/)[1];
const output = fileURLToPath(new URL("../../build/artifacts/file-download/", import.meta.url));
const { chromium, webkit } = createRequire(join(process.env.SPOOL_NODE_MODULES || join(root, "node_modules"), "__download__.cjs"))("playwright");
const cleanups = [];
const source = `
import React,{useState} from 'react';import {createRoot} from 'react-dom/client';
import {FileSidebar} from '/src/file-sidebar.tsx';import {translator} from '/src/i18n.ts';import '/src/style.css';
const t=translator('zh-CN');document.documentElement.lang='zh-CN';
function App(){const [selection,setSelection]=useState({target:{path:'报告.txt'},scope:'project',request:1});
return <div className="app left-collapsed" style={{'--right-panel-width':'420px'}}><main className="workspace"/><FileSidebar
project={{id:'project',name:'Project'}} active mobileOpen width={420} selection={selection} t={t} theme="light"
onSelectWorkspace={()=>{}} onRefreshWorkspace={()=>{}} onOpen={target=>setSelection({target,scope:'project',request:2})}
onClear={()=>setSelection(null)} onClose={()=>{}} onResize={()=>{}}/></div>}
createRoot(document.getElementById('root')).render(<App/>);`;
const plugin = {
  name: "file-download-fixture",
  resolveId: id => id === "/__download.tsx" ? id : undefined,
  load: id => id === "/__download.tsx" ? source : undefined,
  configureServer(server) {
    server.middlewares.use(async (req, res, next) => {
      if (req.url.startsWith("/api/") && new URL(req.url, "http://fixture").searchParams.get("download") === "1") {
        res.setHeader("Content-Type", "application/octet-stream");
        res.setHeader("Content-Disposition", 'attachment; filename="report.txt"');
        res.end("original file bytes");
        return;
      }
      if (req.url !== "/__download") return next();
      res.setHeader("Content-Type", "text/html");
      res.end(await server.transformIndexHtml(req.url, '<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="/__download.tsx"></script>'));
    });
  },
};
const server = await createTestViteServer({ after: callback => cleanups.push(callback) }, {
  configFile: false, root, plugins: [react(), plugin], server: { host: "127.0.0.1", port: 0 },
});
const reports = [];
try {
  await server.listen(); await mkdir(output, { recursive: true });
  for (const [engine, type, launch] of [["chromium", chromium, { channel: "chrome" }], ["webkit", webkit, {}]]) {
    const browser = await type.launch({ ...launch, headless: true });
    try {
      for (const mode of ["desktop", "picker", "browser"]) {
        const context = await browser.newContext({ viewport: { width: mode === "browser" ? 390 : 1280, height: 900 }, acceptDownloads: true });
        try {
          await context.addInitScript(mode => {
            Object.defineProperty(window, "showSaveFilePicker", { configurable: true, value: mode === "picker" ? async () => {
              if (window.cancelPicker) throw new DOMException("cancelled", "AbortError");
              return { name: "报告 (2).txt", createWritable: async () => new WritableStream({ write(bytes) { window.savedBytes = [...bytes]; }, close() { if (window.failWrite) throw new Error("disk full"); } }) };
            } : undefined });
          }, mode);
          if (mode === "desktop") await context.addInitScript({ content: `
            (() => {
              const listeners = new Map();
              window.mygo = { platform: 'linux', on: (name, listener) => listeners.set(name, listener) };
              window.emitNativeDownload = result => listeners.get('spool:download')?.(result);
            })();
            ${preload}
          ` });
          const page = await context.newPage(); const errors = []; const requests = [];
          page.on("pageerror", error => errors.push(error.message));
          let failure = false;
          await page.route("**/api/**", async route => {
            const request = route.request(), url = new URL(request.url());
            if (url.searchParams.get("download") === "1") {
              requests.push(request.method());
              return failure ? route.fulfill({ status: 403, json: { error: "File is protected" } })
                : route.fulfill({ contentType: "application/octet-stream", headers: { "Content-Disposition": 'attachment; filename="report.txt"' }, body: "original file bytes" });
            }
            return route.fulfill({ json: url.pathname.endsWith("/files") ? { entries: [{ path: "报告.txt", name: "报告.txt", kind: "file" }] }
              : { path: "报告.txt", name: "报告.txt", kind: "text", media_type: "text/plain", size: 19, content: "original file bytes" } });
          });
          const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
          const open = async () => { await page.goto(origin + "/__download"); await page.getByRole("link", { name: "下载文件", exact: true }).waitFor(); };
          const click = () => page.getByRole("link", { name: "下载文件", exact: true }).click();
          const status = page.locator(".file-download-status");
          await open();
          if (mode === "desktop") {
            const downloadEvent = page.waitForEvent("download"); await click(); const download = await downloadEvent;
            assert.equal(await download.failure(), null);
            assert.match(await status.innerText(), /正在下载/);
            assert.equal(requests[0], "HEAD");
            await download.saveAs(join(scratch, `${engine}-desktop.txt`));
            assert.equal(await readFile(join(scratch, `${engine}-desktop.txt`), "utf8"), "original file bytes");
            await page.evaluate(() => window.emitNativeDownload({ url: "http://elsewhere/file", state: "saved", path: "/wrong" }));
            assert.match(await status.innerText(), /正在下载/);
            const url = new URL(await page.getByRole("link", { name: "下载文件", exact: true }).getAttribute("href"), origin).href;
            await page.getByRole("button", { name: "关闭文件预览", exact: true }).click();
            await page.evaluate(url => window.emitNativeDownload({ url, state: "saved", path: "/Users/test/Downloads/报告 (2).txt" }), url);
            await page.getByRole("status").filter({ hasText: "下载完成，已保存到 /Users/test/Downloads/报告 (2).txt" }).waitFor();
            await page.screenshot({ path: join(output, `${engine}-desktop-saved.png`) });
            await page.getByRole("treeitem", { name: "报告.txt", exact: true }).click();
            const next = page.waitForEvent("download"); await click(); await next;
            await page.evaluate(url => window.emitNativeDownload({ url, state: "failed", error: "disk full" }), url);
            await page.getByRole("alert").filter({ hasText: "disk full" }).waitFor();
            const retry = page.waitForEvent("download"); await click(); await retry;
            await page.evaluate(url => window.emitNativeDownload({ url, state: "saved", path: "/Users/test/Downloads/报告 (3).txt" }), url);
            await page.getByRole("status").filter({ hasText: "报告 (3).txt" }).waitFor();
          } else if (mode === "picker") {
            await click(); await page.getByRole("status").filter({ hasText: "下载完成，已保存到 报告 (2).txt" }).waitFor();
            assert.equal(Buffer.from(await page.evaluate(() => window.savedBytes)).toString(), "original file bytes");
            await page.evaluate(() => window.failWrite = true); await click();
            await page.getByRole("alert").filter({ hasText: "disk full" }).waitFor();
            await page.evaluate(() => window.cancelPicker = true); const before = requests.length; await click();
            await page.getByRole("status").filter({ hasText: "已取消下载" }).waitFor(); assert.equal(requests.length, before);
          } else {
            const event = page.waitForEvent("download"); await click(); const download = await event;
            await page.getByRole("status").filter({ hasText: "交给浏览器保存" }).waitFor();
            await download.saveAs(join(scratch, `${engine}-browser.txt`));
            assert.equal(await readFile(join(scratch, `${engine}-browser.txt`), "utf8"), "original file bytes");
            await page.screenshot({ path: join(output, `${engine}-mobile-saved.png`) });
            assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
          }
          failure = true; await open(); await click();
          await page.getByRole("alert").filter({ hasText: /下载 报告.txt 失败/ }).waitFor();
          await page.screenshot({ path: join(output, `${engine}-${mode}-failed.png`) });
          assert.deepEqual(errors, []); reports.push({ engine, mode, success: true, errors });
        } finally { await context.close(); }
      }
    } finally { await browser.close(); }
  }
  await writeFile(join(output, "report.json"), JSON.stringify(reports, null, 2) + "\n");
  console.log(JSON.stringify(reports));
} finally { for (const cleanup of cleanups.reverse()) await cleanup(); }
