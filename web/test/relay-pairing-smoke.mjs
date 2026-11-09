// Production pairing UI with inert Go-generated QR data and a local bridge stub.
// CHROME_BIN and a repository-owned TMPDIR are required. No real user state.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
assert.ok(process.env.CHROME_BIN, "Set CHROME_BIN");
assert.ok(process.env.TMPDIR && /\/build\/tmp\//.test(process.env.TMPDIR));
const scratch = await mkdtemp(join(process.env.TMPDIR, "relay-browser-"));
const output = join(root, "build/artifacts/relay");
const code = await readFile(join(output, "pairing-synthetic.txt"), "utf8");
const png = await readFile(join(output, "pairing-synthetic.png"));
const pairing = {
  code,
  qr: "data:image/png;base64," + png.toString("base64"),
  relay_url: "https://relay.example.com",
  server_id: "synthetic",
  fingerprint:
    "66687aad f862bd77 6c8fc18b 8e9f8e20 08971485 6ee233b3 902a591d 0d5f2925",
};
let report, child, socket, javascript, css;
const source = `
import React from 'react';
import { createRoot } from 'react-dom/client';
import { ServicePage } from './src/settings-pages';
import { translator } from './src/i18n';
import './src/style.css';
import './src/settings.css';
const pairing = ${JSON.stringify(pairing)};
const calls = [];
let completeRegistration, rejectRegistration;
window.mygo = { on: () => () => {}, call: async (method, ...args) => {
  calls.push({ method, args });
  if (method === 'DesktopSettings.RegisterRelay') return new Promise((resolve, reject) => { completeRegistration = resolve; rejectRegistration = reject; });
  if (method === 'DesktopSettings.RelayPairing') return pairing;
  throw new Error('unexpected bridge method: ' + method);
} };
let data = {
  settings: { host: '127.0.0.1', port: 8787, public_url: '', idle_minutes: 15, history_cache_mib: 64, auto_start: false, relay_enabled: false, relay_url: pairing.relay_url, relay_lan_port: 28082 },
  status: { Running: true, Relay: { State: 'disabled', RegistrationTokenSet: false, Registered: false, Registering: false } },
  database: 'fixture.sqlite',
};
const t = translator('zh-CN');
const root = createRoot(document.getElementById('root'));
const run = async operation => {
  const command = typeof operation === 'function' ? operation(data) : operation;
  if (command.kind !== 'save-settings') throw new Error('unexpected command');
  data = { ...data, settings: command.settings, status: { ...data.status, Relay: { ...data.status.Relay, State: command.settings.relay_enabled ? 'connected' : 'disabled' } } };
  render(); return data;
};
const render = () => root.render(<ServicePage data={data} run={run} t={t} />);
render();
const pause = ms => new Promise(r => setTimeout(r, ms));
const button = text => [...document.querySelectorAll('button')].find(b => b.textContent.includes(text));
const check = (value, label) => { if (!value) throw new Error(label); };
(async () => {
  for (let i = 0; i < 100 && !button('显示配对二维码'); i++) await pause(50);
  check(calls.length === 0 && !document.querySelector('img'), 'QR stays private until requested');
  const toggle = () => document.querySelector('[role="switch"][aria-label="启用局域网与中继连接"]');
  const address = () => document.querySelector('input[aria-label="中继 HTTPS 地址"]');
  const registration = () => document.querySelector('input[aria-label="中继注册令牌"]');
  const card = address().closest('.settings-card');
  check(card.firstElementChild.contains(toggle()), 'enable switch stays at the top');
  check(toggle().disabled && toggle().getAttribute('aria-checked') === 'false', 'unregistered switch is disabled');
  check(address().closest('[role="group"]').contains(registration()), 'registration belongs inside address group');
  check(registration().closest('[role="group"]').querySelectorAll('button').length === 1, 'single register button');
  check(!card.querySelector('[aria-label="中继状态"]'), 'disabled relay has no status badge or status row');
  button('注册').click(); await pause(50);
  check(toggle().disabled && !!registration() && address().disabled, 'registration keeps switch locked until confirmation');
  const automatic = calls.at(-1);
  check(automatic.method === 'DesktopSettings.RegisterRelay' && automatic.args[0] === pairing.relay_url && automatic.args[1] === '', 'empty token performs automatic registration');
  rejectRegistration(new Error('Relay registration was rejected. Check the token or whether automatic registration is allowed.'));
  await pause(100);
  check(toggle().disabled && !!registration() && !!document.querySelector('[role="alert"]'), 'registration failure preserves form and locked switch');
  const token = 'synthetic-registration-token-0000000000000001';
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(registration(), token);
  registration().dispatchEvent(new Event('input', { bubbles: true })); await pause(50);
  button('注册').click(); await pause(50);
  check(calls.at(-1).args[1] === token, 'supplied token registers through the same action');
  completeRegistration({ Registered: true }); await pause(100);
  check(!registration() && !toggle().disabled && toggle().getAttribute('aria-checked') === 'false', 'success hides registration and unlocks without enabling');
  data = { ...data, status: { ...data.status, Relay: { ...data.status.Relay, Registered: true, RegistrationTokenSet: true } } };
  render(); await pause(50);
  toggle().click(); await pause(100);
  const status = card.querySelector('[aria-label="中继状态"]');
  check(status && status.textContent.includes('已连接') && status.closest('.settings-row') === toggle().closest('.settings-row'), 'enabled status belongs beside switch');
  check(status.getBoundingClientRect().right <= toggle().getBoundingClientRect().left, 'status is left of switch');
  check(card.firstElementChild.contains(toggle()) && address().closest('.settings-card') === card, 'registration success preserves top switch and address');
  button('显示配对二维码').click(); await pause(250);
  const image = document.querySelector('img'); await image.decode();
  check(image.naturalWidth === 512 && image.naturalHeight === 512, 'actual Go QR image loaded');
  check(document.body.textContent.includes(pairing.fingerprint), 'fingerprint is shown');
  check(document.body.textContent.includes(pairing.relay_url), 'relay address is shown');
  check(image.getBoundingClientRect().right <= innerWidth, 'QR stays in viewport');
  check(document.querySelector('textarea').value === pairing.code, 'manual code matches QR payload');
  button('隐藏配对码').click(); await pause(100);
  check(!document.querySelector('img') && !document.querySelector('textarea'), 'hide removes code and image');
  button('显示配对二维码').click(); await pause(250); await document.querySelector('img').decode();
  check(calls.every(call => ['DesktopSettings.RelayPairing', 'DesktopSettings.RegisterRelay'].includes(call.method)), 'registration and pairing use only the private bridge');
  await fetch('/report', { method: 'POST', body: JSON.stringify({ ok: true, checks: 23 }) });
})().catch(async error => { await fetch('/report', { method: 'POST', body: JSON.stringify({ error: String(error) }) }); });
`;
const server = createServer(async (req, res) => {
  if (req.url === "/report") {
    let body = "";
    for await (const part of req) body += part;
    report = JSON.parse(body);
    res.end("ok");
    return;
  }
  if (req.url === "/app.js") {
    res.setHeader("Content-Type", "text/javascript");
    res.end(javascript);
    return;
  }
  if (req.url === "/app.css") {
    res.setHeader("Content-Type", "text/css");
    res.end(css);
    return;
  }
  res.setHeader("Content-Type", "text/html; charset=utf-8");
  res.end(
    `<!doctype html><html lang="zh-CN" data-theme="light"><head><meta charset="utf-8"><link rel="stylesheet" href="/app.css"><style>#root > :not(.settings-group:has(input[aria-label="中继 HTTPS 地址"])) { display: none; }</style></head><body><main class="desktop-settings-shell" style="display:block"><div id="root" class="settings-content" style="max-width:740px;margin:24px auto;padding:16px"></div></main><script src="/app.js"></script></body></html>`,
  );
});
try {
  await mkdir(output, { recursive: true });
  const bundle = await build({
    stdin: { contents: source, resolveDir: join(root, "web"), loader: "tsx" },
    bundle: true,
    write: false,
    outdir: scratch,
    format: "iife",
    loader: { ".woff2": "dataurl" },
  });
  javascript = bundle.outputFiles.find((file) =>
    file.path.endsWith(".js"),
  ).text;
  css = bundle.outputFiles.find((file) => file.path.endsWith(".css")).text;
  await new Promise((done, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", done);
  });
  child = spawn(
    process.env.CHROME_BIN,
    [
      "--headless",
      "--no-first-run",
      "--no-default-browser-check",
      "--disable-background-networking",
      "--disable-component-update",
      "--disable-sync",
      "--disable-extensions",
      "--disable-breakpad",
      "--disable-crash-reporter",
      "--user-data-dir=" + join(scratch, "profile"),
      "--window-size=1000,1400",
      "--remote-debugging-port=0",
      "--remote-debugging-address=127.0.0.1",
      "about:blank",
    ],
    { stdio: ["ignore", "ignore", "pipe"] },
  );
  let diagnostic = "";
  child.stderr.on("data", (data) => {
    diagnostic = (diagnostic + data).slice(-4000);
  });
  const pause = (ms) => new Promise((done) => setTimeout(done, ms));
  let port;
  for (let i = 0; i < 100; i++) {
    try {
      port = Number(
        (
          await readFile(join(scratch, "profile/DevToolsActivePort"), "utf8")
        ).split("\n")[0],
      );
      break;
    } catch {
      await pause(50);
    }
  }
  assert.ok(port, diagnostic);
  const targets = await (
    await fetch(`http://127.0.0.1:${port}/json/list`)
  ).json();
  socket = new WebSocket(
    targets.find((target) => target.type === "page").webSocketDebuggerUrl,
  );
  await new Promise((done, reject) => {
    socket.addEventListener("open", done, { once: true });
    socket.addEventListener("error", reject, { once: true });
  });
  let sequence = 0;
  const pending = new Map();
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(event.data);
    if (message.method === "Runtime.exceptionThrown")
      diagnostic += JSON.stringify(message.params);
    const waiter = pending.get(message.id);
    if (waiter) {
      pending.delete(message.id);
      message.error
        ? waiter.reject(new Error(JSON.stringify(message.error)))
        : waiter.done(message.result);
    }
  });
  const send = (method, params = {}) =>
    new Promise((done, reject) => {
      const id = ++sequence;
      const timer = setTimeout(() => {
        pending.delete(id);
        reject(new Error("CDP timeout: " + method));
      }, 5000);
      pending.set(id, {
        done: (value) => {
          clearTimeout(timer);
          done(value);
        },
        reject: (error) => {
          clearTimeout(timer);
          reject(error);
        },
      });
      socket.send(JSON.stringify({ id, method, params }));
    });
  await send("Runtime.enable");
  await send("Page.navigate", {
    url: `http://127.0.0.1:${server.address().port}/`,
  });
  for (let i = 0; i < 200 && !report; i++) await pause(50);
  const screenshot = await send("Page.captureScreenshot", { format: "png" });
  await writeFile(
    join(output, "desktop-pairing.png"),
    Buffer.from(screenshot.data, "base64"),
  );
  assert.equal(report?.ok, true, JSON.stringify(report) + diagnostic);
  await writeFile(
    join(output, "desktop-pairing.json"),
    JSON.stringify(report, null, 2),
  );
  console.log(
    "PASS: registration gating and errors, single action, address grouping, inline status, QR and hide controls",
  );
} finally {
  socket?.close();
  if (child && child.exitCode === null) {
    const exited = new Promise((done) => child.once("exit", done));
    child.kill("SIGTERM");
    await exited;
  }
  server.closeAllConnections();
  await new Promise((done) => server.close(done));
  await rm(scratch, { recursive: true, force: true });
}
