// Run against built frontend assets with CHROME_BIN and an owned build/tmp TMPDIR.
// Exercises permission failures using synthetic data; no provider or real user data.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
assert.ok(process.env.CHROME_BIN, "Set CHROME_BIN to headless Chromium");
assert.ok(process.env.TMPDIR?.startsWith(join(root, "build/tmp/")));
const scratch = await mkdtemp(join(process.env.TMPDIR, "permission-browser-"));
const output = join(root, "build/artifacts/permission-discovery");
const reports = new Map();
let scenario, requests, messages, failures;
const chat = {
  id: "chat",
  project_id: "project",
  user_id: "owner",
  title: "Permission recovery",
  backend: "codex",
  status: "ready",
  can_control: true,
  updated_at: 1,
  permission_mode: "ask",
  turns: [],
  transcript: { parts: [] },
};
const probe = `
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check) {
  for (let i = 0; i < 600; i++) { const result = await check(); if (result) return result; await pause(25); }
  throw new Error('Timed out: ' + check.toString());
}
const errors = [];
window.addEventListener('error', event => errors.push(event.message));
window.addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
(async () => {
  const report = {};
  const scenario = new URL(location.href).searchParams.get('scenario');
  const stats = () => fetch('/smoke/stats').then(response => response.json());
  try {
    const input = await until(() => document.querySelector('#chat-message:not(:disabled)'));
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Keep my draft');
    input.dispatchEvent(new Event('input', { bubbles: true }));
    const send = document.querySelector('.send');
    if (scenario === 'unavailable') {
      await until(() => document.querySelector('.permission-status')?.textContent.includes('Permission mode is unavailable'));
      report.blockedUnavailableMode = send.disabled;
      document.querySelector('.permission-status button').click();
      await until(() => document.querySelector('.permission-option'));
      [...document.querySelectorAll('.permission-option')].find(button => button.textContent.includes('Ask for approval')).click();
    } else {
      await until(() => document.querySelector('.permission-status[role="alert"] button'));
      report.failureIsVisibleAndBlocked = send.disabled && !input.disabled;
      if (scenario === 'manual') {
        await until(async () => (await stats()).requests === 3 && document.querySelector('.permission-status[role="alert"]'));
        await pause(3500);
        report.retriesAreBounded = (await stats()).requests === 3;
        await fetch('/smoke/recover', { method: 'POST' });
        document.querySelector('.permission-status button').click();
      } else if (scenario === 'denied') {
        await pause(4500);
        report.doesNotRetryAuthorizationFailure = (await stats()).requests === 1 && send.disabled;
      }
    }
    if (scenario !== 'denied') {
      await until(() => !send.disabled);
      report.recoveredWithoutReload = !document.querySelector('.permission-status') && input.value === 'Keep my draft';
      report.doesNotSubmitOnRecovery = (await stats()).messages === 0;
      if (scenario === 'auto') {
        send.click();
        await until(() => input.value === 'Keep my draft' && document.querySelector('.error')?.textContent.includes('Permission discovery'));
        await until(async () => (await stats()).requests >= 4 && !send.disabled);
        await pause(1000);
        report.failedSendRefreshesPermissionsAndKeepsDraft = input.value === 'Keep my draft' && !send.disabled;
        report.failedMessageIsNotResent = (await stats()).messages === 1;
      }
    }
    report.noJavascriptErrors = errors.length === 0;
    report.noPageOverflow = document.documentElement.scrollWidth <= innerWidth;
  } catch (error) { report.error = String(error); report.errors = errors; }
  await fetch('/smoke/report', { method: 'POST', body: JSON.stringify(report) });
})();
`;

const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost");
  const path = url.pathname;
  const json = (data, status = 200) => {
    res.writeHead(status, { "Content-Type": "application/json" });
    res.end(JSON.stringify(data));
  };
  if (path === "/smoke/probe.js") {
    res.setHeader("Content-Type", "text/javascript");
    return res.end(probe);
  }
  if (path === "/smoke/stats") return json({ requests, messages });
  if (path === "/smoke/recover") {
    failures = 0;
    return json({ ok: true });
  }
  if (path === "/smoke/report") {
    let body = "";
    for await (const chunk of req) body += chunk;
    reports.set(scenario, JSON.parse(body));
    return json({ ok: true });
  }
  if (path === "/api/me")
    return json({
      user: { id: "owner", nickname: "Owner" },
      csrf: "fixture",
      preferences: { language: "en", theme: "light" },
    });
  if (path === "/api/version") return json({ version: "fixture" });
  if (path === "/api/profiles")
    return json({ revision: 1, people: [{ id: "owner", nickname: "Owner" }] });
  if (path === "/api/navigation")
    return json({
      projects: [
        { id: "project", name: "Permission recovery", can_create: true },
      ],
      chats: [chat],
    });
  if (path === "/api/agents")
    return json([
      {
        backend: "codex",
        enabled: true,
        installed: true,
        catalog: { models: [], defaultModel: "" },
      },
    ]);
  if (path === "/api/permissions") {
    requests++;
    if (scenario === "denied") return json({ error: "Not authorized" }, 403);
    if (failures-- > 0)
      return json(
        {
          error: "Permission discovery is unavailable",
          code: "permission_discovery_unavailable",
        },
        503,
      );
    return json({
      backend: "codex",
      default: "ask",
      current: chat.permission_mode,
      modes: [
        { id: "ask", available: true },
        { id: "auto", available: false },
      ],
    });
  }
  if (path === "/api/chats/chat/messages") {
    for await (const _ of req) {
      /* Drain the synthetic request. */
    }
    messages++;
    failures = 1;
    return json(
      {
        error: "Permission discovery is unavailable",
        code: "permission_discovery_unavailable",
      },
      503,
    );
  }
  if (path === "/api/chats/chat") return json(chat);
  if (path.endsWith("/events")) {
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    return res.end("event: snapshot\ndata: " + JSON.stringify(chat) + "\n\n");
  }
  if (path.endsWith("/workspace"))
    return json({ path: "/work", kind: "main", available: true });
  if (path.startsWith("/api/"))
    return json({ path: ".", entries: [], truncated: false });
  try {
    assert.ok(!path.includes(".."));
    const file = path === "/" ? "index.html" : path.slice(1);
    let body = await readFile(join(root, "frontend", file));
    if (file === "index.html")
      body = Buffer.from(
        body
          .toString()
          .replace("</body>", '<script src="/smoke/probe.js"></script></body>'),
      );
    res.setHeader(
      "Content-Type",
      {
        ".html": "text/html",
        ".js": "text/javascript",
        ".css": "text/css",
        ".png": "image/png",
        ".svg": "image/svg+xml",
      }[extname(file)] || "application/octet-stream",
    );
    res.end(body);
  } catch {
    res.writeHead(404);
    res.end();
  }
});

try {
  await mkdir(output, { recursive: true });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  for (const test of ["auto", "manual", "denied", "unavailable"]) {
    scenario = test;
    requests = messages = 0;
    failures = test === "manual" ? Infinity : test === "auto" ? 1 : 0;
    chat.permission_mode = test === "unavailable" ? "auto" : "ask";
    await new Promise((resolve, reject) => {
      const child = spawn(
        process.env.CHROME_BIN,
        [
          "--headless",
          "--disable-gpu",
          "--no-first-run",
          "--no-default-browser-check",
          "--disable-background-networking",
          "--disable-component-update",
          "--disable-sync",
          "--disable-extensions",
          "--disable-breakpad",
          `--user-data-dir=${join(scratch, test)}`,
          "--window-size=500,844",
          "--force-device-scale-factor=1",
          "--virtual-time-budget=25000",
          `--screenshot=${join(output, test + ".png")}`,
          `http://127.0.0.1:${server.address().port}/?scenario=${test}`,
        ],
        { stdio: ["ignore", "pipe", "pipe"] },
      );
      let diagnostics = "";
      for (const stream of [child.stdout, child.stderr])
        stream.on("data", (data) => {
          diagnostics = (diagnostics + data).slice(-4000);
        });
      const timeout = setTimeout(() => child.kill("SIGKILL"), 30000);
      child.once("error", (error) => {
        clearTimeout(timeout);
        reject(error);
      });
      child.once("close", async (code) => {
        clearTimeout(timeout);
        await writeFile(join(output, test + ".log"), diagnostics);
        code === 0
          ? resolve()
          : reject(new Error(`Chrome exited ${code}: ${diagnostics}`));
      });
    });
    await writeFile(
      join(output, "browser-report.json"),
      JSON.stringify(Object.fromEntries(reports), null, 2),
    );
    const report = reports.get(test);
    assert.ok(report, `No ${test} report`);
    assert.equal(report.error, undefined, JSON.stringify(report));
    for (const [key, value] of Object.entries(report))
      assert.equal(value, true, `${test}: ${key}`);
  }
  console.log(JSON.stringify(Object.fromEntries(reports), null, 2));
} finally {
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
  await rm(scratch, { recursive: true, force: true });
}
