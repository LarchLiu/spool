// Production App notification routing and read state in browsers and desktop pages.
// Run through tools/run.sh; SPOOL_NODE_MODULES may locate shared Playwright.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import {
  createTestViteServer,
  mockDesktopNavigation,
} from "./vite-fixture.mjs";

requireRunTemp();
const root = fileURLToPath(new URL("../../", import.meta.url));
const { chromium, webkit } = createRequire(
  path.join(
    process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"),
    "__session_notifications__.cjs",
  ),
)("playwright");
const output = path.join(root, "build/artifacts/session-notifications");
const mount = "/web/" + "a".repeat(52) + "/";
const cleanup = [],
  reports = [];
const server = await createTestViteServer(
  { after: (fn) => cleanup.push(fn) },
  {
    configFile: false,
    root: path.join(root, "web"),
    plugins: [
      react(),
      mockDesktopNavigation(),
      {
        name: "session-notification-server-mount",
        configureServer(server) {
          server.middlewares.use((req, _res, next) => {
            if (req.url.startsWith(mount))
              req.url = "/" + req.url.slice(mount.length);
            next();
          });
        },
      },
    ],
    server: { host: "127.0.0.1", port: 0, strictPort: true, hmr: false },
  },
);
const user = "user-12345678";
const now = new Date(2026, 10, 26, 12, 34);
const ageMinutes = [0, 120, 4_320, 86_400, 1, 1_051_200];
const chats = [
  "current",
  "completed",
  "failed",
  "approval",
  "running",
  "hidden-completed",
].map((name, index) => ({
  id: "chat-" + name + "-12345678",
  title: name,
  project_id: "project",
  user_id: user,
  backend: "codex",
  status: name === "running" ? "running" : "idle",
  updated_at: now.getTime() - ageMinutes[index] * 60_000,
  can_control: true,
  turns: [],
  transcript: { parts: [] },
}));
const projects = [
  {
    id: "project",
    name: "Project with a long name to check status icon spacing",
  },
  { id: "empty", name: "Empty project" },
];
let browser;
try {
  await mkdir(output, { recursive: true });
  await server.listen(0);
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const [engine, type, launch] of [
    ["chromium", chromium, { channel: "chrome" }],
    ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
  ]) {
    browser = await type.launch({ ...launch, headless: true });
    try {
      for (const mode of ["web", "local", "remote"]) {
        const context = await browser.newContext({
          viewport: { width: 1280, height: 900 },
          serviceWorkers: "block",
        });
        try {
          const page = await context.newPage();
          await page.clock.install({ time: now });
          page.setDefaultTimeout(10000);
          const errors = [];
          page.on("pageerror", (error) => errors.push(String(error)));
          await page.addInitScript(
            ({ mode, user, chat }) => {
              const base =
                location.pathname.match(/^\/web\/[a-z2-7]{52}\//)?.[0] || "";
              localStorage.setItem(
                base + "spool.ui-preferences.v1",
                JSON.stringify({ language: "en", theme: "light" }),
              );
              // Control OS focus/visibility at the boundary, including cached native pages.
              window.fixtureFocused = true;
              window.fixtureVisible = true;
              document.hasFocus = () => window.fixtureFocused;
              Object.defineProperty(document, "visibilityState", {
                get: () => (window.fixtureVisible ? "visible" : "hidden"),
              });
              if (mode !== "web") {
                window.spoolDesktop = true;
                window.mygo = {
                  platform: "darwin",
                  locale: "en",
                  on: () => () => {},
                };
                window.spoolServers = {
                  entries: [
                    {
                      id: "local",
                      name: "Home",
                      local: true,
                      status: "connected",
                    },
                    {
                      id: "remote-1234",
                      name: "Remote",
                      local: false,
                      status: "connected",
                    },
                  ],
                  selected: mode === "local" ? "local" : "remote-1234",
                };
                if (mode === "local") window.spoolViewedChat = chat;
              }
              window.fixtureSockets = [];
              window.fixtureSent = [];
              window.WebSocket = class extends EventTarget {
                readyState = 0;
                constructor() {
                  super();
                  window.fixtureSockets.push(this);
                }
                send(value) {
                  window.fixtureSent.push(JSON.parse(value));
                }
                close() {
                  this.readyState = 3;
                }
              };
              window.fixtureNotify = (
                chat,
                kind,
                owner = user,
                native = mode === "local",
              ) => {
                const note = { chat, kind, user: owner };
                if (native) {
                  window.spoolSessionNotifications = [note];
                  window.dispatchEvent(
                    new Event("spool:session-notification-changed"),
                  );
                } else {
                  window.fixtureSocket.onmessage({
                    data: JSON.stringify({
                      type: "session_notification",
                      data: note,
                    }),
                  });
                }
              };
            },
            { mode, user, chat: chats[0].id },
          );
          await page.route("**/api/**", (route) => {
            const endpoint = new URL(route.request().url()).pathname.replace(
              /^\/web\/[a-z2-7]{52}/,
              "",
            );
            const json = (value) =>
              route.fulfill({
                contentType: "application/json",
                body: JSON.stringify(value),
              });
            if (endpoint === "/api/me")
              return json({
                user: { id: user, nickname: "Owner" },
                csrf: "fixture",
              });
            if (endpoint === "/api/version")
              return json({ version: "fixture" });
            if (endpoint === "/api/profiles")
              return json({
                revision: 1,
                people: [{ id: user, nickname: "Owner" }],
              });
            if (endpoint === "/api/navigation")
              return json({
                projects,
                chats,
                sidebar: { revision: 1, pinned: [chats[0].id] },
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
              endpoint.startsWith("/api/chats/" + chat.id),
            );
            if (chat && endpoint.endsWith("/events"))
              return route.fulfill({
                contentType: "text/event-stream",
                body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n`,
              });
            if (chat && endpoint === "/api/chats/" + chat.id) return json(chat);
            if (endpoint.endsWith("/workspace"))
              return json({ path: "/work", kind: "main", available: true });
            if (endpoint.endsWith("/files"))
              return json({ path: ".", entries: [], truncated: false });
            return json([]);
          });
          await page.goto(
            origin + (mode === "remote" ? mount : "/") + "?chat=" + chats[0].id,
          );
          await page.locator(".composer textarea").waitFor();
          await page.waitForFunction(() =>
            window.fixtureSockets.some(
              (socket) => typeof socket.onopen === "function",
            ),
          );
          await page.evaluate(() => {
            window.fixtureSocket = window.fixtureSockets.find(
              (socket) => typeof socket.onopen === "function",
            );
            window.fixtureSocket.readyState = 1;
            window.fixtureSocket.onopen();
            window.fixtureSocket.onmessage({
              data: JSON.stringify({ type: "ready", data: true }),
            });
          });
          const row = (title) =>
            page.locator(".recents .nav-chat").filter({
              has: page.locator(".nav-chat-title", {
                hasText: new RegExp(`^${title}$`),
              }),
            });
          const expectAge = async (title, label) => {
            const time = row(title).locator(".nav-chat-time");
            await page.waitForFunction(
              ({ title, label }) =>
                [...document.querySelectorAll(".recents .nav-chat")].some(
                  (row) =>
                    row.querySelector(".nav-chat-title")?.textContent ===
                      title &&
                    row.querySelector(".nav-chat-time")?.textContent === label,
                ),
              { title, label },
            );
            return time;
          };
          await page.mouse.move(1100, 850);
          const currentAge = await expectAge("current", "now");
          await expectAge("completed", "2h");
          await expectAge("failed", "3d");
          await expectAge("approval", "2mo");
          assert.equal(
            await row("running").locator(".nav-chat-time").count(),
            0,
          );
          for (const section of ["pinned", "projects", "recents"]) {
            const time = page
              .locator(`.${section} .nav-chat`)
              .filter({
                has: page.locator(".nav-chat-title", { hasText: /^current$/ }),
              })
              .locator(".nav-chat-time");
            assert.equal(await time.textContent(), "now");
            assert.ok(await time.isVisible());
          }
          const timeBounds = await currentAge.boundingBox();
          const titleBounds = await row("current")
            .locator(".nav-chat-title")
            .boundingBox();
          const rowBounds = await row("current").boundingBox();
          assert.ok(
            titleBounds.x + titleBounds.width <= timeBounds.x &&
              timeBounds.x + timeBounds.width <= rowBounds.x + rowBounds.width,
          );
          await page.screenshot({
            path: path.join(output, `${engine}-${mode}-ages.png`),
          });
          await row("current").hover();
          assert.equal(
            await currentAge.isVisible(),
            false,
            "Hover hides the age to make room for row actions",
          );
          assert.ok(
            await row("current")
              .getByRole("button", { name: "Unpin chat", exact: true })
              .isVisible(),
          );
          assert.deepEqual(await row("current").boundingBox(), rowBounds);
          await page.mouse.move(1100, 850);
          assert.ok(await currentAge.isVisible());
          const expectStatus = async (title, kind) => {
            await page.waitForFunction(
              ({ title, kind }) => {
                const row = [
                  ...document.querySelectorAll(".recents .nav-chat"),
                ].find(
                  (row) =>
                    row.querySelector(".nav-chat-title")?.textContent === title,
                );
                return (
                  !!row &&
                  (row
                    .querySelector(".session-notification-icon")
                    ?.getAttribute("data-status") || "") === kind
                );
              },
              { title, kind },
            );
            if (kind)
              assert.equal(
                await row(title).locator(".nav-chat-time").count(),
                0,
                "Unread states replace the age",
              );
          };
          const notify = (
            chat,
            kind,
            owner = user,
            native = mode === "local",
          ) =>
            page.evaluate(
              ({ chat, kind, owner, native }) =>
                window.fixtureNotify(chat, kind, owner, native),
              { chat, kind, owner, native },
            );
          const project = page.locator(".project-group").filter({
            has: page.locator(
              '.project-toggle[aria-controls="project-chats-project"]',
            ),
          });
          const toggleProject = project.locator(".project-toggle");
          const summary = project.locator(".project-chat-status");
          const expectProjectStatuses = async (kinds, running = true) => {
            await page.waitForFunction(
              ({ kinds, running }) => {
                const row = document
                  .querySelector(
                    '.project-toggle[aria-controls="project-chats-project"]',
                  )
                  .closest(".project-row");
                const statuses = [
                  ...row.querySelectorAll(".session-notification-icon"),
                ].map((icon) => icon.dataset.status);
                return (
                  JSON.stringify(statuses) === JSON.stringify(kinds) &&
                  !!row.querySelector('[role="status"]') === running
                );
              },
              { kinds, running },
            );
          };
          assert.equal(
            await toggleProject.getAttribute("aria-expanded"),
            "true",
          );
          assert.equal(
            await summary.count(),
            0,
            "Expanded projects leave status icons on their conversation rows",
          );
          await toggleProject.click();
          await expectProjectStatuses([]);
          assert.equal(await project.locator(".project-chats").count(), 0);
          assert.equal(
            await page
              .locator('.project-toggle[aria-controls="project-chats-empty"]')
              .locator("..")
              .locator(".project-chat-status")
              .count(),
            0,
          );
          const view = await page.evaluate(() =>
            window.fixtureSent.find((value) => value.type === "hello"),
          );
          assert.equal(
            view.focused,
            mode !== "local",
            "remote pages report the viewed session through their own socket",
          );
          assert.equal(view.chat, chats[0].id);
          for (const chat of chats.slice(1, 4)) {
            await notify(chat.id, chat.title);
            await expectStatus(chat.title, chat.title);
          }
          await notify(chats[5].id, "completed");
          await expectProjectStatuses(["approval", "failed", "completed"]);
          assert.equal(
            await summary.getByRole("img").count(),
            3,
            "Repeated notification kinds only appear once, including off-page conversations",
          );
          const iconBounds = await summary.boundingBox();
          const newChatBounds = await project
            .locator(".project-new")
            .boundingBox();
          const toggleBounds = await toggleProject.boundingBox();
          assert.ok(toggleBounds.x + toggleBounds.width <= iconBounds.x + 1);
          assert.ok(
            iconBounds.x + iconBounds.width <= newChatBounds.x + 1,
            "Icons do not overlap the new-chat action",
          );
          await toggleProject.click();
          assert.equal(
            await summary.count(),
            0,
            "Expanding a project removes its summary",
          );
          assert.equal(
            await project.getByRole("status", { name: "Working" }).count(),
            1,
          );
          await toggleProject.click();
          await expectProjectStatuses(["approval", "failed", "completed"]);
          await notify(chats[0].id, "completed");
          await expectStatus("current", "");
          await notify(chats[3].id, "failed", "another-user-12345678");
          // Local sockets cannot overwrite native state; remote pages ignore local native events.
          if (mode !== "web")
            await notify(chats[3].id, "failed", user, mode !== "local");
          await page.evaluate(
            () =>
              new Promise((resolve) =>
                requestAnimationFrame(() => requestAnimationFrame(resolve)),
              ),
          );
          await expectStatus("approval", "approval");
          if (mode === "remote")
            await page.screenshot({
              path: path.join(output, engine + "-remote.png"),
            });
          await row("completed").locator(".nav-chat-open").click();
          await expectStatus("completed", "");
          await expectAge("completed", "2h");
          assert.equal(
            await summary.count(),
            0,
            "Selecting a conversation reveals the project without duplicate summary icons",
          );
          await toggleProject.click();
          await expectProjectStatuses(["approval", "failed", "completed"]);
          await page
            .locator(".recents")
            .getByRole("button", { name: "Show more", exact: true })
            .click();
          await expectStatus("hidden-completed", "completed");
          await row("hidden-completed").locator(".nav-chat-open").click();
          await expectStatus("hidden-completed", "");
          await expectAge("hidden-completed", "2y");
          await toggleProject.click();
          await expectProjectStatuses(["approval", "failed"]);
          await row("completed").locator(".nav-chat-open").click();
          await page.waitForFunction(
            (id) =>
              window.fixtureSent.some(
                (value) => value.type === "view" && value.chat === id,
              ),
            chats[1].id,
          );
          if (mode === "local") {
            await page.evaluate(() => {
              window.spoolViewedChat = "";
              window.dispatchEvent(new Event("spool:session-view-changed"));
            });
          } else {
            await page.evaluate(() => {
              window.fixtureFocused = false;
              window.dispatchEvent(new Event("blur"));
            });
            assert.equal(
              await page.evaluate(() => window.fixtureSent.at(-1).focused),
              false,
            );
          }
          await notify(chats[1].id, "failed");
          await expectStatus("completed", "failed");
          if (mode === "local") {
            await page.evaluate((chat) => {
              window.spoolViewedChat = chat;
              window.dispatchEvent(new Event("spool:session-view-changed"));
            }, chats[1].id);
          } else {
            await page.evaluate(() => {
              window.fixtureFocused = true;
              window.dispatchEvent(new Event("focus"));
            });
          }
          await expectStatus("completed", "");
          if (mode !== "local") {
            await page.evaluate(() => {
              window.fixtureVisible = false;
              document.dispatchEvent(new Event("visibilitychange"));
            });
            await notify(chats[1].id, "approval");
            await expectStatus("completed", "approval");
            await page.evaluate(() => {
              window.fixtureVisible = true;
              document.dispatchEvent(new Event("visibilitychange"));
            });
            await expectStatus("completed", "");
          }
          if (mode === "remote") {
            await page.evaluate(() => {
              window.fixtureFocused = false;
              window.dispatchEvent(new Event("blur"));
            });
            await notify(chats[1].id, "completed");
            await expectStatus("completed", "completed");
            await page.evaluate(() => {
              window.fixtureFocused = true;
              window.dispatchEvent(new Event("spool:server-selected"));
            });
            await expectStatus("completed", "");
          }
          await expectStatus("failed", "failed");
          await expectStatus("approval", "approval");
          await toggleProject.click();
          await expectProjectStatuses(["approval", "failed"]);
          await page.evaluate(
            ({ projects, chats }) => {
              window.fixtureSocket.onmessage({
                data: JSON.stringify({
                  type: "navigation",
                  data: {
                    projects,
                    chats: chats.map((chat) =>
                      chat.status === "running"
                        ? { ...chat, status: "completed" }
                        : chat,
                    ),
                  },
                }),
              });
            },
            { projects, chats },
          );
          await expectProjectStatuses(["approval", "failed"], false);
          await expectAge("running", "1m");
          assert.equal(
            await toggleProject.getAttribute("aria-expanded"),
            "false",
            "Live updates do not reopen a collapsed project",
          );
          await page.clock.fastForward(60_000);
          await expectAge("current", "1m");
          await expectAge("running", "2m");
          assert.deepEqual(errors, []);
          reports.push({
            engine,
            mode,
            threeStatuses: true,
            accountIsolation: true,
            readState: true,
            collapsedProjectStatuses: true,
            relativeAges: true,
            hoverHidesAge: true,
          });
          console.log(`${engine}/${mode}: session notifications passed`);
        } finally {
          await context.close();
        }
      }
    } finally {
      await browser.close();
      browser = undefined;
    }
  }
  await writeFile(
    path.join(output, "report.json"),
    JSON.stringify(reports, null, 2) + "\n",
  );
} finally {
  await browser?.close();
  for (const close of cleanup.reverse()) await close();
}
