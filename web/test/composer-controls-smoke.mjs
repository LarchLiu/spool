// Actual composer controls across desktop panels and mobile browser widths.
// Run: sh tools/run.sh composer-controls bun web/test/composer-controls-smoke.mjs
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { translator } from "../src/i18n.ts";
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
    "__composer_controls__.cjs",
  ),
);
const { chromium, webkit } = require("playwright");
const output = path.join(root, "build/artifacts/composer-controls");
const cleanup = [];
const server = await createTestViteServer(
  { after: (fn) => cleanup.push(fn) },
  {
    configFile: false,
    root: path.join(root, "web"),
    plugins: [react(), mockDesktopNavigation()],
    server: { host: "127.0.0.1", port: 0, strictPort: true },
  },
);
const chat = {
  id: "chat",
  title: "Composer layout",
  project_id: "project",
  user_id: "owner",
  backend: "codex",
  model: "fixture-model",
  reasoning_effort: "high",
  permission_mode: "ask",
  status: "completed",
  updated_at: 1,
  turns: [],
  transcript: { parts: [] },
};
const model = {
  model: "fixture-model",
  displayName: "GPT-6.1 Sol · quya",
  defaultReasoningEffort: "high",
  supportedReasoningEfforts: [{ reasoningEffort: "high", description: "High" }],
};
try {
  await mkdir(output, { recursive: true });
  await server.listen();
  for (const [engine, type, launch] of [
    ["chromium", chromium, { channel: "chrome" }],
    ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
  ]) {
    if (process.argv.includes("--engine=webkit") && engine !== "webkit")
      continue;
    const browser = await type.launch({ ...launch, headless: true });
    try {
      for (const desktop of [false, true]) {
        for (const locale of ["en", "zh-CN", "ja"]) {
          const page = await browser.newPage({
            viewport: { width: 1280, height: 800 },
            locale,
            colorScheme: "dark",
            // Keep synthetic API requests in Playwright's routing boundary.
            // WebKit bypasses page routes after a service worker takes control.
            serviceWorkers: "block",
          });
          const errors = [],
            saves = [],
            submissions = [];
          const t = translator(locale);
          let permissionMode = "ask",
            fullAccessAvailable = true,
            currentChat = structuredClone(chat);
          page.on("pageerror", (error) => errors.push(String(error)));
          await page.addInitScript(
            ({ desktop, locale }) => {
              localStorage.setItem(
                "spool.ui-preferences.v1",
                JSON.stringify({ language: locale, theme: "dark" }),
              );
              localStorage.setItem(
                "spool.panel-layout.v1",
                JSON.stringify({ leftOpen: false, rightOpen: false }),
              );
              if (desktop) {
                window.spoolDesktop = true;
                window.mygo = { platform: "darwin" };
              }
              window.WebSocket = class extends EventTarget {
                readyState = 0;
                send() {}
                close() {}
              };
              const Source = window.EventSource;
              window.__composerChatSources = [];
              window.EventSource = class extends Source {
                constructor(url, options) {
                  super(url, options);
                  window.__composerChatSources.push(this);
                }
              };
            },
            { desktop, locale },
          );
          await page.route("**/api/**", (route) => {
            const endpoint = new URL(route.request().url()).pathname;
            let value = { path: ".", entries: [], truncated: false };
            if (endpoint === "/api/me")
              value = {
                user: { id: "owner", nickname: "Owner" },
                csrf: "fixture",
              };
            else if (endpoint === "/api/version")
              value = { version: "fixture" };
            else if (endpoint === "/api/profiles")
              value = {
                revision: 1,
                people: [{ id: "owner", nickname: "Owner" }],
              };
            else if (endpoint === "/api/federation/agents") value = [];
            else if (endpoint === "/api/navigation")
              value = {
                projects: [{ id: "project", name: "Project" }],
                chats: [currentChat],
              };
            else if (endpoint === "/api/agents")
              value = [
                {
                  backend: "codex",
                  enabled: true,
                  installed: true,
                  catalog: {
                    defaultModel: model.model,
                    defaultEffort: "high",
                    models: [model],
                  },
                },
              ];
            else if (endpoint === "/api/permissions") {
              if (route.request().method() === "POST") {
                permissionMode = route.request().postDataJSON().mode;
                saves.push(permissionMode);
              }
              value = {
                backend: "codex",
                default: permissionMode,
                current: permissionMode,
                modes: [
                  { id: "ask", available: true },
                  { id: "fullAccess", available: fullAccessAvailable },
                ],
              };
            } else if (endpoint === "/api/chats/chat/messages") {
              const submitted = route.request().postDataJSON();
              submissions.push(submitted);
              currentChat = {
                ...currentChat,
                permission_mode: permissionMode,
                status: "running",
                updated_at: 2,
                turns: [
                  {
                    id: "turn",
                    backend: "codex",
                    user_id: "owner",
                    permission_mode: permissionMode,
                    status: "running",
                    started_at: 2,
                    part_start: 0,
                    input_end: 1,
                    part_end: 1,
                  },
                ],
                transcript: {
                  parts: [
                    { role: "user", kind: "text", text: submitted.message },
                  ],
                },
              };
              return route.fulfill({ status: 202, json: currentChat });
            } else if (endpoint === "/api/chats/chat") value = currentChat;
            else if (endpoint.endsWith("/workspace"))
              value = { path: "/work", kind: "main", available: true };
            else if (endpoint.endsWith("/events"))
              return route.fulfill({
                contentType: "text/event-stream",
                body: `event: snapshot\ndata: ${JSON.stringify(currentChat)}\n\n`,
              });
            return route.fulfill({ json: value });
          });
          try {
            await page.goto(
              `http://127.0.0.1:${server.httpServer.address().port}/?chat=chat`,
            );
            await page.waitForFunction(() => {
              const button = document.querySelector(".picker-summary");
              return (
                button &&
                !button.disabled &&
                button.textContent.includes("GPT-6.1 Sol")
              );
            });
            assert.equal(
              await page.locator("html").getAttribute("lang"),
              locale,
            );
            assert.equal(
              await page.locator("html").getAttribute("data-theme"),
              "dark",
            );
            // Resize only the composer in a wide viewport, then the whole viewport.
            for (const [viewport, width, compact] of [
              [1280, 600, false],
              [1280, 482, true],
              [1280, 328, true],
              [1280, 600, false],
              [390, null, true],
              [1280, 600, false],
            ]) {
              await page.setViewportSize({ width: viewport, height: 800 });
              await page.locator(".composer").evaluate((element, width) => {
                element.style.width = width ? `${width}px` : "";
              }, width);
              const permission = page.locator(".permission-summary");
              const picker = page.locator(".picker-summary");
              assert.equal(
                await permission.locator("span").isVisible(),
                !compact,
              );
              assert.equal(
                await picker.locator("span").first().isVisible(),
                !compact,
              );
              assert.equal(
                await picker.locator("span").last().isVisible(),
                !compact,
              );
              assert.ok(await permission.getAttribute("title"));
              assert.match(await picker.getAttribute("title"), /GPT-6.1 Sol/);
              const bounds = await page
                .locator(".composer-bottom")
                .evaluate((element) => {
                  const surface = element
                    .closest(".composer")
                    .getBoundingClientRect();
                  return [
                    ...element.querySelectorAll("button, a[role=button]"),
                  ].map((button) => {
                    const r = button.getBoundingClientRect();
                    return (
                      r.width === 0 ||
                      (r.left >= surface.left && r.right <= surface.right)
                    );
                  });
                });
              assert.ok(
                bounds.every(Boolean),
                `controls overflow: ${engine}/${desktop}/${locale}/${viewport}/${width}`,
              );
              await permission.click();
              await page.locator(".permission-popover").waitFor();
              await page.keyboard.press("Escape");
              await picker.click();
              await page
                .locator(".model-popover .agent-model-option")
                .waitFor();
              await page.keyboard.press("Escape");
              if (desktop) {
                const screenshot = page.locator(".composer-screenshot > a");
                const hit = await screenshot.boundingBox();
                assert.equal(hit.width, 32);
                assert.equal(hit.height, 32);
                const icon = await screenshot.locator("svg").boundingBox();
                const plus = await page
                  .locator("button.attachment-add svg")
                  .boundingBox();
                assert.ok(icon.width < plus.width);
                await page.locator(".screenshot-mode").click();
                await page.locator(".screenshot-options a").waitFor();
                await page.keyboard.press("Escape");
              } else
                assert.equal(
                  await page.locator(".composer-screenshot").count(),
                  0,
                );
              if (
                locale === "zh-CN" &&
                (width === 328 || (width === 600 && viewport === 1280))
              ) {
                await page.locator("#chat-message").focus();
                await page.locator(".composer").screenshot({
                  path: path.join(
                    output,
                    `${engine}-${desktop ? "desktop" : "web"}-${compact ? "narrow" : "wide"}.png`,
                  ),
                });
              }
            }
            const summary = page.locator(".permission-summary");
            const fullAccess = page.locator(".permission-option").filter({
              has: page.locator("strong", { hasText: t("Full access") }),
            });
            await summary.click();
            await fullAccess.click();
            await page.locator(".bypass-confirmation").waitFor();
            assert.deepEqual(saves, []);
            await page
              .getByRole("button", { name: t("Cancel"), exact: true })
              .click();
            assert.deepEqual(saves, []);
            await fullAccess.click();
            await page
              .getByRole("button", {
                name: t("Enable full access"),
                exact: true,
              })
              .click();
            await page.waitForFunction(
              (label) =>
                document
                  .querySelector(".permission-summary")
                  .title.endsWith(label),
              t("Full access"),
            );
            assert.deepEqual(saves, ["fullAccess"]);
            assert.match(await summary.getAttribute("class"), /danger/);
            await summary.click();
            assert.equal(await fullAccess.getAttribute("aria-pressed"), "true");
            assert.equal(
              await page.locator(".permission-scope").textContent(),
              t("Full access · no sandbox or approval prompts"),
            );
            if (locale === "zh-CN")
              await page.locator(".permission-popover").screenshot({
                path: path.join(
                  output,
                  `${engine}-${desktop ? "desktop" : "web"}-full-access.png`,
                ),
              });
            await page.keyboard.press("Escape");
            await page
              .locator("#chat-message")
              .fill("Explicit full access request");
            const accepted = page.waitForResponse((response) =>
              new URL(response.url()).pathname.endsWith("/messages"),
            );
            await page.locator(".send").click();
            await (await accepted).finished();
            assert.equal(submissions.length, 1);
            assert.equal(submissions[0].permission_mode, "fullAccess");
            await page
              .getByRole("button", { name: t("Stop response"), exact: true })
              .waitFor();
            await page.waitForFunction(
              () => !document.querySelector(".attachment-add").disabled,
            );
            // Finish the accepted turn through the live subscription. Navigation
            // can interrupt unrelated workspace fetches, especially in WebKit.
            currentChat = {
              ...currentChat,
              status: "completed",
              updated_at: 3,
              turns: currentChat.turns.map((turn) => ({
                ...turn,
                status: "completed",
                finished_at: 3,
                part_end: 2,
              })),
              transcript: {
                parts: [
                  ...currentChat.transcript.parts,
                  {
                    role: "assistant",
                    kind: "text",
                    text: "Fixture completed.",
                  },
                ],
              },
            };
            await page.evaluate((snapshot) => {
              window.__composerChatSources
                .filter((source) => source.url.includes("/chats/chat/"))
                .at(-1)
                .dispatchEvent(
                  new MessageEvent("snapshot", {
                    data: JSON.stringify(snapshot),
                  }),
                );
            }, currentChat);
            await page.waitForFunction(
              () => !document.querySelector(".permission-summary").disabled,
            );
            await page
              .getByText("Fixture completed.", { exact: true })
              .waitFor();
            fullAccessAvailable = false;
            await summary.click();
            await page
              .locator(".permission-option")
              .filter({
                has: page.locator("strong", { hasText: t("Ask for approval") }),
              })
              .click();
            await page.waitForFunction(
              () =>
                !document
                  .querySelector(".permission-summary")
                  .classList.contains("danger"),
            );
            assert.deepEqual(saves, ["fullAccess", "ask"]);
            await summary.click();
            await fullAccess.waitFor();
            assert.equal(await fullAccess.isDisabled(), true);
            await page.waitForLoadState("networkidle");
            assert.deepEqual(errors, []);
            console.log({
              engine,
              surface: desktop ? "desktop" : "web",
              locale,
              responsiveLabels: true,
              menus: true,
              bounds: true,
              fullAccessSelectionAndSubmission: true,
              restrictedFullAccessDisabled: true,
            });
          } catch (error) {
            console.error({
              engine,
              desktop,
              locale,
              errors,
              permissionMode,
              saves,
              submissions,
              state: await page
                .locator(".composer")
                .textContent()
                .catch(() => "no composer"),
            });
            await page.screenshot({
              path: path.join(
                output,
                `${engine}-${desktop ? "desktop" : "web"}-${locale}-failure.png`,
              ),
            });
            throw error;
          } finally {
            await page.close();
          }
        }
      }
    } finally {
      await browser.close();
    }
  }
} finally {
  for (const dispose of cleanup.reverse()) await dispose();
}
