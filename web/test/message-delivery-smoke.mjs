// Exercise the production App with synthetic chats and delayed HTTP acceptance.
// Run with: sh tools/run.sh message-delivery bun web/test/message-delivery-smoke.mjs
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const require = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__delivery__.cjs"));
const { chromium } = require("playwright");
const cleanup = [];
const server = await createTestViteServer({ after: fn => cleanup.push(fn) }, {
  configFile: false,
  root: path.join(root, "web"),
  plugins: [react()],
  server: { host: "127.0.0.1", port: 0 },
});
let browser;

function fixture(id, running = false) {
  return {
    id, project_id: "project", user_id: "owner", backend: "codex",
    title: id === "chat-A" ? "Chat A" : "Running B", can_control: true,
    status: running ? "running" : "ready", permission_mode: "ask", updated_at: 1,
    transcript: { parts: running ? [{ role: "user", kind: "text", text: "B is working" }] : [] },
    turns: running ? [{ id: "turn-B", status: "running", started_at: Date.now(), part_start: 0, input_end: 1, part_end: 1 }] : [],
  };
}

try {
  await server.listen();
  browser = await chromium.launch({ channel: "chrome", headless: true });
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const scenario of ["queue", "steer", "same-chat"]) {
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, serviceWorkers: "block" });
    let releaseFirst;
    const acceptance = new Promise(resolve => { releaseFirst = resolve; });
    try {
      const page = await context.newPage();
      const errors = [];
      const requests = [];
      page.on("pageerror", error => errors.push(String(error)));
      await page.addInitScript(() => {
        localStorage.setItem("spool.ui-preferences.v1", JSON.stringify({ language: "en", theme: "light" }));
        // Navigation uses the REST fallback. No live server or provider is used.
        window.WebSocket = class extends EventTarget {
          readyState = 0;
          send() {}
          close() { this.readyState = 3; }
        };
      });
      const a = fixture("chat-A"), b = fixture("chat-B", true);
      const accepted = {
        ...a, status: "running", updated_at: 2,
        transcript: { parts: [{ role: "user", kind: "text", text: "First in A" }] },
        turns: [{ id: "turn-A", status: "running", started_at: Date.now(), part_start: 0, input_end: 1, part_end: 1 }],
      };
      await page.route("**/api/**", async route => {
        const request = route.request(), url = new URL(request.url()), endpoint = url.pathname;
        const json = (value, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
        if (endpoint === "/api/me") return json({ user: { id: "owner", nickname: "Owner" }, csrf: "fixture" });
        if (endpoint === "/api/version") return json({ version: "fixture" });
        if (endpoint === "/api/profiles") return json({ revision: 1, people: [{ id: "owner", nickname: "Owner" }] });
        if (endpoint === "/api/navigation") return json({ projects: [{ id: "project", name: "Project", can_create: true }], chats: [a, b] });
        if (endpoint === "/api/agents") return json([{ backend: "codex", enabled: true, installed: true, catalog: { models: [] } }]);
        if (endpoint === "/api/permissions") return json({ backend: "codex", default: "ask", modes: [{ id: "ask", available: true }] });
        if (request.method() === "POST" && endpoint.endsWith("/messages")) {
          const payload = request.postDataJSON();
          requests.push({ endpoint, ...payload });
          if (!payload.delivery) {
            await acceptance;
            return json(accepted, 202);
          }
          return json(endpoint.includes("chat-B") ? b : accepted, 202);
        }
        if (endpoint.endsWith("/events")) {
          const chat = endpoint.includes("chat-B") ? b : a;
          return route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n` });
        }
        if (endpoint === "/api/chats/chat-A") return json(a);
        if (endpoint === "/api/chats/chat-B") return json(b);
        if (endpoint.endsWith("/workspace")) return json({ path: "/work", kind: "main", available: true });
        return json({ path: ".", entries: [], truncated: false });
      });
      await page.goto(origin + "/?chat=chat-A");
      const composer = page.locator("#chat-message");
      await composer.fill("First in A");
      const firstPosted = page.waitForRequest(request => request.url().includes("/chat-A/messages"));
      await page.getByRole("button", { name: "Send message", exact: true }).click();
      await firstPosted;
      if (scenario !== "same-chat") {
        await page.locator(".nav-chat").filter({ hasText: "Running B" }).first().click();
        await page.getByText("B is working", { exact: true }).waitFor();
      }
      const followup = `Follow-up for ${scenario === "same-chat" ? "A" : "B"}`;
      await composer.fill(followup);
      const delivery = scenario === "steer" ? "steer" : "queue";
      const posted = page.waitForRequest(request => request.url().includes("/messages") && request.postDataJSON().delivery === delivery, { timeout: 5000 });
      const answered = page.waitForResponse(response => response.url().includes("/messages") && response.request().postDataJSON().delivery === delivery, { timeout: 5000 });
      await composer.press(scenario === "steer" ? "Control+Enter" : "Enter");
      if (scenario === "same-chat") {
        await page.waitForFunction(() => document.querySelector("#chat-message").value === "");
        assert.equal(requests.length, 1, "same-chat follow-up must wait for the first acceptance");
        releaseFirst();
      }
      const request = await posted;
      await answered;
      const target = scenario === "same-chat" ? "chat-A" : "chat-B";
      assert.equal(new URL(request.url()).pathname, `/api/chats/${target}/messages`);
      assert.equal(request.postDataJSON().message, followup);
      assert.equal(requests.length, 2);
      releaseFirst();
      assert.deepEqual(errors, []);
      console.log(`${scenario}: follow-up targeted ${target}`);
    } finally {
      releaseFirst();
      await context.close();
    }
  }
} finally {
  if (browser) await browser.close();
  for (const close of cleanup.reverse()) await close();
}
