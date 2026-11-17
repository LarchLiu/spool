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
function Scene(){const [state,setState]=useState({messages:[],running:true}),[draft,setDraft]=useState('');window.bind=next=>flushSync(()=>setState(next));
return <main className="workspace" style={{display:'flex',flexDirection:'column',justifyContent:'flex-end',minHeight:'100vh'}}><div className="composer-wrap">
<QueuedMessages messages={state.messages} running={state.running} busy={!!state.busy} t={t} onSteer={id=>window.actions.push('steer:'+id)} onDelete={id=>window.actions.push('delete:'+id)}
onEdit={id=>{window.actions.push('edit:'+id);setDraft(state.messages.find(m=>m.id===id).message);setState({...state,messages:state.messages.filter(m=>m.id!==id)})}}
onMove={(id,before)=>{window.actions.push('move:'+id+':'+before);const apply=()=>setState(old=>{const messages=old.messages.filter(m=>m.id!==id),at=messages.findIndex(m=>m.id===before);messages.splice(at<0?messages.length:at,0,old.messages.find(m=>m.id===id));return {...old,messages,busy:false}});if(window.deferMoves){setState(old=>({...old,busy:true}));window.acceptMove=apply}else apply()}}/>
<form className="composer"><textarea id="chat-message" rows={1} placeholder="Message Codex" value={draft} onChange={e=>setDraft(e.target.value)}/>
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
  for (const theme of ["light", "dark"]) {
    await page.evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
    for (const [name, width] of [["desktop", 1100], ["tablet", 800], ["narrow", 320], ["mobile", 390]]) {
      await page.setViewportSize({ width, height: 420 });
      for (const [count, label] of [[1, "one"], [3, "several"]]) {
        await page.evaluate(state => window.bind(state), { messages: messages.slice(0, count), running: true });
        await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
        const box = await page.evaluate(() => {
          const rect = selector => { const r = document.querySelector(selector).getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom }; };
          const rows = [...document.querySelectorAll(".queued-message")].map(row => {
            const r = row.getBoundingClientRect(), text = row.querySelector(".queued-message-text").getBoundingClientRect();
            const steer = row.querySelector(".queued-message-steer").getBoundingClientRect(), remove = row.querySelector("[aria-label='Delete queued message']").getBoundingClientRect();
            return { left: r.left, right: r.right, height: r.height, textRight: text.right, steerLeft: steer.left, steerRight: steer.right, removeLeft: remove.left, removeRight: remove.right };
          });
          return { queue: rect(".queued-messages"), composer: rect(".composer"), rows, overflow: document.documentElement.scrollWidth - innerWidth };
        });
        // The row sits on the composer's top edge, within its width.
        assert.ok(Math.abs(box.queue.bottom - box.composer.top) < 1, JSON.stringify(box));
        assert.ok(Math.abs(box.queue.left - box.composer.left - 16) < 1, JSON.stringify(box));
        assert.ok(Math.abs(box.composer.right - box.queue.right - 16) < 1, JSON.stringify(box));
        assert.ok(box.overflow <= 0, JSON.stringify(box));
        assert.equal(box.rows.length, count);
        for (const row of box.rows) {
          // Long text gives way to the actions instead of pushing them out.
          assert.ok(row.textRight <= row.steerLeft && row.steerRight <= row.removeLeft && row.removeRight <= row.right, JSON.stringify(row));
          assert.ok(row.height < 44, JSON.stringify(row));
        }
        const borders = await page.evaluate(() => [".queued-messages", ".composer"].map(selector => getComputedStyle(document.querySelector(selector)).borderTopColor));
        assert.equal(borders[0], borders[1], "queue and input share the same visible frame");
        await page.screenshot({ path: path.join(output, `${theme === "dark" ? "dark-" : ""}${name}-${label}.png`) });
        await page.locator(".queued-message").first().getByRole("button", { name: "Queued message actions" }).click();
        await page.getByRole("menuitem", { name: "Edit message" }).waitFor();
        const menu = await page.getByRole("menu").boundingBox();
        assert.ok(menu.x >= 0 && menu.x + menu.width <= width && menu.y >= 0 && menu.y + menu.height <= 420);
        if (count === 3) await page.screenshot({ path: path.join(output, `${theme === "dark" ? "dark-" : ""}${name}-menu.png`) });
        await page.keyboard.press("Escape");
        assert.equal(await page.getByRole("menu").count(), 0);
      }
    }
  }
  assert.deepEqual(await page.locator(".queued-message-text").allTextContents(), [messages[0].message, long, "notes.txt"]);
  assert.equal(await page.locator(".composer button[aria-label='Stop response']").count(), 1);
  await page.locator(".queued-message").nth(1).getByRole("button", { name: "Steer" }).click();
  await page.locator(".queued-message").nth(2).getByRole("button", { name: "Delete queued message" }).click();
  assert.deepEqual(await page.evaluate(() => window.actions), ["steer:second", "delete:third"]);
  const settled = () => page.waitForFunction(() => [...document.querySelectorAll(".queued-message")].every(row => row.getAnimations().every(animation => animation.playState === "finished")));
  const drag = async (id, target, after = false, inspect = false) => {
    await settled();
    const grip = await page.locator(`[data-queue-id='${id}'] .queued-message-grip`).boundingBox();
    const row = await page.locator(`[data-queue-id='${target}']`).boundingBox();
    await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
    await page.mouse.down();
    if (inspect) {
      assert.equal(grip.width, grip.height, "pressed drag handles remain circular");
      await page.screenshot({ path: path.join(output, "dark-grip-pressed.png") });
    }
    await page.mouse.move(grip.x + grip.width / 2, row.y + (after ? row.height - 2 : 2), { steps: 8 });
    if (inspect) {
      await page.waitForFunction(() => new DOMMatrix(getComputedStyle(document.querySelector("[data-queue-id='first']")).transform).m42 > 1);
      const moving = await page.locator(`[data-queue-id='${id}']`).boundingBox();
      assert.ok(moving.y < grip.y - 30, "the dragged message follows the pointer");
      assert.equal(await page.locator(".queued-message.dragging").count(), 1);
      assert.equal(await page.locator(".queued-message.dragging").evaluate(row => getComputedStyle(row).opacity), "1");
      await page.screenshot({ path: path.join(output, "dark-dragging.png") });
    }
    await page.mouse.up();
    await settled();
  };
  await page.evaluate(() => { window.deferMoves = true; });
  await drag("third", "first", false, true);
  assert.deepEqual(await page.locator(".queued-message").evaluateAll(rows => rows.map(row => row.dataset.queueId)), ["third", "first", "second"]);
  assert.equal(await page.locator(".queued-message-grip:disabled").count(), 3, "the new order stays visible while the server accepts it");
  await page.evaluate(() => { window.acceptMove(); window.deferMoves = false; });
  await page.evaluate(() => { window.deferMoves = true; });
  await drag("third", "second", true);
  assert.deepEqual(await page.locator(".queued-message").evaluateAll(rows => rows.map(row => row.dataset.queueId)), ["first", "second", "third"]);
  await page.evaluate(state => { window.bind(state); window.deferMoves = false; }, { messages: [messages[2], messages[0], messages[1]], running: true, busy: false });
  await settled();
  assert.deepEqual(await page.locator(".queued-message").evaluateAll(rows => rows.map(row => row.dataset.queueId)), ["third", "first", "second"], "a rejected reorder returns to the server's order");
  await drag("third", "second", true);
  const touch = await page.context().newCDPSession(page);
  try {
    await touch.send("Emulation.setTouchEmulationEnabled", { enabled: true });
    const grip = await page.locator("[data-queue-id='third'] .queued-message-grip").boundingBox();
    const first = await page.locator("[data-queue-id='first']").boundingBox();
    await touch.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: grip.x + grip.width / 2, y: grip.y + grip.height / 2 }] });
    await touch.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: grip.x + grip.width / 2, y: first.y + 2 }] });
    await touch.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    assert.equal(await page.locator(".queued-message").first().getAttribute("data-queue-id"), "third");
  } finally { await touch.detach(); }
  await drag("third", "second", true);
  await page.locator("[data-queue-id='second'] .queued-message-grip").press("ArrowUp");
  assert.equal(await page.locator(".queued-message").first().getAttribute("data-queue-id"), "second");
  await page.locator("[data-queue-id='second'] .queued-message-more").click();
  await page.getByRole("menuitem", { name: "Edit message" }).click();
  assert.equal(await page.locator("#chat-message").inputValue(), long);
  assert.equal(await page.locator("[data-queue-id='second']").count(), 0);
  const longQueue = Array.from({ length: 20 }, (_, i) => ({ id: `long-${i}`, message: `Message ${i}`, created_at: i }));
  await page.evaluate(messages => window.bind({ messages, running: true }), longQueue);
  const handle = await page.locator("[data-queue-id='long-0'] .queued-message-grip").boundingBox();
  const queue = await page.locator(".queued-messages").boundingBox();
  await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2);
  await page.mouse.down();
  await page.mouse.move(handle.x + handle.width / 2, queue.y + queue.height - 3, { steps: 5 });
  await page.waitForFunction(() => { const queue = document.querySelector(".queued-messages"); return queue.scrollTop + queue.clientHeight >= queue.scrollHeight - 1; });
  await page.mouse.up();
  await settled();
  assert.equal(await page.locator(".queued-message").last().getAttribute("data-queue-id"), "long-0");
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.evaluate(state => window.bind(state), { messages, running: true });
  await drag("third", "first");
  assert.deepEqual(await page.locator(".queued-message").evaluateAll(rows => rows.map(row => row.dataset.queueId)), ["third", "first", "second"]);
  assert.equal(await page.locator(".queued-message").evaluateAll(rows => rows.reduce((count, row) => count + row.getAnimations().length, 0)), 0);
  await page.evaluate(state => window.bind(state), { messages, running: true, busy: true });
  for (const button of await page.locator(".queued-message button").all()) assert.equal(await button.isDisabled(), true);
  // Once the turn has ended a remaining message can only be sent.
  await page.evaluate(state => window.bind(state), { messages: messages.slice(0, 1), running: false });
  assert.equal(await page.getByRole("button", { name: "Steer" }).count(), 0);
  assert.equal(await page.getByRole("button", { name: "Send now" }).count(), 1);
  assert.deepEqual(errors, []);
  console.log({ themes: ["light", "dark"], rowSitsOnComposer: true, circularGrip: true, animatedReordering: true, touchReordering: true, reducedMotion: true, actionsStayVisible: true, noHorizontalOverflow: true, javascriptErrors: errors });
} finally { if (browser) await browser.close(); if (server) await server.close(); }
