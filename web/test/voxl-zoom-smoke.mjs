// Uses upstream's real viewer markup, layout and input handlers with a synthetic
// desktop. No screen capture, Spool service, user data or remote connection.
// Run from the repository root with CHROME_BIN, VOXL_SOURCE and isolated TMPDIR.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
assert.ok(process.env.CHROME_BIN, "Set CHROME_BIN to headless Chromium");
assert.ok(
  process.env.VOXL_SOURCE,
  "Set VOXL_SOURCE to the upstream voxl source directory",
);
assert.ok(process.env.TMPDIR?.startsWith(join(root, "build/tmp/")));
const source = resolve(process.env.VOXL_SOURCE);
const scratch = await mkdtemp(join(process.env.TMPDIR, "voxl-zoom-browser-"));
const output = join(root, "build/artifacts/voxl-zoom");
const reports = {};
let bundle;
const fixture = `
import { configureVoxlFrame } from ${JSON.stringify(join(root, "web/src/voxl-frame.ts"))};
import { translator } from ${JSON.stringify(join(root, "web/src/i18n.ts"))};
import { setupPointer } from ${JSON.stringify(join(source, "web/src/client/pointer.ts"))};
import { createVideo } from ${JSON.stringify(join(source, "web/src/client/video.ts"))};
import { setupToolbar } from ${JSON.stringify(join(source, "web/src/client/toolbar.ts"))};
const app = {
  surface: document.getElementById('surface'), kbd: document.getElementById('kbd'),
  tools: document.getElementById('tools'), desk: { w: 2560, h: 1440 },
  view: { scale: 1 }, send: command => commands.push(command),
};
const commands = [];
const canvas = document.getElementById('screen');
canvas.width = app.desk.w; canvas.height = app.desk.h;
const ctx = canvas.getContext('2d');
ctx.fillStyle = '#dce5e0'; ctx.fillRect(0, 0, 2560, 1440);
ctx.fillStyle = '#243c38'; ctx.fillRect(0, 0, 2560, 64);
ctx.font = '26px sans-serif'; ctx.fillStyle = '#fff'; ctx.fillText('Desktop preview · synthetic test data', 32, 42);
ctx.fillStyle = '#fafbf9'; ctx.fillRect(170, 180, 1350, 1060);
ctx.fillStyle = '#e8ece7'; ctx.fillRect(170, 180, 1350, 70);
ctx.fillStyle = '#263c36'; ctx.font = '28px sans-serif'; ctx.fillText('Project notes', 210, 226);
ctx.font = '46px sans-serif'; ctx.fillText('A closer look', 240, 350);
ctx.font = '27px sans-serif'; ctx.fillStyle = '#60746b';
ctx.fillText('Zoom the view. Keep the remote desktop unchanged.', 240, 420);
for (let i = 0; i < 10; i++) { ctx.fillStyle = i % 2 ? '#dae5df' : '#e8eeea'; ctx.fillRect(240, 490 + i * 53, 1080 - (i % 3) * 160, 18); }
ctx.fillStyle = '#1f2e2c'; ctx.fillRect(1570, 450, 790, 600);
ctx.fillStyle = '#a6d4bd'; ctx.font = '26px monospace';
['$ spool', 'Remote desktop connected', 'Viewer zoom stays local', 'Keyboard and files available', '> ready'].forEach((line, i) => ctx.fillText(line, 1620, 530 + i * 65));
ctx.fillStyle = '#243c38'; ctx.fillRect(0, 1370, 2560, 70);
ctx.fillStyle = '#fff'; ctx.fillText('Files     Browser     Terminal', 40, 1415);
const video = createVideo(app, canvas, false, () => {});
video.build([[0, 720], [720, 720]]);
video.layout();
window.addEventListener('resize', video.layout);
setupPointer(app, () => {});
setupToolbar(app, video.layout);
app.tools.classList.add('files');
document.getElementById('status').style.display = 'none';
if (parent.location.search.includes('mobile')) app.tools.classList.add('touch');
const dispose = configureVoxlFrame(frameElement, translator('zh-CN'));
window.viewer = { app, commands, dispose, configure: () => configureVoxlFrame(frameElement, translator('zh-CN')) };
`;

const probe = `
const frame = document.querySelector('iframe');
const report = {};
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const check = (value, label) => { if (!value) throw new Error(label); report[label] = true; };
const near = (a, b) => Math.abs(a-b) < 2;
async function run() {
  for (let i = 0; i < 200 && !frame.contentWindow?.viewer; i++) await pause(20);
  const win = frame.contentWindow, doc = win.document;
  const { app, commands, dispose } = win.viewer;
  const surface = app.surface;
  const button = label => doc.querySelector('button[aria-label="' + label + '"]');
  const zoomLevel = () => doc.querySelector('.zoom-level').textContent;
  const emit = (type, x, y, options = {}) => surface.dispatchEvent(new win.PointerEvent(type, {
    bubbles: true, cancelable: true, clientX: x, clientY: y,
    pointerType: 'mouse', pointerId: 1, button: 0, ...options,
  }));
  const wheel = options => surface.dispatchEvent(new win.WheelEvent('wheel', { bubbles: true, cancelable: true, clientX: win.innerWidth/2, clientY: win.innerHeight/2, ...options }));
  const tick = async () => { await new Promise(resolve => win.requestAnimationFrame(resolve)); await pause(40); };
  const center = () => [win.innerWidth / 2, win.innerHeight / 2];
  const [cx, cy] = center();
  check(zoomLevel() === '100%', 'starts fitted');
  check(button('缩小').disabled && button('放大').disabled && button('适应窗口').disabled && !button('平移画面').disabled && button('平移画面').getAttribute('aria-pressed') === 'false', 'pan mode starts off with zoom controls disabled and mode toggle available');
  button('放大').click();
  check(zoomLevel() === '100%', 'disabled zoom button cannot change the view');
  commands.length = 0;
  wheel({ deltaY: 30 }); await tick();
  check(commands.some(c => c.startsWith('sp,')), 'ordinary wheel reaches native remote scrolling');
  commands.length = 0;
  wheel({ deltaY: -60, ctrlKey: true }); await tick();
  check(zoomLevel() === '100%' && commands.some(c => c.startsWith('sp,')), 'control wheel keeps native remote behavior while pan mode is off');
  commands.length = 0;
  emit('pointerdown', cx, cy, { pointerType: 'touch', pointerId: 31 });
  emit('pointerdown', cx+30, cy, { pointerType: 'touch', pointerId: 32 });
  emit('pointermove', cx-10, cy+20, { pointerType: 'touch', pointerId: 31 });
  emit('pointermove', cx+40, cy+20, { pointerType: 'touch', pointerId: 32 });
  emit('pointerup', cx-10, cy+20, { pointerType: 'touch', pointerId: 31 });
  emit('pointerup', cx+40, cy+20, { pointerType: 'touch', pointerId: 32 });
  await tick();
  check(zoomLevel() === '100%' && commands.some(c => c.startsWith('sp,')), 'two fingers scroll the remote desktop without zooming while pan mode is off');
  button('平移画面').click();
  check(button('平移画面').getAttribute('aria-pressed') === 'true' && !button('放大').disabled, 'pan mode can be enabled at fit and unlocks zoom controls');
  commands.length = 0;
  wheel({ deltaY: -60, ctrlKey: true }); await tick();
  check(zoomLevel() !== '100%' && !commands.length, 'control wheel zooms locally only while pan mode is on');
  button('适应窗口').click(); button('放大').click(); button('放大').click();
  await tick();
  check(zoomLevel() === '156%', 'zoom buttons update percentage');
  button('平移画面').click();
  const frozen = surface.style.transform;
  check(zoomLevel() === '156%' && button('平移画面').getAttribute('aria-pressed') === 'false' && button('放大').disabled, 'turning pan mode off preserves zoom and disables zoom controls');
  const rect = surface.getBoundingClientRect();
  const pixel = [1640, 820];
  const x = rect.left + pixel[0] / app.desk.w * rect.width;
  const y = rect.top + pixel[1] / app.desk.h * rect.height;
  commands.length = 0;
  emit('pointerdown', x, y); emit('pointerup', x, y);
  check(commands.includes('m,1640,820') && commands.includes('b,1,1') && commands.includes('b,1,0'), 'native clicks map to correct zoomed desktop pixels after pan mode is turned off');
  commands.length = 0;
  emit('pointerdown', cx, cy, { button: 1 });
  emit('pointermove', cx+60, cy+30, { button: 1 });
  emit('pointerup', cx+60, cy+30, { button: 1 });
  check(commands.includes('b,2,1') && commands.includes('b,2,0') && surface.style.transform === frozen, 'middle drag keeps native remote behavior while pan mode is off');
  commands.length = 0;
  wheel({ deltaY: -60, metaKey: true }); await tick();
  button('放大').click(); button('适应窗口').click();
  check(commands.some(c => c.startsWith('sp,')) && surface.style.transform === frozen, 'disabled zoom controls and command wheel preserve the zoomed view');
  button('平移画面').click();
  check(surface.style.transform === frozen, 'reenabling pan mode preserves the existing view');
  commands.length = 0;
  emit('pointerdown', cx, cy, { button: 1 });
  emit('pointermove', cx+60, cy+30, { button: 1 });
  emit('pointerup', cx+60, cy+30, { button: 1 });
  check(!commands.length && surface.getBoundingClientRect().left > rect.left, 'middle drag pans locally only while pan mode is on');
  commands.length = 0;
  emit('pointerdown', cx, cy); emit('pointermove', cx-30, cy); emit('pointerup', cx-30, cy);
  check(!commands.length && button('平移画面').getAttribute('aria-pressed') === 'true', 'pan mode consumes left dragging');
  const panned = surface.style.transform;
  button('平移画面').click();
  check(surface.style.transform === panned && zoomLevel() === '156%', 'turning pan mode off preserves both zoom and panned position');
  commands.length = 0;
  emit('pointerdown', cx, cy, { pointerType: 'touch', pointerId: 33 });
  emit('pointerdown', cx+30, cy, { pointerType: 'touch', pointerId: 34 });
  emit('pointermove', cx-10, cy+20, { pointerType: 'touch', pointerId: 33 });
  emit('pointermove', cx+40, cy+20, { pointerType: 'touch', pointerId: 34 });
  emit('pointerup', cx-10, cy+20, { pointerType: 'touch', pointerId: 33 });
  emit('pointerup', cx+40, cy+20, { pointerType: 'touch', pointerId: 34 });
  await tick();
  check(commands.some(c => c.startsWith('sp,')) && surface.style.transform === panned, 'native two-finger scrolling resumes at the retained zoom and position');
  button('平移画面').click();
  button('适应窗口').click();
  check(zoomLevel() === '100%' && button('平移画面').getAttribute('aria-pressed') === 'true', 'fit resets translation while keeping pan mode enabled');
  commands.length = 0;
  emit('pointerdown', cx, cy, { pointerType: 'touch', pointerId: 35 });
  emit('pointermove', cx+30, cy, { pointerType: 'touch', pointerId: 35 });
  emit('pointerup', cx+30, cy, { pointerType: 'touch', pointerId: 35 });
  await pause(600);
  check(!commands.length, 'single touch pans locally without clicks drag or long press while pan mode is on');
  button('平移画面').click();
  commands.length = 0;
  emit('pointerdown', cx, cy, { pointerType: 'touch', pointerId: 11 });
  emit('pointerup', cx, cy, { pointerType: 'touch', pointerId: 11 });
  check(commands.includes('b,1,1') && commands.includes('b,1,0'), 'single touch retains native click');
  commands.length = 0;
  emit('pointerdown', cx-30, cy, { pointerType: 'touch', pointerId: 12 });
  emit('pointermove', cx-50, cy, { pointerType: 'touch', pointerId: 12 });
  check(commands.includes('b,1,1'), 'single touch can begin native drag');
  button('平移画面').click();
  check(commands.at(-1) === 'b,1,0', 'enabling pan mode releases an existing remote touch drag');
  commands.length = 0;
  emit('pointerdown', cx+50, cy, { pointerType: 'touch', pointerId: 13 });
  emit('pointermove', cx-100, cy, { pointerType: 'touch', pointerId: 12 });
  emit('pointermove', cx+100, cy, { pointerType: 'touch', pointerId: 13 });
  emit('pointerup', cx+100, cy, { pointerType: 'touch', pointerId: 13 });
  emit('pointermove', cx-90, cy+10, { pointerType: 'touch', pointerId: 12 });
  emit('pointerup', cx-90, cy+10, { pointerType: 'touch', pointerId: 12 });
  await pause(600);
  check(zoomLevel() === '200%' && !commands.length, 'pinch and remaining finger never send clicks scroll or delayed long press');
  emit('pointerdown', cx-20, cy, { pointerType: 'touch', pointerId: 36 });
  button('平移画面').click();
  const heldView = surface.style.transform;
  emit('pointermove', cx-60, cy+30, { pointerType: 'touch', pointerId: 36 });
  emit('pointerup', cx-60, cy+30, { pointerType: 'touch', pointerId: 36 });
  check(!commands.length && surface.style.transform === heldView, 'turning mode off during a local gesture freezes the view without leaking remote input');
  emit('pointerdown', cx, cy, { pointerType: 'touch', pointerId: 37 });
  emit('pointerup', cx, cy, { pointerType: 'touch', pointerId: 37 });
  check(commands.includes('b,1,1') && commands.includes('b,1,0'), 'a new touch returns to native remote input after the local gesture ends');
  button('平移画面').click();
  const oldZoom = zoomLevel();
  frame.style.height = '420px'; await tick();
  doc.dispatchEvent(new win.Event('fullscreenchange')); await tick();
  check(zoomLevel() === oldZoom && !surface.style.transform.includes('NaN'), 'resize and fullscreen layout retain zoom');
  const controls = [...doc.querySelectorAll('#spool-voxl-zoom button, #kb-btn, #files-btn, #fs-btn')];
  check(controls.every(el => { const r = el.getBoundingClientRect(); return r.width && r.left >= 0 && r.right <= win.innerWidth+1 && r.bottom <= win.innerHeight+1; }), 'zoom keyboard files and native fullscreen controls remain inside viewport');
  commands.length = 0;
  dispose();
  check(!doc.getElementById('spool-voxl-zoom') && !surface.style.transform, 'dispose removes controls transform and observers');
  wheel({ ctrlKey: true, deltaY: -40 }); await tick();
  check(commands.some(c => c.startsWith('sp,')), 'dispose restores native event handling');
  // Reinstall to leave a useful zoomed screenshot, not a teardown screenshot.
  win.viewer.configure();
  check(doc.querySelectorAll('#spool-voxl-zoom').length === 1, 'same document can reconfigure after cleanup without duplicate adapters');
  win.viewer.configure();
  check(doc.querySelectorAll('#spool-voxl-zoom').length === 1, 'repeat configuration is idempotent');
  frame.style.height = '';
  await tick();
  doc.querySelector('button[aria-label="平移画面"]').click();
  doc.querySelector('button[aria-label="放大"]').click();
  doc.querySelector('button[aria-label="放大"]').click();
}
run().catch(error => { report.error = error.stack || String(error); }).finally(async () => {
  await fetch('/report?mode=' + new URL(location.href).searchParams.get('mode'), { method: 'POST', body: JSON.stringify(report) });
});
`;

const html = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>
*{box-sizing:border-box}html,body{margin:0;height:100%;background:#e9ede7;font:14px system-ui;color:#30332e}
main{margin:20px auto;padding:16px;max-width:1240px;height:calc(100vh - 40px);background:#f8f9f6;border:1px solid #cbd3c7;border-radius:12px;display:flex;flex-direction:column;gap:12px}
header{display:flex;align-items:center;justify-content:space-between;font-weight:600}small{font-weight:400;color:#858981}iframe{border:0;border-radius:9px;width:100%;flex:1;min-height:0}
body.mobile main{width:360px;margin:8px auto;height:calc(100vh - 16px);padding:10px}
</style></head><body><main><header>远程桌面 <small>缩放交互验证 · 模拟画面</small></header><iframe src="/frame" allow="fullscreen"></iframe></main><script src="/probe.js"></script></body></html>`;
let upstream;
const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost");
  if (url.pathname === "/report") {
    let body = "";
    for await (const part of req) body += part;
    reports[url.searchParams.get("mode")] = JSON.parse(body);
    res.end("ok");
    return;
  }
  if (url.pathname === "/client.js" || url.pathname === "/probe.js") {
    res.setHeader("Content-Type", "text/javascript; charset=utf-8");
    res.end(url.pathname === "/client.js" ? bundle : probe);
    return;
  }
  if (url.pathname === "/frame") {
    res.setHeader("Content-Type", "text/html; charset=utf-8");
    res.end(upstream);
    return;
  }
  if (url.pathname === "/inter.woff2") {
    res.setHeader("Content-Type", "font/woff2");
    res.end(await readFile(join(source, "web/inter.woff2")));
    return;
  }
  if (url.pathname === "/") {
    res.setHeader("Content-Type", "text/html; charset=utf-8");
    res.end(
      html.replace(
        "<body>",
        url.search.includes("mobile") ? '<body class="mobile">' : "<body>",
      ),
    );
    return;
  }
  res.writeHead(404);
  res.end();
});

try {
  await mkdir(output, { recursive: true });
  bundle = (
    await build({
      stdin: { contents: fixture, resolveDir: root, loader: "ts" },
      bundle: true,
      write: false,
      format: "iife",
    })
  ).outputFiles[0].text;
  upstream = (
    await readFile(join(source, "web/index.html"), "utf8")
  ).replaceAll("{{title}}", "Spool");
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  for (const [mode, width, height] of [
    ["desktop", 1280, 900],
    ["mobile", 500, 844],
  ]) {
    const profile = join(scratch, mode);
    const child = spawn(
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
        "--remote-debugging-port=0",
        "--remote-debugging-address=127.0.0.1",
        `--user-data-dir=${profile}`,
        "about:blank",
      ],
      { stdio: ["ignore", "pipe", "pipe"] },
    );
    let log = "";
    for (const stream of [child.stdout, child.stderr])
      stream.on("data", (data) => {
        log = (log + data).slice(-8000);
      });
    const closed = new Promise((resolve) => child.once("close", resolve));
    let socket;
    try {
      let port;
      for (let i = 0; i < 100; i++) {
        try {
          port = Number(
            (await readFile(join(profile, "DevToolsActivePort"), "utf8")).split(
              "\n",
            )[0],
          );
          break;
        } catch {
          await new Promise((resolve) => setTimeout(resolve, 50));
        }
      }
      assert.ok(port, "Chrome debugging port unavailable: " + log);
      const targets = await (
        await fetch(`http://127.0.0.1:${port}/json/list`)
      ).json();
      const target = targets.find((item) => item.type === "page");
      socket = new WebSocket(target.webSocketDebuggerUrl);
      await new Promise((resolve, reject) => {
        socket.addEventListener("open", resolve, { once: true });
        socket.addEventListener("error", reject, { once: true });
      });
      let id = 0;
      const pending = new Map();
      socket.addEventListener("message", (event) => {
        const message = JSON.parse(event.data);
        const result = pending.get(message.id);
        if (result) {
          pending.delete(message.id);
          message.error
            ? result.reject(new Error(JSON.stringify(message.error)))
            : result.resolve(message.result);
        }
      });
      const send = (method, params = {}) =>
        new Promise((resolve, reject) => {
          const request = ++id;
          const timer = setTimeout(() => {
            pending.delete(request);
            reject(new Error("CDP timeout: " + method));
          }, 10000);
          pending.set(request, {
            resolve: (value) => {
              clearTimeout(timer);
              resolve(value);
            },
            reject: (error) => {
              clearTimeout(timer);
              reject(error);
            },
          });
          socket.send(JSON.stringify({ id: request, method, params }));
        });
      await send("Page.enable");
      await send("Emulation.setDeviceMetricsOverride", {
        width,
        height,
        deviceScaleFactor: 1,
        mobile: false,
      });
      await send("Page.navigate", {
        url: `http://127.0.0.1:${server.address().port}/?mode=${mode}`,
      });
      for (let i = 0; i < 150 && !reports[mode]; i++)
        await new Promise((resolve) => setTimeout(resolve, 100));
      const screenshot = await send("Page.captureScreenshot", {
        format: "png",
      });
      await writeFile(
        join(output, mode + ".png"),
        Buffer.from(screenshot.data, "base64"),
      );
    } finally {
      socket?.close();
      child.kill("SIGTERM");
      const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
      await closed;
      clearTimeout(timer);
      await writeFile(join(output, mode + "-chrome.log"), log);
    }
    await writeFile(
      join(output, "browser-report.json"),
      JSON.stringify(reports, null, 2),
    );
    assert.ok(reports[mode], `No ${mode} report`);
    assert.equal(
      reports[mode].error,
      undefined,
      JSON.stringify(reports[mode], null, 2),
    );
  }
  console.log(JSON.stringify(reports, null, 2));
} finally {
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
  await rm(scratch, { recursive: true, force: true });
}
