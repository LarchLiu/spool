// Production App with synthetic titles; run through tools/run.sh.
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
    "__chat_title__.cjs",
  ),
);
const { chromium, webkit } = require("playwright");
const output = path.join(root, "build/artifacts/chat-title");
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
const longTitle = "完整标题中文 English ".repeat(100) + "末尾✅";
const titles = [longTitle, "Wide title ".repeat(17) + "end", "Short title"];
const chats = titles.map((title, index) => ({
  id: `title-${index}`,
  title,
  backend: "codex",
  project_id: "project",
  user_id: "owner",
  can_control: true,
  status: "completed",
  updated_at: 10 - index,
  turns: [],
  transcript: { parts: [] },
}));
let browser;
try {
  await mkdir(output, { recursive: true });
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const [engine, type, launch] of [
    ["chromium", chromium, { channel: "chrome" }],
    ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
  ]) {
    browser = await type.launch({ ...launch, headless: true });
    try {
      for (const width of [1600, 390]) {
        const context = await browser.newContext({
          viewport: { width, height: 820 },
          serviceWorkers: "block",
        });
        const page = await context.newPage();
        const errors = [];
        try {
          page.setDefaultTimeout(8000);
          page.on("pageerror", (error) =>
            errors.push(error.stack || String(error)),
          );
          await page.addInitScript(() => {
            localStorage.setItem(
              "spool.ui-preferences.v1",
              JSON.stringify({ language: "en", theme: "light" }),
            );
            localStorage.setItem(
              "spool.panel-layout.v1",
              JSON.stringify({ leftOpen: true, rightOpen: false }),
            );
            window.WebSocket = class extends EventTarget {
              readyState = 0;
              send() {}
              close() {
                this.readyState = 3;
              }
            };
          });
          await page.route("**/api/**", async (route) => {
            const endpoint = new URL(route.request().url()).pathname;
            const json = (value) =>
              route.fulfill({
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
            if (endpoint === "/api/adb")
              return json({ available: false, reason: "disabled" });
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
                chats,
              });
            if (endpoint === "/api/agents")
              return json([
                {
                  backend: "codex",
                  enabled: true,
                  installed: true,
                  catalog: { models: [] },
                },
              ]);
            if (endpoint === "/api/permissions")
              return json({
                backend: "codex",
                default: "ask",
                modes: [{ id: "ask", available: true }],
              });
            const chat = chats.find((chat) =>
              endpoint.startsWith(`/api/chats/${chat.id}`),
            );
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
          await page.goto(origin + "/?chat=title-0");
          const trigger = page.locator(".chat-title-trigger");
          const tooltip = page.getByRole("tooltip");
          await trigger.waitFor();
          const assertFullTooltip = async () => {
            await tooltip.waitFor();
            assert.equal(await tooltip.textContent(), longTitle);
            const box = await tooltip.boundingBox();
            assert.ok(box.x >= 8 && box.x + box.width <= width - 7);
            assert.ok(box.y >= 8 && box.y + box.height <= 813);
            assert.ok(
              await tooltip.evaluate((el) => el.scrollWidth <= el.clientWidth),
              "The title wraps without horizontal clipping",
            );
            await tooltip.hover();
            await tooltip.evaluate((el) => {
              el.scrollTop = el.scrollHeight;
            });
            assert.ok(
              await tooltip.evaluate(
                (el) => el.scrollTop + el.clientHeight >= el.scrollHeight - 1,
              ),
              "The complete title can be read by scrolling",
            );
          };
          await trigger.hover();
          await page.waitForTimeout(250);
          assert.equal(
            await tooltip.count(),
            0,
            "A brief hover does not show the title",
          );
          await page.mouse.move(width - 10, 400);
          await page.waitForTimeout(750);
          assert.equal(
            await tooltip.count(),
            0,
            "Leaving cancels the pending tooltip",
          );
          await trigger.hover();
          await assertFullTooltip();
          assert.ok((await tooltip.boundingBox()).width <= 360);
          await page.screenshot({
            path: path.join(output, `${engine}-${width}-header.png`),
          });
          await page.keyboard.press("Escape");
          await tooltip.waitFor({ state: "detached" });
          await trigger.focus();
          await assertFullTooltip();
          await page.keyboard.press("Escape");
          await trigger.click();
          await page.getByRole("menu", { name: "Chat options" }).waitFor();
          assert.equal(
            await tooltip.count(),
            0,
            "Opening actions hides the title tooltip",
          );
          await page.keyboard.press("Escape");

          if (width < 500)
            await page
              .getByRole("button", { name: "Open sidebar", exact: true })
              .click();
          const row = page
            .locator(".recents .nav-chat-open")
            .filter({ hasText: longTitle });
          await row.hover();
          await page.waitForTimeout(250);
          assert.equal(await tooltip.count(), 0);
          await assertFullTooltip();
          const sidebarTip = await tooltip.boundingBox();
          assert.ok(sidebarTip.width <= 300);
          if (width >= 500) {
            const sidebar = await page.locator(".sidebar").boundingBox();
            assert.ok(
              sidebarTip.x >= sidebar.x + sidebar.width,
              "Sidebar title appears to the right of the sidebar",
            );
          }
          await page.screenshot({
            path: path.join(output, `${engine}-${width}-sidebar.png`),
          });
          await page.keyboard.press("Escape");
          await row.focus();
          await row.press("Tab");
          await page.keyboard.press("Shift+Tab");
          await tooltip.waitFor();
          await row.press("Shift+F10");
          await page.getByRole("menu", { name: "Chat options" }).waitFor();
          assert.equal(await tooltip.count(), 0);
          await page.keyboard.press("Escape");

          const select = async (title) => {
            await page
              .locator(".recents .nav-chat-open")
              .filter({ hasText: title })
              .click();
            await page.waitForFunction(
              (title) =>
                document.querySelector(".chat-title-trigger strong")
                  ?.textContent === title,
              title,
            );
            await page.mouse.move(width - 10, 400);
            await tooltip.waitFor({ state: "detached" });
          };
          if (width >= 500) {
            await select(titles[1]);
            await page
              .getByRole("button", { name: "Collapse sidebar", exact: true })
              .click();
            const label = trigger.locator("strong");
            const layout = await label.evaluate((el) => ({
              width: el.clientWidth,
              full: el.scrollWidth,
            }));
            assert.ok(
              layout.width <= width / 2,
              "Header title keeps its original 50vw limit",
            );
            const actions = await page.locator(".header-actions").boundingBox();
            const heading = await trigger.boundingBox();
            assert.ok(heading.x + heading.width <= actions.x);
            await page.screenshot({
              path: path.join(output, `${engine}-wide-header.png`),
            });
            await page
              .getByRole("button", { name: "Open sidebar", exact: true })
              .click();
          }
          await select(titles[2]);
          assert.ok(
            await trigger
              .locator("strong")
              .evaluate((el) => el.scrollWidth <= el.clientWidth),
            "A title that fits is shown completely",
          );
          await trigger.hover();
          await page.waitForTimeout(800);
          assert.equal(
            await tooltip.count(),
            0,
            "A fully visible short title needs no tooltip",
          );
          await page.setViewportSize({ width, height: 600 });
          assert.ok(
            await page.evaluate(
              () => document.documentElement.scrollWidth <= window.innerWidth,
            ),
            "Header stays within the viewport",
          );
          assert.deepEqual(errors, []);
          reports.push({
            engine,
            width,
            completeTitle: true,
            delayedHover: true,
            keyboard: true,
            wrappingAndScroll: true,
            headerLayout: true,
          });
        } catch (error) {
          await page.screenshot({ path: path.join(output, "failure.png") });
          console.error({ engine, width, errors, error: error.stack });
          throw error;
        } finally {
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
