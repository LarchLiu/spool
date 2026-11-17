// Run through tools/run.sh with SPOOL_NODE_MODULES pointing at Playwright.
// Exercises the production App using synthetic sessions and intercepted APIs.
// WEBKIT_BIN may select an already-installed WebKit pw_run.sh.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const require = createRequire(
  path.join(
    process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"),
    "__rename__.cjs",
  ),
);
const { chromium, webkit } = require("playwright");
const output = path.join(root, "build/artifacts/chat-rename");
const cleanup = [],
  reports = [];
const server = await createTestViteServer(
  { after: (fn) => cleanup.push(fn) },
  {
    configFile: false,
    root: path.join(root, "web"),
    plugins: [react()],
    server: { host: "127.0.0.1", port: 0 },
  },
);
let browser;
const fixture = (id, backend, title, writable = true) => ({
  id,
  backend,
  title,
  project_id: "project",
  user_id: "owner",
  can_control: writable,
  status: "completed",
  updated_at: 1,
  turns: [
    {
      id: "turn",
      status: "completed",
      part_start: 0,
      input_end: 1,
      part_end: 2,
    },
  ],
  transcript: {
    parts: [
      { role: "user", kind: "text", text: "Original prompt" },
      { role: "assistant", kind: "text", text: "Original response" },
    ],
  },
});
try {
  await mkdir(output, { recursive: true });
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const [engine, type, launch] of [
    ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
    ["chromium", chromium, { channel: "chrome" }],
  ]) {
    browser = await type.launch({ ...launch, headless: true });
    try {
      for (const [backend, width, theme] of [
        ["codex", 1280, "light"],
        ["claude", 390, "light"],
        ["codex", 390, "dark"],
        ["claude", 1280, "dark"],
      ]) {
        const context = await browser.newContext({
          viewport: { width, height: 820 },
          hasTouch: width < 500,
          serviceWorkers: "block",
        });
        const page = await context.newPage();
        page.setDefaultTimeout(8000);
        const errors = [],
          requests = [],
          archiveRequests = [],
          deleteRequests = [];
        page.on("pageerror", (error) => errors.push(String(error)));
        const chats = [
          fixture("rename-chat", backend, "查询 Codex 和 Claude 原生会话支持"),
          fixture("other-chat", backend, "Other conversation"),
          fixture("readonly-chat", backend, "Read only conversation", false),
          fixture("delete-chat", backend, "Delete this conversation"),
          fixture("delete-next", backend, "Delete next conversation"),
        ];
        let fail = false,
          hold = false,
          release,
          archiveFail = true,
          archiveHold = false,
          releaseArchive,
          deleteFail = true,
          deleteHold = false,
          releaseDelete;
        try {
          await page.addInitScript(
            ({ theme, desktop }) => {
              localStorage.setItem(
                "spool.ui-preferences.v1",
                JSON.stringify({ language: "en", theme }),
              );
              if (desktop) {
                window.spoolDesktop = true;
                window.mygo = { platform: "darwin" };
              }
              window.WebSocket = class extends EventTarget {
                readyState = 0;
                send() {}
                close() {
                  this.readyState = 3;
                }
              };
            },
            { theme, desktop: width >= 500 },
          );
          await page.route("**/api/**", async (route) => {
            const request = route.request(),
              endpoint = new URL(request.url()).pathname;
            const json = (value, status = 200) =>
              route.fulfill({
                status,
                contentType: "application/json",
                body: JSON.stringify(value),
              });
            if (endpoint === "/api/me")
              return json({
                user: { id: "owner", nickname: "Owner" },
                csrf: "fixture",
              });
            if (endpoint === "/api/version")
              return json({ version: "fixture" });
            if (endpoint === "/api/profiles")
              return json({
                revision: 1,
                people: [{ id: "owner", nickname: "Owner" }],
              });
            if (endpoint === "/api/navigation")
              return json({
                projects: [
                  { id: "project", name: "Project", can_create: true },
                ],
                chats: chats.filter((chat) => !chat.archived && !chat.deleted),
              });
            if (endpoint === "/api/agents")
              return json([
                {
                  backend,
                  enabled: true,
                  installed: true,
                  catalog: { models: [] },
                },
              ]);
            if (endpoint === "/api/permissions")
              return json({
                backend,
                default: "ask",
                modes: [{ id: "ask", available: true }],
              });
            const chat = chats.find((chat) =>
              endpoint.startsWith(`/api/chats/${chat.id}`),
            );
            if (endpoint.endsWith("/rename")) {
              requests.push({ endpoint, ...request.postDataJSON() });
              if (hold)
                await new Promise((resolve) => {
                  release = resolve;
                });
              if (fail) return json({ error: "Could not rename chat" }, 502);
              chat.title = request.postDataJSON().title.trim();
              return json({
                id: chat.id,
                title: chat.title,
                updated_at: chat.updated_at,
              });
            }
            if (endpoint.endsWith("/archive")) {
              archiveRequests.push(endpoint);
              if (archiveHold)
                await new Promise((resolve) => {
                  releaseArchive = resolve;
                });
              if (archiveFail)
                return json({ error: "Could not archive chat" }, 502);
              chat.archived = true;
              return json({
                projects: [
                  { id: "project", name: "Project", can_create: true },
                ],
                chats: chats.filter((item) => !item.archived && !item.deleted),
              });
            }
            if (endpoint.endsWith("/delete")) {
              deleteRequests.push(endpoint);
              if (deleteHold)
                await new Promise((resolve) => {
                  releaseDelete = resolve;
                });
              if (deleteFail)
                return json({ error: "Could not delete chat" }, 502);
              chat.deleted = true;
              if (chat.id === "delete-chat")
                chats[4].title = "Refreshed native title";
              return json({
                projects: [
                  { id: "project", name: "Project", can_create: true },
                ],
                chats: chats.filter((item) => !item.archived && !item.deleted),
              });
            }
            if (endpoint.endsWith("/events"))
              return route.fulfill({
                contentType: "text/event-stream",
                body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n`,
              });
            if (chat && endpoint === `/api/chats/${chat.id}`) return json(chat);
            if (endpoint.endsWith("/workspace"))
              return json({ path: "/work", kind: "main", available: true });
            return json({ path: ".", entries: [], truncated: false });
          });
          await page.goto(origin + "/?chat=rename-chat");
          const trigger = page.locator(".chat-title-trigger"),
            menu = page.getByRole("menu", { name: "Chat options" });
          const click = (locator) =>
            width < 500 ? locator.tap() : locator.click();
          const openRename = async () => {
            await click(trigger);
            await click(
              page.getByRole("menuitem", { name: "Rename", exact: true }),
            );
          };
          await trigger.click();
          await menu.waitFor();
          const bounds = await menu.boundingBox();
          assert.ok(
            bounds.x >= 0 && bounds.x + bounds.width <= width,
            "menu extends beyond viewport",
          );
          await page.screenshot({
            path: path.join(
              output,
              `${engine}-${backend}-${width}-${theme}-menu.png`,
            ),
          });
          await page.keyboard.press("Escape");
          await menu.waitFor({ state: "detached" });
          assert.ok(
            await trigger.evaluate(
              (element) => element === document.activeElement,
            ),
          );
          await trigger.press("ArrowDown");
          await menu.waitFor();
          assert.ok(
            await page
              .getByRole("menuitem", { name: "Rename" })
              .evaluate((element) => element === document.activeElement),
          );
          await page.keyboard.press("Tab");
          await menu.waitFor({ state: "detached" });
          await trigger.press("ArrowDown");
          await menu.waitFor();
          await page.locator(".history").click({ position: { x: 5, y: 5 } });
          await menu.waitFor({ state: "detached" });
          await openRename();
          const dialog = page.getByRole("dialog", { name: "Rename chat" }),
            input = dialog.getByLabel("Chat title");
          await dialog.waitFor();
          assert.equal(await input.inputValue(), chats[0].title);
          assert.ok(
            await input.evaluate(
              (element) => element === document.activeElement,
            ),
          );
          assert.equal(
            await input.evaluate(
              (element) => element.selectionEnd - element.selectionStart,
            ),
            chats[0].title.length,
          );
          await input.fill("   ");
          assert.ok(
            await dialog
              .getByRole("button", { name: "Rename", exact: true })
              .isDisabled(),
          );
          await input.fill("输入法确认");
          await input.dispatchEvent("keydown", {
            key: "Enter",
            isComposing: true,
            bubbles: true,
          });
          assert.equal(requests.length, 0);
          await page.keyboard.press("Escape");
          await dialog.waitFor({ state: "detached" });
          assert.equal(requests.length, 0);

          await trigger.press("ArrowDown");
          await page
            .getByRole("menuitem", { name: "Rename", exact: true })
            .press("Enter");
          await dialog.waitFor();
          await dialog
            .getByRole("button", { name: "Cancel", exact: true })
            .last()
            .click();
          await dialog.waitFor({ state: "detached" });

          fail = true;
          await openRename();
          await input.fill("Renamed conversation");
          await input.press("Enter");
          await dialog.getByRole("alert").waitFor();
          assert.equal(await input.inputValue(), "Renamed conversation");
          assert.ok(
            !(await trigger.textContent()).includes("Renamed conversation"),
          );
          fail = false;
          await page.screenshot({
            path: path.join(
              output,
              `${engine}-${backend}-${width}-${theme}-dialog.png`,
            ),
          });
          await dialog
            .getByRole("button", { name: "Rename", exact: true })
            .click();
          await dialog.waitFor({ state: "detached" });
          await page.waitForFunction(
            () =>
              document.querySelector(".chat-title-trigger strong")
                ?.textContent === "Renamed conversation",
          );
          assert.equal(
            await page
              .locator(".nav-chat-title")
              .filter({ hasText: "Renamed conversation" })
              .count(),
            2,
            "project and recents entries both update",
          );
          assert.equal(
            await page.getByText("Original response", { exact: true }).count(),
            1,
          );

          // A slow response must update its own sidebar entry after navigation.
          hold = true;
          await openRename();
          await input.fill("Delayed rename");
          await dialog
            .getByRole("button", { name: "Rename", exact: true })
            .click();
          assert.ok(
            await dialog.getByRole("button", { name: "Saving…" }).isDisabled(),
          );
          await page
            .locator(".nav-chat-open")
            .filter({ hasText: "Other conversation" })
            .first()
            .evaluate((element) => element.click());
          await page.waitForFunction(
            () =>
              document.querySelector(".chat-title-trigger strong")
                ?.textContent === "Other conversation",
          );
          await page.waitForFunction(
            () => !document.querySelector(".rename-chat-dialog"),
          );
          release();
          await page
            .locator(".nav-chat-title")
            .filter({ hasText: "Delayed rename" })
            .first()
            .waitFor({ state: "attached" });
          assert.equal(
            await trigger.locator("strong").textContent(),
            "Other conversation",
          );

          // Confirm deletion, preserve failures, then apply the returned native list.
          await page
            .locator(".nav-chat-open")
            .filter({ hasText: "Delete this conversation" })
            .first()
            .evaluate((element) => element.click());
          await page.waitForFunction(
            () =>
              document.querySelector(".chat-title-trigger strong")
                ?.textContent === "Delete this conversation",
          );
          const openDelete = async () => {
            await click(trigger);
            await page.keyboard.press("End");
            const item = page.getByRole("menuitem", {
              name: "Delete",
              exact: true,
            });
            assert.ok(
              await item.evaluate(
                (element) => element === document.activeElement,
              ),
            );
            await click(item);
          };
          const deletion = page.getByRole("dialog", { name: "Delete chat" });
          await openDelete();
          await deletion.waitFor();
          for (const button of await deletion
            .locator(".rename-chat-actions button")
            .all()) {
            const bounds = await button.boundingBox();
            assert.ok(
              bounds.width >= 44 && bounds.height >= 36,
              "delete dialog action is too small to use",
            );
          }
          assert.ok(
            await deletion
              .getByRole("button", { name: "Cancel", exact: true })
              .last()
              .evaluate((element) => element === document.activeElement),
          );
          await page.keyboard.press("Escape");
          await deletion.waitFor({ state: "detached" });
          assert.equal(deleteRequests.length, 0);
          await openDelete();
          await deletion
            .getByRole("button", { name: "Cancel", exact: true })
            .last()
            .click();
          await deletion.waitFor({ state: "detached" });
          assert.equal(deleteRequests.length, 0);
          await openDelete();
          await deletion
            .getByRole("button", { name: "Delete", exact: true })
            .click();
          await page
            .getByRole("alert")
            .filter({ hasText: "Could not delete chat" })
            .waitFor();
          assert.equal(
            await page
              .locator(".nav-chat-title")
              .filter({ hasText: "Delete this conversation" })
              .count(),
            2,
          );
          deleteFail = false;
          deleteHold = true;
          await page
            .getByRole("button", { name: "Dismiss", exact: true })
            .click();
          await openDelete();
          await deletion
            .getByRole("button", { name: "Delete", exact: true })
            .click();
          await deletion
            .getByRole("button", { name: "Deleting…", exact: true })
            .waitFor();
          assert.ok(
            await deletion
              .getByRole("button", { name: "Deleting…", exact: true })
              .isDisabled(),
          );
          assert.ok(
            await deletion
              .getByRole("button", { name: "Cancel", exact: true })
              .last()
              .isDisabled(),
          );
          await page.keyboard.press("Escape");
          assert.ok(await deletion.isVisible());
          await page
            .locator(".nav-chat-open")
            .filter({ hasText: "Other conversation" })
            .first()
            .evaluate((element) => element.click());
          await page.waitForFunction(
            () =>
              document.querySelector(".chat-title-trigger strong")
                ?.textContent === "Other conversation",
          );
          releaseDelete();
          await page
            .locator(".nav-chat-title")
            .filter({ hasText: "Delete this conversation" })
            .first()
            .waitFor({ state: "detached" });
          assert.equal(
            await trigger.locator("strong").textContent(),
            "Other conversation",
          );
          await page
            .locator(".nav-chat-title")
            .filter({ hasText: "Refreshed native title" })
            .first()
            .waitFor({ state: "attached" });
          deleteHold = false;
          await page
            .locator(".nav-chat-open")
            .filter({ hasText: "Refreshed native title" })
            .first()
            .evaluate((element) => element.click());
          await page.waitForFunction(
            () =>
              document.querySelector(".chat-title-trigger strong")
                ?.textContent === "Refreshed native title",
          );
          await openDelete();
          await page.screenshot({
            path: path.join(
              output,
              `${engine}-${backend}-${width}-${theme}-delete.png`,
            ),
          });
          await deletion
            .getByRole("button", { name: "Delete", exact: true })
            .click();
          await trigger.waitFor({ state: "detached" });
          assert.equal(
            await page
              .locator(".nav-chat-title")
              .filter({ hasText: "Refreshed native title" })
              .count(),
            0,
          );
          assert.ok(!new URL(page.url()).searchParams.has("chat"));
          await page
            .locator(".nav-chat-open")
            .filter({ hasText: "Other conversation" })
            .first()
            .evaluate((element) => element.click());
          await page.waitForFunction(
            () =>
              document.querySelector(".chat-title-trigger strong")
                ?.textContent === "Other conversation",
          );

          // Native archive failure remains visible; retry applies its refreshed list.
          await click(trigger);
          await page.keyboard.press("Home");
          await page.keyboard.press("ArrowDown");
          await page.keyboard.press("ArrowDown");
          const archive = page.getByRole("menuitem", {
            name: "Archive",
            exact: true,
          });
          assert.ok(
            await archive.evaluate(
              (element) => element === document.activeElement,
            ),
          );
          await click(archive);
          await page
            .getByRole("alert")
            .filter({ hasText: "Could not archive chat" })
            .waitFor();
          assert.equal(
            await trigger.locator("strong").textContent(),
            "Other conversation",
          );
          assert.equal(
            await page
              .locator(".nav-chat-title")
              .filter({ hasText: "Other conversation" })
              .count(),
            2,
          );
          await page
            .getByRole("button", { name: "Dismiss", exact: true })
            .click();
          archiveFail = false;
          archiveHold = true;
          await click(trigger);
          await click(archive);
          await page.getByRole("menuitem", { name: "Archiving…" }).waitFor();
          assert.ok(await trigger.isDisabled());
          assert.equal(
            await page
              .locator(".nav-chat-title")
              .filter({ hasText: "Other conversation" })
              .count(),
            2,
          );
          await page
            .locator(".nav-chat-open")
            .filter({ hasText: "Delayed rename" })
            .first()
            .evaluate((element) => element.click());
          await page.waitForFunction(
            () =>
              document.querySelector(".chat-title-trigger strong")
                ?.textContent === "Delayed rename",
          );
          releaseArchive();
          await page
            .locator(".nav-chat-title")
            .filter({ hasText: "Other conversation" })
            .first()
            .waitFor({ state: "detached" });
          assert.equal(
            await trigger.locator("strong").textContent(),
            "Delayed rename",
          );
          archiveHold = false;
          await click(trigger);
          await click(archive);
          await trigger.waitFor({ state: "detached" });
          assert.equal(
            await page
              .locator(".nav-chat-title")
              .filter({ hasText: "Delayed rename" })
              .count(),
            0,
          );
          assert.ok(!new URL(page.url()).searchParams.has("chat"));
          await page.reload();
          await page
            .locator(".nav-chat-open")
            .filter({ hasText: "Read only conversation" })
            .first()
            .evaluate((element) => element.click());
          await page
            .locator(".chat-heading strong")
            .filter({ hasText: "Read only conversation" })
            .waitFor();
          assert.equal(await trigger.count(), 1);
          await trigger.evaluate(element => element.click());
          await menu.waitFor();
          assert.deepEqual(await page.getByRole("menuitem").allTextContents(), ["Pin chat"]);
          await page.keyboard.press("Escape");
          assert.equal(requests.length, 3);
          assert.equal(archiveRequests.length, 3);
          assert.equal(deleteRequests.length, 3);
          assert.equal(
            await page
              .locator(".nav-chat-title")
              .filter({ hasText: /Other conversation|Delayed rename/ })
              .count(),
            0,
          );
          assert.deepEqual(errors, []);
          reports.push({
            engine,
            backend,
            width,
            theme,
            rename: true,
            errorRetry: true,
            keyboardAndIME: true,
            navigation: true,
            readOnly: true,
            archive: true,
            archiveFailureRetry: true,
            archiveNavigation: true,
            delete: true,
            deleteConfirmation: true,
            deleteFailureRetry: true,
            deleteNavigation: true,
          });
        } catch (error) {
          await page.screenshot({ path: path.join(output, "failure.png") });
          console.error({
            engine,
            backend,
            width,
            theme,
            errors,
            error: error.stack,
          });
          throw error;
        } finally {
          release?.();
          releaseArchive?.();
          releaseDelete?.();
          await context.close();
        }
      }
    } finally {
      await browser.close();
      browser = undefined;
    }
  }
  console.log(JSON.stringify(reports, null, 2));
} finally {
  await writeFile(
    path.join(output, "browser-report.json"),
    JSON.stringify(reports, null, 2) + "\n",
  );
  await browser?.close();
  for (const close of cleanup.reverse()) await close();
}
