// Production App, synthetic navigation, and an owned browser; no provider data.
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const require = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__sidebar__.cjs"));
const { chromium, webkit } = require("playwright");
const output = path.join(root, "build/artifacts/sidebar-pins");
const cleanup = [];
const server = await createTestViteServer({ after: fn => cleanup.push(fn) }, {
  configFile: false,
  root: path.join(root, "web"),
  plugins: [react()],
  server: { host: "127.0.0.1", port: 0 },
});
let browser;
const chats = [
  { id: "chat-one", title: "Review the sidebar navigation", backend: "codex", can_control: true, updated_at: 3 },
  { id: "chat-two", title: "Read only shared conversation", backend: "claude", can_control: false, updated_at: 2 },
  { id: "chat-three", title: "Local session", backend: "codex", local_session: true, updated_at: 1 },
].map(chat => ({ ...chat, project_id: "project", user_id: "alice", status: "completed", turns: [], transcript: { parts: [] } }));
const projects = [{ id: "project", name: "Workspace project", can_create: true }];
try {
  await mkdir(output, { recursive: true });
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const [engine, type, launch] of [["chromium", chromium, { channel: "chrome" }], ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }]]) {
    browser = await type.launch({ ...launch, headless: true });
    for (const [width, theme] of [[1280, "light"], [390, "dark"]]) {
      const context = await browser.newContext({ viewport: { width, height: 820 }, hasTouch: width < 500, serviceWorkers: "block" });
      try {
        const page = await context.newPage();
        page.setDefaultTimeout(10000);
        const errors = [], writes = [], stored = new Map();
        let owner = "alice";
        page.on("pageerror", error => errors.push(String(error)));
        const initialize = ({ theme, desktop }) => {
          localStorage.setItem("spool.ui-preferences.v1", JSON.stringify({ language: "en", theme }));
          if (desktop) { window.spoolDesktop = true; window.mygo = { platform: "darwin" }; }
          window.WebSocket = class extends EventTarget { constructor() { super(); window.fixtureSocket = this; } readyState = 0; send() {} close() { this.readyState = 3; } };
        };
        await page.addInitScript(initialize, { theme, desktop: width >= 500 });
        const routeAPI = async route => {
          const request = route.request();
          const endpoint = new URL(request.url()).pathname.replace(/^\/web\/[a-z2-7]{52}/, "");
          const scope = new URL(request.url()).pathname.slice(0, -endpoint.length) + ":" + owner;
          const preferences = () => structuredClone(stored.get(scope) || { revision: 0, pinned: [] });
          const json = value => route.fulfill({ contentType: "application/json", body: JSON.stringify(value) });
          if (request.method() !== "GET") writes.push(endpoint);
          if (endpoint === "/api/me") return json({ user: { id: owner, nickname: owner }, csrf: "fixture" });
          if (endpoint === "/api/version") return json({ version: "fixture" });
          if (endpoint === "/api/profiles") return json({ revision: 1, people: [{ id: owner, nickname: owner }] });
          if (endpoint === "/api/navigation") return json({ projects, chats, sidebar: preferences() });
          if (endpoint === "/api/sidebar-preferences") {
            const change = request.postDataJSON();
            let next = preferences();
            if (change.initial) { if (next.revision === 0) next = { ...change.initial, revision: 1 }; }
            else {
              next.revision++;
              assert.equal(change.section, undefined); assert.equal(change.collapsed, undefined);
              next.pinned = next.pinned.filter(id => id !== change.chat_id);
              if (change.pinned) next.pinned.unshift(change.chat_id);
            }
            stored.set(scope, next);
            return json(next);
          }
          if (endpoint === "/api/agents") return json(["codex", "claude"].map(backend => ({ backend, enabled: true, installed: true, catalog: { models: [] } })));
          if (endpoint === "/api/permissions") return json({ backend: "codex", default: "ask", modes: [{ id: "ask", available: true }] });
          const chat = chats.find(chat => endpoint.startsWith(`/api/chats/${chat.id}`));
          if (endpoint.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n` });
          if (chat && endpoint === `/api/chats/${chat.id}`) return json(chat);
          if (endpoint.endsWith("/workspace")) return json({ path: "/work", kind: "main", available: true });
          return json({ path: ".", entries: [], truncated: false });
        };
        await page.route("**/api/**", routeAPI);
        const sidebar = page.locator(".sidebar");
        const heading = section => sidebar.locator(`.${section} .nav-section-toggle`);
        const rows = section => sidebar.locator(`.${section} .nav-chat-open`);
        const titles = section => rows(section).locator(".nav-chat-title").allTextContents();
        const save = async action => {
          const response = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/api/sidebar-preferences") && response.request().method() === "POST");
          await action();
          const result = await (await response).json();
          const base = new URL(page.url()).pathname.match(/^\/web\/[a-z2-7]{52}/)?.[0] || "";
          await page.waitForFunction(({ key, revision }) => JSON.parse(localStorage.getItem(key) || "{}").revision >= revision,
            { key: base + "/spool.sidebar.v1:" + encodeURIComponent(owner), revision: result.revision });
        };
        const showSidebar = async () => {
          if (width < 500 && !(await sidebar.evaluate(el => el.classList.contains("open")))) await page.getByRole("button", { name: "Open sidebar", exact: true }).click();
          await heading("pinned").waitFor({ state: "visible" });
          if (width < 500) await page.waitForFunction(() => document.querySelector(".sidebar").getBoundingClientRect().left >= -1);
        };
        const hideSidebar = async () => {
          if (width < 500) await sidebar.getByRole("button", { name: "Close sidebar", exact: true }).click();
        };
        const titleMenu = async label => {
          await page.locator(".chat-title-trigger").click();
          assert.deepEqual(await page.getByRole("menuitem").allTextContents(), ["Rename", label, "Archive", "Delete"]);
          const separator = page.getByRole("menu").getByRole("separator");
          assert.deepEqual(await separator.evaluate(el => ({ before: el.previousElementSibling.textContent, after: el.nextElementSibling.textContent, height: el.getBoundingClientRect().height })), { before: label, after: "Archive", height: 1 });
          await page.screenshot({ path: path.join(output, `${engine}-${width}-${theme}-menu.png`) });
          await save(() => page.getByRole("menuitem", { name: label, exact: true }).click());
          await page.getByRole("menu").waitFor({ state: "detached" });
        };
        const load = async (base = "") => {
          await page.goto(origin + base + "/?chat=chat-one");
          await page.locator(".chat-title-trigger").waitFor();
        };
        await load(); await showSidebar();
        for (const section of ["pinned", "projects", "recents"]) {
          assert.equal(await heading(section).getAttribute("aria-expanded"), "true");
          await heading(section).click();
          assert.equal(await heading(section).getAttribute("aria-expanded"), "false");
          assert.equal(await sidebar.locator(`#${section}-content`).isVisible(), false);
          assert.equal(await heading(section).locator("svg").getAttribute("aria-hidden"), "true");
        }
        await load(); await showSidebar();
        for (const section of ["pinned", "projects", "recents"]) assert.equal(await heading(section).getAttribute("aria-expanded"), "false");
        await hideSidebar(); await titleMenu("Pin chat"); await showSidebar();
        assert.equal(await heading("pinned").getAttribute("aria-expanded"), "true");
        assert.equal(await heading("projects").getAttribute("aria-expanded"), "false");
        assert.equal(await rows("pinned").count(), 1);
        assert.equal(await rows("pinned").first().getAttribute("aria-current"), "page");
        assert.equal(await rows("recents").filter({ hasText: chats[0].title }).count(), 1);
        await heading("recents").click();
        await rows("recents").filter({ hasText: chats[1].title }).click();
        await page.waitForFunction(title => document.querySelector(".chat-title-trigger strong")?.textContent === title, chats[1].title);
        await page.locator(".chat-title-trigger").click();
        assert.deepEqual(await page.getByRole("menuitem").allTextContents(), ["Pin chat"]);
        await save(() => page.getByRole("menuitem", { name: "Pin chat", exact: true }).click());
        await showSidebar();
        assert.deepEqual(await titles("pinned"), [chats[1].title, chats[0].title]);
        const geometry = await heading("pinned").evaluate(el => {
          const row = el.getBoundingClientRect(), icon = el.querySelector("svg").getBoundingClientRect(), label = el.querySelector("span").getBoundingClientRect();
          return { gap: icon.left - label.right, inset: row.right - icon.right, overflow: document.documentElement.scrollWidth - innerWidth, height: row.height, itemHeight: document.querySelector(".pinned .nav-chat").getBoundingClientRect().height };
        });
        assert.ok(geometry.gap > 50 && geometry.inset >= 7 && geometry.inset <= 9 && geometry.overflow === 0, JSON.stringify(geometry));
        assert.equal(geometry.height, 31); assert.equal(geometry.height, geometry.itemHeight);
        await page.screenshot({ path: path.join(output, `${engine}-${width}-${theme}.png`) });
        await rows("pinned").filter({ hasText: chats[0].title }).click();
        await page.waitForFunction(title => document.querySelector(".chat-title-trigger strong")?.textContent === title, chats[0].title);
        await titleMenu("Unpin chat"); await showSidebar();
        assert.deepEqual(await titles("pinned"), [chats[1].title]);
        assert.equal(await rows("recents").filter({ hasText: chats[0].title }).count(), 1);
        await load(); await showSidebar();
        assert.deepEqual(await titles("pinned"), [chats[1].title]);
        owner = "bob"; await load(); await showSidebar();
        assert.equal(await rows("pinned").count(), 0);
        assert.equal(await heading("projects").getAttribute("aria-expanded"), "true");
        owner = "alice"; await load(); await showSidebar();
        assert.deepEqual(await titles("pinned"), [chats[1].title]);
        // A separate browser profile has no sidebar cache and restores from the
        // account's server record. Deliver the same navigation frames used by
        // the real WebSocket server (covered by the Go integration tests).
        const device = await browser.newContext({ viewport: { width: 1280, height: 820 }, serviceWorkers: "block" });
        try {
          const remote = await device.newPage();
          remote.on("pageerror", error => errors.push(String(error)));
          await remote.addInitScript(initialize, { theme, desktop: false });
          await remote.route("**/api/**", routeAPI);
          await remote.goto(origin + "/?chat=chat-one");
          const remoteRows = remote.locator(".pinned .nav-chat-title");
          await remoteRows.first().waitFor();
          assert.deepEqual(await remoteRows.allTextContents(), [chats[1].title]);
          assert.equal(await remote.locator(".projects .nav-section-toggle").getAttribute("aria-expanded"), "true");
          const push = () => page.evaluate(data => window.fixtureSocket.onmessage({ data: JSON.stringify({ type: "navigation", data }) }),
            { projects, chats, sidebar: stored.get(":alice") });
          await remote.locator(".chat-title-trigger").click();
          const pinned = remote.waitForResponse(response => response.url().endsWith("/api/sidebar-preferences"));
          await remote.getByRole("menuitem", { name: "Pin chat", exact: true }).click();
          await pinned; await push();
          await page.waitForFunction(() => document.querySelectorAll(".pinned .nav-chat-title").length === 2);
          assert.deepEqual(await titles("pinned"), [chats[0].title, chats[1].title]);
          await remote.locator(".projects .nav-section-toggle").click();
          await remote.locator(".projects .nav-section-toggle").click(); await push();
          assert.equal(await heading("projects").getAttribute("aria-expanded"), "false", "other devices retain their local sections");
          assert.equal(await remote.locator(".projects .nav-section-toggle").getAttribute("aria-expanded"), "true");
          await remote.locator(".chat-title-trigger").click();
          const unpinned = remote.waitForResponse(response => response.url().endsWith("/api/sidebar-preferences"));
          await remote.getByRole("menuitem", { name: "Unpin chat", exact: true }).click(); await unpinned; await push();
          await page.waitForFunction(() => document.querySelectorAll(".pinned .nav-chat-title").length === 1);
        } finally { await device.close(); }
        await page.evaluate(() => localStorage.removeItem("/spool.sidebar.v1:alice"));
        await load(); await showSidebar();
        assert.deepEqual(await titles("pinned"), [chats[1].title]);
        await load("/web/" + "a".repeat(52)); await showSidebar();
        assert.equal(await rows("pinned").count(), 0);
        await load(); await showSidebar();
        assert.deepEqual(await titles("pinned"), [chats[1].title]);
        assert.equal(writes.length, 5); assert.ok(writes.every(path => path === "/api/sidebar-preferences")); assert.deepEqual(errors, []);
        console.log(`${engine} ${width}px ${theme}: pin/unpin, sections, independent device restore, live navigation sync, cache removal and account/server isolation passed`);
      } finally { await context.close(); }
    }
    await browser.close(); browser = null;
  }
} finally {
  if (browser) await browser.close();
  for (const close of cleanup.reverse()) await close();
}
