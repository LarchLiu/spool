import { requireRunTemp } from "../../tools/test-scratch.mjs";
// Synthetic MCP approvals against the built app. All browser state is disposable.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
assert.ok(process.env.CHROME_BIN, "Set CHROME_BIN to headless Chromium");
requireRunTemp();
const scratch = await mkdtemp(join(process.env.TMPDIR, "approval-browser-"));
const audit = process.argv.includes("--audit");
const output = join(
  root,
  audit ? "build/artifacts/approval-audit" : "build/artifacts/codex-approval",
);
const requests = [],
  reports = new Map();
const approval = (id, extra) => ({
  id,
  title: "Computer Use",
  reason: 'Allow Computer Use to use "Calculator"?',
  detail: "App: Calculator",
  ...extra,
});
const approvals = [
  approval("app-access"),
  approval("form", {
    fields: [
      { name: "confirm", label: "Confirm", type: "boolean", required: true },
      { name: "name", label: "Name", type: "string", required: true },
      {
        name: "count",
        label: "Count",
        type: "integer",
        required: true,
        minimum: 1,
        maximum: 4,
      },
      {
        name: "app",
        label: "App",
        type: "string",
        required: true,
        options: [{ value: "calc", label: "Calculator" }],
      },
      {
        name: "apps",
        label: "Applications",
        type: "array",
        options: [{ value: "notes", label: "Notes" }],
      },
    ],
  }),
  approval("url", { url: "https://example.com/authorize" }),
  approval("verify", {
    unavailable: "This request requires device verification in the Codex app.",
  }),
  approval("dynamic", {
    decisions: [
      {id: "deny", label: "Deny", kind: "deny"},
      {id: "allow", label: "Allow once", kind: "approve"},
      {id: "session", label: "Allow for this session", description: "Applies to this session only.", kind: "approve"},
      {id: "cancel", label: "Cancel request", kind: "cancel"},
    ],
  }),
  approval("revoked"),
];
const now = Date.now();
const chat = {
  id: "approval-chat",
  project_id: "project",
  user_id: "owner",
  title: "Computer Use approval",
  backend: "codex",
  status: "running",
  updated_at: now,
  transcript: {
    parts: [
      {
        role: "user",
        kind: "text",
        text: "Use Calculator to check the result.",
      },
    ],
  },
  turns: [
    {
      id: "turn",
      user_id: "owner",
      backend: "codex",
      status: "running",
      started_at: now,
      part_start: 0,
      part_end: 1,
      pending_approval: approvals[0],
    },
  ],
};
let stage = 0,
  theme = "light";
function update(next) {
  stage = next;
  chat.updated_at++;
  chat.turns[0].pending_approval = approvals[stage];
}
const probe = `
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check) {
  for (let i = 0; i < 250; i++) { const result = check(); if (result) return result; await pause(25); }
  throw new Error('Timed out: ' + check.toString());
}
const errors = [];
window.addEventListener('error', event => errors.push(event.message));
window.addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
(async () => {
  const report = {};
  const card = () => document.querySelector('.approval-card');
  try {
    await until(card);
    report.appApprovalVisible = card().textContent.includes('Calculator') && !card().querySelector('.primary').disabled;
    report.denyRemainsVisible = parseFloat(getComputedStyle(card().querySelector('.quiet')).fontSize) > 0;
    report.singleOptionHasNoDropdown = !card().querySelector('.custom-select-icon-only');
    const deny = card().querySelector('.quiet'), allow = card().querySelector('.primary');
    const denyRect = deny.getBoundingClientRect(), allowRect = allow.getBoundingClientRect();
    const range = document.createRange(); range.selectNodeContents(deny);
    const textRect = range.getBoundingClientRect();
    report.actionsCentered = Math.abs(denyRect.y + denyRect.height / 2 - allowRect.y - allowRect.height / 2) < 0.5
      && Math.abs(denyRect.height - allowRect.height) < 0.5
      && Math.abs(textRect.x + textRect.width / 2 - denyRect.x - denyRect.width / 2) < 0.5;
    card().querySelector('.primary').click();
    await until(() => card()?.querySelector('[name="count"]'));
    for (const [name, value] of [['name', 'Alex'], ['count', '2']]) card().querySelector('[name="' + name + '"]').value = value;
    card().querySelector('[name="apps"]').click();
    card().querySelector('[aria-label="App"]').click();
    (await until(() => [...document.querySelectorAll('[role="option"]')].find(button => button.textContent === 'Calculator'))).click();
    await until(() => card()?.querySelector('[name="app"]').value === 'calc');
    report.usesSharedSelect = !!card().querySelector('.custom-select') && !card().querySelector('select');
    card().querySelector('.primary').click();
    await until(() => card()?.querySelector('a[href="https://example.com/authorize"]'));
    report.auditSummaryVisible = document.querySelector('.turn-actions')?.textContent.includes('Computer Use · Allow Computer Use to use "Calculator"?') === true;
    report.urlRequiresSeparateConfirmation = card().textContent.includes('Confirm authorization');
    card().querySelector('.primary').click();
    await until(() => card()?.textContent.includes('requires device verification'));
    report.unsupportedRequestVisible = card().querySelector('.primary').disabled && !card().querySelector('.quiet').disabled;
    card().querySelector('.quiet').click();
    await until(() => card()?.querySelector('.custom-select-icon-only'));
    const beforeSelection = await (await fetch('/smoke/count')).json();
    card().querySelector('.custom-select-icon-only button').click();
    (await until(() => document.querySelector('[role="option"][data-value="session"]'))).click();
    await until(() => card()?.querySelector('.primary').textContent === 'Allow for this session');
    const afterSelection = await (await fetch('/smoke/count')).json();
    report.selectingDoesNotSubmit = beforeSelection.count === afterSelection.count;
    report.selectedOptionUpdatesMainButton = card().querySelector('.primary').textContent === 'Allow for this session';
    report.denialStaysSeparate = card().querySelector('.quiet').textContent === 'Deny';
    card().querySelector('.primary').click();
    await until(() => card() && !card().querySelector('.custom-select-icon-only') && !card().querySelector('.primary').disabled);
    await fetch('/smoke/revoke', { method: 'POST' });
    await until(() => !card());
    report.cancelledRequestCleared = true;
    report.noPageOverflow = document.documentElement.scrollWidth <= innerWidth;
    report.noJavascriptErrors = errors.length === 0;
    await fetch('/smoke/restore', { method: 'POST' });
    await until(card);
    ${
      audit
        ? `await fetch('/smoke/audit', { method: 'POST' });
    await until(() => !card() && document.querySelector('.assistant-text'));
    report.auditSummaryFits = document.documentElement.scrollWidth <= innerWidth;`
        : `await fetch('/smoke/restore', { method: 'POST' });
    await until(() => card()?.querySelector('.custom-select-icon-only'));`
    }
    await pause(250);
  } catch (error) { report.error = String(error); report.errors = errors; }
  await fetch('/smoke/report?mode=' + new URL(location.href).searchParams.get('mode'), { method: 'POST', body: JSON.stringify(report) });
})();
`;
const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost"),
    path = url.pathname;
  const json = (data) => {
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify(data));
  };
  if (path === "/smoke/probe.js") {
    res.setHeader("Content-Type", "text/javascript");
    return res.end(probe);
  }
  if (path === "/smoke/report") {
    let body = "";
    for await (const data of req) body += data;
    reports.set(url.searchParams.get("mode"), JSON.parse(body));
    return json({ ok: true });
  }
  if (path === "/smoke/count") return json({count: requests.length});
  if (path === "/smoke/revoke") {
    update(-1);
    return json({ ok: true });
  }
  if (path === "/smoke/restore") {
    update(4);
    return json({ ok: true });
  }
  if (path === "/smoke/audit") {
    chat.status = chat.turns[0].status = "completed";
    chat.turns[0].finished_at = Date.now();
    chat.turns[0].part_end = 2;
    chat.turns[0].pending_approval = undefined;
    chat.transcript.parts.push({
      role: "assistant",
      kind: "text",
      phase: "final_answer",
      text: "Checked the application. Approval decisions now retain their request summaries.",
    });
    chat.updated_at++;
    return json({ ok: true });
  }
  if (path === "/api/me")
    return json({
      user: { id: "owner", username: "owner", nickname: "Owner" },
      csrf: "fixture",
      preferences: { language: "en", theme },
    });
  if (path === "/api/version") return json({ version: "fixture" });
  if (path === "/api/profiles")
    return json({ revision: 1, people: [{ id: "owner", nickname: "Owner" }] });
  if (path === "/api/navigation")
    return json({
      projects: [{ id: "project", name: "Approvals", shared_chats: false }],
      chats: [chat],
    });
  if (path === "/api/agents")
    return json([
      {
        backend: "codex",
        enabled: true,
        installed: true,
        catalog: {
          models: [
            {
              model: "demo",
              displayName: "Demo",
              supportedReasoningEfforts: [],
            },
          ],
          defaultModel: "demo",
        },
      },
    ]);
  if (path === "/api/permissions")
    return json({
      backend: "codex",
      default: "ask",
      modes: [{ id: "ask", available: true }],
    });
  if (path.endsWith("/events")) {
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    if (path.includes("approval-chat")) {
      return res.end(
        "retry: 100\nevent: snapshot\ndata: " + JSON.stringify(chat) + "\n\n",
      );
    }
    return res.end("event: snapshot\ndata: []\n\n");
  }
  if (path === "/api/chats/approval-chat/approvals") {
    let body = "";
    for await (const data of req) body += data;
    const decision = JSON.parse(body);
    requests.push(decision);
    chat.turns[0].actions ??= [];
    chat.turns[0].actions.push({
      id: decision.id,
      user_id: "owner",
      kind: decision.allow ? "approve" : "deny",
      created_at: Date.now(),
      summary: approvals[stage].title + " · " + approvals[stage].reason,
    });
    json({ ok: true });
    setTimeout(() => update(stage + 1), 10);
    return;
  }
  if (path === "/api/chats/approval-chat") return json(chat);
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
  for (const [mode, width, height, selectedTheme] of [
    ["desktop", 1440, 1000, "light"],
    ["mobile", 500, 844, "light"],
    ["desktop-dark", 1440, 1000, "dark"],
    ["mobile-dark", 500, 844, "dark"],
  ]) {
    theme = selectedTheme;
    chat.status = chat.turns[0].status = "running";
    chat.turns[0].part_end = 1;
    chat.turns[0].finished_at = undefined;
    chat.turns[0].actions = [];
    chat.transcript.parts.length = 1;
    update(0);
    const start = requests.length;
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
          `--user-data-dir=${join(scratch, mode)}`,
          `--window-size=${width},${height}`,
          "--force-device-scale-factor=1",
          "--virtual-time-budget=15000",
          `--screenshot=${join(output, mode + ".png")}`,
          `http://127.0.0.1:${server.address().port}/?mode=${mode}`,
        ],
        { stdio: ["ignore", "pipe", "pipe"] },
      );
      let diagnostics = "";
      for (const stream of [child.stdout, child.stderr])
        stream.on("data", (data) => {
          diagnostics = (diagnostics + data).slice(-8000);
        });
      const timeout = setTimeout(() => child.kill("SIGTERM"), 30000);
      child.once("error", reject);
      child.once("close", async (code) => {
        clearTimeout(timeout);
        await writeFile(join(output, mode + ".log"), diagnostics);
        code === 0 ? resolve() : reject(new Error(`Chrome exited ${code}`));
      });
    });
    await writeFile(
      join(output, "browser-report.json"),
      JSON.stringify(Object.fromEntries(reports), null, 2),
    );
    const report = reports.get(mode);
    assert.ok(report, `No ${mode} browser report`);
    assert.equal(report.error, undefined, JSON.stringify(report));
    for (const [key, value] of Object.entries(report))
      assert.equal(value, true, `${mode}: ${key}`);
    assert.deepEqual(requests.slice(start), [
      { id: "app-access", turn_id: "turn", allow: true, decision: "allow", content: {} },
      {
        id: "form",
        turn_id: "turn",
        allow: true,
        decision: "allow",
        content: {
          confirm: false,
          name: "Alex",
          count: 2,
          app: "calc",
          apps: ["notes"],
        },
      },
      { id: "url", turn_id: "turn", allow: true, decision: "allow", content: {} },
      { id: "verify", turn_id: "turn", allow: false, decision: "deny" },
      { id: "dynamic", turn_id: "turn", allow: true, decision: "session", content: {} },
    ]);
  }
  console.log(JSON.stringify(Object.fromEntries(reports), null, 2));
} finally {
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
  await rm(scratch, { recursive: true, force: true });
}
