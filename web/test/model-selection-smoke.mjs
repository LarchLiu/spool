// Exercise the real composer, including popover unmounts between selections.
// Run: sh tools/run.sh model-selection-browser bun web/test/model-selection-smoke.mjs
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer, mockDesktopNavigation } from "./vite-fixture.mjs";

requireRunTemp();
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const require = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__model_selection__.cjs"));
const { chromium, webkit } = require("playwright");
const cleanup = [];
const server = await createTestViteServer({ after: fn => cleanup.push(fn) }, {
  configFile: false,
  root: path.join(root, "web"),
  plugins: [react(), mockDesktopNavigation()],
  server: { host: "127.0.0.1", port: 0, strictPort: true },
});
const chat = {
  id: "chat", title: "Model selection", project_id: "project", user_id: "owner",
  backend: "claude", model: "shared-id", reasoning_effort: "high",
  permission_mode: "ask", status: "completed", updated_at: 1,
  turns: [], transcript: { parts: [] },
};
const agents = [
  {
    backend: "codex", enabled: true, installed: true,
    catalog: { defaultModel: "shared-id", defaultEffort: "high", models: [{
      model: "shared-id", displayName: "Codex Model", defaultReasoningEffort: "medium",
      supportedReasoningEfforts: ["low", "medium", "high", "xhigh"].map(reasoningEffort => ({ reasoningEffort })),
    }] },
  },
  {
    backend: "claude", enabled: true, installed: true,
    catalog: { defaultModel: "shared-id", defaultEffort: "", models: [
      {
        model: "shared-id", displayName: "Claude Model", defaultReasoningEffort: "",
        supportedReasoningEfforts: ["low", "high"].map(reasoningEffort => ({ reasoningEffort })),
      },
      { model: "fixed", displayName: "Claude Fixed", defaultReasoningEffort: "", supportedReasoningEfforts: [] },
    ] },
  },
];

try {
  await server.listen();
  for (const [engine, type, launch] of [
    ["chromium", chromium, { channel: "chrome" }],
    ["webkit", webkit, { executablePath: process.env.WEBKIT_BIN }],
  ]) {
    const browser = await type.launch({ ...launch, headless: true });
    try {
      for (const [surface, width] of [["desktop", 1280], ["web", 1280], ["mobile", 390]]) {
        const page = await browser.newPage({ viewport: { width, height: 800 }, serviceWorkers: "block" });
        try {
          const errors = [], submissions = [], permissionReads = [];
          page.on("pageerror", error => errors.push(String(error)));
          await page.addInitScript(desktop => {
            localStorage.setItem("spool.ui-preferences.v1", JSON.stringify({ language: "en", theme: "light" }));
            localStorage.setItem("spool.panel-layout.v1", JSON.stringify({ leftOpen: false, rightOpen: false }));
            if (desktop) { window.spoolDesktop = true; window.mygo = { platform: "darwin" }; }
            window.WebSocket = class extends EventTarget { readyState = 0; send() {} close() {} };
          }, surface === "desktop");
          await page.route("**/api/**", route => {
            const endpoint = new URL(route.request().url()).pathname;
            let value = { path: ".", entries: [], truncated: false };
            if (endpoint === "/api/me") value = { user: { id: "owner", nickname: "Owner" }, csrf: "fixture" };
            else if (endpoint === "/api/version") value = { version: "fixture" };
            else if (endpoint === "/api/profiles") value = { revision: 1, people: [{ id: "owner", nickname: "Owner" }] };
            else if (endpoint === "/api/federation/agents") value = [];
            else if (endpoint === "/api/navigation") value = { projects: [{ id: "project", name: "Project", can_create: true }], chats: [chat] };
            else if (endpoint === "/api/agents") value = agents;
            else if (endpoint === "/api/permissions") {
              const backend = new URL(route.request().url()).searchParams.get("backend") || "claude";
              permissionReads.push(backend);
              value = { backend, default: "ask", current: "ask", modes: [{ id: "ask", available: true }] };
            }
            else if (endpoint === "/api/chats/chat/messages") {
              submissions.push(route.request().postDataJSON()); value = chat;
            }
            else if (endpoint === "/api/chats/chat") value = chat;
            else if (endpoint.endsWith("/workspace")) value = { path: "/work", kind: "main", available: true };
            else if (endpoint.endsWith("/events")) return route.fulfill({
              contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n`,
            });
            return route.fulfill({ json: value });
          });
          await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/?chat=chat`);
          await page.waitForFunction(() => {
            const picker = document.querySelector(".picker-summary");
            return picker && !picker.disabled && picker.textContent.includes("Claude Model") && picker.textContent.includes("High");
          });
          const picker = page.locator(".picker-summary");
          const slider = page.getByRole("slider", { name: "Reasoning level" });
          const choose = name => page.getByRole("button", { name, exact: true }).click();
          await picker.click();
          await choose("Claude Model");
          assert.equal(await slider.inputValue(), "2");
          await choose("Codex Model");
          await slider.focus(); await page.keyboard.press("ArrowLeft");
          assert.equal(await slider.getAttribute("aria-valuetext"), "Medium");
          await page.keyboard.press("Escape");
          await picker.click();
          await choose("Claude Model");
          assert.equal(await slider.getAttribute("aria-valuetext"), "High");
          await choose("Claude Fixed");
          assert.equal(await slider.count(), 0);
          await choose("Claude Model");
          assert.equal(await slider.getAttribute("aria-valuetext"), "High");
          await slider.focus(); await page.keyboard.press("Home");
          assert.equal(await slider.getAttribute("aria-valuetext"), "Default");
          await choose("Codex Model");
          assert.equal(await slider.getAttribute("aria-valuetext"), "Medium");
          await page.keyboard.press("Escape");
          await picker.click();
          await choose("Claude Model");
          assert.equal(await slider.inputValue(), "0");
          assert.equal(await slider.getAttribute("aria-valuetext"), "Default");
          await slider.focus(); await page.keyboard.press("End");
          await choose("Codex Model"); await choose("Claude Model");
          assert.equal(await slider.getAttribute("aria-valuetext"), "High");
          await page.keyboard.press("Escape");
          await page.locator("#chat-message").fill("Use the remembered reasoning level");
          await page.waitForFunction(() => !document.querySelector(".send")?.disabled, null, { timeout: 10_000 }).catch(async error => {
            console.error({ engine, surface, permissionReads, state: await page.evaluate(() => ({
              draft: document.querySelector("#chat-message")?.value,
              permission: document.querySelector(".permission-summary")?.outerHTML,
              model: document.querySelector(".picker-summary")?.textContent,
              alerts: [...document.querySelectorAll('[role="alert"]')].map(element => element.textContent),
            })) });
            throw error;
          });
          const accepted = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/messages"));
          await page.locator(".send").click(); await accepted;
          assert.equal(submissions.length, 1);
          assert.equal(submissions[0].reasoning_effort, "high");
          assert.deepEqual(errors, []);
          console.log(`${engine}/${surface}: remembered Claude/Codex effort, menu reopening, explicit default and send payload passed`);
        } finally { await page.close(); }
      }
    } finally { await browser.close(); }
  }
} finally {
  for (const stop of cleanup.reverse()) await stop();
}
