// Production chat and standalone remote viewer with synthetic API boundaries.
// Run through tools/run.sh; SPOOL_NODE_MODULES locates Playwright if necessary.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = fileURLToPath(new URL("../..", import.meta.url));
const { chromium, webkit } = createRequire(
  path.join(
    process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"),
    "__remote__.cjs",
  ),
)("playwright");
const output = path.join(root, "build/artifacts/remote-desktop");
const cleanup = [],
  reports = [],
  disconnections = [];
const mount = "/web/" + "a".repeat(52) + "/";
const server = await createTestViteServer(
  { after: (fn) => cleanup.push(fn) },
  {
    configFile: false,
    root: path.join(root, "web"),
    plugins: [
      react(),
      {
        name: "synthetic-relay-mount",
        configureServer(server) {
          server.middlewares.use((req, res, next) => {
            const requested = new URL(req.url, "http://fixture");
            if (req.url.startsWith(mount))
              req.url = "/" + req.url.slice(mount.length);
            if (
              new URL(req.url, "http://fixture").pathname ===
              "/api/desktop/disconnect"
            ) {
              disconnections.push({
                path: requested.pathname,
                token: requested.searchParams.get("token"),
                method: req.method,
                headers: req.headers,
              });
              req.resume();
              res.setHeader("Content-Type", "application/json");
              res.end('{"ok":true}');
              return;
            }
            next();
          });
        },
      },
    ],
    server: { host: "127.0.0.1", port: 0, strictPort: true },
  },
);
const chat = {
  id: "remote-chat",
  title: "Chat stays available",
  backend: "codex",
  project_id: "project",
  user_id: "owner",
  can_control: true,
  status: "completed",
  updated_at: 10,
  turns: [],
  transcript: { parts: [] },
};
let browser;
try {
  await mkdir(output, { recursive: true });
  await server.listen(0);
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const [engine, type, options] of [
    ["chromium", chromium, { channel: "chrome" }],
    ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
  ].filter(
    ([name]) =>
      !process.env.SPOOL_SMOKE_ENGINE ||
      name === process.env.SPOOL_SMOKE_ENGINE,
  )) {
    browser = await type.launch({ ...options, headless: true });
    try {
      for (const base of ["/", mount]) {
        const context = await browser.newContext({
          viewport: { width: 1440, height: 900 },
          serviceWorkers: "block",
        });
        const errors = [],
          requests = [];
        const disconnectOffset = disconnections.length;
        const waitForDisconnect = async (token) => {
          const received = () =>
            disconnections
              .slice(disconnectOffset)
              .find(
                (request) =>
                  request.path === base + "api/desktop/disconnect" &&
                  request.token === token,
              );
          const deadline = Date.now() + 10000;
          while (!received() && Date.now() < deadline) await delay(20);
          const request = received();
          assert.ok(request, `Server did not receive disconnect for ${token}`);
          assert.equal(request.method, "POST");
          assert.equal(request.headers["x-csrf-token"], "fixture-csrf");
          return request;
        };
        let sequence = 0;
        context.on("page", (page) => {
          page.setDefaultTimeout(15000);
          page.on("pageerror", (error) => errors.push(String(error)));
        });
        await context.addInitScript(() => {
          if (!/^https?:$/.test(location.protocol)) return;
          const base =
            location.pathname.match(/^\/web\/[a-z2-7]{52}\//)?.[0] ?? "";
          localStorage.setItem(
            base + "spool.ui-preferences.v1",
            JSON.stringify({ language: "en", theme: "light" }),
          );
          localStorage.setItem(
            base + "spool.panel-layout.v1",
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
        // Let teardown keepalive requests reach the HTTP server directly;
        // browser interception can lose events when their document unloads.
        await context.route(
          /\/api\/(?!desktop\/disconnect(?:\?|$))/,
          async (route) => {
            const request = route.request(),
              url = new URL(request.url());
            const requestBase = url.pathname.startsWith(mount) ? mount : "/";
            const endpoint = url.pathname.slice(
              requestBase === "/" ? 0 : requestBase.length - 1,
            );
            requests.push({
              path: url.pathname,
              endpoint,
              method: request.method(),
              headers: request.headers(),
              body: request.postData(),
              token: url.searchParams.get("token"),
            });
            const json = (value) =>
              route.fulfill({
                contentType: "application/json",
                body: JSON.stringify(value),
              });
            if (endpoint === "/api/me")
              return json({
                user: { id: "owner", nickname: "Owner" },
                csrf: "fixture-csrf",
              });
            if (endpoint === "/api/version")
              return json({
                version: "fixture",
                server_name:
                  requestBase === "/" ? "Local workstation" : "Build server",
              });
            if (endpoint === "/api/desktop") return json({ available: true });
            if (endpoint === "/api/desktop/connect")
              return json({ token: `viewer-${++sequence}` });
            if (endpoint === "/api/desktop/view/")
              return route.fulfill({
                contentType: "text/html",
                body: "<!doctype html><title>Synthetic remote desktop</title><style>body{background:#e4eae5;font:24px system-ui;padding:40px}</style><h1>Remote desktop</h1><p>Synthetic desktop for window verification</p>",
              });
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
                chats: [chat],
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
            if (endpoint === "/api/chats/remote-chat/messages")
              return json(chat);
            if (endpoint.endsWith("/events"))
              return route.fulfill({
                contentType: "text/event-stream",
                body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n`,
              });
            if (endpoint.endsWith("/workspace"))
              return json({ path: "/work", kind: "main", available: true });
            if (endpoint === "/api/chats/remote-chat") return json(chat);
            return json({ path: ".", entries: [], truncated: false });
          },
        );
        const page = await context.newPage();
        try {
          await page.goto(origin + base + "?chat=remote-chat");
          const composer = page.locator(".composer textarea");
          await composer.fill("Draft survives opening the desktop");
          const connect = async () => {
            await page
              .getByRole("button", { name: "Remote connection", exact: true })
              .click();
            await page
              .getByRole("button", { name: "Connect to desktop", exact: true })
              .click();
          };
          const firstPopup = page.waitForEvent("popup");
          await connect();
          const viewer = await firstPopup;
          await viewer.locator("iframe").waitFor();
          await page
            .locator('.connection-status-dot[data-state="connected"]')
            .waitFor();
          assert.equal(
            new URL(viewer.url()).pathname,
            base + "remote-desktop.html",
          );
          const name = base === "/" ? "Local workstation" : "Build server";
          await viewer.waitForFunction(
            (name) => document.title === `Spool · ${name} · Remote desktop`,
            name,
          );
          assert.equal(
            await viewer.getByRole("heading", { level: 2 }).textContent(),
            `Remote desktop · ${name}`,
          );
          assert.equal(await page.locator("dialog[open]").count(), 0);
          assert.equal(
            await composer.inputValue(),
            "Draft survives opening the desktop",
          );
          await page.bringToFront();
          await composer.fill("Message sent while desktop is open");
          const sent = page.waitForRequest(
            (req) =>
              req.method() === "POST" &&
              req.url().includes("/chats/remote-chat/messages"),
          );
          await composer.press("Enter");
          assert.equal(
            (await sent).postDataJSON().message,
            "Message sent while desktop is open",
          );
          assert.equal(viewer.isClosed(), false);
          await connect();
          assert.equal(
            context.pages().length,
            2,
            "Repeated connection focuses the existing window",
          );
          assert.equal(sequence, 1);
          const bounds = await viewer.locator("iframe").boundingBox();
          assert.ok(bounds.width > 400 && bounds.height > 200);
          await viewer.setViewportSize({ width: 600, height: 420 });
          assert.equal(
            await viewer.evaluate(
              () =>
                document.documentElement.scrollWidth <= innerWidth &&
                document.documentElement.scrollHeight <= innerHeight,
            ),
            true,
          );
          await viewer.screenshot({
            path: path.join(
              output,
              `${engine}-${base === "/" ? "direct" : "relay"}.png`,
            ),
          });
          await viewer.close();
          await waitForDisconnect("viewer-1");
          await page
            .locator(".connection-status-dot")
            .waitFor({ state: "detached" });
          const secondPopup = page.waitForEvent("popup");
          await connect();
          const second = await secondPopup;
          await second.locator("iframe").waitFor();
          assert.equal(sequence, 2);
          const closed = second.waitForEvent("close");
          await second
            .getByRole("button", { name: "Disconnect desktop", exact: true })
            .click();
          await closed;
          await waitForDisconnect("viewer-2");
          await page
            .locator(".connection-status-dot")
            .waitFor({ state: "detached" });
          const beforeReloadPopup = page.waitForEvent("popup");
          await connect();
          const beforeReload = await beforeReloadPopup;
          await beforeReload.locator("iframe").waitFor();
          await page
            .locator('.connection-status-dot[data-state="connected"]')
            .waitFor();
          const reloadClosed = beforeReload.waitForEvent("close");
          await page.reload();
          await reloadClosed;
          await waitForDisconnect("viewer-3");
          await page
            .getByRole("button", { name: "Remote connection", exact: true })
            .waitFor();
          assert.equal(
            context.pages().length,
            1,
            "Reload closes the previous viewer",
          );
          assert.equal(await page.locator(".connection-status-dot").count(), 0);
          const afterReloadPopup = page.waitForEvent("popup");
          await connect();
          const afterReload = await afterReloadPopup;
          await afterReload.locator("iframe").waitFor();
          await page
            .locator('.connection-status-dot[data-state="connected"]')
            .waitFor();
          await connect();
          assert.equal(
            context.pages().length,
            2,
            "Reconnect after reload owns exactly one viewer",
          );
          assert.equal(sequence, 4);
          const navigationClosed = afterReload.waitForEvent("close");
          await page.goto(origin + base + "?chat=remote-chat&reloaded=1");
          await navigationClosed;
          await waitForDisconnect("viewer-4");
          await page
            .getByRole("button", { name: "Remote connection", exact: true })
            .waitFor();
          assert.equal(
            context.pages().length,
            1,
            "Leaving the chat document closes its viewer",
          );
          assert.ok(
            requests.every((request) => request.path.startsWith(base + "api/")),
            "API calls stay under the selected server mount",
          );
          if (base === "/") {
            const thirdPopup = page.waitForEvent("popup");
            await connect();
            const localViewer = await thirdPopup;
            await localViewer.locator("iframe").waitFor();
            const remotePage = await context.newPage();
            await remotePage.goto(origin + mount + "?chat=remote-chat");
            await remotePage
              .getByRole("button", { name: "Remote connection", exact: true })
              .click();
            const remotePopup = remotePage.waitForEvent("popup");
            await remotePage
              .getByRole("button", { name: "Connect to desktop", exact: true })
              .click();
            const remoteViewer = await remotePopup;
            await remoteViewer.locator("iframe").waitFor();
            await remotePage
              .locator('.connection-status-dot[data-state="connected"]')
              .waitFor();
            assert.equal(
              await localViewer.title(),
              "Spool · Local workstation · Remote desktop",
            );
            assert.equal(
              await remoteViewer.title(),
              "Spool · Build server · Remote desktop",
            );
            assert.equal(localViewer.isClosed(), false);
            assert.equal(context.pages().length, 4);
            await localViewer.close();
            await page
              .locator(".connection-status-dot")
              .waitFor({ state: "detached" });
            assert.equal(remoteViewer.isClosed(), false);
            assert.equal(
              await remotePage
                .locator('.connection-status-dot[data-state="connected"]')
                .count(),
              1,
            );
            await remoteViewer.close();
            await remotePage
              .locator(".connection-status-dot")
              .waitFor({ state: "detached" });
            await remotePage.close();
          }
          assert.deepEqual(errors, []);
          reports.push({
            engine,
            base,
            chatSent: true,
            reusedWindow: true,
            closeRevoked: true,
            reconnect: true,
            reloadClosesViewer: true,
            navigationClosesViewer: true,
            serverTitle: name,
            simultaneousServers: base === "/",
            errors,
          });
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
  console.log(JSON.stringify(reports, null, 2));
} finally {
  await browser?.close();
  for (const fn of cleanup.reverse()) await fn();
}
