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
  server_id: "a".repeat(52),
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
const registrations = new Set();
const copied = [];
let rejectCopy = false;
let completeRegistration, rejectRegistration;
let failUnregister = false;
let unregisterCalls = 0;
let checks = 0;
window.mygo = { on: () => () => {}, call: async (method, ...args) => {
  calls.push({ method, args });
  if (method === 'DesktopSettings.RegisterRelay') return new Promise((resolve, reject) => { completeRegistration = resolve; rejectRegistration = reject; });
  if (method === 'DesktopSettings.RelayPairing') return pairing;
  if (method === 'DesktopSettings.Action' && ['copy-relay-web-url', 'copy-tsnet-url'].includes(args[0]?.kind)) {
    if (rejectCopy) throw new Error('synthetic copy failure');
    copied.push(args[0].kind === 'copy-tsnet-url' ? data.status.TSNet.URL : data.status.Relay.WebURL);
    return true;
  }
  throw new Error('unexpected bridge method: ' + method);
} };
let data = {
  settings: { host: '127.0.0.1', port: 8787, public_url: '', idle_minutes: 15, history_cache_mib: 64, auto_start: false, relay_enabled: false, relay_web_enabled: false, relay_url: pairing.relay_url, relay_lan_port: 28082, tsnet_enabled: true, tsnet_hostname: 'spool-server', tsnet_port: 443 },
  status: { Running: true, Relay: { State: 'disabled', RegistrationTokenSet: false, Registered: false, Registering: false }, TSNet: { State: 'Running', URL: 'https://spool.example.ts.net' } },
  database: 'fixture.sqlite',
};
const t = translator('zh-CN');
const root = createRoot(document.getElementById('root'));
const run = async operation => {
  const command = typeof operation === 'function' ? operation(data) : operation;
  if (command.kind === 'unregister-relay') {
    unregisterCalls++;
    if (failUnregister) throw new Error('Could not unregister from the relay. Check its address and network connection, then retry.');
    registrations.delete(command.target);
    data = { ...data, settings: { ...data.settings, relay_enabled: false }, status: { ...data.status, Relay: { State: 'disabled', Registered: false, RegistrationTokenSet: false } } };
    render(); return data;
  }
  if (command.kind !== 'save-settings') throw new Error('unexpected command');
  const registered = registrations.has(command.settings.relay_url);
  data = { ...data, settings: command.settings, status: { ...data.status, Relay: { ...data.status.Relay, Registered: registered, RegistrationTokenSet: registered, State: command.settings.relay_enabled ? 'connected' : 'disabled', ServerID: pairing.server_id, WebURL: command.settings.relay_enabled && command.settings.relay_web_enabled ? pairing.relay_url + '/web/' + pairing.server_id + '/' : '' } } };
  render(); return data;
};
const render = () => root.render(<ServicePage data={data} run={run} t={t} />);
render();
const pause = ms => new Promise(r => setTimeout(r, ms));
const button = text => [...document.querySelectorAll('button')].find(b => b.textContent.includes(text));
const check = (value, label) => { checks++; if (!value) throw new Error(label); };
(async () => {
  for (let i = 0; i < 100 && !button('显示配对二维码'); i++) await pause(50);
  check(calls.length === 0 && !document.querySelector('img'), 'QR stays private until requested');
  const toggle = () => document.querySelector('[role="switch"][aria-label="启用 Spool 中继"]');
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
  const name = document.querySelector('input[aria-label="服务器名称（可选）"]');
  check(name && registration().closest('[role="group"]').contains(name), 'optional name grouped with token');
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(name, '家里的 Mac');
  name.dispatchEvent(new Event('input', { bubbles: true })); await pause(50);
  const token = 'synthetic-registration-token-0000000000000001';
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(registration(), token);
  registration().dispatchEvent(new Event('input', { bubbles: true })); await pause(50);
  button('注册').click(); await pause(50);
  check(calls.at(-1).args[1] === token && calls.at(-1).args[2] === '家里的 Mac', 'supplied name and token register through the same action');
  completeRegistration({ Registered: true }); await pause(100);
  check(!registration() && !toggle().disabled && toggle().getAttribute('aria-checked') === 'false', 'success hides registration and unlocks without enabling');
  registrations.add(pairing.relay_url);
  data = { ...data, status: { ...data.status, Relay: { ...data.status.Relay, Registered: true, RegistrationTokenSet: true } } };
  render(); await pause(50);
  const setAddress = async value => {
    address().focus();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(address(), value);
    address().dispatchEvent(new Event('input', { bubbles: true }));
    await pause(50);
    address().blur();
    await pause(100);
    check(data.settings.relay_url === value, 'address commits on blur: ' + JSON.stringify({ saved: data.settings.relay_url, input: address().value, focused: document.activeElement?.outerHTML, focus: document.hasFocus() }));
  };
  const secondRelay = 'https://second-relay.example.com';
  await setAddress(secondRelay);
  check(!!registration() && registration().value === '' && toggle().disabled, 'new relay shows empty registration and locks enablement');
  button('注册').click(); await pause(50);
  check(calls.at(-1).args[0] === secondRelay && calls.at(-1).args[1] === '', 'new relay does not inherit another relay token');
  completeRegistration({ Registered: true }); await pause(100);
  registrations.add(secondRelay);
  data = { ...data, status: { ...data.status, Relay: { ...data.status.Relay, Registered: true, RegistrationTokenSet: true } } };
  render(); await pause(50);
  const registrationCalls = calls.filter(call => call.method === 'DesktopSettings.RegisterRelay').length;
  for (const value of [pairing.relay_url, secondRelay, pairing.relay_url]) {
    await setAddress(value);
    check(!registration() && !!button('注销') && !toggle().disabled, 'remembered relay shows unregister and unlocks enablement');
  }
  check(calls.filter(call => call.method === 'DesktopSettings.RegisterRelay').length === registrationCalls, 'switching remembered relays never registers again');
  toggle().click(); await pause(100);
  const status = card.querySelector('[aria-label="中继状态"]');
  check(status && status.textContent.includes('已连接') && status.closest('.settings-row') === toggle().closest('.settings-row'), 'enabled status belongs beside switch');
  check(status.getBoundingClientRect().right <= toggle().getBoundingClientRect().left, 'status is left of switch');
  check(card.firstElementChild.contains(toggle()) && address().closest('.settings-card') === card, 'registration success preserves top switch and address');
  for (const state of ['connecting', 'reconnecting', 'connected']) {
    data = { ...data, status: { ...data.status, Relay: { ...data.status.Relay, State: state } } };
    render(); await pause(50);
    check(!button('注销') && !registration(), state + ' hides unregister without reopening registration');
  }
  data = { ...data, status: { ...data.status, Relay: { ...data.status.Relay, State: 'connected' } } };
  render(); await pause(50);
  check(!button('注销'), 'connected relay keeps unregister hidden');
  const webGroup = document.querySelector('[role="group"][aria-label="公网 Web / PWA 访问"]');
  const webToggle = webGroup.querySelector('[role="switch"]');
  check(!webGroup.querySelector('.settings-copy-address') && !webGroup.textContent.includes('公网 Web 地址'), 'disabled Web hides address and copy control');
  webToggle.click(); await pause(100);
  const publicURL = pairing.relay_url + '/web/' + pairing.server_id + '/';
  const copy = webGroup.querySelector('.settings-copy-address');
  check(webGroup.textContent.includes('公网 Web 地址') && webGroup.textContent.includes(publicURL), 'public address belongs to Web access group');
  check(!card.textContent.includes('服务器 ID'), 'standalone server ID row is removed');
  check(getComputedStyle(webToggle.closest('.settings-row')).borderBottomWidth === '0px', 'Web toggle and address share one box');
  check(copy?.classList.contains('icon') && !!copy.querySelector('svg.lucide-copy') && copy.getAttribute('aria-label') === '复制地址', 'address has an accessible copy icon');
  check(copy.getBoundingClientRect().left >= copy.closest('.settings-row').querySelector('.settings-row-heading').getBoundingClientRect().right, 'copy icon follows public address');
  copy.click(); await pause(100);
  check(copied.length === 1 && copied[0] === publicURL, 'copy action copies complete public URL');
  check(!!copy.querySelector('svg.lucide-check') && copy.querySelector('[role="status"]').textContent === '已复制', 'successful copy shows check and live feedback');
  rejectCopy = true; copy.click(); await pause(100);
  check(!!copy.querySelector('svg.lucide-circle-alert') && copy.title.includes('复制地址失败'), 'copy failure has local feedback');
  rejectCopy = false; copy.click(); await pause(100);
  check(copied.length === 2 && !!copy.querySelector('svg.lucide-check'), 'copy can retry without saving settings');
  webToggle.click(); await pause(100);
  check(!webGroup.querySelector('.settings-copy-address') && !webGroup.textContent.includes(publicURL), 'disabling Web hides address');
  webToggle.click(); await pause(100);
  check(!!webGroup.querySelector('svg.lucide-copy'), 're-enabling shows an idle copy icon');
  const tailnetToggle = document.querySelector('[role="switch"][aria-label="启用内嵌 Tailscale"]');
  const tailnetCard = tailnetToggle.closest('.settings-card');
  const tailnetStatus = tailnetCard.querySelector('[aria-label="Tailnet 状态"]');
  check(tailnetStatus?.textContent.includes('已连接 Tailnet') && tailnetStatus.closest('.settings-row') === tailnetToggle.closest('.settings-row') && tailnetStatus.getBoundingClientRect().right <= tailnetToggle.getBoundingClientRect().left, 'Tailnet status is left of its toggle');
  check(![...tailnetCard.querySelectorAll('.settings-row-label > span')].some(label => label.textContent === 'Tailnet 状态'), 'standalone Tailnet status row is removed');
  const key = tailnetCard.querySelector('input[type="password"]');
  const keyGroup = key.closest('[role="group"]');
  check(keyGroup?.contains(button('使用 Auth Key')) && getComputedStyle(key.closest('.settings-row')).borderBottomWidth === '0px' && getComputedStyle(button('使用 Auth Key').parentElement).borderTopWidth === '0px', 'Auth Key field and action share one box');
  const tailnetCopy = tailnetCard.querySelector('.settings-copy-address');
  const tailnetGroup = tailnetToggle.closest('[role="group"]');
  check(tailnetGroup.contains(tailnetCopy) && tailnetGroup.textContent.includes(data.status.TSNet.URL) && getComputedStyle(tailnetToggle.closest('.settings-row')).borderBottomWidth === '0px', 'connected Tailnet address belongs to its enable group');
  check(tailnetCopy?.classList.contains('icon') && !!tailnetCopy.querySelector('svg.lucide-copy') && tailnetCopy.getAttribute('aria-label') === '复制地址', 'Tailnet address has an accessible copy icon');
  check(tailnetCopy.getBoundingClientRect().left >= tailnetCopy.closest('.settings-row').querySelector('.settings-row-heading').getBoundingClientRect().right, 'Tailnet copy icon follows the address');
  tailnetCopy.click(); await pause(100);
  check(copied.at(-1) === data.status.TSNet.URL && !!tailnetCopy.querySelector('svg.lucide-check'), 'Tailnet copy uses its own address and shows success');
  rejectCopy = true; tailnetCopy.click(); await pause(100);
  check(!!tailnetCopy.querySelector('svg.lucide-circle-alert'), 'Tailnet copy failure has local feedback');
  rejectCopy = false; tailnetCopy.click(); await pause(100);
  check(copied.at(-1) === data.status.TSNet.URL && !!tailnetCopy.querySelector('svg.lucide-check'), 'Tailnet copy can retry');
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
  check(calls.every(call => ['DesktopSettings.RelayPairing', 'DesktopSettings.RegisterRelay', 'DesktopSettings.Action'].includes(call.method)), 'registration and pairing use only the private bridge');
  toggle().click(); await pause(100);
  check(!!button('注销') && !registration() && !document.querySelector('img'), 'disabled relay shows unregister and clears pairing display');
  failUnregister = true;
  button('注销').click(); await pause(100);
  check(!!button('注销') && !registration() && toggle().getAttribute('aria-checked') === 'false' && !!document.querySelector('[role="alert"]'), 'failed unregister preserves saved registration for retry');
  failUnregister = false;
  button('注销').click(); await pause(100);
  check(!!registration() && !button('注销') && toggle().disabled && toggle().getAttribute('aria-checked') === 'false', 'unregister disables relay and restores form');
  check(!document.querySelector('img') && unregisterCalls === 2, 'unregister clears pairing display and supports retry');
  await setAddress(secondRelay);
  check(!!button('注销') && !registration(), 'unregister preserves other origin');
  await setAddress(pairing.relay_url);
  check(!!registration() && !button('注销'), 'unregistered origin stays unregistered');
  await fetch('/report', { method: 'POST', body: JSON.stringify({ ok: true, checks }) });
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
    `<!doctype html><html lang="zh-CN" data-theme="light"><head><meta charset="utf-8"><link rel="stylesheet" href="/app.css"><style>#root > :not(.settings-group:has(input[aria-label="中继 HTTPS 地址"]),.settings-group:has([aria-label="启用内嵌 Tailscale"])) { display: none; }</style></head><body><main class="desktop-settings-shell" style="display:block;overflow:auto"><div id="root" class="settings-content" style="max-width:740px;margin:24px auto;padding:16px"></div></main><script src="/app.js"></script></body></html>`,
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
  await send("Emulation.setFocusEmulationEnabled", { enabled: true });
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
  await send("Runtime.evaluate", {
    expression: `document.querySelector('[aria-label="启用内嵌 Tailscale"]').closest('.settings-group').scrollIntoView({ block: 'start', behavior: 'instant' })`,
  });
  const tailnetScreenshot = await send("Page.captureScreenshot", {
    format: "png",
  });
  await writeFile(
    join(output, "desktop-tailnet.png"),
    Buffer.from(tailnetScreenshot.data, "base64"),
  );
  await writeFile(
    join(output, "desktop-pairing.json"),
    JSON.stringify(report, null, 2),
  );
  console.log(
    "PASS: remembered registrations, named enrollment, unregister and retry, Web/Tailnet copying, inline status and pairing controls",
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
