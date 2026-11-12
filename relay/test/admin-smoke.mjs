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
let diagnose;
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
  fixture = spawn(join(scratch, "relay.test"), ["-test.run=^TestAdminBrowserFixture$", "-test.timeout=150s"], {
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
  let registrationResponses = 0;
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
    if (message.method === "Network.responseReceived" && new URL(message.params.response.url).pathname === '/v1/registrations/search') registrationResponses++;
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
  diagnose = () => evaluate(`({notice:document.getElementById('notice')?.textContent,dialogs:[...document.querySelectorAll('dialog[open]')].map(e=>e.id),listBusy:document.getElementById('token-panel')?.getAttribute('aria-busy'),selection:document.getElementById('token-selection-count')?.textContent,refreshDisabled:document.getElementById('refresh')?.disabled,bulkDisabled:document.getElementById('bulk-disable-web')?.disabled})`);
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
  const idleLists = () => until(() => evaluate('document.getElementById("token-panel")?.getAttribute("aria-busy")!=="true" && document.getElementById("activity-page")?.getAttribute("aria-busy")!=="true"'), 'server list response');
  const click = async (selector) => {
    const { x, y } = await evaluate(`(() => {
      const element = document.querySelector(${JSON.stringify(selector)});
      element.scrollIntoView({block:'nearest'});
      const rect = element.getBoundingClientRect();
      return {x:rect.x+rect.width/2,y:rect.y+rect.height/2};
    })()`);
    await send("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount: 1 });
    await send("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount: 1 });
    await idleLists();
  };
  const key = async (key, code, windowsVirtualKeyCode, modifiers = 0) => {
    for (const type of ['keyDown', 'keyUp'])
      await send('Input.dispatchKeyEvent', { type, key, code, windowsVirtualKeyCode, modifiers });
    await idleLists();
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
  const rowNames = () => evaluate('[...document.querySelectorAll("#servers .server-name")].map(e=>e.textContent)');
  const filterValue = async (id, value) => {
    await evaluate(`(() => {const element=document.getElementById(${JSON.stringify(id)});element.value=${JSON.stringify(value)};element.dispatchEvent(new Event(element.tagName==='INPUT'?'input':'change',{bubbles:true}));})()`);
    await idleLists();
  };
  const checkedIDs = () => evaluate('[...document.querySelectorAll("#servers [data-select-token]:checked")].map(e=>e.dataset.selectToken)');
  const pagination = () => evaluate(`({
    summary: document.getElementById('token-page-summary').textContent,
    status: document.getElementById('token-page-status').textContent,
    previousDisabled: document.getElementById('token-previous-page').disabled,
    nextDisabled: document.getElementById('token-next-page').disabled
  })`);
  const fitsViewport = async () => {
    assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth'), true, "Page overflows horizontally");
    assert.equal(await evaluate(`document.getElementById('dashboard').hidden || document.getElementById('settings-main').scrollWidth <= document.getElementById('settings-main').clientWidth`), true, "Settings content overflows horizontally");
  };
  const deviceStatusAligned = async () => {
    assert.equal(await evaluate(`(() => {
      const center = element => { const rect = element.getBoundingClientRect(); return (rect.left + rect.right) / 2; };
      if (matchMedia('(max-width: 700px)').matches) {
        return [...document.querySelectorAll('#servers .device-status')].every(cell => {
          const left = cell.getBoundingClientRect().left;
          const badge = cell.querySelector('.badge').getBoundingClientRect();
          const online = cell.querySelector('.connection-state');
          const onlineRect = online?.getBoundingClientRect();
          return Math.abs(badge.left - left) < 1
            && Math.abs(cell.parentElement.querySelector('.web-status').closest('td').getBoundingClientRect().left - left) < 1
            && (!online || (onlineRect.left > badge.right && Math.abs((onlineRect.top + onlineRect.bottom) / 2 - (badge.top + badge.bottom) / 2) < 1));
        });
      }
      return [...document.querySelectorAll('#servers .connection-state')].every(online => {
        const cell = online.closest('td');
        return Math.abs(center(cell.querySelector('.badge')) - center(online)) < 1 && Math.abs(center(cell) - center(online)) < 1;
      });
    })()`), true, 'Narrow device status must align left with the online state beside its badge; desktop status must stay centered');
  };
  await send("Runtime.enable");
  await send("Log.enable");
  await send("Page.enable");
  await send("Network.enable");
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
  assert.deepEqual(await evaluate('[...document.querySelectorAll("[data-page]")].map(e=>e.dataset.page)'), ['servers', 'network', 'activity']);
  assert.deepEqual(await tabCounts(), [2, 1, 0, 3]);
  assert.deepEqual(await evaluate('[...document.querySelectorAll("#servers .server-name")].map(e=>e.textContent).sort()'), ['home-mac', 'offline-server']);
  assert.deepEqual(await evaluate('[...document.querySelectorAll("#servers .badge")].map(e=>e.textContent)'), ['已注册', '已注册']);
  assert.deepEqual(await evaluate('[...document.querySelectorAll("#servers .connection-state")].map(e=>e.textContent).sort()'), ['在线', '离线']);
  assert.deepEqual(await evaluate('[...document.querySelectorAll("#token-table th")].map(e=>e.textContent)'), ['Token 名称', '设备状态', '注册 IP', 'Web 状态', '操作']);
  const initialList = await evaluate(`fetch('/v1/registrations',{headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.json()).then(s=>s.registrations)`);
  for (const entry of initialList.filter(entry => !entry.unregistered_at)) {
    const rendered = await evaluate(`(() => {
      const row = [...document.querySelectorAll('#servers tr')].find(row => row.querySelector('.server-name').textContent === ${JSON.stringify(entry.name)});
      const link = row.cells[3].querySelector('a');
      return { deviceID: row.cells[1].querySelector('.badge').title, nameText: row.cells[0].textContent, webURL: link?.href, webTitle: link?.title, webLabel: link?.getAttribute('aria-label'), hasIcon: !!link?.querySelector('svg'), count: Number(row.cells[3].querySelector('.connection-count').textContent) };
    })()`);
    assert.equal(rendered.deviceID, entry.server_id, 'Registration badge hover must reveal the device ID');
    assert.equal(rendered.nameText.includes(entry.server_id), false, 'Device IDs belong on the status badge');
    assert.equal(rendered.webURL, entry.web_url);
    assert.equal(rendered.webTitle, entry.web_url, 'Web icon hover must reveal the URL');
    assert.equal(rendered.webLabel, entry.web_url);
    assert.equal(rendered.hasIcon, !!entry.web_url);
    assert.equal(rendered.count, entry.connections);
  }
  await deviceStatusAligned();
  const published = initialList.find(entry => entry.web_url);
  assert.ok(published, 'Fixture must publish a Web entry');
  await click('#servers .web-link');
  const workspaceTab = await until(async () => {
    const tabs = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
    return tabs.find(tab => tab.url === published.web_url && tab.title === 'Spool browser fixture');
  }, 'Web icon opens the published workspace');
  await send('Target.closeTarget', { targetId: workspaceTab.id });
  await filterValue('token-search', 'HOME-MAC');
  assert.deepEqual(await rowNames(), ['home-mac'], 'Search must ignore case');
  await filterValue('token-search', published.server_id.slice(8, 30));
  assert.deepEqual(await rowNames(), ['home-mac'], 'Search must match Device ID');
  await filterValue('token-search', published.registration_ip);
  assert.equal((await rowNames()).length, 2, 'Search must match registration IP');
  await filterValue('token-search', 'does-not-exist');
  assert.equal((await rowNames()).length, 0);
  assert.equal(await evaluate('document.getElementById("empty-state").textContent'), '没有匹配的 token，请调整搜索或筛选条件。');
  assert.equal(await evaluate('document.getElementById("token-pagination").hidden'), true);
  await click('#clear-token-filters');
  await filterValue('token-connection-filter', 'offline');
  assert.deepEqual(await rowNames(), ['offline-server']);
  await filterValue('token-web-filter', 'published');
  assert.deepEqual(await rowNames(), [], 'Filters must combine');
  await filterValue('token-connection-filter', 'online');
  assert.deepEqual(await rowNames(), ['home-mac']);
  await click('#servers .server-name');
  assert.equal(await evaluate('document.getElementById("token-details").open'), true);
  assert.equal(await evaluate('document.getElementById("detail-device-id").textContent'), published.server_id);
  assert.equal(await evaluate('document.getElementById("detail-web-url").href'), published.web_url);
  await send('Browser.grantPermissions', { origin: url, permissions: ['clipboardReadWrite', 'clipboardSanitizedWrite'] });
  await click('[data-copy-detail="detail-device-id"]');
  assert.equal(await evaluate('navigator.clipboard.readText()'), published.server_id, 'Detail copy must use the full ID');
  await viewport(320, 720, true);
  await fitsViewport();
  assert.equal(await evaluate('document.getElementById("token-details").scrollWidth <= document.getElementById("token-details").clientWidth'), true, 'Details must fit on mobile');
  await capture('token-details-mobile');
  await key('Escape', 'Escape', 27);
  assert.equal(await evaluate('document.activeElement.matches("#servers .server-name")'), true, 'Details must restore focus');
  await viewport(1280, 900);
  await click('#clear-token-filters');
  await selectFilter('revoked');
  assert.equal(await evaluate('document.querySelector("#servers .server-name").textContent'), 'retired-server');
  assert.equal(await evaluate('document.querySelector("#servers .badge").textContent'), '已注销');
  assert.equal(await evaluate('!!document.querySelector("#servers .web-link")'), false, 'Retired tokens must not have a Web link');
  assert.equal(await evaluate('document.querySelectorAll("#servers .server-actions button:not(.purge)").length'), 0, 'Retired tokens must not offer active actions');
  assert.equal(await evaluate('document.querySelectorAll("#servers [data-select-token]").length'), 1, 'Retired tokens must remain selectable for permanent deletion');
  await click('#servers .server-name');
  assert.notEqual(await evaluate('document.getElementById("detail-unregistered-at").textContent'), '—');
  assert.equal(await evaluate('document.querySelector("[data-copy-detail=detail-web-url]").disabled'), true);
  await click('#close-token-details');
  await selectFilter('unused');
  assert.equal(await evaluate('document.getElementById("empty-state").hidden || !document.getElementById("token-table").hidden'), false, 'Unused view must have an empty state');
  assert.equal(await evaluate('document.getElementById("token-pagination").hidden'), true, 'Empty lists must hide pagination');
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
  const publishedRow = await evaluate('[...document.querySelectorAll("#servers tr")].findIndex(row => row.querySelector(".web-link")) + 1');
  const publishedWebButton = `#servers tr:nth-child(${publishedRow}) .server-actions button:not(.revoke)`;
  await click(publishedWebButton);
  await until(() => evaluate('!document.querySelector("#servers .web-link")'), 'disabled Web removes the link');
  assert.equal(await evaluate(`document.querySelector(${JSON.stringify(publishedWebButton)}).textContent`), '允许 Web');
  await click(publishedWebButton);
  await until(() => evaluate('!!document.querySelector("#servers .web-link")'), 'allowed Web restores the link');
  assert.equal(await evaluate(`document.querySelector(${JSON.stringify(publishedWebButton)}).textContent`), '停用 Web');
  await click('#refresh');
  const name = "office-linux";
  await evaluate(`document.getElementById("server-name").value=${JSON.stringify(name)};document.getElementById("create-form").requestSubmit()`);
  await until(() => evaluate('!document.getElementById("token-result").hidden && document.querySelectorAll("#servers tr").length===1 && document.querySelector("#servers .server-name").textContent==="office-linux"'), "token creation");
  assert.equal(await evaluate('document.getElementById("token-tab-unused").getAttribute("aria-selected")'), 'true');
  assert.deepEqual(await tabCounts(), [2, 1, 1, 4]);
  const token = await evaluate('document.getElementById("issued-token").value');
  assert.match(token, /^[A-Za-z0-9_-]{32,}$/);
  assert.equal(await evaluate('document.querySelector("#servers .server-name").textContent'), name);
  assert.deepEqual(await pagination(), { summary: '第 1–1 条，共 1 条', status: '第 1 / 1 页', previousDisabled: true, nextDisabled: true });
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
  await deviceStatusAligned();
  await fitsViewport();
  await capture("mobile");
  await selectFilter('registered');
  await evaluate('document.getElementById("servers-title").scrollIntoView({block:"start"})');
  await capture('device-status-mobile');
  await selectFilter('all');
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
  await deviceStatusAligned();
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
  assert.deepEqual(await evaluate('[...document.querySelectorAll("#token-table th")].map(e=>e.textContent)'), ['Token name', 'Device status', 'Registration IP', 'Web status', 'Action']);
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
  // More than two pages exercise real ordering, refreshes and page boundaries.
  await evaluate('document.getElementById("batch-count").value="36";document.getElementById("create-form").requestSubmit()');
  await until(() => evaluate('!document.getElementById("create-token").disabled && document.querySelector("#token-tab-unused .tab-count").textContent==="41"'), 'pagination batch');
  await click('#dismiss-token');
  const paginationList = await evaluate(`fetch('/v1/registrations',{headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)}}}).then(r=>r.json()).then(s=>s.registrations)`);
  const unusedNames = paginationList.filter(entry => !entry.server_id && !entry.revoked_at && !entry.unregistered_at).reverse().map(entry => entry.name);
  assert.equal(unusedNames.length, 41);
  assert.deepEqual(await rowNames(), unusedNames.slice(0, 20), 'First page must show the newest 20 tokens');
  assert.deepEqual(await pagination(), { summary: 'Showing 1–20 of 41', status: 'Page 1 of 3', previousDisabled: true, nextDisabled: false });
  await click('#token-next-page');
  assert.deepEqual(await rowNames(), unusedNames.slice(20, 40));
  assert.deepEqual(await pagination(), { summary: 'Showing 21–40 of 41', status: 'Page 2 of 3', previousDisabled: false, nextDisabled: false });
  const beforeRefresh = registrationResponses;
  await click('#refresh');
  await until(() => registrationResponses > beforeRefresh, 'page refresh');
  assert.deepEqual(await rowNames(), unusedNames.slice(20, 40), 'Refresh must preserve the page and ordering');
  const beforePoll = registrationResponses;
  await until(() => registrationResponses > beforePoll, 'automatic page refresh');
  assert.deepEqual(await rowNames(), unusedNames.slice(20, 40), 'Polling must preserve the page');
  await navigate('network');
  await navigate('servers');
  assert.equal((await pagination()).status, 'Page 2 of 3', 'Navigation must preserve the page');
  await language('zh-CN');
  assert.equal((await pagination()).status, '第 2 / 3 页', 'Language changes must preserve the page');
  assert.equal((await pagination()).summary, '第 21–40 条，共 41 条');
  await language('ja');
  assert.equal((await pagination()).status, '2 / 3 ページ');
  assert.equal((await pagination()).summary, '41 件中 21～40 件');
  await language('en');
  await click('#token-previous-page');
  assert.deepEqual(await rowNames(), unusedNames.slice(0, 20));
  await click('#token-next-page');
  await click('#token-next-page');
  assert.deepEqual(await rowNames(), unusedNames.slice(40));
  assert.deepEqual(await pagination(), { summary: 'Showing 41–41 of 41', status: 'Page 3 of 3', previousDisabled: false, nextDisabled: true });
  await evaluate('document.getElementById("servers-title").scrollIntoView({block:"center"})');
  await capture('pagination-desktop-dark');
  await evaluate('document.querySelector("[data-theme-choice=light]").click()');
  await evaluate('document.getElementById("servers-title").scrollIntoView({block:"center"})');
  await capture('pagination-desktop');
  await viewport(320, 720, true);
  await tabsFit();
  await fitsViewport();
  await evaluate('document.getElementById("servers-title").scrollIntoView({block:"start"})');
  await capture('pagination-mobile');
  await language('ja');
  await tabsFit();
  await fitsViewport();
  assert.equal(await evaluate('document.querySelector("#token-pagination").scrollWidth <= document.querySelector("#token-pagination").clientWidth'), true, 'Translated pagination must fit on mobile');
  await language('en');
  await viewport(1280, 900);
  await evaluate('document.querySelector("#servers .revoke").click()');
  await until(() => evaluate('document.getElementById("revoke-dialog").open'), 'last-page revocation confirmation');
  await click('#revoke-dialog .danger');
  await until(() => evaluate('document.getElementById("token-page-status").textContent === "Page 2 of 2"'), 'last-page clamp after revocation');
  assert.deepEqual(await rowNames(), unusedNames.slice(20, 40));
  assert.deepEqual(await pagination(), { summary: 'Showing 21–40 of 40', status: 'Page 2 of 2', previousDisabled: false, nextDisabled: true });
  await click('[data-token-mode="single"]');
  await evaluate('document.getElementById("server-name").value="pagination-new-token";document.getElementById("create-form").requestSubmit()');
  await until(() => evaluate('!document.getElementById("create-token").disabled && document.querySelector("#servers .server-name").textContent === "pagination-new-token"'), 'creation returns to the first page');
  assert.deepEqual(await pagination(), { summary: 'Showing 1–20 of 41', status: 'Page 1 of 3', previousDisabled: true, nextDisabled: false });
  await click('#dismiss-token');
  await click('#token-next-page');
  await selectFilter('registered');
  assert.equal((await pagination()).status, 'Page 1 of 1', 'Changing filters must return to the first page');
  assert.equal((await rowNames()).length, 2);
  await selectFilter('revoked');
  assert.equal((await rowNames()).length, 3);
  await selectFilter('all');
  assert.equal((await pagination()).summary, 'Showing 1–20 of 46');
  assert.deepEqual(await tabCounts(), [2, 3, 41, 46], 'Tab counts must include all pages');
  await selectFilter('unused');
  await click('#select-token-page');
  assert.equal((await checkedIDs()).length, 20, 'Select page must select exactly its 20 active records');
  await click('#token-next-page');
  assert.equal((await checkedIDs()).length, 0);
  await click('#servers [data-select-token]');
  assert.equal(await evaluate('document.getElementById("token-selection-count").textContent'), '21 selected', 'Selection must persist across pages');
  assert.equal(await evaluate('document.getElementById("select-token-page").indeterminate'), true);
  await click('#refresh');
  await until(() => evaluate('document.getElementById("token-selection-count").textContent==="21 selected"'), 'refresh retains selection');
  await filterValue('token-search', 'pagination-new-token');
  assert.deepEqual(await rowNames(), ['pagination-new-token']);
  assert.equal(await evaluate('document.getElementById("token-selection-count").textContent'), '0 selected', 'Search must clear selection');
  assert.equal((await pagination()).status, 'Page 1 of 1');
  await click('#servers [data-select-token]');
  assert.equal(await evaluate('document.getElementById("bulk-disable-web").disabled'), true, 'Unused tokens must not offer Web changes');
  await click('#clear-token-filters');
  await selectFilter('registered');
  await click('#select-token-page');
  await click('#bulk-disable-web');
  assert.equal(await evaluate('document.querySelectorAll("#bulk-dialog-items li").length'), 2);
  await click('#bulk-dialog button[value=cancel]');
  assert.equal((await checkedIDs()).length, 2, 'Cancel must preserve selection');
  // Fail one real request while allowing the other to reach the Relay.
  const offlineEntry = initialList.find(entry => entry.name === 'offline-server');
  await evaluate(`(() => {
    const original=window.fetch;
    window.fetch=(path, options) => {
      if(path===${JSON.stringify('/v1/registrations/' + offlineEntry.id)} && options?.method==='PATCH') {
        window.fetch=original;
        return Promise.resolve(new Response(JSON.stringify({error:'fixture temporary failure'}),{status:503,headers:{'Content-Type':'application/json'}}));
      }
      return original(path,options);
    };
  })()`);
  await click('#bulk-disable-web');
  await click('#confirm-bulk-action');
  await until(() => evaluate('document.getElementById("notice").textContent.includes("1 succeeded, 1 failed")'), 'partial batch failure');
  assert.deepEqual(await checkedIDs(), [offlineEntry.id], 'Only failed batch items must remain selected');
  await filterValue('token-web-filter', 'blocked');
  assert.deepEqual(await rowNames(), ['home-mac'], 'Web disabled filter must reflect the successful mutation');
  await click('#clear-token-filters');
  await evaluate(`document.querySelector('[data-select-token="${offlineEntry.id}"]').click()`);
  await click('#bulk-disable-web');
  await click('#confirm-bulk-action');
  await until(() => evaluate('document.getElementById("notice").textContent==="Updated 1 items."'), 'retry failed Web change');
  await click('#select-token-page');
  await click('#bulk-allow-web');
  await click('#confirm-bulk-action');
  await until(() => evaluate('document.getElementById("notice").textContent==="Updated 2 items."'), 'bulk allow Web');
  assert.equal(await evaluate('!!document.querySelector("#servers .web-link")'), true);
  await selectFilter('unused');
  await click('#servers [data-select-token]');
  await click('#token-next-page');
  await click('#servers [data-select-token]');
  await click('#bulk-revoke');
  assert.equal(await evaluate('document.querySelectorAll("#bulk-dialog-items li").length'), 2, 'Confirmation must list cross-page targets');
  await click('#bulk-dialog button[value=cancel]');
  assert.equal(await evaluate('document.getElementById("token-selection-count").textContent'), '2 selected');
  await click('#bulk-revoke');
  await click('#confirm-bulk-action');
  await until(() => evaluate('document.getElementById("notice").textContent==="Updated 2 items." && document.querySelector("#token-tab-unused .tab-count").textContent==="39"'), 'bulk revocation');
  assert.deepEqual(await checkedIDs(), []);
  await selectFilter('revoked');
  assert.equal((await rowNames()).length, 5);
  await click('#servers .server-name');
  assert.notEqual(await evaluate('document.getElementById("detail-revoked-at").textContent'), '—');
  await capture('token-details-desktop');
  await click('#close-token-details');
  // Permanently deleting inactive records is a separate, explicit operation.
  const purgedName = (await rowNames())[0];
  await click('#servers .purge');
  assert.equal(await evaluate('document.getElementById("purge-dialog").open'), true);
  assert.equal(await evaluate('document.getElementById("confirm-purge").disabled'), true);
  assert.ok((await evaluate('document.getElementById("purge-warning").textContent')).includes('Devices may enroll again'));
  await click('#purge-dialog button[value=cancel]');
  assert.ok((await rowNames()).includes(purgedName), 'Cancel must preserve the record');
  await click('#servers .purge');
  await evaluate('document.getElementById("purge-confirmation").value="delete";document.getElementById("purge-confirmation").dispatchEvent(new Event("input"))');
  assert.equal(await evaluate('document.getElementById("confirm-purge").disabled'), true, 'Confirmation must match DELETE exactly');
  await evaluate('document.getElementById("purge-confirmation").value="DELETE";document.getElementById("purge-confirmation").dispatchEvent(new Event("input"))');
  await click('#confirm-purge');
  await until(() => evaluate('document.getElementById("notice").textContent==="Permanently deleted 1 records." && !document.getElementById("refresh").disabled'), 'single permanent deletion');
  assert.equal((await rowNames()).includes(purgedName), false);
  await selectFilter('unused');
  await click('#servers [data-select-token]');
  await click('#token-next-page');
  await click('#servers [data-select-token]');
  await click('#bulk-purge');
  assert.equal(await evaluate('document.querySelectorAll("#purge-items li").length'), 2, 'Purge must preserve cross-page targets');
  await viewport(320, 720, true);
  await fitsViewport();
  await capture('purge-confirmation-mobile');
  await evaluate('document.getElementById("purge-confirmation").value="DELETE";document.getElementById("purge-confirmation").dispatchEvent(new Event("input"))');
  await click('#confirm-purge');
  await until(() => evaluate('document.getElementById("notice").textContent==="Permanently deleted 2 records." && !document.getElementById("refresh").disabled'), 'cross-page permanent deletion');
  assert.equal(await evaluate('document.getElementById("token-selection-count").textContent'), '0 selected');
  assert.equal((await tabCounts())[2], 37);
  await viewport(1280, 900);
  await selectFilter('registered');
  // Delayed older responses must never overwrite a newer search.
  await evaluate(`(() => {
    const original=window.fetch;
    window.fetch=(path, options) => {
      const response=original(path,options);
      if(path==='/v1/registrations/search' && JSON.parse(options.body).query==='delayed-old-query') {
        window.fetch=original;
        return response.then(value => new Promise(resolve => {window.releaseSearch=()=>resolve(value);}));
      }
      return response;
    };
    document.getElementById('token-search').value='delayed-old-query';
    document.getElementById('token-search').dispatchEvent(new Event('input'));
  })()`);
  await until(() => evaluate('typeof window.releaseSearch==="function"'), 'delayed search response');
  await filterValue('token-search', 'HOME-MAC');
  assert.deepEqual(await rowNames(), ['home-mac']);
  await evaluate('window.releaseSearch();new Promise(resolve=>setTimeout(resolve,50))');
  assert.deepEqual(await rowNames(), ['home-mac'], 'Old search must not replace new results');
  await evaluate(`(() => {
    const original=window.fetch;
    window.fetch=(path,options) => {
      if(path==='/v1/registrations/search') {
        window.fetch=original;
        return Promise.resolve(new Response(JSON.stringify({error:'fixture list failure'}),{status:503,headers:{'Content-Type':'application/json'}}));
      }
      return original(path,options);
    };
  })()`);
  await filterValue('token-search', 'offline-server');
  assert.equal(await evaluate('document.getElementById("token-table").hidden && document.getElementById("token-list-status").textContent.includes("Refresh to retry")'), true, 'Failed search must not present stale rows as results');
  await click('#refresh');
  assert.deepEqual(await rowNames(), ['offline-server'], 'Refresh must retry a failed list request');
  assert.equal(await evaluate('document.getElementById("token-list-status").hidden'), true);
  await click('#clear-token-filters');
  // A real failed operation appears alongside successful changes, without secrets.
  const retiredEntry = initialList.find(entry => entry.name === 'retired-server');
  assert.equal(await evaluate(`fetch('/v1/registrations/${retiredEntry.id}',{method:'PATCH',headers:{Authorization:'Bearer '+${JSON.stringify(adminToken)},'Content-Type':'application/json'},body:JSON.stringify({web_blocked:true})}).then(r=>r.status)`), 404);
  await navigate('activity');
  await until(() => evaluate('document.querySelectorAll("#activity-rows tr").length===20'), 'activity history');
  assert.equal(await evaluate('document.getElementById("page-title").textContent'), 'Activity');
  assert.equal(await evaluate('document.querySelector("#activity-rows tr:nth-child(1) td:nth-child(4)").textContent'), 'Failed');
  assert.equal(await evaluate('document.querySelector("#activity-rows tr:nth-child(2) td:nth-child(2)").textContent'), 'Delete permanently');
  assert.equal(await evaluate(`document.getElementById('activity-rows').textContent.includes(${JSON.stringify(purgedName)})`), true, 'History must retain deleted target names');
  assert.equal(await evaluate(`document.getElementById('activity-rows').textContent.includes(${JSON.stringify(adminToken)})`), false);
  await capture('activity-desktop');
  const retryFailedActivityPage = async (selector, page) => {
    const previousStatus = await evaluate('document.getElementById("activity-page-status").textContent');
    const previousRows = await evaluate('document.getElementById("activity-rows").textContent');
    await evaluate(`(() => {
      const original=window.fetch;
      window.activityPageAttempts=[];
      window.fetch=(path,options) => {
        if(path==='/v1/activity/search') {
          window.activityPageAttempts.push(JSON.parse(options.body).page);
          if(window.activityPageAttempts.length===1)
            return Promise.resolve(new Response(JSON.stringify({error:'fixture activity failure'}),{status:503,headers:{'Content-Type':'application/json'}}));
          window.fetch=original;
        }
        return original(path,options);
      };
    })()`);
    await click(selector);
    await until(() => evaluate('document.getElementById("notice").textContent==="fixture activity failure"'), 'failed activity page');
    assert.equal(await evaluate('document.getElementById("activity-page-status").textContent'), previousStatus, 'A failed activity request must preserve the displayed page');
    assert.equal(await evaluate('document.getElementById("activity-rows").textContent'), previousRows, 'A failed activity request must preserve the displayed events');
    await click(selector);
    assert.deepEqual(await evaluate('window.activityPageAttempts'), [page, page], 'Retrying activity navigation must request the same page');
    assert.ok((await evaluate('document.getElementById("activity-page-status").textContent')).startsWith(`Page ${page} of`));
  };
  await retryFailedActivityPage('#activity-next-page', 2);
  await retryFailedActivityPage('#activity-previous-page', 1);
  await click('#activity-next-page');
  assert.ok((await evaluate('document.getElementById("activity-page-status").textContent')).startsWith('Page 2 of'));
  await click('#refresh');
  assert.ok((await evaluate('document.getElementById("activity-page-status").textContent')).startsWith('Page 2 of'), 'Refresh must preserve activity page');
  await language('ja');
  assert.equal(await evaluate('document.getElementById("page-title").textContent'), '操作履歴');
  await evaluate('document.querySelector("[data-theme-choice=dark]").click()');
  await viewport(320, 720, true);
  await fitsViewport();
  await capture('activity-mobile-dark');
  await language('en');
  await viewport(1280, 900);
  await navigate('servers');
  await selectFilter('registered');
  await click('#select-token-page');
  await evaluate('document.getElementById("servers-title").scrollIntoView({block:"start"})');
  await capture('token-management-desktop');
  await evaluate('document.querySelector("[data-theme-choice=dark]").click()');
  await evaluate('document.getElementById("servers-title").scrollIntoView({block:"start"})');
  await capture('token-management-dark');
  await viewport(320, 720, true);
  await fitsViewport();
  await tabsFit();
  await evaluate('document.getElementById("token-panel").scrollIntoView({block:"start"})');
  await capture('token-management-mobile');
  await language('ja');
  await fitsViewport();
  await click('#bulk-disable-web');
  assert.equal(await evaluate('document.getElementById("bulk-dialog-description").textContent.includes("2 台")'), true);
  await fitsViewport();
  await click('#bulk-dialog button[value=cancel]');
  await language('en');
  await evaluate('document.querySelector("[data-theme-choice=dark]").click()');
  await evaluate(`(() => {
    const original=window.fetch;
    window.actionAttempts=0;
    window.fetch=(path,options) => {
      if(options?.method==='PATCH') {
        window.actionAttempts++;
        return new Promise(resolve => {window.releaseAction=() => {window.fetch=original;resolve(new Response(null,{status:204}));};});
      }
      return original(path,options);
    };
  })()`);
  await click('#bulk-disable-web');
  await click('#confirm-bulk-action');
  await until(() => evaluate('typeof window.releaseAction === "function"'), 'batch in flight');
  await viewport(320, 720, true);
  assert.equal(await evaluate('document.getElementById("settings-sidebar").inert'), true, 'Narrow sidebar must start closed after resizing');
  await openSidebar();
  await click('#logout');
  assert.equal(await evaluate('document.getElementById("dashboard").hidden && document.getElementById("issued-results").childElementCount===0 && document.getElementById("servers").childElementCount===0'), true);
  assert.equal(await evaluate('document.getElementById("token-search").value==="" && document.getElementById("token-selection-count").textContent==="0 selected" && !document.getElementById("token-details").open'), true, 'Logout must clear management state');
  assert.equal(await evaluate('document.getElementById("detail-device-id").textContent'), '', 'Logout must clear detail metadata');
  assert.equal(await evaluate('document.getElementById("activity-rows").childElementCount'), 0, 'Logout must clear activity metadata');
  await evaluate('window.releaseAction()');
  await evaluate('new Promise(resolve => setTimeout(resolve, 50))');
  assert.equal(await evaluate('window.actionAttempts'), 1, 'Logging out must prevent subsequent batch requests');
  assert.equal(await evaluate('document.getElementById("login").hidden'), false);
  await fitsViewport();
  await send('Page.reload');
  await until(() => evaluate('document.getElementById("login-title")?.textContent === "Sign in to Relay" && document.documentElement.dataset.theme === "dark"'), 'preferences survive reload');
  assert.equal(await evaluate('document.getElementById("dashboard").hidden && document.getElementById("issued-results").childElementCount===0 && document.getElementById("token-result").hidden'), true, 'Reload must not restore credentials');
  await fitsViewport();
  assert.deepEqual(errors, [], "Browser errors");
  console.log(JSON.stringify({ ok: true, coverage: ["login", "login-settings-position", "navigation-order", "mobile-drawer", "drawer-keyboard-focus", "token-tabs", "server-pagination", "pagination-refresh", "pagination-boundaries", "token-search", "stale-search-responses", "combined-filters", "token-details", "detail-clipboard", "cross-page-selection", "bulk-web", "bulk-revocation", "partial-failure-retry", "permanent-deletion", "cross-page-purge", "activity-history", "activity-pagination", "retired-tokens", "offline-registration", "deployment-addresses", "proxy-settings", "tokens", "random-names", "batch-summary", "txt-download", "revocation", "languages", "preferences-persistence", "version", "light-dark", "desktop-tablet-mobile"], artifacts }));
} catch (error) {
  if (diagnose) console.error('Browser state:', await diagnose().catch(() => 'unavailable'));
  throw error;
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
