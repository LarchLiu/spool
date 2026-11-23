// Production composer keyboard behavior in Web and desktop shells.
// Run: sh tools/run.sh composer-history bun web/test/composer-history-smoke.mjs
import assert from "node:assert/strict";
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
const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const require = createRequire(
  path.join(
    process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"),
    "__composer_history__.cjs",
  ),
);
const { chromium } = require("playwright");
const cleanup = [];
const server = await createTestViteServer(
  { after: (fn) => cleanup.push(fn) },
  {
    configFile: false,
    root: path.join(root, "web"),
    plugins: [react(), mockDesktopNavigation()],
    server: { host: "127.0.0.1", port: 0 },
  },
);
const fixture = (id, messages) => ({
  id,
  title: id,
  project_id: "project",
  user_id: "owner",
  backend: "codex",
  status: "completed",
  updated_at: 1,
  permission_mode: "ask",
  turns: messages.map((message, index) => ({
    id: `${id}-${index}`,
    part_start: index * 2,
    input_end: index * 2 + 1,
    part_end: index * 2 + 2,
    status: "completed",
    started_at: index + 1,
  })),
  transcript: {
    parts: messages.flatMap((text) => [
      { role: "user", kind: "text", text },
      { role: "assistant", kind: "text", text: `Answer to ${text}` },
    ]),
  },
});
let browser;
try {
  await server.listen();
  browser = await chromium.launch({ channel: "chrome", headless: true });
  for (const desktop of [false, true]) {
    const context = await browser.newContext({
      viewport: { width: 1280, height: 800 },
      serviceWorkers: "block",
    });
    try {
      const page = await context.newPage();
      const errors = [],
        submissions = [];
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
      const messages = [
        "Oldest input",
        "Two lines\nSecond line",
        "/review",
        "Latest input",
      ];
      const chats = [
        fixture("Chat A", messages),
        fixture("Chat B", ["Other conversation"]),
      ];
      await page.route("**/api/**", async (route) => {
        const request = route.request(),
          endpoint = new URL(request.url()).pathname;
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
        if (endpoint === "/api/version") return json({ version: "fixture" });
        if (endpoint === "/api/profiles")
          return json({
            revision: 1,
            people: [{ id: "owner", nickname: "Owner" }],
          });
        if (endpoint === "/api/navigation")
          return json({
            projects: [{ id: "project", name: "Project" }],
            chats,
          });
        const commands = ["review", "reset"].map((name) => ({
          name,
          description: name,
          builtin: true,
          kind: "prompt",
        }));
        if (endpoint === "/api/agents")
          return json([
            {
              backend: "codex",
              enabled: true,
              installed: true,
              catalog: { models: [], commands },
            },
          ]);
        if (endpoint === "/api/permissions")
          return json({
            backend: "codex",
            default: "ask",
            modes: [{ id: "ask", available: true }],
            commands,
          });
        const chat = chats.find((chat) =>
          endpoint.startsWith(`/api/chats/${encodeURIComponent(chat.id)}`),
        );
        if (endpoint.endsWith("/messages") && request.method() === "POST") {
          const message = request.postDataJSON().message;
          submissions.push(message);
          Object.assign(chat, fixture(chat.id, [...messages, message]));
          chat.updated_at++;
          return json(chat);
        }
        if (endpoint.endsWith("/events"))
          return route.fulfill({
            contentType: "text/event-stream",
            body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n`,
          });
        if (endpoint.endsWith("/workspace"))
          return json({ path: "/work", kind: "main", available: true });
        if (chat && endpoint === `/api/chats/${encodeURIComponent(chat.id)}`)
          return json(chat);
        return json({ path: ".", entries: [], truncated: false });
      });
      await page.goto(
        `http://127.0.0.1:${server.httpServer.address().port}/?chat=${encodeURIComponent(chats[0].id)}`,
      );
      const composer = page.locator("#chat-message");
      await page.getByText("Answer to Latest input", { exact: true }).waitFor();
      const expectValue = (expected) =>
        page.waitForFunction(
          (expected) =>
            document.getElementById("chat-message").value === expected,
          expected,
        );
      const caret = (start, end = start) =>
        composer.evaluate(
          (element, [start, end]) => element.setSelectionRange(start, end),
          [start, end],
        );

      // Walk back without wrapping and forward to the original draft/selection.
      await composer.fill("Unsent draft");
      await caret(4);
      for (const expected of [...messages].reverse()) {
        await composer.press("ArrowUp");
        await expectValue(expected);
      }
      await composer.press("ArrowUp");
      await expectValue(messages[0]);
      for (const expected of [...messages.slice(1), "Unsent draft"]) {
        await caret((await composer.inputValue()).length);
        await composer.press("ArrowDown");
        await expectValue(expected);
      }
      assert.deepEqual(
        await composer.evaluate((element) => [
          element.selectionStart,
          element.selectionEnd,
        ]),
        [4, 4],
      );
      await composer.press("ArrowDown");
      await expectValue("Unsent draft");
      await composer.fill("");
      await composer.press("ArrowUp");
      await expectValue("Latest input");
      await composer.press("ArrowDown");
      await expectValue("");

      // Editing a recalled message starts a new draft and preserves the edit.
      await composer.press("ArrowUp");
      await composer.press("End");
      await composer.press("!");
      await composer.press("ArrowDown");
      await expectValue("Latest input!");
      await composer.press("ArrowUp");
      await expectValue("Latest input");
      await composer.press("ArrowDown");
      await expectValue("Latest input!");

      // Cursor movement, selection, modifiers and IME leave the text intact.
      for (const text of [
        "First line\nLast line",
        "Wrapped words ".repeat(100),
      ]) {
        await composer.fill(text);
        await composer.press("ArrowUp");
        await expectValue(text);
        await composer.press("ArrowDown");
        await expectValue(text);
      }
      await composer.fill("Selection");
      await caret(0, 4);
      await composer.press("ArrowUp");
      await expectValue("Selection");
      for (const shortcut of [
        "Shift+ArrowUp",
        "Alt+ArrowUp",
        "Control+ArrowUp",
        "Meta+ArrowUp",
      ]) {
        await caret(0);
        await composer.press(shortcut);
        await expectValue("Selection");
      }
      await composer.fill("");
      for (const extra of [{ isComposing: true }, { keyCode: 229 }]) {
        await composer.evaluate(
          (element, extra) =>
            element.dispatchEvent(
              new KeyboardEvent("keydown", {
                key: "ArrowUp",
                bubbles: true,
                ...extra,
              }),
            ),
          extra,
        );
        await expectValue("");
      }

      // Slash menu navigation takes priority; recalled commands keep browsing.
      await composer.fill("/");
      await page.locator("#slash-command-menu").waitFor();
      await composer.press("ArrowDown");
      await expectValue("/");
      assert.equal(
        await composer.getAttribute("aria-activedescendant"),
        "slash-command-1",
      );
      await composer.fill("");
      await composer.press("ArrowUp");
      await composer.press("ArrowUp");
      await expectValue("/review");
      assert.equal(await composer.getAttribute("aria-expanded"), "false");
      await composer.press("ArrowUp");
      await expectValue(messages[1]);

      // Sending a recalled input resets browsing; chat navigation resets scope.
      await composer.fill("");
      await composer.press("ArrowUp");
      const accepted = page.waitForResponse((response) =>
        new URL(response.url()).pathname.endsWith("/messages"),
      );
      await composer.press("Enter");
      await accepted;
      await expectValue("");
      await composer.press("ArrowUp");
      await expectValue("Latest input");
      assert.deepEqual(submissions, ["Latest input"]);
      await page
        .locator(".nav-chat")
        .filter({ hasText: "Chat B" })
        .first()
        .click();
      await page
        .getByText("Answer to Other conversation", { exact: true })
        .waitFor();
      await composer.press("ArrowUp");
      await expectValue("Other conversation");
      await composer.press("ArrowDown");
      await expectValue("");
      assert.deepEqual(errors, []);
      console.log({
        shell: desktop ? "desktop" : "web",
        historyRecall: true,
        draftRestored: true,
        editing: true,
        cursorAndIME: true,
        slashMenu: true,
        sendAndNavigation: true,
      });
    } finally {
      await context.close();
    }
  }
} finally {
  if (browser) await browser.close();
  for (const dispose of cleanup.reverse()) await dispose();
}
