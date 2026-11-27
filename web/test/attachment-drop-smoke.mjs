// Production composer drag/drop and multipart delivery in browser and desktop shells.
// Run through tools/run.sh with SPOOL_NODE_MODULES pointing to Playwright if needed.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { join, resolve } from "node:path";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import {
  createTestViteServer,
  mockDesktopNavigation,
} from "./vite-fixture.mjs";

const scratch = requireRunTemp();
const root = resolve(import.meta.dirname, "../..");
const output = join(root, "build/artifacts/attachment-drop");
const { chromium, webkit } = createRequire(
  join(
    process.env.SPOOL_NODE_MODULES || join(root, "web/node_modules"),
    "__drop__.cjs",
  ),
)("playwright");
const cleanup = [];
let uploadFixture;
const uploadServer = {
  name: "attachment-drop-upload",
  configureServer(server) {
    server.middlewares.use(async (request, response, next) => {
      if (
        request.method !== "POST" ||
        !request.url.startsWith("/api/chats/") ||
        !request.url.includes("/messages")
      )
        return next();
      try {
        assert.equal(request.headers["x-csrf-token"], "drop-csrf");
        const chunks = [];
        for await (const chunk of request) chunks.push(chunk);
        // WebKit's debugging protocol omits multipart file bytes. Read the actual HTTP body.
        const form = await new Request("http://localhost" + request.url, {
          method: "POST",
          headers: { "Content-Type": request.headers["content-type"] },
          body: Buffer.concat(chunks),
        }).formData();
        const files = await Promise.all(
          form.getAll("files").map(async (file) => ({
            name: file.name,
            type: file.type,
            bytes: Buffer.from(await file.arrayBuffer()).toString("base64"),
          })),
        );
        uploadFixture.uploads.push({ message: form.get("message"), files });
        await uploadFixture.gate;
        response.setHeader("Content-Type", "application/json");
        response.end(JSON.stringify(uploadFixture.chat));
      } catch (error) {
        response.statusCode = 500;
        response.end(String(error));
      }
    });
  },
};
const server = await createTestViteServer(
  { after: (fn) => cleanup.push(fn) },
  {
    configFile: false,
    root: join(root, "web"),
    plugins: [react(), mockDesktopNavigation(), uploadServer],
    server: { host: "127.0.0.1", port: 0, strictPort: true },
  },
);
const png =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=";
const files = [
  { name: "图片.png", type: "image/png", base64: png },
  { name: "notes.md", type: "text/markdown", text: "# Notes\n保留原始内容" },
];
let browser;
const reports = [];
try {
  await server.listen();
  await mkdir(output, { recursive: true });
  for (const [engine, type, launch] of [
    ["chromium", chromium, { channel: "chrome" }],
    ["webkit", webkit, {}],
  ]) {
    for (const desktop of [false, true]) {
      const surface = `${engine}-${desktop ? "desktop" : "web"}`;
      browser = await type.launchPersistentContext(join(scratch, surface), {
        ...launch,
        headless: true,
        viewport: { width: 1280, height: 800 },
        serviceWorkers: "block",
      });
      const page = browser.pages()[0];
      page.setDefaultTimeout(12000);
      const errors = [],
        uploads = [];
      page.on("pageerror", (error) => errors.push(String(error)));
      await page.addInitScript((desktop) => {
        localStorage.setItem(
          "spool.ui-preferences.v1",
          JSON.stringify({ language: "en", theme: "light" }),
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
      }, desktop);
      const chat = (id, writable = true) => ({
        id,
        title: id,
        project_id: writable ? "project" : "readonly",
        user_id: "owner",
        backend: "codex",
        status: "completed",
        updated_at: 1,
        permission_mode: "ask",
        can_control: writable,
        turns: [],
        transcript: { parts: [] },
      });
      const chats = [chat("Chat A"), chat("Read-only chat", false)];
      let releaseUpload;
      uploadFixture = {
        chat: chats[0],
        uploads,
        gate: new Promise((resolve) => {
          releaseUpload = resolve;
        }),
        release: () => releaseUpload(),
      };
      await page.route("**/api/**", async (route) => {
        const request = route.request(),
          url = new URL(request.url()),
          endpoint = url.pathname;
        const json = (value) =>
          route.fulfill({
            contentType: "application/json",
            body: JSON.stringify(value),
          });
        if (endpoint === "/api/me")
          return json({
            user: { id: "owner", nickname: "Owner" },
            csrf: "drop-csrf",
          });
        if (endpoint === "/api/version") return json({ version: "fixture" });
        if (endpoint === "/api/profiles")
          return json({
            revision: 1,
            people: [{ id: "owner", nickname: "Owner" }],
          });
        if (endpoint === "/api/federation/agents") return json([]);
        if (endpoint === "/api/navigation")
          return json({
            projects: [
              { id: "project", name: "Project" },
              { id: "readonly", name: "Read-only project", can_create: false },
            ],
            chats,
          });
        if (endpoint === "/api/agents")
          return json([
            {
              backend: "codex",
              enabled: true,
              installed: true,
              catalog: { models: [], commands: [] },
            },
          ]);
        if (endpoint === "/api/permissions")
          return json({
            backend: "codex",
            default: "ask",
            modes: [{ id: "ask", available: true }],
            commands: [],
          });
        const current = chats.find((chat) =>
          endpoint.startsWith(`/api/chats/${encodeURIComponent(chat.id)}`),
        );
        if (endpoint.endsWith("/messages") && request.method() === "POST") {
          return route.continue();
        }
        if (endpoint.endsWith("/events"))
          return route.fulfill({
            contentType: "text/event-stream",
            body: `event: snapshot\ndata: ${JSON.stringify(current)}\n\n`,
          });
        if (endpoint.endsWith("/workspace"))
          return json({ path: "/work", kind: "main", available: true });
        if (
          current &&
          endpoint === `/api/chats/${encodeURIComponent(current.id)}`
        )
          return json(current);
        return json({ path: ".", entries: [], truncated: false });
      });
      const base = `http://127.0.0.1:${server.httpServer.address().port}/?chat=`;
      await page.goto(base + encodeURIComponent("Chat A"));
      const composer = page.locator(".composer"),
        input = page.locator("#chat-message");
      await input.waitFor();
      await input.fill("Keep this draft");
      async function drag(
        target,
        action,
        entries = files,
        protectedMode = false,
      ) {
        return target.evaluate(
          (element, { action, entries, protectedMode }) => {
            const dataTransfer = new DataTransfer();
            if (protectedMode)
              Object.defineProperty(dataTransfer, "types", {
                value: ["Files"],
              });
            else
              for (const file of entries)
                dataTransfer.items.add(
                  new File(
                    [
                      file.base64
                        ? Uint8Array.from(atob(file.base64), (value) =>
                            value.charCodeAt(0),
                          )
                        : (file.text ?? "x".repeat(file.size ?? 1)),
                    ],
                    file.name,
                    { type: file.type },
                  ),
                );
            const rect = element.getBoundingClientRect();
            const event = new DragEvent(action, {
              dataTransfer,
              bubbles: true,
              cancelable: true,
              clientX: Math.round(rect.x + rect.width / 2),
              clientY: Math.round(rect.y + rect.height / 2),
            });
            element.dispatchEvent(event);
            return {
              prevented: event.defaultPrevented,
              effect: dataTransfer.dropEffect,
            };
          },
          { action, entries, protectedMode },
        );
      }
      async function clearFiles() {
        while (await composer.locator(".attachment-remove").count())
          await composer.locator(".attachment-remove").first().click();
      }
      const history = page.locator(".history"),
        sidebar = page.locator(".sidebar");
      await drag(history, "dragenter", [], true);
      await page.locator(".composer-drop-hint").waitFor();
      assert.equal(
        await history.evaluate((element) => getComputedStyle(element).opacity),
        "1",
        "Entering the history advertises the composer without dimming messages",
      );
      await drag(sidebar, "dragenter", [], true);
      await drag(history, "dragleave", [], true);
      assert.equal(await page.locator(".composer-drop-hint").count(), 1);
      await drag(sidebar, "dragleave", [], true);
      await page.locator(".composer-drop-hint").waitFor({ state: "hidden" });
      if (desktop) {
        await page.evaluate(() =>
          window.spoolAttachmentDrop.hover({ active: true, x: 20, y: 100 }),
        );
        await page.locator(".composer-drop-hint").waitFor();
        await page.screenshot({
          path: join(output, `${surface}-native-page-hover.png`),
        });
        await page.evaluate(() =>
          window.spoolAttachmentDrop.hover({ active: false, x: -1, y: -1 }),
        );
        await page.locator(".composer-drop-hint").waitFor({ state: "hidden" });
      }
      assert.equal(
        (await drag(composer, "dragenter", [], true)).prevented,
        true,
        "The composer accepts protected file drags",
      );
      await page
        .getByRole("status")
        .filter({ hasText: "Drop files or images here" })
        .waitFor();
      await drag(input, "dragenter", [], true);
      await drag(composer, "dragleave", [], true);
      assert.equal(
        await page.locator(".composer-drop-hint").count(),
        1,
        "Moving over child controls preserves the highlight",
      );
      await composer.screenshot({ path: join(output, `${surface}-hover.png`) });
      await page.screenshot({
        path: join(output, `${surface}-drag-light.png`),
      });
      await page.evaluate(() => {
        document.documentElement.dataset.theme = "dark";
      });
      await page.screenshot({ path: join(output, `${surface}-drag-dark.png`) });
      await page.evaluate(() => {
        document.documentElement.dataset.theme = "light";
      });
      await drag(input, "dragleave", [], true);
      await page.locator(".composer-drop-hint").waitFor({ state: "hidden" });
      await page.screenshot({
        path: join(output, `${surface}-drag-ended.png`),
      });
      for (const action of ["dragend", "blur"]) {
        await drag(input, "dragover", [], true);
        await page.evaluate(
          (action) => window.dispatchEvent(new Event(action)),
          action,
        );
        await page.locator(".composer-drop-hint").waitFor({ state: "hidden" });
        assert.equal(await input.inputValue(), "Keep this draft");
      }
      await drag(input, "dragover", [], true);
      await drag(input, "drop");
      await composer.locator(".attachment-card").nth(1).waitFor();
      assert.equal(await input.inputValue(), "Keep this draft");
      assert.equal(await page.locator(".composer-drop-hint").count(), 0);
      if (desktop) {
        assert.equal(
          await input.evaluate((element) => {
            const rect = element.getBoundingClientRect();
            return window.spoolAttachmentDrop.claim(
              Math.round(rect.x + rect.width / 2),
              Math.round(rect.y + rect.height / 2),
            );
          }),
          "",
          "DOM files must not be imported again by the native callback",
        );
        await clearFiles();
        const delivered = await input.evaluate((element, files) => {
          const rect = element.getBoundingClientRect();
          const token = window.spoolAttachmentDrop.claim(
            Math.round(rect.x + rect.width / 2),
            Math.round(rect.y + rect.height / 2),
          );
          if (!token) return false;
          return window.spoolAttachmentDrop.receive({
            token,
            url: location.href,
            files: files.map((file) => ({
              name: file.name,
              type: file.type,
              modified: 1700000000123,
              data:
                file.base64 ||
                btoa(
                  String.fromCharCode(...new TextEncoder().encode(file.text)),
                ),
            })),
          });
        }, files);
        assert.equal(delivered, true);
        await composer.locator(".attachment-card").nth(1).waitFor();
        assert.equal(await input.inputValue(), "Keep this draft");
      } else {
        assert.equal(
          await page.evaluate(() => window.spoolAttachmentDrop),
          undefined,
        );
      }
      assert.match(await composer.innerText(), /notes\.md/);
      assert.match(await composer.innerText(), /保留原始内容/);
      await composer.locator("img").evaluate((image) => {
        if (!image.complete)
          return new Promise((resolve, reject) => {
            image.onload = resolve;
            image.onerror = reject;
          });
      });
      assert.ok(
        await composer
          .locator("img")
          .evaluate((image) => image.naturalWidth > 0),
      );
      await composer.screenshot({
        path: join(output, `${surface}-attachments.png`),
      });
      await composer.locator(".image-card .attachment-open").click();
      await page.getByRole("dialog").waitFor();
      await page
        .getByRole("button", { name: "Close preview", exact: true })
        .click();
      const sent = page.waitForResponse(
        (response) =>
          response.request().method() === "POST" &&
          new URL(response.url()).pathname.endsWith("/messages"),
      );
      await page
        .getByRole("button", { name: "Send message", exact: true })
        .click();
      assert.equal((await drag(composer, "dragenter")).effect, "none");
      assert.equal((await drag(composer, "drop")).prevented, true);
      assert.equal(
        await composer.locator(".attachment-card").count(),
        0,
        "Drops cannot change a draft during submission",
      );
      releaseUpload();
      assert.equal((await sent).status(), 200);
      await page.waitForFunction(
        () =>
          !document.querySelector(".composer .attachment-card") &&
          !document.querySelector("#chat-message").value,
      );
      assert.equal(uploads.length, 1);
      assert.deepEqual(uploads[0], {
        message: "Keep this draft",
        files: files.map((file) => ({
          name: file.name,
          type: file.type,
          bytes: file.base64 ?? Buffer.from(file.text).toString("base64"),
        })),
      });

      // Existing attachments append in order and reject each invalid batch atomically.
      await drag(composer, "drop", [files[1]]);
      await composer.locator(".attachment-card").waitFor();
      await drag(composer, "drop", [files[0]]);
      await composer.locator(".attachment-card").nth(1).waitFor();
      for (const [entries, message] of [
        [
          Array.from({ length: 5 }, (_, i) => ({
            name: `${i}.txt`,
            type: "text/plain",
            text: "x",
          })),
          "Attach at most 6 files, up to 16 MB total",
        ],
        [
          [
            {
              name: "large.txt",
              type: "text/plain",
              size: 4 * 1024 * 1024 + 1,
            },
          ],
          "Each attachment must be nonempty and at most 4 MB",
        ],
        [
          [{ name: "empty.txt", type: "text/plain", text: "" }],
          "Each attachment must be nonempty and at most 4 MB",
        ],
      ]) {
        await drag(composer, "drop", entries);
        await page.getByRole("alert").filter({ hasText: message }).waitFor();
        assert.equal(await composer.locator(".attachment-card").count(), 2);
        await page
          .getByRole("button", { name: "Dismiss", exact: true })
          .click();
      }
      await clearFiles();
      await drag(
        composer,
        "drop",
        Array.from({ length: 5 }, (_, i) => ({
          name: `${i}.bin`,
          type: "application/octet-stream",
          size: 4 * 1024 * 1024,
        })),
      );
      await page
        .getByRole("alert")
        .filter({ hasText: "Attach at most 6 files, up to 16 MB total" })
        .waitFor();
      assert.equal(await composer.locator(".attachment-card").count(), 0);
      await input.fill("Still here");
      const before = page.url();
      await drag(history, "dragenter", [], true);
      await page.locator(".composer-drop-hint").waitFor();
      assert.equal((await drag(page.locator("body"), "drop")).prevented, true);
      assert.equal(await page.locator(".composer-drop-hint").count(), 0);
      assert.equal(await composer.locator(".attachment-card").count(), 0);
      assert.equal(page.url(), before);
      assert.equal(await input.inputValue(), "Still here");
      const plain = await input.evaluate((element) => {
        const dataTransfer = new DataTransfer();
        dataTransfer.setData("text/plain", "normal text");
        dataTransfer.setData("text/uri-list", "https://example.com/");
        const event = new DragEvent("drop", {
          dataTransfer,
          bubbles: true,
          cancelable: true,
        });
        element.dispatchEvent(event);
        return event.defaultPrevented;
      });
      assert.equal(
        plain,
        false,
        "Text and URL drops retain normal editor behavior",
      );
      await page
        .locator(".nav-chat-open")
        .filter({ hasText: "Read-only chat" })
        .first()
        .click();
      await page.waitForFunction(
        () =>
          document.querySelector("#chat-message")?.placeholder === "Read-only",
      );
      assert.equal(await input.isDisabled(), true);
      if (desktop)
        assert.equal(
          await input.evaluate((element) => {
            const rect = element.getBoundingClientRect();
            return window.spoolAttachmentDrop.claim(
              rect.x + rect.width / 2,
              rect.y + rect.height / 2,
            );
          }),
          "",
          "Read-only chats must reject native file drops too",
        );
      assert.equal((await drag(composer, "dragenter")).effect, "none");
      await drag(history, "dragenter", [], true);
      assert.equal(await page.locator(".composer-drop-hint").count(), 0);
      assert.equal((await drag(composer, "drop")).prevented, true);
      assert.equal(await composer.locator(".attachment-card").count(), 0);
      assert.deepEqual(errors, []);
      reports.push({
        surface,
        nativeDelivery: desktop,
        pageHover: true,
        osDragGesture: false,
        multipartBytesPreserved: true,
        previews: true,
        limits: true,
        readonly: true,
        navigationGuard: true,
        errors,
      });
      await browser.close();
      browser = null;
    }
  }
  await writeFile(
    join(output, "report.json"),
    JSON.stringify(reports, null, 2) + "\n",
  );
  console.log(JSON.stringify(reports));
} catch (error) {
  await browser?.pages()[0]?.screenshot({ path: join(output, "failure.png") });
  console.error(error.stack);
  throw error;
} finally {
  uploadFixture?.release();
  await browser?.close();
  for (const fn of cleanup.reverse()) await fn();
}
