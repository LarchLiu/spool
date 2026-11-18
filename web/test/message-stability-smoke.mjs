// Keep the submitted message mounted through REST/SSE acceptance and native history.
// Run with: sh tools/run.sh message-stability bun web/test/message-stability-smoke.mjs
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { createServer } from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const require = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__stability__.cjs"));
const { chromium } = require("playwright");
const cleanup = [];
const server = await createTestViteServer({ after: fn => cleanup.push(fn) }, {
  configFile: false, root: path.join(root, "web"), plugins: [react()],
  server: { middlewareMode: true, hmr: false },
});
const httpServer = createServer(server.middlewares);
let browser;
try {
  await new Promise((resolve, reject) => {
    httpServer.once("error", reject);
    httpServer.listen(0, "127.0.0.1", resolve);
  });
  browser = await chromium.launch({ channel: "chrome", headless: true });
  const origin = `http://127.0.0.1:${httpServer.address().port}`;
  for (const backend of ["codex", "claude"]) {
    for (const fresh of [false, true]) {
      for (const streamFirst of [false, true]) {
        const label = `${backend}/${fresh ? "new" : "existing"}/${streamFirst ? "SSE" : "REST"}-first`;
        const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, serviceWorkers: "block" });
        let release;
        const acceptance = new Promise(resolve => { release = resolve; });
        try {
          const page = await context.newPage();
          page.setDefaultTimeout(10000);
          const errors = [];
          page.on("pageerror", error => errors.push(String(error)));
          await page.addInitScript(() => {
            localStorage.setItem("spool.ui-preferences.v1", JSON.stringify({ language: "en", theme: "light" }));
            window.WebSocket = class extends EventTarget { readyState = 0; send() {} close() {} };
            const Source = window.EventSource;
            window.__chatSources = [];
            window.EventSource = class extends Source {
              constructor(url, options) { super(url, options); window.__chatSources.push(this); }
            };
          });
          const oldInput = { role: "user", kind: "text", text: "Earlier request" };
          const oldAnswer = { role: "assistant", kind: "text", text: "Earlier answer", phase: "final_answer" };
          const oldTurn = { id: "older", native_turn_id: "native-older", backend, status: "completed", started_at: 1, finished_at: 2, part_start: 0, input_end: 1, part_end: 2 };
          const chat = {
            id: "chat", project_id: "project", user_id: "owner", title: "Conversation", backend,
            status: "ready", can_control: true, updated_at: 1,
            transcript: { parts: fresh ? [] : [oldInput, oldAnswer] }, turns: fresh ? [] : [oldTurn],
          };
          let accepted;
          await page.route("**/api/**", async route => {
            const request = route.request(), endpoint = new URL(request.url()).pathname;
            const json = (value, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
            if (endpoint === "/api/me") return json({ user: { id: "owner", nickname: "Owner" }, csrf: "fixture" });
            if (endpoint === "/api/version") return json({ version: "fixture" });
            if (endpoint === "/api/profiles") return json({ revision: 1, people: [{ id: "owner", nickname: "Owner" }] });
            if (endpoint === "/api/navigation") return json({ projects: [{ id: "project", name: "Project", can_create: true }], chats: fresh ? [] : [chat] });
            if (endpoint === "/api/agents") return json([{ backend, enabled: true, installed: true, catalog: { models: [] } }]);
            if (endpoint === "/api/permissions") return json({ backend, default: "ask", modes: [{ id: "ask", available: true }] });
            if (endpoint === "/api/chats" && request.method() === "POST") return json(chat);
            if (endpoint.endsWith("/messages") && request.method() === "POST") {
              const payload = request.postDataJSON(), start = chat.transcript.parts.length;
              accepted = {
                ...chat, status: "running", updated_at: 2,
                transcript: { parts: [...chat.transcript.parts, { role: "user", kind: "text", text: payload.message }] },
                turns: [...chat.turns, { id: "server-turn", native_turn_id: "native-turn", backend, user_id: "owner", status: "running", started_at: Date.now(), part_start: start, input_end: start + 1, part_end: start + 1 }],
              };
              await acceptance;
              return json(accepted, 202);
            }
            if (endpoint.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n` });
            if (endpoint === "/api/chats/chat") return json(chat);
            if (endpoint.endsWith("/workspace")) return json({ path: "/work", kind: "main", available: true });
            return json({ path: ".", entries: [], truncated: false });
          });
          await page.goto(origin + (fresh ? "/" : "/?chat=chat"));
          const composer = page.locator("#chat-message");
          await composer.fill(Array.from({ length: 12 }, (_, index) => `Prompt line ${index + 1}`).join("\n\n"));
          const posted = page.waitForRequest(request => request.method() === "POST" && new URL(request.url()).pathname.endsWith("/messages"));
          const answered = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname.endsWith("/messages"));
          await page.getByRole("button", { name: "Send message", exact: true }).click();
          await posted;
          await page.locator(".turn").last().getByRole("button", { name: "Show more", exact: true }).click();
          await page.evaluate(() => {
            window.originalTurn = document.querySelector(".turn:last-child");
            window.originalBubble = window.originalTurn.querySelector(".user-bubble");
            window.replayedAnimations = 0;
            document.querySelector(".history").addEventListener("animationstart", event => {
              if (event.target.matches(".turn")) window.replayedAnimations++;
            });
          });
          const push = (value, event = "snapshot") => page.evaluate(({ value, event }) => {
            window.__chatSources.filter(source => source.url.includes("/chats/chat/")).at(-1)
              .dispatchEvent(new MessageEvent(event, { data: JSON.stringify(value) }));
          }, { value, event });
          const assertStable = async stage => {
            assert.equal(await page.evaluate(() => window.originalTurn === document.querySelector(".turn:last-child") && window.originalBubble === document.querySelector(".turn:last-child .user-bubble")), true, `${label}: ${stage} preserves message DOM`);
            assert.equal(await page.locator(".turn").last().getByRole("button", { name: "Show less", exact: true }).getAttribute("aria-expanded"), "true", `${label}: expanded prompt stays open`);
          };
          if (streamFirst) {
            await push(accepted);
            await assertStable("SSE acceptance");
          }
          release();
          await answered;
          await page.waitForFunction(() => !document.querySelector(".attachment-add").disabled);
          await assertStable("REST acceptance");
          if (!streamFirst) await push(accepted);
          const streaming = structuredClone(accepted);
          streaming.updated_at = 3;
          streaming.transcript.parts.push({ role: "assistant", kind: "text", text: "First streamed reply", phase: "final_answer" });
          streaming.turns.at(-1).part_end++;
          await push({ ...streaming, turn: streaming.turns.at(-1) }, "turn");
          await page.getByText("First streamed reply", { exact: true }).waitFor();
          await assertStable("first reply");
          const native = structuredClone(streaming);
          native.updated_at = 4; native.status = "completed"; native.transcript.source = backend + "-native";
          Object.assign(native.turns.at(-1), { id: "history-turn", status: "completed", finished_at: Date.now() });
          await push(native);
          await assertStable("native history");
          await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
          assert.equal(await page.evaluate(() => window.replayedAnimations), 0, `${label}: no repeated arrival animation`);
          assert.deepEqual(errors, [], label);
          console.log(`${label}: message DOM, expanded state and arrival animation stayed stable`);
        } finally {
          release();
          await context.close();
        }
      }
    }
  }
} finally {
  if (browser) await browser.close();
  await new Promise(resolve => httpServer.close(resolve));
  for (const close of cleanup.reverse()) await close();
}
