// Real browser + real Relay APIs. Run from the repository root through
// tools/run.sh with CHROME_BIN pointing to a headless Chrome/Chromium binary.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { requireRunTemp } from "../../tools/test-scratch.mjs";

requireRunTemp();
assert.ok(process.env.CHROME_BIN, "Set CHROME_BIN to Chrome/Chromium");
const scratch = await mkdtemp(join(process.env.TMPDIR, "relay-admin-browser-"));
const artifacts = resolve("build/artifacts/relay-admin");
const adminToken = "test-administrator-credential-0000000001";
let fixture, browser, socket, browserDone, fixtureDone;
let errors = [];
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(check, label) {
  for (let i = 0; i < 300; i++) {
    const value = await check();
    if (value) return value;
    await pause(50);
  }
  throw new Error("Timed out: " + label);
}
try {
  const build = spawn("go", ["-C", "relay", "test", "-c", "-o", join(scratch, "relay.test")], { stdio: "inherit" });
  assert.equal((await once(build, "close"))[0], 0, "Could not build Relay test fixture");
  fixture = spawn(join(scratch, "relay.test"), ["-test.run=^TestAdminBrowserFixture$", "-test.timeout=90s"], {
    env: { ...process.env, SPOOL_RELAY_BROWSER_FIXTURE: "1" }, stdio: ["pipe", "pipe", "pipe"],
  });
  fixtureDone = once(fixture, "close");
  let output = "", diagnostics = "";
  fixture.stdout.on("data", (chunk) => { output += chunk; });
  fixture.stderr.on("data", (chunk) => { diagnostics += chunk; });
  const fixtureInfo = await until(() => {
    if (fixture.exitCode !== null) throw new Error("Fixture failed: " + output + diagnostics);
    const line = output.split("\n").slice(0, -1).find((line) => line.startsWith('{"fixture_url":'));
    return line && JSON.parse(line);
  }, "Relay fixture start");
  const url = fixtureInfo.fixture_url;
  const publicURL = fixtureInfo.public_url;
  assert.notEqual(url, publicURL, "Management and service entries must be separate");
  const profile = join(scratch, "profile");
  browser = spawn(process.env.CHROME_BIN, [
    "--headless", "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
    "--disable-component-update", "--disable-sync", "--disable-extensions", "--disable-breakpad",
    "--disable-crash-reporter", "--ignore-certificate-errors", "--remote-debugging-port=0",
    "--user-data-dir=" + profile, "about:blank",
  ], { stdio: "ignore" });
  browserDone = once(browser, "close");
  const port = await until(async () => {
    try { return (await readFile(join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; }
    catch { return undefined; }
  }, "Chrome DevTools port");
  const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  socket = new WebSocket(targets.find((entry) => entry.type === "page").webSocketDebuggerUrl);
  await once(socket, "open");
  let sequence = 0;
  const pending = new Map();
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(event.data);
    if (message.id) {
      const waiter = pending.get(message.id);
      pending.delete(message.id);
      if (message.error) waiter.reject(new Error(JSON.stringify(message.error)));
      else waiter.resolve(message.result);
    }
    if (message.method === "Runtime.exceptionThrown") errors.push(message.params.exceptionDetails.text);
    if (message.method === "Log.entryAdded" && message.params.entry.level === "error" && message.params.entry.source === "security") errors.push(message.params.entry.text);
  });
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++sequence;
    pending.set(id, { resolve, reject });
    socket.send(JSON.stringify({ id, method, params }));
  });
  const evaluate = async (expression) => {
    const result = await send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
    return result.result.value;
  };
  await send("Runtime.enable");
  await send("Log.enable");
  await send("Page.enable");
  await send("Emulation.setFocusEmulationEnabled", { enabled: true });
  await send("Emulation.setDeviceMetricsOverride", { width: 1280, height: 1100, deviceScaleFactor: 1, mobile: false });
  await send("Page.navigate", { url });
  await until(() => evaluate('!!document.getElementById("login-form")'), "dashboard shell");
  await evaluate(`document.getElementById("admin-token").value="wrong";document.getElementById("login-form").requestSubmit()`);
  await until(() => evaluate('document.getElementById("notice").textContent.includes("无效")'), "bad-token rejection");
  assert.equal(await evaluate('document.getElementById("dashboard").hidden'), true);
  await evaluate(`document.getElementById("admin-token").value=${JSON.stringify(adminToken)};document.getElementById("login-form").requestSubmit()`);
  await until(() => evaluate('!document.getElementById("dashboard").hidden'), "login");
  await until(() => evaluate(`fetch('/v1/settings',{headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.json()).then(s=>s.admin_origin===location.origin && s.detecting && s.waiting_for_proxy)`), "startup waits for the public proxy");
  assert.equal(await evaluate('document.getElementById("detect").disabled'), true);
  assert.equal(await evaluate('document.getElementById("detect").textContent'), "等待代理…");
  assert.equal(await evaluate('document.getElementById("detection-status").textContent.includes("等待公网 HTTPS 代理就绪")'), true);
  assert.equal(await evaluate(`fetch('/fixture/proxy-ready',{method:'POST',headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.status)`), 204);
  await until(() => evaluate(`fetch('/v1/settings',{headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.json()).then(s=>s.admin_origin===location.origin && !s.detecting && s.public_url===${JSON.stringify(publicURL)})`), "deployment URL survives management login");
  await until(() => evaluate('document.getElementById("detection-status").textContent.includes("检测成功")'), "startup proxy detection");
  assert.equal(await evaluate('document.getElementById("detect").disabled'), false);
  assert.equal(await evaluate('document.getElementById("public-url").readOnly'), true);
  assert.equal(await evaluate('document.getElementById("web-domain").readOnly'), true);
  assert.equal(await evaluate('document.getElementById("public-url").value'), publicURL);
  assert.equal(await evaluate('document.getElementById("web-domain").value'), "web." + new URL(publicURL).hostname);
  assert.equal(await evaluate('document.getElementById("certificate-mode")'), null);
  assert.equal(await evaluate('document.getElementById("save-web-settings")'), null);
  assert.equal(await evaluate('document.getElementById("proxy-type")'), null);
  for (const body of [{public_url:"https://changed.example.com"},{web_domain:"changed.example.com"},{certificate_mode:"wildcard"}]) {
    assert.equal(await evaluate(`fetch('/v1/settings',{method:'PUT',headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)},'Content-Type':'application/json'},body:${JSON.stringify(JSON.stringify(body))}}).then(r=>r.status)`),400);
  }
  assert.equal(await evaluate('document.getElementById("advanced").open'), false);
  await evaluate('document.getElementById("advanced").open=true;document.getElementById("trusted-proxies").value="10.10.0.0/24";document.getElementById("trusted-proxies").dispatchEvent(new Event("input",{bubbles:true}));document.getElementById("settings-form").requestSubmit()');
  await until(() => evaluate(`!document.getElementById('save-settings').disabled && fetch('/v1/settings',{headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.json()).then(s=>s.trusted_proxies.join(',')==='10.10.0.0/24' && s.public_url===${JSON.stringify(publicURL)} && !!s.detected_proxy)`), "manual proxy edits preserve deployment and detected peer");
  await evaluate('document.getElementById("advanced").open=false');
  assert.equal(await evaluate('document.getElementById("detect").textContent'), "检测");
  assert.equal(await evaluate('document.getElementById("online-count").textContent'), "1");
  assert.equal(await evaluate('document.getElementById("admin-token").value'), "");
  assert.deepEqual(await evaluate('[localStorage.length,sessionStorage.length,document.cookie]'), [0, 0, ""]);
  const name = "office-linux";
  await evaluate(`document.getElementById("server-name").value=${JSON.stringify(name)};document.getElementById("create-form").requestSubmit()`);
  await until(() => evaluate('!document.getElementById("token-result").hidden && document.querySelectorAll("#servers tr").length===2'), "token creation");
  const token = await evaluate('document.getElementById("issued-token").value');
  assert.match(token, /^[A-Za-z0-9_-]{32,}$/);
  assert.equal(await evaluate('document.querySelector("#servers .server-name").textContent'), name);
  // Native protocol reuses the same real token store; listing never reveals it.
  const list = await evaluate(`fetch('/v1/registrations',{headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.json())`);
  assert.equal(JSON.stringify(list).includes(token), false);
  const issued = list.registrations.find((entry) => entry.name === name);
  assert.ok(issued);
  await mkdir(artifacts, { recursive: true });
  await evaluate('document.getElementById("dismiss-token").click()');
  await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true }).then((image) => writeFile(join(artifacts, "desktop.png"), Buffer.from(image.data, "base64")));
  await evaluate('document.querySelector("#servers .revoke").click()');
  await until(() => evaluate('document.getElementById("revoke-dialog").open'), "revocation confirmation");
  await evaluate('document.querySelector("#revoke-dialog .danger").click()');
  await until(() => evaluate('document.querySelector("#servers .badge").textContent==="已撤销"'), "revocation");
  // The native endpoint is on the service origin, outside the admin page's
  // strict same-origin CSP. The test client accepts only fixture TLS here.
  const revoked = await fetch(publicURL + "/v1/host", { headers: { Authorization: "Bearer " + token }, tls: { rejectUnauthorized: false } });
  assert.equal(revoked.status, 401);
  await revoked.arrayBuffer();
  await evaluate('document.getElementById("detect").click()');
  await until(() => evaluate('!document.getElementById("detect").disabled && document.getElementById("detection-status").textContent.includes("检测成功")'), "manual detection retry");
  await send("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
  assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth'), true, "Mobile page overflows horizontally");
  await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true }).then((image) => writeFile(join(artifacts, "mobile.png"), Buffer.from(image.data, "base64")));
  // Exercise hostile names after capturing the ordinary example screens.
  // The fixture and every registration disappear when this run exits.
  const hostileName = '<img src=x onerror="window.relayXSS=true">';
  await evaluate(`document.getElementById("server-name").value=${JSON.stringify(hostileName)};document.getElementById("create-form").requestSubmit()`);
  await until(() => evaluate('document.querySelectorAll("#servers tr").length===3'), "hostile-name rendering");
  assert.equal(await evaluate('window.relayXSS || !!document.querySelector("#servers img")'), false);
  assert.equal(await evaluate('document.querySelector("#servers .server-name").textContent'), hostileName);
  await evaluate('document.getElementById("logout").click()');
  assert.equal(await evaluate('document.getElementById("dashboard").hidden && document.getElementById("issued-token").value === "" && document.getElementById("servers").childElementCount===0'), true);
  assert.deepEqual(errors, [], "Browser errors");
  console.log(JSON.stringify({ ok: true, checks: 37, artifacts }));
} finally {
  socket?.close();
  if (browser) { browser.kill("SIGTERM"); await browserDone; }
  if (fixture) {
    fixture.stdin.end();
    const code = (await fixtureDone)[0];
    if (code !== 0) console.error("Relay fixture exit:", code);
  }
  await rm(scratch, { recursive: true, force: true });
}
