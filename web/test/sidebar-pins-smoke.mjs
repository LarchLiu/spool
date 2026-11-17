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
        const errors = [], writes = [];
        let owner = "alice";
        page.on("pageerror", error => errors.push(String(error)));
        await page.addInitScript(({ theme, desktop }) => {
          localStorage.setItem("spool.ui-preferences.v1", JSON.stringify({ language: "en", theme }));
          if (desktop) { window.spoolDesktop = true; window.mygo = { platform: "darwin" }; }
          window.WebSocket = class extends EventTarget { readyState = 0; send() {} close() { this.readyState = 3; } };
        }, { theme, desktop: width >= 500 });
        await page.route("**/api/**", async route => {
          const request = route.request();
          const endpoint = new URL(request.url()).pathname.replace(/^\/web\/[a-z2-7]{52}/, "");
          const json = value => route.fulfill({ contentType: "application/json", body: JSON.stringify(value) });
          if (request.method() !== "GET") writes.push(endpoint);
          if (endpoint === "/api/me") return json({ user: { id: owner, nickname: owner }, csrf: "fixture" });
          if (endpoint === "/api/version") return json({ version: "fixture" });
          if (endpoint === "/api/profiles") return json({ revision: 1, people: [{ id: owner, nickname: owner }] });
          if (endpoint === "/api/navigation") return json({ projects, chats });
          if (endpoint === "/api/agents") return json(["codex", "claude"].map(backend => ({ backend, enabled: true, installed: true, catalog: { models: [] } })));
          if (endpoint === "/api/permissions") return json({ backend: "codex", default: "ask", modes: [{ id: "ask", available: true }] });
          const chat = chats.find(chat => endpoint.startsWith(`/api/chats/${chat.id}`));
          if (endpoint.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n` });
          if (chat && endpoint === `/api/chats/${chat.id}`) return json(chat);
          if (endpoint.endsWith("/workspace")) return json({ path: "/work", kind: "main", available: true });
          return json({ path: ".", entries: [], truncated: false });
        });
        const sidebar = page.locator(".sidebar");
        const heading = section => sidebar.locator(`.${section} .nav-section-toggle`);
        const rows = section => sidebar.locator(`.${section} .nav-chat-open`);
        const titles = section => rows(section).locator(".nav-chat-title").allTextContents();
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
          await page.getByRole("menuitem", { name: label, exact: true }).click();
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
        await page.getByRole("menuitem", { name: "Pin chat", exact: true }).click();
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
        await load("/web/" + "a".repeat(52)); await showSidebar();
        assert.equal(await rows("pinned").count(), 0);
        await load(); await showSidebar();
        assert.deepEqual(await titles("pinned"), [chats[1].title]);
        assert.deepEqual(writes, []); assert.deepEqual(errors, []);
        console.log(`${engine} ${width}px ${theme}: pin/unpin, independent sections, navigation, reload, read-only chat and account/server isolation passed`);
      } finally { await context.close(); }
    }
    await browser.close(); browser = null;
  }
} finally {
  if (browser) await browser.close();
  for (const close of cleanup.reverse()) await close();
}
