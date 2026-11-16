import { requireRunTemp } from "../../tools/test-scratch.mjs";
// The production queued-message row and stylesheet above a composer of the
// production shape, in an owned browser.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
requireRunTemp();
const require = createRequire(path.join(root, "web/package.json"));
const { createServer } = await import(require.resolve("vite"));
const { default: react } = await import(require.resolve("@vitejs/plugin-react"));
const { chromium } = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__queued_messages__.cjs"))("playwright");
const output = path.join(root, "build/artifacts/queued-messages");
const script = `import React,{useState} from 'react';import {createRoot} from 'react-dom/client';import {flushSync} from 'react-dom';
import {QueuedMessages} from '/src/queued-messages.tsx';import {ComposerSendButton} from '/src/composer-send-button.tsx';
import {translator} from '/src/i18n.ts';import '/src/style.css';
const t=translator('en');window.actions=[];
function Scene(){const [state,setState]=useState({messages:[],running:true});window.bind=next=>flushSync(()=>setState(next));
return <main className="workspace" style={{display:'flex',flexDirection:'column',justifyContent:'flex-end',minHeight:'100vh'}}><div className="composer-wrap">
<QueuedMessages messages={state.messages} running={state.running} busy={false} t={t} onSteer={id=>window.actions.push('steer:'+id)} onDelete={id=>window.actions.push('delete:'+id)}/>
<form className="composer"><textarea id="chat-message" rows={1} placeholder="Message Codex" defaultValue=""/>
<div className="composer-bottom"><div className="composer-tools"/><div className="composer-actions">
<ComposerSendButton active={state.running} stopping={false} disabled={false} t={t} onStop={()=>{}}/></div></div></form></div></main>}
createRoot(document.getElementById('root')).render(<Scene/>);`;
const plugin = { name: "queued-messages-smoke", resolveId: id => id === "/__queued-messages.tsx" ? id : undefined,
  load: id => id === "/__queued-messages.tsx" ? script : undefined, configureServer(instance) {
    instance.middlewares.use(async (req, res, next) => {
      if (req.url === "/__queued-messages") { res.setHeader("Content-Type", "text/html"); res.end(await instance.transformIndexHtml(req.url, '<!doctype html><div id="root"></div><script type="module" src="/__queued-messages.tsx"></script>')); return; }
      next();
    });
  } };
const long = "Check whether only the TypeScript SDK is published, and if so list what the other languages would need before they can ship ".repeat(3).trim();
const messages = [
  { id: "first", message: "Is there only a TypeScript SDK?", created_at: 1 },
  { id: "second", message: long, created_at: 2 },
  { id: "third", message: "", attachments: ["notes.txt"], created_at: 3 },
];
let server, browser;
const errors = [];
try {
  await fs.mkdir(output, { recursive: true });
  server = await createServer({ cacheDir: path.join(requireRunTemp(), "vite-cache"), configFile: false, root: path.join(root, "web"), plugins: [plugin, react()], server: { host: "127.0.0.1", port: 0 } });
  await server.listen(); browser = await chromium.launch({ channel: "chrome", headless: true });
  const page = await browser.newPage({ viewport: { width: 1100, height: 420 } });
  page.on("pageerror", error => errors.push(String(error)));
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__queued-messages`);
  await page.waitForFunction(() => typeof window.bind === "function");
  assert.equal(await page.locator(".queued-messages").count(), 0);
  for (const [name, width] of [["desktop", 1100], ["mobile", 390]]) {
    await page.setViewportSize({ width, height: 420 });
    for (const [count, label] of [[1, "one"], [3, "several"]]) {
      await page.evaluate(state => window.bind(state), { messages: messages.slice(0, count), running: true });
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      const box = await page.evaluate(() => {
        const rect = selector => { const r = document.querySelector(selector).getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom }; };
        const rows = [...document.querySelectorAll(".queued-message")].map(row => {
          const r = row.getBoundingClientRect(), text = row.querySelector(".queued-message-text").getBoundingClientRect();
          const steer = row.querySelector(".queued-message-steer").getBoundingClientRect(), remove = row.querySelector("button.icon").getBoundingClientRect();
          return { left: r.left, right: r.right, height: r.height, textRight: text.right, steerLeft: steer.left, steerRight: steer.right, removeLeft: remove.left, removeRight: remove.right };
        });
        return { queue: rect(".queued-messages"), composer: rect(".composer"), rows, overflow: document.documentElement.scrollWidth - innerWidth };
      });
      // The row sits on the composer's top edge, within its width.
      assert.ok(Math.abs(box.queue.bottom - box.composer.top) < 1, JSON.stringify(box));
      assert.ok(box.queue.left >= box.composer.left && box.queue.right <= box.composer.right, JSON.stringify(box));
      assert.ok(Math.abs((box.queue.left - box.composer.left) - (box.composer.right - box.queue.right)) < 1, JSON.stringify(box));
      assert.ok(box.overflow <= 0, JSON.stringify(box));
      assert.equal(box.rows.length, count);
      for (const row of box.rows) {
        // Long text gives way to the actions instead of pushing them out.
        assert.ok(row.textRight <= row.steerLeft && row.steerRight <= row.removeLeft && row.removeRight <= row.right, JSON.stringify(row));
        assert.ok(row.height < 44, JSON.stringify(row));
      }
      await page.screenshot({ path: path.join(output, `${name}-${label}.png`) });
    }
  }
  assert.deepEqual(await page.locator(".queued-message-text").allTextContents(), [messages[0].message, long, "notes.txt"]);
  assert.equal(await page.locator(".composer button[aria-label='Stop response']").count(), 1);
  await page.locator(".queued-message").nth(1).getByRole("button", { name: "Steer" }).click();
  await page.locator(".queued-message").nth(2).getByRole("button", { name: "Delete queued message" }).click();
  assert.deepEqual(await page.evaluate(() => window.actions), ["steer:second", "delete:third"]);
  // Once the turn has ended a remaining message can only be sent.
  await page.evaluate(state => window.bind(state), { messages: messages.slice(0, 1), running: false });
  assert.equal(await page.getByRole("button", { name: "Steer" }).count(), 0);
  assert.equal(await page.getByRole("button", { name: "Send now" }).count(), 1);
  assert.deepEqual(errors, []);
  console.log({ rowSitsOnComposer: true, actionsStayVisible: true, noHorizontalOverflow: true, javascriptErrors: errors });
} finally { if (browser) await browser.close(); if (server) await server.close(); }
