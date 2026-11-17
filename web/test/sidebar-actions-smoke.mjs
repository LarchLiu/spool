// Production App with synthetic navigation; run through tools/run.sh.
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
const require = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__sidebar_actions__.cjs"));
const { chromium, webkit } = require("playwright");
const output = path.join(root, "build/artifacts/sidebar-actions");
const cleanup = [];
const server = await createTestViteServer({ after: fn => cleanup.push(fn) }, {
  configFile: false, root: path.join(root, "web"), plugins: [react()],
  server: { host: "127.0.0.1", port: 0 },
});
let browser;
try {
  await mkdir(output, { recursive: true });
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const [engine, type, launch] of [["chromium", chromium, { channel: "chrome" }], ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }]]) {
    browser = await type.launch({ ...launch, headless: true });
    for (const desktop of [false, true]) {
      const context = await browser.newContext({ viewport: { width: 1280, height: 820 }, serviceWorkers: "block" });
      try {
        const page = await context.newPage();
        page.setDefaultTimeout(8000);
        const errors = [], writes = [];
        page.on("pageerror", error => errors.push(String(error)));
        await page.addInitScript(({ desktop }) => {
          localStorage.setItem("spool.ui-preferences.v1", JSON.stringify({ language: "en", theme: desktop ? "dark" : "light" }));
          if (desktop) { window.spoolDesktop = true; window.mygo = { platform: "darwin" }; }
          window.WebSocket = class extends EventTarget { readyState = 0; send() {} close() { this.readyState = 3; } };
        }, { desktop });
        const chats = [
          { id: "selected", title: "Current conversation", backend: "codex", can_control: true },
          { id: "native", title: "Native conversation", backend: "claude", local_session: true, can_control: true },
          { id: "readonly", title: "Read only conversation", backend: "codex", local_session: true, can_control: false },
          { id: "delete", title: "Delete conversation", backend: "codex", can_control: true },
        ].map((chat, index) => ({ ...chat, project_id: "project", user_id: "owner", status: "completed", updated_at: 10 - index, turns: [], transcript: { parts: [] } }));
        const projects = [{ id: "project", name: "Project", can_create: true }];
        let sidebar = { revision: 1, pinned: [] }, renameFail = true, archiveFail = true;
        const navigation = () => ({ projects, chats: chats.filter(chat => !chat.removed), sidebar });
        await page.route("**/api/**", async route => {
          const request = route.request(), endpoint = new URL(request.url()).pathname;
          const json = (value, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
          if (request.method() === "POST") writes.push({ endpoint, body: request.postDataJSON() });
          if (endpoint === "/api/me") return json({ user: { id: "owner", nickname: "Owner" }, csrf: "fixture" });
          if (endpoint === "/api/version") return json({ version: "fixture" });
          if (endpoint === "/api/profiles") return json({ revision: 1, people: [{ id: "owner", nickname: "Owner" }] });
          if (endpoint === "/api/navigation") return json(navigation());
          if (endpoint === "/api/sidebar-preferences") {
            const change = request.postDataJSON();
            sidebar = { revision: sidebar.revision + 1, pinned: sidebar.pinned.filter(id => id !== change.chat_id) };
            if (change.pinned) sidebar.pinned.unshift(change.chat_id);
            return json(sidebar);
          }
          if (endpoint === "/api/agents") return json(["codex", "claude"].map(backend => ({ backend, enabled: true, installed: true, catalog: { models: [] } })));
          if (endpoint === "/api/permissions") return json({ backend: "codex", default: "ask", modes: [{ id: "ask", available: true }] });
          const chat = chats.find(chat => endpoint.startsWith(`/api/chats/${chat.id}`));
          if (endpoint.endsWith("/rename")) {
            if (renameFail) return json({ error: "Could not rename chat" }, 502);
            chat.title = request.postDataJSON().title;
            return json(chat);
          }
          if (endpoint.endsWith("/archive") || endpoint.endsWith("/delete")) {
            if (endpoint.endsWith("/archive") && archiveFail) return json({ error: "Could not archive chat" }, 502);
            chat.removed = true;
            return json(navigation());
          }
          if (endpoint.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n` });
          if (chat && endpoint === `/api/chats/${chat.id}`) return json(chat);
          if (endpoint.endsWith("/workspace")) return json({ path: "/work", kind: "main", available: true });
          return json({ path: ".", entries: [], truncated: false });
        });
        await page.goto(origin + "/?chat=selected");
        const title = page.locator(".chat-title-trigger strong");
        await title.waitFor();
        const draft = page.locator(".composer textarea");
        await draft.fill("Keep the current draft");
        const selectedURL = page.url();
        const row = (section, text) => page.locator(`.${section} .nav-chat`).filter({ has: page.locator(".nav-chat-title", { hasText: text }) });
        const native = () => row("recents", chats[1].title);
        const menu = page.getByRole("menu", { name: "Chat options" });
        const assertSelection = async () => {
          assert.equal(await title.textContent(), "Current conversation");
          assert.equal(await draft.inputValue(), "Keep the current draft");
          assert.equal(page.url(), selectedURL);
        };
        const waitWrite = async (endpoint, action) => {
          const response = page.waitForResponse(response => new URL(response.url()).pathname === endpoint && response.request().method() === "POST");
          await action(); await response;
        };
        await page.mouse.move(1000, 30);
        assert.equal(await page.locator(".nav-chat .lucide-monitor").count(), 0);
        assert.equal(await native().getByRole("button", { name: "Pin chat", exact: true }).isVisible(), false);
        await native().hover();
        assert.ok(await native().getByRole("button", { name: "Archive", exact: true }).isVisible());
        await page.screenshot({ path: path.join(output, `${engine}-${desktop ? "desktop" : "web"}-hover.png`) });
        const label = await native().locator(".nav-chat-title").boundingBox();
        await page.mouse.move(label.x + 3, label.y + label.height / 2);
        await page.mouse.down();
        await page.mouse.move(700, label.y + label.height / 2, { steps: 8 });
        await page.mouse.up();
        assert.equal(await page.evaluate(() => window.getSelection().toString()), "", "Dragging over a sidebar title cannot select its text");
        await assertSelection();
        await native().hover();
        await waitWrite("/api/sidebar-preferences", () => native().getByRole("button", { name: "Pin chat", exact: true }).click());
        await row("pinned", chats[1].title).waitFor();
        await assertSelection();
        await row("pinned", chats[1].title).hover();
        await waitWrite("/api/sidebar-preferences", () => row("pinned", chats[1].title).getByRole("button", { name: "Unpin chat", exact: true }).click());
        await row("pinned", chats[1].title).waitFor({ state: "detached" });
        await native().click({ button: "right" });
        assert.equal(await page.evaluate(() => window.getSelection().toString()), "");
        assert.deepEqual(await menu.getByRole("menuitem").allTextContents(), ["Rename", "Pin chat", "Archive", "Delete"]);
        assert.deepEqual(await menu.getByRole("separator").evaluate(el => [el.previousElementSibling.textContent, el.nextElementSibling.textContent]), ["Pin chat", "Archive"]);
        await page.screenshot({ path: path.join(output, `${engine}-${desktop ? "desktop" : "web"}-menu.png`) });
        await assertSelection();
        await page.keyboard.press("Escape");
        assert.ok(await native().locator(".nav-chat-open").evaluate(el => document.activeElement === el));
        await native().locator(".nav-chat-open").press("Shift+F10");
        await menu.waitFor();
        await page.keyboard.press("End");
        assert.ok(await menu.getByRole("menuitem", { name: "Delete", exact: true }).evaluate(el => document.activeElement === el));
        await page.keyboard.press("Escape");
        await native().locator(".nav-chat-open").press("Shift+F10");
        await menu.waitFor();
        const pin = await native().getByRole("button", { name: "Pin chat", exact: true }).boundingBox();
        await page.mouse.move(pin.x + pin.width / 2, pin.y + pin.height / 2);
        await page.mouse.down();
        assert.ok(await menu.isVisible(), "The backdrop remains until the full dismissal click is consumed");
        await page.mouse.up();
        await menu.waitFor({ state: "detached" });
        assert.equal(writes.length, 2, "Clicking an underlying pin only dismisses the menu");
        await assertSelection();
        await native().evaluate(el => el.dispatchEvent(new MouseEvent("contextmenu", { clientX: 1279, clientY: 819, bubbles: true, cancelable: true })));
        await menu.waitFor();
        const bounds = await menu.boundingBox();
        assert.ok(bounds.x >= 8 && bounds.y >= 8 && bounds.x + bounds.width <= 1272 && bounds.y + bounds.height <= 812);
        assert.ok(bounds.x > 900 && bounds.y > 600, JSON.stringify(bounds));
        const outside = await row("recents", chats[2].title).boundingBox();
        await page.mouse.click(outside.x + 80, outside.y + outside.height / 2);
        await menu.waitFor({ state: "detached" });
        await assertSelection();
        assert.equal(writes.length, 2, "The dismissal click cannot activate an underlying action");
        await row("recents", chats[2].title).click({ button: "right" });
        assert.deepEqual(await menu.getByRole("menuitem").allTextContents(), ["Pin chat"]);
        await waitWrite("/api/sidebar-preferences", () => menu.getByRole("menuitem").click());
        await row("pinned", chats[2].title).waitFor();
        await menu.waitFor({ state: "detached" });
        await page.mouse.move(900, 300);
        assert.equal(await row("recents", chats[2].title).getByRole("button", { name: "Unpin chat", exact: true }).isVisible(), false, "Pointer menu actions cannot leave hover icons stuck through restored focus");
        await row("recents", chats[2].title).locator(".nav-chat-open").focus();
        await page.keyboard.press("ArrowDown");
        assert.ok(await row("recents", chats[2].title).getByRole("button", { name: "Unpin chat", exact: true }).isVisible(), "Keyboard navigation keeps row actions available");
        await draft.click();
        await assertSelection();
        await native().click({ button: "right" });
        await menu.getByRole("menuitem", { name: "Rename", exact: true }).click();
        const rename = page.getByRole("dialog", { name: "Rename chat" });
        assert.equal(await rename.getByLabel("Chat title").inputValue(), chats[1].title);
        await rename.getByLabel("Chat title").fill("Renamed native conversation");
        await rename.getByRole("button", { name: "Rename", exact: true }).click();
        await rename.getByRole("alert").waitFor();
        renameFail = false;
        await rename.getByRole("button", { name: "Rename", exact: true }).click();
        await rename.waitFor({ state: "detached" });
        await native().waitFor();
        assert.equal(await page.locator(".nav-chat-title", { hasText: chats[1].title }).count(), 2);
        await assertSelection();
        await native().hover();
        await native().getByRole("button", { name: "Archive", exact: true }).click();
        await page.getByRole("alert").filter({ hasText: "Could not archive chat" }).waitFor();
        await assertSelection();
        archiveFail = false;
        await page.getByRole("button", { name: "Dismiss", exact: true }).click();
        await native().hover();
        await native().getByRole("button", { name: "Archive", exact: true }).click();
        await native().waitFor({ state: "detached" });
        await assertSelection();
        await row("projects", chats[3].title).click({ button: "right" });
        await menu.getByRole("menuitem", { name: "Delete", exact: true }).click();
        const deletion = page.getByRole("dialog", { name: "Delete chat" });
        await deletion.waitFor();
        assert.ok(!writes.some(write => write.endpoint.endsWith("/delete")));
        await deletion.getByRole("button", { name: "Cancel", exact: true }).last().click();
        assert.ok(!writes.some(write => write.endpoint.endsWith("/delete")));
        await row("projects", chats[3].title).click({ button: "right" });
        await menu.getByRole("menuitem", { name: "Delete", exact: true }).click();
        await deletion.getByRole("button", { name: "Delete", exact: true }).click();
        await row("recents", chats[3].title).waitFor({ state: "detached" });
        await assertSelection();
        assert.deepEqual(writes.map(write => write.endpoint), ["/api/sidebar-preferences", "/api/sidebar-preferences", "/api/sidebar-preferences", "/api/chats/native/rename", "/api/chats/native/rename", "/api/chats/native/archive", "/api/chats/native/archive", "/api/chats/delete/delete"]);
        assert.deepEqual(errors, []);
        console.log(`${engine} ${desktop ? "desktop" : "web"}: hover actions, context/keyboard menu, permissions, row targets, retries and draft preservation passed`);
      } finally { await context.close(); }
    }
    await browser.close(); browser = null;
  }
} finally {
  if (browser) await browser.close();
  for (const close of cleanup.reverse()) await close();
}
