// Exercise the production App with synthetic chats and delayed HTTP acceptance.
// Run with: sh tools/run.sh message-delivery bun web/test/message-delivery-smoke.mjs
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { mkdir } from "node:fs/promises";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const require = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__delivery__.cjs"));
const { chromium } = require("playwright");
const cleanup = [];
const server = await createTestViteServer({ after: fn => cleanup.push(fn) }, {
  configFile: false,
  root: path.join(root, "web"),
  plugins: [react()],
  server: { host: "127.0.0.1", port: 0 },
});
let browser;

function fixture(id, running = false) {
  return {
    id, project_id: "project", user_id: "owner", backend: "codex",
    title: id === "chat-A" ? "Chat A" : "Running B", can_control: true,
    status: running ? "running" : "ready", permission_mode: "ask", updated_at: 1,
    transcript: { parts: running ? [{ role: "user", kind: "text", text: "B is working" }] : [] },
    turns: running ? [{ id: "turn-B", status: "running", started_at: Date.now(), part_start: 0, input_end: 1, part_end: 1 }] : [],
  };
}

try {
  await server.listen();
  browser = await chromium.launch({ channel: "chrome", headless: true });
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  for (const scenario of ["queue", "steer", "same-chat", "queue-stream", "steer-stream", "steer-pending-error", "queue-error", "steer-error", "queue-attachment-error", "steer-attachment-error", "queue-navigation", "edit", "edit-conflict", "edit-error", "edit-navigation", "edit-limit"]) {
    const editing = scenario.startsWith("edit");
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, serviceWorkers: "block" });
    let releaseFirst;
    const acceptance = new Promise(resolve => { releaseFirst = resolve; });
    let releaseDelivery;
    const deliveryAcceptance = new Promise(resolve => { releaseDelivery = resolve; });
    let delivered;
    try {
      const page = await context.newPage();
      const errors = [];
      const requests = [];
      page.on("pageerror", error => errors.push(String(error)));
      await page.addInitScript(() => {
        localStorage.setItem("spool.ui-preferences.v1", JSON.stringify({ language: "en", theme: "light" }));
        // Navigation uses the REST fallback. No live server or provider is used.
        window.WebSocket = class extends EventTarget {
          readyState = 0;
          send() {}
          close() { this.readyState = 3; }
        };
        const Source = window.EventSource;
        window.__chatSources = [];
        window.EventSource = class extends Source {
          constructor(url, options) { super(url, options); window.__chatSources.push(this); }
        };
      });
      const a = fixture("chat-A"), b = fixture("chat-B", true);
      if (editing) b.queued_messages = [{ id: "queued", message: "  Queued original\nmessage  ", attachments: ["notes.txt"], created_at: 2 }];
      const accepted = {
        ...a, status: "running", updated_at: 2,
        transcript: { parts: [{ role: "user", kind: "text", text: "First in A" }] },
        turns: [{ id: "turn-A", status: "running", started_at: Date.now(), part_start: 0, input_end: 1, part_end: 1 }],
      };
      await page.route("**/api/**", async route => {
        const request = route.request(), url = new URL(request.url()), endpoint = url.pathname;
        const json = (value, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
        if (endpoint === "/api/me") return json({ user: { id: "owner", nickname: "Owner" }, csrf: "fixture" });
        if (endpoint === "/api/version") return json({ version: "fixture" });
        if (endpoint === "/api/profiles") return json({ revision: 1, people: [{ id: "owner", nickname: "Owner" }] });
        if (endpoint === "/api/navigation") return json({ projects: [{ id: "project", name: "Project", can_create: true }], chats: [a, b] });
        if (endpoint === "/api/agents") return json([{ backend: "codex", enabled: true, installed: true, catalog: { models: [] } }]);
        if (endpoint === "/api/permissions") return json({ backend: "codex", default: "ask", modes: [{ id: "ask", available: true }] });
        if (request.method() === "POST" && endpoint.endsWith("/queue")) {
          const payload = request.postDataJSON();
          requests.push({ endpoint, ...payload });
          if (payload.action === "draft") return json({ message: b.queued_messages[0].message, files: [{ name: "notes.txt", media_type: "text/plain", data: Buffer.from("Original attachment\n").toString("base64") }] });
          if (scenario === "edit-conflict") return json({ error: "This message is no longer queued" }, 409);
          if (scenario === "edit-error") return json({ error: "Edit request failed" }, 500);
          if (scenario === "edit-navigation") await acceptance;
          b.queued_messages = []; b.updated_at = 3;
          return json(b);
        }
        if (request.method() === "POST" && endpoint.endsWith("/messages")) {
          const form = request.headers()["content-type"]?.startsWith("multipart/")
            ? await new Response(request.postDataBuffer(), { headers: { "Content-Type": request.headers()["content-type"] } }).formData() : null;
          const payload = form ? Object.fromEntries([...form.entries()].filter(([, value]) => typeof value === "string")) : request.postDataJSON();
          const attachments = form ? form.getAll("files").map(file => file.name) : [];
          requests.push({ endpoint, ...payload });
          if (!payload.delivery) {
            await acceptance;
            return json(accepted, 202);
          }
          const current = endpoint.includes("chat-B") ? b : accepted;
          delivered = structuredClone(current);
          delivered.updated_at = 3;
          if (payload.delivery === "queue") delivered.queued_messages = [{ id: "server-queued", client_message_id: payload.client_message_id, message: payload.message, attachments, created_at: 3 }];
          else {
            delivered.transcript.parts[0].timestamp = 1;
            delivered.transcript.parts.push({ role: "user", kind: "text", text: payload.message, client_message_id: payload.client_message_id, timestamp: 3, ...(scenario === "steer-pending-error" ? { pending: true } : {}) });
            delivered.turns[0].part_end = delivered.transcript.parts.length;
          }
          await deliveryAcceptance;
          if (scenario.endsWith("error")) return json({ error: "Delivery failed" }, 500);
          return json(delivered, 202);
        }
        if (endpoint.endsWith("/events")) {
          const chat = endpoint.includes("chat-B") ? b : a;
          return route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n` });
        }
        if (endpoint === "/api/chats/chat-A") return json(a);
        if (endpoint === "/api/chats/chat-B") return json(b);
        if (endpoint.endsWith("/workspace")) return json({ path: "/work", kind: "main", available: true });
        return json({ path: ".", entries: [], truncated: false });
      });
      await page.goto(origin + "/?chat=chat-A");
      const composer = page.locator("#chat-message");
      if (editing) {
        await page.locator(".nav-chat").filter({ hasText: "Running B" }).first().click();
        await page.getByText("B is working", { exact: true }).waitFor();
        await composer.fill("Existing draft");
        if (scenario === "edit-limit") {
          await page.locator("input[type=file]").setInputFiles(Array.from({ length: 6 }, (_, i) => ({ name: `existing-${i}.txt`, mimeType: "text/plain", buffer: Buffer.from("existing") })));
          await page.getByText("existing-5.txt", { exact: true }).waitFor();
        }
        const edited = page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/queue") && response.request().postDataJSON().action === (scenario === "edit-limit" ? "draft" : "edit"), { timeout: 10000 }).catch(async error => {
          throw new Error(`${scenario}: ${error.message}; requests=${JSON.stringify(requests)}; composer=${await composer.inputValue()}; ui=${(await page.locator(".composer-wrap").innerText()).slice(-1200)}; errors=${JSON.stringify(errors)}`);
        });
        const editPosted = scenario === "edit-navigation" ? page.waitForRequest(request => new URL(request.url()).pathname.endsWith("/queue") && request.postDataJSON().action === "edit") : null;
        await page.getByRole("button", { name: "Queued message actions" }).click();
        await page.getByRole("menuitem", { name: "Edit message" }).click();
        if (scenario === "edit-navigation") {
          await editPosted;
          await page.locator(".nav-chat").filter({ hasText: "Chat A" }).first().click();
          releaseFirst(); await edited;
          await page.waitForFunction(() => !document.querySelector("#chat-message").disabled);
          await page.locator(".nav-chat").filter({ hasText: "Running B" }).first().click();
        } else await edited;
        if (["edit-conflict", "edit-error", "edit-limit"].includes(scenario)) {
          await page.getByText(scenario === "edit-conflict" ? "This message is no longer queued" : scenario === "edit-error" ? "Edit request failed" : "Attach at most 6 files, up to 16 MB total", { exact: true }).waitFor();
          assert.equal(await composer.inputValue(), "Existing draft");
          assert.equal(await page.locator(".queued-message").count(), 1);
          assert.deepEqual(requests.map(request => request.action), scenario === "edit-limit" ? ["draft"] : ["draft", "edit"]);
        } else {
          const expected = "  Queued original\nmessage  " + (scenario === "edit-navigation" ? "" : "\n\nExisting draft");
          await page.waitForFunction(value => document.querySelector("#chat-message").value === value, expected);
          assert.equal(await page.locator(".queued-message").count(), 0);
          await page.locator(".composer-wrap").getByText("notes.txt", { exact: true }).waitFor();
          assert.deepEqual(requests.map(request => request.action), ["draft", "edit"]);
        }
        assert.deepEqual(errors, []);
        console.log(`${scenario}: queued draft preserved through edit outcome`);
        continue;
      }
      await composer.fill("First in A");
      const firstPosted = page.waitForRequest(request => request.url().includes("/chat-A/messages"));
      await page.getByRole("button", { name: "Send message", exact: true }).click();
      await firstPosted;
      if (scenario !== "same-chat") {
        await page.locator(".nav-chat").filter({ hasText: "Running B" }).first().click();
        await page.getByText("B is working", { exact: true }).waitFor();
      }
      const action = page.locator(".composer-actions .send");
      const assertAction = async (label, type) => {
        assert.equal(await action.count(), 1, "the composer keeps a single send/stop button");
        assert.equal(await action.getAttribute("aria-label"), label);
        assert.equal(await action.getAttribute("type"), type);
      };
      await assertAction("Stop response", "button");
      const originalAction = await action.elementHandle();
      if (scenario === "queue") {
        await composer.fill(" \n\t ");
        await assertAction("Stop response", "button");
      }
      const followup = `Follow-up for ${scenario === "same-chat" ? "A" : "B"}`;
      await composer.fill(followup);
      await assertAction("Send message", "submit");
      assert.equal(await action.evaluate((button, original) => button === original, originalAction), true, "typing reuses the original stop button");
      await composer.fill("");
      await assertAction("Stop response", "button");
      await composer.fill(followup);
      await assertAction("Send message", "submit");
      const delivery = scenario.startsWith("steer") ? "steer" : "queue";
      if (scenario.includes("attachment")) {
        releaseFirst();
        await page.waitForFunction(() => !document.querySelector(".attachment-add").disabled);
        await page.locator("input[type=file]").setInputFiles({ name: "retained.png", mimeType: "image/png", buffer: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a/nkAAAAASUVORK5CYII=", "base64") });
        await page.getByRole("button", { name: "Remove retained.png", exact: true }).waitFor();
      }
      const matchesDelivery = request => request.url().includes("/messages") && request.method() === "POST" &&
        (request.headers()["content-type"]?.startsWith("multipart/") ? request.postData().includes(`\r\n\r\n${delivery}\r\n`) : request.postDataJSON().delivery === delivery);
      const posted = page.waitForRequest(matchesDelivery, { timeout: 5000 });
      const answered = page.waitForResponse(response => matchesDelivery(response.request()), { timeout: 5000 });
      if (delivery === "queue" && scenario !== "same-chat") await action.click();
      else await composer.press(delivery === "steer" ? "Control+Enter" : "Enter");
      await assertAction("Stop response", "button");
      await originalAction.dispose();
      const submittedMessage = () => delivery === "queue" ? page.locator(".queued-message").filter({ hasText: followup }) : page.locator(".user-message").filter({ hasText: followup });
      await submittedMessage().getByText("Sending…", { exact: true }).waitFor();
      assert.equal(await submittedMessage().count(), 1, "the message is visible before HTTP acceptance");
      if (delivery === "steer") {
        const order = await submittedMessage().locator(".message-footer").evaluate(footer => {
          const status = footer.querySelector(".message-delivery-status"), author = footer.querySelector(".message-author"), time = footer.querySelector("time");
          return !!status && !!author && !!time && !!(status.compareDocumentPosition(author) & Node.DOCUMENT_POSITION_FOLLOWING) && !!(author.compareDocumentPosition(time) & Node.DOCUMENT_POSITION_FOLLOWING);
        });
        assert.equal(order, true, "sending comes before the name and time in the same footer");
      }
      if (scenario === "same-chat") {
        await page.waitForFunction(() => document.querySelector("#chat-message").value === "");
        assert.equal(requests.length, 1, "same-chat follow-up must wait for the first acceptance");
        releaseFirst();
      }
      const request = await posted;
      if (scenario.includes("stream") || scenario === "steer-pending-error") {
        await page.evaluate(value => {
          window.__chatSources.filter(source => source.url.includes(`/chats/${value.id}/`)).at(-1).dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify(value) }));
        }, delivered);
        if (scenario === "steer-pending-error") await submittedMessage().getByText("Sending…", { exact: true }).waitFor();
        else await page.waitForFunction(() => !document.querySelector(".message-delivery-status"));
        assert.equal(await submittedMessage().count(), 1, "a server push replaces the provisional message");
      }
      if (["queue", "steer"].includes(scenario)) {
        await page.evaluate(() => document.documentElement.dataset.theme = "dark");
        await mkdir(path.join(root, "build/artifacts/message-delivery"), { recursive: true });
        await page.screenshot({ path: path.join(root, `build/artifacts/message-delivery/web-${scenario}-sending.png`) });
      }
      if (scenario === "queue-navigation") {
        await page.locator(".nav-chat").filter({ hasText: "Chat A" }).first().click();
        await composer.fill("Other chat draft");
        assert.equal(await page.locator(".message-delivery-status").count(), 0);
      }
      releaseDelivery();
      await answered;
      if (scenario.endsWith("error")) {
        await submittedMessage().getByText("Send failed", { exact: true }).waitFor();
        if (scenario === "steer-pending-error") {
          await page.evaluate(value => window.__chatSources.filter(source => source.url.includes(`/chats/${value.id}/`)).at(-1).dispatchEvent(new MessageEvent("snapshot", { data: JSON.stringify({ ...value, updated_at: 4 }) })), b);
          await page.locator(".local-delivery").waitFor();
        }
        if (delivery === "queue") {
          assert.equal(await submittedMessage().getByRole("button", { name: "Delete queued message", exact: true }).count(), 1);
          await submittedMessage().getByRole("button", { name: "Queued message actions", exact: true }).click();
          await page.getByRole("menuitem", { name: "Edit message", exact: true }).click();
        } else await submittedMessage().getByRole("button", { name: "Edit message", exact: true }).click();
        assert.equal(await composer.inputValue(), followup);
        if (scenario.includes("attachment")) {
          await page.getByRole("button", { name: "Remove retained.png", exact: true }).waitFor();
          assert.equal(await page.locator(".composer .attachment-card img").evaluate(image => image.complete && image.naturalWidth > 0), true, "the failed message keeps its live local preview");
        }
        assert.equal(await page.locator(".message-delivery-status").count(), 0);
      } else {
        await page.waitForFunction(() => !document.querySelector(".message-delivery-status"));
        if (scenario !== "queue-navigation") assert.equal(await submittedMessage().count(), 1);
        else assert.equal(await composer.inputValue(), "Other chat draft");
      }
      const target = scenario === "same-chat" ? "chat-A" : "chat-B";
      assert.equal(new URL(request.url()).pathname, `/api/chats/${target}/messages`);
      assert.equal(requests[1].message, followup);
      assert.equal(requests.length, 2);
      releaseFirst();
      if (scenario === "queue") {
        await page.waitForFunction(() => !document.querySelector(".attachment-add").disabled);
        await page.locator("input[type=file]").setInputFiles({ name: "followup.txt", mimeType: "text/plain", buffer: Buffer.from("Attachment only") });
        const remove = page.getByRole("button", { name: "Remove followup.txt", exact: true });
        await remove.waitFor();
        await assertAction("Send message", "submit");
        assert.equal(await action.isEnabled(), true);
        await remove.click();
        await assertAction("Stop response", "button");
      }
      assert.deepEqual(errors, []);
      console.log(`${scenario}: follow-up targeted ${target}`);
    } finally {
      releaseFirst();
      releaseDelivery();
      await context.close();
    }
  }
} finally {
  if (browser) await browser.close();
  for (const close of cleanup.reverse()) await close();
}
