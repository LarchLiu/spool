// Fault injection into the production App and Markdown renderer, without
// production-only test hooks. Run through tools/run.sh with Playwright available.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = fileURLToPath(new URL("../../", import.meta.url));
const require = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__render_recovery__.cjs"));
const { chromium, webkit } = require("playwright");
const output = path.join(root, "build/artifacts/render-recovery");
const cleanup = [], reports = [];
const server = await createTestViteServer({ after: fn => cleanup.push(fn) }, {
  configFile: false,
  root: path.join(root, "web"),
  plugins: [{
    name: "inject-render-failures",
    enforce: "pre",
    async resolveId(source, importer) {
      if (source !== "react-markdown") return;
      const actual = await this.resolve(source, importer, { skipSelf: true });
      return { id: "\0faultable-markdown:" + actual.id };
    },
    load(id) {
      if (!id.startsWith("\0faultable-markdown:")) return;
      const actual = id.slice("\0faultable-markdown:".length);
      return `import Markdown from ${JSON.stringify(actual)};
export * from ${JSON.stringify(actual)};
import {createElement, useEffect, useState} from 'react';
export default function FaultableMarkdown(props) {
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const fail = () => setFailed(true);
    window.addEventListener('fixture:markdown-failure', fail);
    return () => window.removeEventListener('fixture:markdown-failure', fail);
  }, []);
  if (failed && props.children.includes('Faultable response')) throw new TypeError('fixture rich text failure');
  return createElement(Markdown, props);
}`;
    },
    transform(code, id) {
      if (!id.endsWith("/src/main.tsx")) return;
      return code.replace("function App() {", `function App() {
  const [fixtureFailed, setFixtureFailed] = useState(false);
  useEffect(() => {
    const fail = () => setFixtureFailed(true);
    window.addEventListener('fixture:application-failure', fail);
    return () => window.removeEventListener('fixture:application-failure', fail);
  }, []);
  if (fixtureFailed || globalThis.fixtureStartupFailure) throw new Error('fixture application failure');`);
    },
  }, react()],
  server: { host: "127.0.0.1", port: 0, hmr: false },
});
let browser;
try {
  await mkdir(output, { recursive: true });
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const engine of ["chromium", "webkit"]) {
    browser = await (engine === "chromium" ? chromium : webkit).launch({ ...(engine === "chromium" ? { channel: "chrome" } : {}), headless: true });
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: "block" });
    try {
      const page = await context.newPage();
      page.setDefaultTimeout(10000);
      const uncaught = [], caught = [];
      page.on("pageerror", error => uncaught.push(String(error)));
      page.on("console", message => {
        if (message.type() === "error" && message.text().includes("Spool rendering failed")) caught.push(message.text());
      });
      await page.addInitScript(() => {
        localStorage.setItem("spool.ui-preferences.v1", JSON.stringify({ language: "en", theme: "light" }));
        window.fixtureSockets = [];
        window.WebSocket = class extends EventTarget {
          readyState = 0;
          constructor() { super(); window.fixtureSockets.push(this); }
          send() {
            if (window.fixtureSendFails) throw new DOMException("Relay disconnected", "InvalidStateError");
          }
          close() { this.readyState = 3; }
        };
        window.fixtureDocument = document;
        const Source = window.EventSource;
        window.fixtureSources = [];
        window.EventSource = class extends Source {
          constructor(...args) { super(...args); window.fixtureSources.push(this); }
        };
      });
      let interrupted = false;
      const chat = {
        id: "chat-12345678", title: "Recovery conversation", project_id: "project", user_id: "owner",
        backend: "codex", status: "running", updated_at: 1,
        turns: [{ id: "turn", status: "running", part_start: 0, input_end: 1, part_end: 2 }],
        transcript: { parts: [{ role: "user", kind: "text", text: "Earlier request" }, { role: "assistant", kind: "text", text: "Faultable response **with formatting**" }] },
      };
      await page.route("**/api/**", route => {
        const endpoint = new URL(route.request().url()).pathname;
        const json = value => route.fulfill({ contentType: "application/json", body: JSON.stringify(value) });
        if (endpoint === "/api/me") return json({ user: { id: "owner", nickname: "Owner" }, csrf: "fixture" });
        if (endpoint === "/api/version") return json({ version: "fixture" });
        if (endpoint === "/api/profiles") return json({ revision: 1, people: [{ id: "owner", nickname: "Owner" }] });
        if (endpoint === "/api/navigation") return json({ projects: [{ id: "project", name: "Project" }], chats: [chat] });
        if (endpoint === "/api/agents") return json([{ backend: "codex", enabled: true, installed: true, catalog: { models: [] } }]);
        if (endpoint === "/api/permissions") return json({ backend: "codex", default: "ask", modes: [{ id: "ask", available: true }] });
        if (endpoint.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n` });
        if (endpoint.endsWith("/workspace")) return json({ path: "/repo", kind: "main", available: true, workspaces: [] });
        if (endpoint.endsWith("/files")) return json({ path: "", entries: [], truncated: false });
        if (endpoint === "/api/chats/" + chat.id) {
          if (interrupted) return route.fulfill({ status: 200, contentType: "application/json", body: '{"id":"chat-12345678","turns":[' });
          return json(chat);
        }
        return json([]);
      });
      await page.goto(origin);
      await page.getByText("Faultable response with formatting", { exact: true }).waitFor();
      await page.evaluate(() => {
        const socket = window.fixtureSockets.find(socket => typeof socket.onopen === 'function');
        window.fixtureWorkspaceSocket = socket;
        socket.readyState = 1;
        socket.onopen();
        socket.onmessage({ data: JSON.stringify({ type: "ready", data: true }) });
        window.fixtureSendFails = true;
      });
      // Changing chatID calls stream.view() from a real React effect. A socket
      // that fails here must reconnect without unmounting the workspace.
      await page.getByRole("button", { name: "New chat in Project", exact: true }).click();
      await page.locator(".welcome").waitFor();
      assert.equal(await page.locator(".application-error").count(), 0);
      assert.equal(await page.evaluate(() => window.fixtureWorkspaceSocket.readyState), 3);
      await page.evaluate(() => { window.fixtureSendFails = false; });
      await page.locator(".nav-chat-open").filter({ hasText: chat.title }).first().click();
      await page.getByText("Faultable response with formatting", { exact: true }).waitFor();
      const composer = page.locator(".composer textarea");
      await composer.fill("Keep my unsent draft");
      await composer.evaluate(element => { window.fixtureComposer = element; });
      // Reproduce a relay failing after the upstream's 200 headers reached the
      // desktop. Running-turn polling must retain the last valid conversation.
      interrupted = true;
      for (let attempt = 0; attempt < 3; attempt++) {
        await page.waitForResponse(response => new URL(response.url()).pathname === "/api/chats/" + chat.id);
      }
      assert.equal(await page.getByText("Faultable response with formatting", { exact: true }).isVisible(), true);
      assert.equal(await composer.inputValue(), "Keep my unsent draft");
      assert.equal(await page.locator(".application-error").count(), 0, "truncated relay JSON cannot reach the render boundary");
      interrupted = false;
      chat.status = chat.turns[0].status = "completed";
      chat.updated_at++;
      await page.waitForResponse(response => new URL(response.url()).pathname === "/api/chats/" + chat.id);
      await page.evaluate(() => {
        const source = window.fixtureSources.find(source => source.url.includes('/api/chats/'));
        source.dispatchEvent(new MessageEvent('snapshot', { data: '{"id":' }));
      });
      assert.equal(await composer.inputValue(), "Keep my unsent draft");
      assert.equal(await page.locator(".application-error").count(), 0);
      await page.evaluate(chat => {
        const source = window.fixtureSources.find(source => source.url.includes('/api/chats/'));
        source.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify(chat) }));
      }, chat);
      await page.evaluate(() => window.dispatchEvent(new Event("fixture:markdown-failure")));
      await page.locator(".markdown-fallback").waitFor();
      assert.equal(await page.locator(".markdown-fallback").textContent(), chat.transcript.parts[1].text);
      assert.equal(await composer.inputValue(), "Keep my unsent draft");
      assert.equal(await composer.evaluate(element => element === window.fixtureComposer), true, "rich text recovery preserves the composer DOM");
      assert.equal(await page.locator(".sidebar").isVisible(), true);
      assert.equal(await page.locator(".application-error").count(), 0);

      await page.evaluate(() => window.dispatchEvent(new Event("fixture:application-failure")));
      await page.getByRole("heading", { name: "The page could not be displayed.", exact: true }).waitFor();
      await page.getByRole("button", { name: "Retry", exact: true }).click();
      await composer.waitFor();
      assert.equal(await page.evaluate(() => document === window.fixtureDocument), true, "Retry restores the app without reloading the document");

      await page.evaluate(() => {
        document.documentElement.lang = "zh-CN";
        window.fixtureStartupFailure = true;
        window.dispatchEvent(new Event("fixture:application-failure"));
      });
      await page.getByRole("heading", { name: "页面暂时无法显示。", exact: true }).waitFor();
      await page.getByRole("button", { name: "重试", exact: true }).click();
      await page.getByRole("heading", { name: "页面暂时无法显示。", exact: true }).waitFor();
      await page.screenshot({ path: path.join(output, `${engine}-recovery.png`) });
      await page.evaluate(() => { window.fixtureStartupFailure = false; });
      await page.getByRole("button", { name: "重试", exact: true }).click();
      await composer.waitFor();
      assert.equal(await page.evaluate(() => document === window.fixtureDocument), true);
      assert.deepEqual(uncaught, [], "render failures must be contained");
      assert.ok(caught.length >= 4, "caught failures retain console diagnostics");
      reports.push({ engine, socketSendFailureContained: true, truncatedRelayResponsesRetainChat: true, malformedEventRetainsChat: true, richTextFallback: true, draftPreserved: true, retryWithoutReload: true, repeatedFailureContained: true, localizedRecovery: true });
      console.log(`${engine}: rich text failure retained draft; application failure recovered without reload`);
    } finally {
      await context.close();
      await browser.close();
      browser = undefined;
    }
  }
} finally {
  await writeFile(path.join(output, "browser-report.json"), JSON.stringify(reports, null, 2) + "\n");
  await browser?.close();
  for (const close of cleanup.reverse()) await close();
}
