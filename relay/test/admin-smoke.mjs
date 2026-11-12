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
  const capture = async (name) => {
    await evaluate('document.fonts.ready.then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))');
    const image = await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
    await writeFile(join(artifacts, name + ".png"), Buffer.from(image.data, "base64"));
  };
  const viewport = async (width, height, mobile = false) => {
    await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile });
    await evaluate('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
  };
  const theme = (value) => send("Emulation.setEmulatedMedia", { features: [
    { name: "prefers-color-scheme", value },
    { name: "prefers-reduced-motion", value: "reduce" },
  ] });
  const click = async (selector) => {
    const { x, y } = await evaluate(`(() => {
      const element = document.querySelector(${JSON.stringify(selector)});
      element.scrollIntoView({block:'nearest'});
      const rect = element.getBoundingClientRect();
      return {x:rect.x+rect.width/2,y:rect.y+rect.height/2};
    })()`);
    await send("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount: 1 });
    await send("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount: 1 });
  };
  const key = async (key, code, windowsVirtualKeyCode, modifiers = 0) => {
    for (const type of ['keyDown', 'keyUp'])
      await send('Input.dispatchKeyEvent', { type, key, code, windowsVirtualKeyCode, modifiers });
  };
  const openSidebar = async () => {
    if (await evaluate('matchMedia("(max-width: 700px)").matches && document.getElementById("sidebar-toggle").getAttribute("aria-expanded") !== "true"')) {
      await click('#sidebar-toggle');
      await until(() => evaluate('document.getElementById("sidebar-toggle").getAttribute("aria-expanded")==="true" && document.getElementById("settings-sidebar").getBoundingClientRect().x===0'), 'drawer opening');
    }
  };
  const navigate = async (page) => {
    await openSidebar();
    await click(`[data-page="${page}"]`);
    assert.equal(await evaluate(`document.querySelector('[data-page="${page}"]').getAttribute('aria-current')`), "page", 'Navigation to ' + page);
    assert.equal(await evaluate(`document.getElementById('${page}-page').hidden`), false);
    assert.equal(await evaluate('document.getElementById("sidebar-toggle").getAttribute("aria-expanded")'), 'false');
  };
  const selectFilter = async (filter) => {
    await click(`[data-token-filter="${filter}"]`);
    assert.equal(await evaluate(`document.getElementById('token-tab-${filter}').getAttribute('aria-selected')`), 'true');
    assert.equal(await evaluate('document.querySelectorAll(".token-tabs [aria-selected=true]").length'), 1);
    assert.equal(await evaluate(`document.querySelectorAll('.token-tabs [tabindex="0"]').length`), 1);
    assert.equal(await evaluate('document.getElementById("token-panel").getAttribute("aria-labelledby")'), 'token-tab-' + filter);
  };
  const tabCounts = () => evaluate('[...document.querySelectorAll(".token-tabs .tab-count")].map(e=>Number(e.textContent))');
  const fitsViewport = async () => {
    assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth'), true, "Page overflows horizontally");
    assert.equal(await evaluate(`document.getElementById('dashboard').hidden || document.getElementById('settings-main').scrollWidth <= document.getElementById('settings-main').clientWidth`), true, "Settings content overflows horizontally");
  };
  await send("Runtime.enable");
  await send("Log.enable");
  await send("Page.enable");
  const downloads = join(scratch, "downloads");
  await mkdir(downloads);
  await send("Browser.setDownloadBehavior", { behavior: "allow", downloadPath: downloads });
  await send("Page.addScriptToEvaluateOnNewDocument", { source: `if (!localStorage.getItem('spool.relay.ui-preferences.v1')) localStorage.setItem('spool.relay.ui-preferences.v1',JSON.stringify({language:'zh-CN',theme:'system'}));` });
  await send("Emulation.setFocusEmulationEnabled", { enabled: true });
  await viewport(1280, 900);
  await theme("light");
  await send("Page.navigate", { url });
  await until(() => evaluate('!!document.documentElement.dataset.theme && !!document.getElementById("login-form")'), "dashboard shell");
  await until(() => evaluate('document.querySelector(".brand-mark").naturalWidth > 0'), "Spool logo");
  await mkdir(artifacts, { recursive: true });
  const loginAppearance = async () => {
    assert.equal(await evaluate('!!document.querySelector("#login .version")'), false, 'Login must not show a version number');
    assert.equal(await evaluate('(() => {const r=document.getElementById("login-preferences").getBoundingClientRect();return r.top === 20 && innerWidth-r.right === 20})()'), true, 'Login settings must stay in the top right');
    if (await evaluate('matchMedia("(max-width: 700px)").matches')) {
      assert.equal(await evaluate('(() => {const story=document.querySelector(".login-story").getBoundingClientRect(), panel=document.querySelector(".login-panel").getBoundingClientRect();return Math.abs(story.height-panel.height)<1 && Math.abs(story.height-innerHeight/2)<1 && Math.abs(panel.top-story.bottom)<1})()'), true, 'Narrow login must split the viewport into equal top and bottom halves');
      assert.equal(await evaluate('(() => {const form=document.getElementById("login-form").getBoundingClientRect(), panel=document.querySelector(".login-panel").getBoundingClientRect();return form.top>=panel.top && form.bottom<=panel.bottom})()'), true, 'Login form must fit within its half');
      assert.equal(await evaluate('(() => {const story=document.querySelector(".login-story").getBoundingClientRect();return [...document.querySelectorAll(".login-story .eyebrow, .login-story h1, .login-story p, .login-foot")].every(e=>{const r=e.getBoundingClientRect();return e.checkVisibility() && r.height>0 && r.top>=story.top && r.bottom<=story.bottom})})()'), true, 'All story text must be visible within the upper half');
      assert.equal(await evaluate('(() => {const story=document.querySelector(".login-story").getBoundingClientRect(), copy=document.querySelector(".login-story-copy").getBoundingClientRect();return Math.abs((copy.top+copy.bottom)/2-(story.top+story.bottom)/2)<32})()'), true, 'Story copy must sit near the center of the upper half');
    }
  };
  await loginAppearance();
  await fitsViewport();
  await capture("login-desktop");
  await viewport(390, 844, true);
  await loginAppearance();
  await fitsViewport();
  await capture("login-mobile");
  await viewport(320, 720, true);
  await loginAppearance();
  await fitsViewport();
  await capture('login-mobile-compact');
  await viewport(1280, 900);
  await theme("dark");
  await capture("login-dark");
  await theme("light");
  const language = async (value) => {
    await evaluate(`document.getElementById('language-choice').value=${JSON.stringify(value)};document.getElementById('language-choice').dispatchEvent(new Event('change',{bubbles:true}))`);
  };
  await click('#login-preferences');
  assert.equal(await evaluate('document.getElementById("appearance-dialog").open'), true);
  const version = JSON.parse(await readFile(resolve("relay/version.json"), "utf8")).version;
  assert.equal(await evaluate('document.querySelector(".appearance-version code").textContent'), 'v' + version);
  await language('en');
  assert.equal(await evaluate('document.documentElement.lang'), 'en');
  assert.equal(await evaluate('document.getElementById("login-title").textContent'), 'Sign in to Relay');
  assert.equal(await evaluate('document.getElementById("admin-token").placeholder'), 'Enter your administrator token');
  await click('[data-theme-choice="light"]');
  await theme('dark');
  assert.equal(await evaluate('document.documentElement.dataset.theme'), 'light', 'Explicit light theme must override the system');
  await click('[data-theme-choice="dark"]');
  await theme('light');
  assert.equal(await evaluate('document.documentElement.dataset.theme'), 'dark', 'Explicit dark theme must override the system');
  await click('[data-theme-choice="system"]');
  assert.equal(await evaluate('document.documentElement.dataset.theme'), 'light');
  await viewport(320, 720, true);
  await fitsViewport();
  assert.equal(await evaluate('document.getElementById("appearance-dialog").scrollWidth <= document.getElementById("appearance-dialog").clientWidth'), true, 'Preferences overflow on a narrow screen');
  await capture('preferences-mobile');
  await language('ja');
  assert.equal(await evaluate('document.getElementById("login-title").textContent'), 'Relay 管理にログイン');
  await click('#close-preferences');
  await capture('login-mobile-ja');
  await loginAppearance();
  await fitsViewport();
  await language('en');
  await loginAppearance();
  await fitsViewport();
  await capture('login-mobile-en');
  await language('zh-CN');
  await viewport(768, 900);
  await fitsViewport();
  await capture('login-tablet');
  await viewport(1280, 900);
  await evaluate(`document.getElementById("admin-token").value="wrong";document.getElementById("login-form").requestSubmit()`);
  await until(() => evaluate('document.getElementById("notice").textContent.includes("无效")'), "bad-token rejection");
  assert.equal(await evaluate('document.getElementById("dashboard").hidden'), true);
  await evaluate(`document.getElementById("admin-token").value=${JSON.stringify(adminToken)};document.getElementById("login-form").requestSubmit()`);
  await until(() => evaluate('!document.getElementById("dashboard").hidden'), "login");
  assert.equal(await evaluate('document.getElementById("network-page").hidden'), true);
  assert.equal(await evaluate('document.getElementById("servers-page").hidden'), false);
  assert.deepEqual(await evaluate('[...document.querySelectorAll("[data-page]")].map(e=>e.dataset.page)'), ['servers', 'network']);
  assert.deepEqual(await tabCounts(), [2, 1, 0, 3]);
  assert.deepEqual(await evaluate('[...document.querySelectorAll("#servers .server-name")].map(e=>e.textContent).sort()'), ['home-mac', 'offline-server']);
  assert.deepEqual(await evaluate('[...document.querySelectorAll("#servers .badge")].map(e=>e.textContent)'), ['已注册', '已注册']);
  assert.deepEqual(await evaluate('[...document.querySelectorAll("#servers .connection-state")].map(e=>e.textContent).sort()'), ['在线', '离线']);
  await selectFilter('revoked');
  assert.equal(await evaluate('document.querySelector("#servers .server-name").textContent'), 'retired-server');
  assert.equal(await evaluate('document.querySelector("#servers .badge").textContent'), '已注销');
  assert.equal(await evaluate('document.querySelectorAll("#servers button").length'), 0, 'Retired tokens must not offer active actions');
  await selectFilter('unused');
  assert.equal(await evaluate('document.getElementById("empty-state").hidden || !document.getElementById("token-table").hidden'), false, 'Unused view must have an empty state');
  await key('End', 'End', 35);
  assert.equal(await evaluate('document.activeElement.id'), 'token-tab-all');
  assert.equal(await evaluate('document.querySelectorAll("#servers tr").length'), 3);
  await key('ArrowRight', 'ArrowRight', 39);
  assert.equal(await evaluate('document.activeElement.id'), 'token-tab-registered');
  await key('ArrowLeft', 'ArrowLeft', 37);
  assert.equal(await evaluate('document.activeElement.id'), 'token-tab-all');
  await key('Home', 'Home', 36);
  assert.equal(await evaluate('document.activeElement.id'), 'token-tab-registered');
  await navigate('network');
  await until(() => evaluate(`fetch('/v1/settings',{headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.json()).then(s=>s.admin_origin===location.origin && s.detecting && s.waiting_for_proxy)`), "startup waits for the public proxy");
  assert.equal(await evaluate('document.getElementById("detect").disabled'), true);
  assert.equal(await evaluate('document.getElementById("detect").textContent'), "等待代理…");
  assert.equal(await evaluate('document.getElementById("detection-status").textContent.includes("等待公网 HTTPS 代理就绪")'), true);
  assert.equal(await evaluate(`fetch('/fixture/proxy-ready',{method:'POST',headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.status)`), 204);
  await until(() => evaluate(`fetch('/v1/settings',{headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.json()).then(s=>s.admin_origin===location.origin && !s.detecting && s.public_url===${JSON.stringify(publicURL)})`), "deployment URL survives management login");
  await until(() => evaluate('document.getElementById("detection-status").textContent.includes("检测成功")'), "startup proxy detection");
  assert.equal(await evaluate('document.getElementById("detect").disabled'), false);
  for (const id of ["public-url", "web-domain"]) {
    assert.equal(await evaluate(`document.getElementById('${id}').tagName`), "CODE", "Deployment addresses must be text, not form fields");
    assert.equal(await evaluate(`document.getElementById('${id}').isContentEditable`), false);
    assert.equal(await evaluate(`document.getElementById('${id}').closest('form')`), null);
  }
  assert.equal(await evaluate('document.getElementById("public-url").textContent'), publicURL);
  assert.equal(await evaluate('document.getElementById("web-domain").textContent'), "web." + new URL(publicURL).hostname);
  assert.equal(await evaluate('document.getElementById("certificate-mode")'), null);
  assert.equal(await evaluate('document.getElementById("save-web-settings")'), null);
  assert.equal(await evaluate('document.getElementById("proxy-type")'), null);
  for (const body of [{public_url:"https://changed.example.com"},{web_domain:"changed.example.com"},{certificate_mode:"wildcard"}]) {
    assert.equal(await evaluate(`fetch('/v1/settings',{method:'PUT',headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)},'Content-Type':'application/json'},body:${JSON.stringify(JSON.stringify(body))}}).then(r=>r.status)`),400);
  }
  await capture("network-desktop");
  await theme("dark");
  await capture("network-dark");
  await theme("light");
  await viewport(390, 844, true);
  await fitsViewport();
  await capture("network-mobile");
  await viewport(320, 720, true);
  await fitsViewport();
  await viewport(1280, 900);
  assert.equal(await evaluate('document.getElementById("advanced").open'), false);
  await evaluate('document.getElementById("advanced").open=true;document.getElementById("trusted-proxies").value="10.10.0.0/24";document.getElementById("trusted-proxies").dispatchEvent(new Event("input",{bubbles:true}))');
  await navigate("servers");
  assert.equal(await evaluate('document.getElementById("network-page").hidden'), true);
  await navigate("network");
  assert.equal(await evaluate('document.getElementById("trusted-proxies").value'), "10.10.0.0/24", "Navigation lost the proxy draft");
  await evaluate('document.getElementById("settings-form").requestSubmit()');
  await until(() => evaluate(`!document.getElementById('save-settings').disabled && fetch('/v1/settings',{headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.json()).then(s=>s.trusted_proxies.join(',')==='10.10.0.0/24' && s.public_url===${JSON.stringify(publicURL)} && !!s.detected_proxy)`), "manual proxy edits preserve deployment and detected peer");
  await evaluate('document.getElementById("advanced").open=false');
  assert.equal(await evaluate('document.getElementById("detect").textContent'), "检测");
  assert.equal(await evaluate('document.getElementById("online-count").textContent'), "1");
  assert.equal(await evaluate('document.getElementById("admin-token").value'), "");
  assert.deepEqual(await evaluate('[Object.keys(localStorage),sessionStorage.length,document.cookie]'), [["spool.relay.ui-preferences.v1"], 0, ""]);
  await navigate("servers");
  await click('#servers .server-actions button:not(.revoke)');
  await until(() => evaluate('document.querySelector("#servers .server-actions button:not(.revoke)").textContent === "允许 Web"'), "block Web publishing");
  await click('#servers .server-actions button:not(.revoke)');
  await until(() => evaluate('document.querySelector("#servers .server-actions button:not(.revoke)").textContent === "停用 Web"'), "restore Web publishing");
  await click('#refresh');
  const name = "office-linux";
  await evaluate(`document.getElementById("server-name").value=${JSON.stringify(name)};document.getElementById("create-form").requestSubmit()`);
  await until(() => evaluate('!document.getElementById("token-result").hidden && document.querySelectorAll("#servers tr").length===1 && document.querySelector("#servers .server-name").textContent==="office-linux"'), "token creation");
  assert.equal(await evaluate('document.getElementById("token-tab-unused").getAttribute("aria-selected")'), 'true');
  assert.deepEqual(await tabCounts(), [2, 1, 1, 4]);
  const token = await evaluate('document.getElementById("issued-token").value');
  assert.match(token, /^[A-Za-z0-9_-]{32,}$/);
  assert.equal(await evaluate('document.querySelector("#servers .server-name").textContent'), name);
  // Native protocol reuses the same real token store; listing never reveals it.
  const list = await evaluate(`fetch('/v1/registrations',{headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.json())`);
  assert.equal(JSON.stringify(list).includes(token), false);
  const issued = list.registrations.find((entry) => entry.name === name);
  assert.ok(issued);
  await evaluate('document.getElementById("dismiss-token").click()');
  await selectFilter('all');
  await fitsViewport();
  await capture("desktop");
  await theme("dark");
  await capture("servers-dark");
  await theme("light");
  await selectFilter('unused');
  await evaluate('document.querySelector("#servers .revoke").click()');
  await until(() => evaluate('document.getElementById("revoke-dialog").open'), "revocation confirmation");
  await evaluate('document.querySelector("#revoke-dialog .danger").click()');
  await until(() => evaluate('document.querySelectorAll("#servers tr").length===0 && !document.getElementById("empty-state").hidden'), "revocation removes unused token");
  assert.deepEqual(await tabCounts(), [2, 2, 0, 4]);
  await selectFilter('revoked');
  assert.equal(await evaluate('document.querySelector("#servers .badge").textContent'), '已撤销');
  // The native endpoint is on the service origin, outside the admin page's
  // strict same-origin CSP. The test client accepts only fixture TLS here.
  const revoked = await fetch(publicURL + "/v1/host", { headers: { Authorization: "Bearer " + token }, tls: { rejectUnauthorized: false } });
  assert.equal(revoked.status, 401);
  await revoked.arrayBuffer();
  await navigate("network");
  await evaluate('document.getElementById("detect").click()');
  await until(() => evaluate('!document.getElementById("detect").disabled && document.getElementById("detection-status").textContent.includes("检测成功")'), "manual detection retry");
  await navigate("servers");
  assert.equal(await evaluate('document.getElementById("token-tab-revoked").getAttribute("aria-selected")'), 'true', 'Navigation must preserve the filter');
  await selectFilter('all');
  await viewport(390, 844, true);
  await fitsViewport();
  await capture("mobile");
  await openSidebar();
  assert.equal(await evaluate('document.getElementById("settings-sidebar").getAttribute("role")'), 'dialog');
  assert.equal(await evaluate('document.getElementById("settings-main").inert && document.getElementById("mobile-header").inert'), true);
  await capture('sidebar-mobile');
  await evaluate('document.querySelector("#settings-sidebar .brand").focus()');
  await key('Tab', 'Tab', 9, 8);
  assert.equal(await evaluate('document.activeElement.id'), 'logout', 'Drawer must trap backward focus');
  await key('Tab', 'Tab', 9);
  assert.equal(await evaluate('document.activeElement.matches("#settings-sidebar .brand")'), true, 'Drawer must trap forward focus');
  await key('Escape', 'Escape', 27);
  assert.equal(await evaluate('document.activeElement.id'), 'sidebar-toggle');
  assert.equal(await evaluate('document.getElementById("settings-sidebar").inert'), true);
  await openSidebar();
  // Dismiss by clicking the exposed part of the backdrop, outside the drawer.
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: 380, y: 400, button: 'left', clickCount: 1 });
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: 380, y: 400, button: 'left', clickCount: 1 });
  assert.equal(await evaluate('document.getElementById("sidebar-toggle").getAttribute("aria-expanded")'), 'false');
  await navigate('network');
  await navigate('servers');
  await openSidebar();
  await click('#dashboard-preferences');
  assert.equal(await evaluate('document.getElementById("appearance-dialog").open && !document.getElementById("settings-main").inert'), true, 'Drawer settings must open without leaving the page inert');
  await click('#close-preferences');
  assert.equal(await evaluate('document.activeElement.id'), 'sidebar-toggle');
  await viewport(320, 720, true);
  await fitsViewport();
  // Exercise hostile names after capturing the ordinary example screens.
  // The fixture and every registration disappear when this run exits.
  const hostileName = '<img src=x onerror="window.relayXSS=true">';
  await evaluate(`document.getElementById("server-name").value=${JSON.stringify(hostileName)};document.getElementById("create-form").requestSubmit()`);
  await until(() => evaluate('document.querySelectorAll("#servers tr").length===1 && document.getElementById("token-tab-unused").getAttribute("aria-selected")==="true"'), "hostile-name rendering");
  assert.equal(await evaluate('window.relayXSS || !!document.querySelector("#servers img")'), false);
  assert.equal(await evaluate('document.querySelector("#servers .server-name").textContent'), hostileName);
  await click('#dismiss-token');
  await evaluate('document.getElementById("server-name").value="";document.getElementById("create-form").requestSubmit()');
  await until(() => evaluate('!!document.getElementById("issued-token") && !document.getElementById("create-token").disabled && document.querySelectorAll("#servers tr").length===2'), 'random name for a blank single token');
  const randomName = await evaluate('document.querySelector(".issued-name").textContent');
  assert.match(randomName, /^[A-Za-z]{12}$/);
  const singleToken = await evaluate('document.getElementById("issued-token").value');
  const exportedPath = join(downloads, 'spool-relay-tokens.txt');
  const exported = () => until(async () => { try { return await readFile(exportedPath, 'utf8'); } catch { return undefined; } }, 'TXT download');
  await click('#export-tokens');
  assert.equal(await exported(), randomName + '\t' + singleToken + '\r\n');
  await rm(exportedPath);
  await click('[data-token-mode="batch"]');
  assert.equal(await evaluate('document.getElementById("server-name").hidden && !document.getElementById("batch-count").hidden'), true);
  await evaluate('document.getElementById("batch-count").value="101"');
  assert.equal(await evaluate('document.getElementById("create-form").checkValidity()'), false);
  await evaluate('document.getElementById("batch-count").value="3";document.getElementById("create-form").requestSubmit()');
  await until(() => evaluate('!document.getElementById("create-token").disabled && document.querySelectorAll("#servers tr").length===5 && document.getElementById("token-result-title").textContent.includes("4")'), 'batch generation');
  assert.deepEqual(await tabCounts(), [2, 2, 5, 9]);
  assert.equal(await evaluate('document.getElementById("issued-results").childElementCount'), 0, 'Batch output must only show a summary');
  await fitsViewport();
  await capture('batch-mobile');
  await viewport(768, 900);
  await fitsViewport();
  await viewport(1280, 900);
  await evaluate('document.getElementById("token-result").scrollIntoView({block:"center"})');
  await capture('batch-desktop');
  await click('#export-tokens');
  const exportedText = await exported();
  const lines = exportedText.trim().split(/\r?\n/).map(line => line.split('\t'));
  assert.equal(lines.length, 4);
  assert.equal(new Set(lines.map(([name]) => name)).size, 4);
  assert.equal(new Set(lines.map(([, token]) => token)).size, 4);
  assert.ok(lines.some(([name, token]) => name === randomName && token === singleToken), 'Export must retain earlier results from this session');
  const allNames = await evaluate(`fetch('/v1/registrations',{headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.json()).then(s=>s.registrations.map(r=>r.name))`);
  for (const [name, token] of lines) {
    assert.match(name, /^[A-Za-z]{12}$/);
    assert.match(token, /^[A-Za-z0-9_-]{43}$/);
    assert.ok(allNames.includes(name));
  }
  const preferences = await evaluate('localStorage.getItem("spool.relay.ui-preferences.v1")');
  for (const [, token] of lines) assert.equal(preferences.includes(token), false, 'Credentials leaked to preferences');
  await click('#dashboard-preferences');
  await language('en');
  assert.equal(await evaluate('document.getElementById("page-title").textContent'), 'Server access');
  assert.equal(await evaluate('document.getElementById("create-token").textContent'), 'Generate tokens');
  assert.equal(await evaluate('document.getElementById("token-result-title").textContent'), 'Session tokens: 4');
  assert.equal(await evaluate('document.getElementById("servers-title").textContent'), 'Token management');
  assert.equal(await evaluate('document.getElementById("token-tab-unused").getAttribute("aria-selected")'), 'true');
  await click('[data-theme-choice="dark"]');
  await click('#close-preferences');
  await viewport(320, 720, true);
  await fitsViewport();
  const tabsFit = async () => assert.equal(await evaluate('[...document.querySelectorAll(".token-tabs [role=tab]")].every(e=>e.scrollWidth<=e.clientWidth)'), true, 'Token tabs overflow on a narrow screen');
  await tabsFit();
  await capture('batch-mobile-dark-en');
  await openSidebar();
  await click('#dashboard-preferences');
  await language('ja');
  await click('#close-preferences');
  await tabsFit();
  await fitsViewport();
  await click('#refresh');
  assert.equal(await evaluate('document.getElementById("token-tab-unused").getAttribute("aria-selected")'), 'true', 'Refresh must preserve the filter');
  await selectFilter('revoked');
  assert.deepEqual(await evaluate('[...document.querySelectorAll("#servers .badge")].map(e=>e.textContent).sort()'), ['取り消し済み', '登録解除済み'].sort());
  await capture('tokens-mobile-ja');
  await openSidebar();
  await viewport(1280, 900);
  assert.equal(await evaluate('document.getElementById("settings-sidebar").inert || document.getElementById("settings-main").inert || !document.getElementById("sidebar-backdrop").hidden'), false, 'Resizing must release the modal drawer');
  await click('#dashboard-preferences');
  await language('en');
  await click('#close-preferences');
  await viewport(320, 720, true);
  assert.equal(await evaluate('document.getElementById("settings-sidebar").inert'), true, 'Narrow sidebar must start closed after resizing');
  await openSidebar();
  await click('#logout');
  assert.equal(await evaluate('document.getElementById("dashboard").hidden && document.getElementById("issued-results").childElementCount===0 && document.getElementById("servers").childElementCount===0'), true);
  assert.equal(await evaluate('document.getElementById("login").hidden'), false);
  await fitsViewport();
  await send('Page.reload');
  await until(() => evaluate('document.getElementById("login-title")?.textContent === "Sign in to Relay" && document.documentElement.dataset.theme === "dark"'), 'preferences survive reload');
  assert.equal(await evaluate('document.getElementById("dashboard").hidden && document.getElementById("issued-results").childElementCount===0 && document.getElementById("token-result").hidden'), true, 'Reload must not restore credentials');
  await fitsViewport();
  assert.deepEqual(errors, [], "Browser errors");
  console.log(JSON.stringify({ ok: true, coverage: ["login", "login-settings-position", "navigation-order", "mobile-drawer", "drawer-keyboard-focus", "token-tabs", "retired-tokens", "offline-registration", "deployment-addresses", "proxy-settings", "tokens", "random-names", "batch-summary", "txt-download", "revocation", "languages", "preferences-persistence", "version", "light-dark", "desktop-tablet-mobile"], artifacts }));
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
