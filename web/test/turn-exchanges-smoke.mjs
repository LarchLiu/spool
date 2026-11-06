// Actual main.tsx turn JSX with synthetic transcripts and an owned browser.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
assert.ok(path.resolve(process.env.TMPDIR || "").startsWith(path.join(root, "build/tmp") + path.sep));
const require = createRequire(path.join(root, "web/package.json"));
const { createServer } = await import(require.resolve("vite"));
const { default: react } = await import(require.resolve("@vitejs/plugin-react"));
const { chromium } = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__turn_exchanges__.cjs"))("playwright");
const output = path.join(root, "build/artifacts/turn-exchanges");
const source = await fs.readFile(path.join(root, "web/src/main.tsx"), "utf8");
const at = source.indexOf("chat.turns.map((turn) => {");
const end = source.indexOf("\n              })", at);
assert.ok(at >= 0 && end > at, "Inspect the moved production turn JSX");
const turns = source.slice(at, end) + "\n              })";
const script = `import React,{useState} from 'react';import {createRoot} from 'react-dom/client';import {flushSync} from 'react-dom';
import {TurnResponse} from '/src/tool-activity.tsx';import {UserMessage,hasUserMessage} from '/src/user-message.tsx';
import {MessageFooter} from '/src/message-footer.tsx';import {TurnOperatorAvatar,TurnActions} from '/src/turn-identity.tsx';
import {turnOperator} from '/src/chat-state.ts';import {AttachmentCards} from '/src/attachments.tsx';
import {turnExchanges,exchangeFiles} from '/src/turn-exchanges.ts';import {turnWorkLabel} from '/src/turn-presentation.ts';
import {translator} from '/src/i18n.ts';import '/src/style.css';
const displayUser={id:'alice',nickname:'Alice'},resolveProfile=()=>displayUser,t=translator('en'),locale='en',clock=6000,effortLabel=x=>x;
const setPreview=()=>{},fail=()=>{},workSummary=(turn,t,now,_effort,answered)=>turnWorkLabel(turn,t,now,answered);
function ChatStatusIndicator(){return <span role="status">Working</span>}function ApprovalCard(){return null}
function Scene(){const [chat,setChat]=useState({turns:[]});window.bind=next=>flushSync(()=>setChat(next));
return <div className="history-inner">{${turns}}</div>}
createRoot(document.getElementById('root')).render(<Scene/>);`;
const pixel = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jhP0AAAAASUVORK5CYII=";
const user = (text, timestamp) => ({ role: "user", kind: "text", text, timestamp });
const answer = text => ({ role: "assistant", kind: "text", phase: "final_answer", text });
const initial = backend => ({ id: "chat", user_id: "alice", project_id: "project", transcript: { parts: [
  user("Initial request", 1000), answer("First answer"), user("Follow up", 4000),
  { role: "user", kind: "image", name: "followup.png", image_url: pixel, text: "[image]" },
  { role: "user", kind: "context", name: "notes.txt", file_path: "/work/notes.txt", text: "notes.txt" }, answer("Second answer"),
] }, turns: [{ id: "turn", backend, started_at: 1000, status: "running", part_start: 0, input_end: 1, part_end: 6,
  attachments: [{ id: "uploaded", name: "first.txt", media_type: "text/plain", size: 4 }] }] });
let server, browser;
const errors = [];
async function assertWorkflowAlignment(page) {
  const edges = await page.locator(".turn").last().evaluate(turn => {
    const edge = selector => turn.querySelector(selector).getBoundingClientRect().right;
    return {
      answer: edge(".assistant-text"),
      workflow: edge(".tool-activity-list"),
      initial: edge(".user-row .user-message"),
      followup: edge(".tool-progress-user .user-message"),
      initialFooter: edge(".user-row .message-footer"),
      followupFooter: edge(".tool-progress-user .message-footer"),
    };
  });
  assert.ok(Math.abs(edges.workflow - edges.answer) < 1, JSON.stringify(edges));
  assert.ok(Math.abs(edges.followup - edges.initial) < 1, JSON.stringify(edges));
  assert.ok(Math.abs(edges.followupFooter - edges.initialFooter) < 1, JSON.stringify(edges));
}
const plugin = { name: "turn-exchanges-smoke", resolveId: id => id === "/__turn-exchanges.tsx" ? id : undefined,
  load: id => id === "/__turn-exchanges.tsx" ? script : undefined, configureServer(instance) {
    instance.middlewares.use(async (req, res, next) => {
      if (req.url === "/__turn-exchanges") { res.setHeader("Content-Type", "text/html"); res.end(await instance.transformIndexHtml(req.url, '<!doctype html><div id="root"></div><script type="module" src="/__turn-exchanges.tsx"></script>')); return; }
      next();
    });
  } };
try {
  await fs.mkdir(output, { recursive: true });
  server = await createServer({ configFile: false, root: path.join(root, "web"), plugins: [plugin, react()], server: { host: "127.0.0.1", port: 0 } });
  await server.listen(); browser = await chromium.launch({ channel: "chrome", headless: true });
  const page = await browser.newPage({ viewport: { width: 1100, height: 1000 } });
  page.on("pageerror", error => errors.push(String(error)));
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__turn-exchanges`);
  await page.waitForFunction(() => typeof window.bind === "function");
  for (const backend of ["codex", "claude"]) {
    const chat = initial(backend);
    await page.evaluate(chat => window.bind(chat), chat);
    assert.equal(await page.locator(".work-label-toggle").count(), 1);
    assert.deepEqual(await page.locator(".user-bubble").allTextContents(), ["Initial request"]);
    assert.deepEqual(await page.locator(".assistant-text").allTextContents(), ["Second answer"]);
    assert.equal(await page.getByText("First answer", { exact: true }).count(), 0);
    assert.equal(await page.getByText("Follow up", { exact: true }).count(), 0);
    await page.locator(".work-label-toggle").click();
    assert.deepEqual(await page.locator(".user-bubble").allTextContents(), ["Initial request", "Follow up"]);
    assert.deepEqual(await page.locator(".assistant-text").allTextContents(), ["Second answer"]);
    assert.deepEqual(await page.locator(".turn .user-row, .turn .tool-progress-message, .turn .assistant").allTextContents().then(items => items.map(text => ["Initial request", "First answer", "Follow up", "Second answer"].find(label => text.includes(label)))), ["Initial request", "First answer", "Follow up", "Second answer"]);
    assert.equal(await page.locator(".user-row").nth(0).locator(".attachment-card").count(), 1);
    assert.equal(await page.locator(".user-row").nth(1).locator(".attachment-card").count(), 2);
    assert.equal(await page.locator(".user-row").nth(1).locator("time").getAttribute("datetime"), new Date(4000).toISOString());
    assert.equal(await page.locator(".user-row").nth(1).evaluate(row => getComputedStyle(row).justifyContent), "flex-end");
    await assertWorkflowAlignment(page);
    assert.equal(await page.locator(".user-row").nth(1).locator("img[alt='followup.png']").count(), 1);
    // Append steering to the same live turn and preserve earlier message DOM.
    await page.evaluate(() => { window.originalBubble = document.querySelector(".user-bubble"); window.originalFollowup = document.querySelectorAll(".user-bubble")[1]; });
    chat.transcript.parts.push(user("Third request", 5000)); chat.turns[0].part_end++;
    await page.evaluate(chat => window.bind(chat), chat);
    assert.deepEqual(await page.locator(".user-bubble").allTextContents(), ["Initial request", "Follow up", "Third request"]);
    assert.ok(await page.evaluate(() => window.originalBubble === document.querySelector(".user-bubble") && window.originalFollowup === document.querySelectorAll(".user-bubble")[1]));
    assert.equal(await page.locator(".assistant-text").count(), 0);
    assert.match(await page.locator(".work-label-toggle").textContent(), /Working for/);
    chat.transcript.parts.push(answer("Third answer")); chat.turns[0].part_end++; chat.turns[0].status = "completed"; chat.turns[0].finished_at = 6000;
    await page.evaluate(chat => window.bind(chat), chat);
    assert.deepEqual(await page.locator(".assistant-text").allTextContents(), ["Third answer"]);
    assert.deepEqual(await page.locator(".user-bubble").allTextContents(), ["Initial request"]);
    assert.equal(await page.locator(".tool-progress-user").count(), 0);
    await page.locator(".work-label-toggle").click();
    assert.deepEqual(await page.locator(".tool-progress-message").allTextContents(), ["First answer", "Second answer"]);
    assert.deepEqual(await page.locator(".user-bubble").allTextContents(), ["Initial request", "Follow up", "Third request"]);
    await page.locator(".work-label-toggle").click();
    assert.deepEqual(await page.locator(".user-bubble").allTextContents(), ["Initial request"]);
    const older = { id: "older", backend, started_at: 1, finished_at: 2, status: "completed", part_start: 0, input_end: 1, part_end: 2 };
    chat.transcript.parts.unshift(user("Older request"), answer("Older answer"));
    for (const turn of chat.turns) { turn.part_start += 2; turn.input_end += 2; turn.part_end += 2; }
    chat.turns.unshift(older); await page.evaluate(chat => window.bind(chat), chat);
    assert.ok(await page.evaluate(() => window.originalBubble === document.querySelectorAll(".turn")[1].querySelector(".user-bubble")));
  }
  for (const [name, width] of [["desktop", 1100], ["mobile", 390]]) {
    await page.setViewportSize({ width, height: 1000 });
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
    await page.screenshot({ path: path.join(output, name + ".png"), fullPage: true });
    await page.locator(".turn").last().locator(".work-label-toggle").click();
    await assertWorkflowAlignment(page);
    await page.screenshot({ path: path.join(output, name + "-expanded.png"), fullPage: true });
    await page.locator(".turn").last().locator(".work-label-toggle").click();
  }
  assert.deepEqual(errors, []);
  const report = { oneDisclosurePerTurn: true, collapsedShowsInitialInputAndFinalAnswer: true, orderedInputsAndResponses: true, attachmentsStayWithInput: true, workflowMatchesAnswerWidth: true, userMessagesShareRightEdge: true, liveAppendPreservesExistingMessages: true, historyPrependPreservesExistingMessages: true, codexAndClaude: true, javascriptErrors: errors };
  await fs.writeFile(path.join(output, "browser-report.json"), JSON.stringify(report, null, 2) + "\n"); console.log(report);
} finally { if (browser) await browser.close(); if (server) await server.close(); }
