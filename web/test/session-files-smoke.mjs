// Run after the web build, with TMPDIR inside build/tmp and CHROME_BIN set.
// Set SPOOL_SMOKE_DEV=1 to verify lazy highlighting through a real Vite server.
// Uses synthetic sessions and an owned browser profile; no developer data.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createTestViteServer } from "./vite-fixture.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
assert.ok(process.env.CHROME_BIN, "Set CHROME_BIN to headless Chromium");
assert.ok(process.env.TMPDIR?.startsWith(join(root, "build/tmp/")));
const scratch = await mkdtemp(
  join(process.env.TMPDIR, "session-files-browser-"),
);
const output = join(root, "build/artifacts/session-files");
const reports = new Map();
const dev = process.env.SPOOL_SMOKE_DEV === "1";
const artifactPrefix = dev ? "dev-" : "";
const viteCleanup = [];
const failedRequests = [];
let preferencesRequests = 0;
let vite;
let blockHighlighter = false;
let blockedHighlighterRequests = 0;
const now = Date.now();
const chats = ["worktree-chat", "main-chat"].map((id) => ({
  id,
  project_id: "project",
  user_id: "owner",
  title: id,
  backend: "codex",
  status: "completed",
  updated_at: now,
  transcript: {
    parts: [
      {
        role: "assistant",
        kind: "text",
        text: "[Open file](same.txt) · [Go module](go.mod#L3) · [Long source](long.ts#L1) · [External report](/outside/report.md)",
      },
    ],
  },
  turns: [
    {
      id: "turn",
      status: "completed",
      part_start: 0,
      input_end: 0,
      part_end: 1,
      started_at: now - 1000,
      finished_at: now,
    },
  ],
}));
const workspaces = {
  "worktree-chat": {
    path: "/work/trees/feature with spaces",
    kind: "worktree",
    branch: "feature/files",
    available: true,
  },
  "main-chat": {
    path: "/work/spool",
    kind: "main",
    branch: "dev",
    available: true,
  },
};
const workspaceChoices = Object.values(workspaces).map(workspace => ({ ...workspace }));
workspaces["worktree-chat"].workspaces = workspaceChoices;
workspaces["worktree-chat"].turn_id = "turn-1";
workspaces["main-chat"].workspaces = [workspaceChoices[1]];
const probe = `
localStorage.setItem('spool.ui-preferences.v1', JSON.stringify({theme:'light',language:'en'}));
const mode = new URL(location.href).searchParams.get('mode');
const dev = new URL(location.href).searchParams.get('dev') === '1';
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check) {
  for (let i = 0; i < (dev ? 800 : 160); i++) { const value = check(); if (value) return value; await pause(25); }
  throw new Error('Timed out: ' + check.toString());
}
const visible = el => el && el.getBoundingClientRect().width > 0;
const button = label => document.querySelector('button[aria-label="' + label + '"]');
const trigger = () => document.querySelector('.session-workspace-info');
const popup = () => document.querySelector('.session-workspace-popover');
const sourceText = () => {
  const highlighted = document.querySelector('.file-code-preview');
  return (visible(highlighted) && highlighted.shadowRoot?.querySelector('code')?.textContent) || document.querySelector('.file-source')?.textContent || '';
};
const fileLink = label => [...document.querySelectorAll('.assistant-text .local-file-link')].find(el => el.textContent === label);
const browserErrors = [];
window.addEventListener('error', event => browserErrors.push(event.message));
window.addEventListener('unhandledrejection', event => browserErrors.push(String(event.reason)));
const consoleError = console.error;
console.error = (...args) => { browserErrors.push(args.map(String).join(' ')); consoleError(...args); };
async function settleDrawer(selector) {
  const drawer = await until(() => document.querySelector(selector));
  drawer.getBoundingClientRect();
  // Chrome's virtual timer can advance without compositor frames. Finish the
  // slide transition before measuring the same final layout used on a phone.
  for (const animation of drawer.getAnimations()) animation.finish();
  await until(() => {
    const rect = drawer.getBoundingClientRect();
    return rect.left >= -1 && rect.right <= innerWidth + 1;
  });
}
async function openFiles() {
  if (mode === 'mobile' ? !document.querySelector('.file-sidebar.open') : !visible(trigger())) button('Open file sidebar')?.click();
  await until(() => visible(trigger()));
  await settleDrawer(mode === 'mobile' ? '.file-sidebar.open' : '.file-sidebar');
}
async function switchChat(id) {
  if (mode === 'mobile') button('Open sidebar')?.click();
  const link = await until(() => [...document.querySelectorAll('.nav-chat-open')].find(el => el.textContent.includes(id)));
  link.click();
  await until(() => document.querySelector('.chat-heading')?.textContent.includes(id));
  await openFiles();
}
(async () => {
  const report = {};
  try {
    await until(() => document.querySelector('.chat-heading')?.textContent.includes('worktree-chat'));
    await openFiles();
    if (mode === 'mobile') {
      report.mobileFileSidebarHasNoBackdrop = !document.querySelector('.scrim');
      const files = document.querySelector('.file-sidebar').getBoundingClientRect();
      report.mobileFileSidebarFillsScreen = Math.abs(files.width - innerWidth) < 1 && Math.abs(files.height - innerHeight) < 1;
      button('Collapse file sidebar').click();
      button('Open sidebar').click();
      await settleDrawer('.sidebar.open');
      report.mobileNavSidebarHasNoBackdrop = !document.querySelector('.scrim');
      const navigation = document.querySelector('.sidebar').getBoundingClientRect();
      report.mobileNavSidebarFillsScreen = Math.abs(navigation.width - innerWidth) < 1 && Math.abs(navigation.height - innerHeight) < 1;
      report.localPreferencesOverrideServerPreferences = document.documentElement.lang === 'en' && document.documentElement.dataset.theme === 'light';
      button('Appearance and language').click();
      await until(() => document.querySelector('.appearance-dialog[open]'));
      document.querySelectorAll('.theme-options button')[1].click();
      await until(() => document.documentElement.dataset.theme === 'dark');
      document.querySelector('.language-choice .custom-select-trigger').click();
      await until(() => document.querySelector('[role="option"][data-value="ja"]'));
      document.querySelector('[role="option"][data-value="ja"]').click();
      await until(() => document.documentElement.lang === 'ja');
      report.appearanceSavedLocally = localStorage.getItem('spool.ui-preferences.v1') === JSON.stringify({theme:'dark',language:'ja'});
      document.querySelector('.language-choice .custom-select-trigger').click();
      await until(() => document.querySelector('[role="option"][data-value="en"]'));
      document.querySelector('[role="option"][data-value="en"]').click();
      await until(() => document.documentElement.lang === 'en');
      document.querySelectorAll('.theme-options button')[0].click();
      await until(() => document.documentElement.lang === 'en' && document.documentElement.dataset.theme === 'light');
      button('Close settings').click();
      button('Close sidebar').click();
      await openFiles();
    }
    if (mode === 'fallback') {
      fileLink('Go module').click();
      await until(() => sourceText().includes('module example.com/spool'));
      report.sourceVisibleWithoutHighlighter = true;
      await until(() => browserErrors.some(error => error.includes('shiki-')));
      report.sourceStaysVisibleAfterHighlightFailure = sourceText().includes('go 1.25.0');
      report.fallbackHasLineNumbers = document.querySelector('.file-source-line.highlighted[data-line="3"] code')?.textContent === 'go 1.25.0';
      fileLink('Long source').click();
      await until(() => sourceText().includes('long source contents'));
      const scroller = document.querySelector('.file-preview-scroll');
      report.plainSourceScrollsHorizontally = scroller.scrollWidth > scroller.clientWidth;
      scroller.scrollLeft = 180;
      report.plainSourceScrollMovesContent = scroller.scrollLeft === 180;
      await fetch('/smoke/report?mode=' + mode, { method: 'POST', body: JSON.stringify(report) });
      return;
    }
    report.iconAfterName = trigger().parentElement.firstElementChild.nextElementSibling.tagName === 'STRONG';
    report.detailsHiddenInitially = !popup() && !document.querySelector('.session-workspace');
    await until(() => {
      trigger()?.dispatchEvent(new PointerEvent('pointerover', { bubbles: true, pointerType: 'mouse' }));
      return popup()?.textContent.includes('feature/files');
    });
    report.hoverShowsWorktree = popup().textContent.includes('feature/files') && popup().textContent.includes('/work/trees/feature with spaces');
    const hoveredPopup = popup();
    trigger().dispatchEvent(new PointerEvent('pointerout', { bubbles: true, pointerType: 'mouse', relatedTarget: hoveredPopup }));
    hoveredPopup.dispatchEvent(new PointerEvent('pointerover', { bubbles: true, pointerType: 'mouse' }));
    await pause(180);
    report.canMoveIntoPopup = popup() === hoveredPopup;
    const box = popup().getBoundingClientRect();
    report.popupFitsViewport = box.left >= 0 && box.right <= innerWidth && box.top >= 0 && box.bottom <= innerHeight;
    hoveredPopup.dispatchEvent(new PointerEvent('pointerout', { bubbles: true, pointerType: 'mouse' }));
    await until(() => !popup());
    report.closesOnLeave = true;
    await until(() => {
      if (!popup()) { trigger()?.blur(); trigger()?.focus({ preventScroll: true }); }
      if (!popup()) trigger()?.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
      return popup();
    });
    report.keyboardFocusOpens = true;
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await until(() => !popup());
    report.escapeCloses = true;
    if (mode === 'mobile') button('Collapse file sidebar')?.click();
    fileLink('Open file').click();
    await until(() => sourceText().includes('worktree file contents'));
    report.correctWorktreeFile = true;
    report.sizeInTitle = document.querySelector('.file-preview-header small')?.textContent === '25 B';
    report.noDuplicateMetadata = !document.querySelector('.file-preview-meta');
    if (mode === 'mobile') button('Collapse file sidebar')?.click();
    fileLink('Go module').click();
    await until(() => sourceText().includes('module example.com/spool'));
    report.goModuleSourceVisible = true;
    await until(() => visible(document.querySelector('.file-code-preview')) && document.querySelector('.file-code-preview')?.shadowRoot?.querySelector('code')?.textContent.includes('module example.com/spool'));
    report.highlightingReplacesPlainSource = !document.querySelector('.file-source');
    if (dev) {
      const response = await fetch('/smoke/cache-isolation');
      const result = await response.json();
      if (!response.ok) throw new Error(JSON.stringify(result));
      report.optimizedModulesSurviveOtherViteTests = result.preserved;
    }
    if (mode === 'mobile') button('Collapse file sidebar')?.click();
    fileLink('Long source').click();
    await until(() => visible(document.querySelector('.file-code-preview')) && document.querySelector('.file-code-preview')?.shadowRoot?.querySelector('code')?.textContent.includes('long source contents'));
    await settleDrawer(mode === 'mobile' ? '.file-sidebar.open' : '.file-sidebar');
    const scroller = document.querySelector('.file-preview-scroll');
    report.longSourceOverflowsBothAxes = scroller.scrollWidth > scroller.clientWidth && scroller.scrollHeight > scroller.clientHeight;
    scroller.scrollLeft = 180;
    report.highlightedSourceScrollsHorizontally = scroller.scrollLeft === 180;
    report.scrollbarAtViewportBottom = scroller.getBoundingClientRect().bottom <= innerHeight + 1 && scroller.offsetHeight - scroller.clientHeight >= 10 && getComputedStyle(scroller, '::-webkit-scrollbar').height === '10px' && getComputedStyle(scroller, '::-webkit-scrollbar-thumb').backgroundColor !== 'rgba(0, 0, 0, 0)';
    report.defaultDirectoryTitle = document.querySelector('.file-sidebar-title strong')?.textContent === 'feature with spaces';
    button('Switch directory').click();
    const choices = await until(() => document.querySelector('[role="listbox"][aria-label="Switch directory"]'));
    report.directoryMenuShowsMainAndWorktree = choices.textContent.includes('Main checkout') && choices.textContent.includes('Worktree');
    choices.querySelector('[data-value="/work/spool"]').click();
    await until(() => document.querySelector('.file-sidebar-title strong')?.textContent === 'spool');
    report.manualSwitchClearsPreview = !document.querySelector('.file-preview');
    if (mode === 'mobile') button('Collapse file sidebar')?.click();
    fileLink('Open file').click();
    await until(() => sourceText().includes('main file contents'));
    button('Refresh files').click();
    await until(() => sourceText().includes('main file contents'));
    report.manualChoiceSurvivesRefresh = document.querySelector('.file-sidebar-title strong')?.textContent === 'spool';
    await fetch('/smoke/next-turn?mode=' + mode);
    button('Refresh files').click();
    await until(() => document.querySelector('.file-sidebar-title strong')?.textContent === 'feature with spaces');
    report.newTurnFollowsDirectory = !document.querySelector('.file-preview');
    await switchChat('main-chat');
    report.oldPreviewCleared = !document.querySelector('.file-preview');
    report.singleDirectoryHidesChooser = !button('Switch directory');
    await until(() => {
      trigger()?.dispatchEvent(new PointerEvent('pointerover', { bubbles: true, pointerType: 'mouse' }));
      return popup()?.textContent.includes('/work/spool') && popup()?.textContent.includes('dev');
    });
    report.mainContextShown = true;
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    if (mode === 'mobile') button('Collapse file sidebar')?.click();
    fileLink('External report').click();
    await until(() => sourceText().includes('external report contents'));
    report.externalPreview = document.querySelector('.file-preview')?.textContent.includes('External file');
    report.externalDownloadChatScoped = document.querySelector('.file-preview-header a')?.getAttribute('href').includes('/chats/main-chat/file?');
    [...document.querySelectorAll('.file-preview-mode button')].find(el => el.textContent === 'Preview').click();
    await until(() => document.querySelector('.file-preview-scroll .markdown h1')?.textContent === 'external report contents');
    report.markdownPreviewVisible = true;
    [...document.querySelectorAll('.file-preview-mode button')].find(el => el.textContent === 'Source').click();
    await until(() => sourceText().includes('# external report contents'));
    report.markdownSourceVisibleAfterPreview = true;
    await until(() => visible(document.querySelector('.file-code-preview')) && document.querySelector('.file-code-preview')?.shadowRoot?.querySelector('code [style]'));
    const colored = [...document.querySelector('.file-code-preview').shadowRoot.querySelectorAll('code span[style]')];
    report.syntaxColorsVisible = new Set(colored.map(el => getComputedStyle(el).color)).size > 1;
    trigger().click();
    await until(popup);
    report.noPageOverflow = document.documentElement.scrollWidth <= innerWidth;
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    if (mode === 'mobile') button('Collapse file sidebar')?.click();
    fileLink('Long source').click();
    await until(() => visible(document.querySelector('.file-code-preview')) && document.querySelector('.file-code-preview')?.shadowRoot?.querySelector('code')?.textContent.includes('long source contents'));
    await settleDrawer(mode === 'mobile' ? '.file-sidebar.open' : '.file-sidebar');
  } catch (error) { report.error = String(error); report.browserErrors = browserErrors; report.viewport = { width: innerWidth, height: innerHeight }; report.drawers = [...document.querySelectorAll('.app > .sidebar, .app > .file-sidebar')].map(el => ({ className: el.className, rect: el.getBoundingClientRect().toJSON(), transform: getComputedStyle(el).transform })); report.document = document.body.innerHTML.slice(0, 3000); report.resources = performance.getEntriesByType('resource').filter(item => item.responseStatus >= 400 || item.name.includes('/src/main') || item.name.includes('@pierre')).map(item => ({ name: item.name, status: item.responseStatus, duration: item.duration })); report.preview = document.querySelector('.file-preview')?.outerHTML; report.shadow = document.querySelector('.file-code-preview')?.shadowRoot?.innerHTML; }
  await fetch('/smoke/report?mode=' + mode, { method: 'POST', body: JSON.stringify(report) });
})();
`;
const mime = {
  ".html": "text/html",
  ".js": "text/javascript",
  ".css": "text/css",
  ".png": "image/png",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
};
const server = createServer(async (req, res) => {
  res.on("finish", () => {
    if (res.statusCode >= 400)
      failedRequests.push({ url: req.url, status: res.statusCode });
  });
  const url = new URL(req.url, "http://localhost");
  const path = url.pathname;
  if (path === '/api/preferences') preferencesRequests++;
  if (!dev)
    res.setHeader(
      "Content-Security-Policy",
      "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; worker-src 'self'; manifest-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'",
    );
  const json = (data) => {
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify(data));
  };
  if (path === "/smoke/report") {
    let body = "";
    for await (const chunk of req) body += chunk;
    reports.set(url.searchParams.get("mode"), JSON.parse(body));
    return json({ ok: true });
  }
  if (path === "/smoke/probe.js") {
    res.setHeader("Content-Type", "text/javascript");
    return res.end(probe);
  }
  if (path === "/smoke/next-turn") {
    workspaces["worktree-chat"].turn_id = "turn-" + url.searchParams.get("mode");
    return json({ ok: true });
  }
  if (path === "/smoke/cache-isolation" && vite) {
    const close = [];
    try {
      const metadata = join(vite.config.cacheDir, "deps/_metadata.json");
      const before = await readFile(metadata);
      const fixture = await createTestViteServer(
        { after: (fn) => close.push(fn) },
        {
          root: join(root, "web"),
          configFile: false,
          cacheDir: vite.config.cacheDir,
          logLevel: "silent",
          appType: "custom",
          server: { middlewareMode: true, watch: null, hmr: false },
          esbuild: { jsx: "automatic" },
        },
      );
      assert.notEqual(fixture.config.cacheDir, vite.config.cacheDir);
      await fixture.ssrLoadModule("/src/settings-pages.tsx");
      for (const cleanup of close.splice(0).reverse()) await cleanup();
      return json({ preserved: before.equals(await readFile(metadata)) });
    } catch (error) {
      res.statusCode = 500;
      return json({ error: String(error) });
    } finally {
      for (const cleanup of close.reverse()) await cleanup();
    }
  }
  if (path === "/api/me")
    return json({
      user: { id: "owner", username: "owner", nickname: "Owner" },
      csrf: "fixture",
      preferences: { theme: "dark", language: "ja" },
    });
  if (path === "/api/version") return json({ version: "fixture" });
  if (path === "/api/profiles")
    return json({ revision: 1, people: [{ id: "owner", nickname: "Owner" }] });
  if (path === "/api/navigation")
    return json({
      projects: [{ id: "project", name: "Spool", shared_chats: false }],
      chats,
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
    const chat = chats.find((chat) => path.includes(chat.id));
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    return res.end(
      "event: snapshot\ndata: " + JSON.stringify(chat || []) + "\n\n",
    );
  }
  const chat = chats.find((chat) => path.startsWith("/api/chats/" + chat.id));
  if (chat) {
    if (path.endsWith("/workspace")) return json(workspaces[chat.id]);
    if (path.endsWith("/files"))
      return json({
        path: ".",
        entries: ["same.txt", "go.mod", "long.ts"].map((name) => ({
          name,
          path: name,
          kind: "file",
        })),
        truncated: false,
      });
    if (path.endsWith("/file")) {
      const file = url.searchParams.get("path");
      const external = file === "/outside/report.md";
      return json({
        path: file,
        name: file.split("/").pop(),
        kind: "text",
        media_type: "text/plain",
        content: external
          ? "# external report contents\n\nA readable **Markdown** document."
          : file === "go.mod"
            ? "module example.com/spool\n\ngo 1.25.0\n"
            : file === "long.ts"
              ? Array.from(
                  { length: 80 },
                  (_, i) =>
                    `export const line${i} = "long source contents ${"wide content ".repeat(35)}";`,
                ).join("\n")
              : (url.searchParams.get("workspace") || workspaces[chat.id].path) !== "/work/spool"
                ? "worktree file contents"
                : "main file contents",
        external,
        size: 25,
        truncated: false,
      });
    }
    return json(chat);
  }
  if (path.startsWith("/api/projects/"))
    return json({ entries: [], path: ".", truncated: false });
  if (path.startsWith("/api/")) return json([]);
  if (blockHighlighter && /^\/assets\/shiki-/.test(path)) {
    blockedHighlighterRequests++;
    res.writeHead(503);
    return res.end();
  }
  try {
    assert.ok(!path.includes(".."));
    if (vite && path !== "/") {
      return vite.middlewares(req, res, () => {
        res.writeHead(404);
        res.end();
      });
    }
    const file = path === "/" ? "index.html" : path.slice(1);
    let body = await readFile(join(root, dev ? "web" : "frontend", file));
    if (file === "index.html")
      body = Buffer.from(
        body
          .toString()
          .replace("</body>", '<script src="/smoke/probe.js"></script></body>'),
      );
    if (vite)
      body = Buffer.from(
        await vite.transformIndexHtml(
          url.pathname + url.search,
          body.toString(),
        ),
      );
    res.setHeader(
      "Content-Type",
      mime[extname(file)] || "application/octet-stream",
    );
    res.end(body);
  } catch {
    res.writeHead(404);
    res.end();
  }
});

try {
  await mkdir(output, { recursive: true });
  if (dev)
    vite = await createTestViteServer(
      { after: (fn) => viteCleanup.push(fn) },
      {
        root: join(root, "web"),
        configFile: join(root, "web/vite.config.ts"),
        logLevel: "silent",
        appType: "custom",
        server: { middlewareMode: true, watch: null, hmr: false, proxy: {} },
      },
    );
  if (vite) await vite.warmupRequest("/src/main.tsx");
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  for (const [mode, width, height] of [
    ["desktop", 1440, 1000],
    // Headless Chrome enforces a 500px minimum layout viewport. Match the
    // screenshot width to it so the narrow-layout controls aren't cropped.
    ["mobile", 500, 844],
    ...(!dev ? [["fallback", 1440, 1000]] : []),
  ]) {
    blockHighlighter = mode === "fallback";
    await new Promise((resolve, reject) => {
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
          `--user-data-dir=${join(scratch, mode)}`,
          `--window-size=${width},${height}`,
          "--force-device-scale-factor=1",
          `--virtual-time-budget=${dev ? 60000 : 10000}`,
          `--screenshot=${join(output, artifactPrefix + mode + ".png")}`,
          `http://127.0.0.1:${server.address().port}/?mode=${mode}&dev=${dev ? "1" : "0"}`,
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
        await writeFile(
          join(output, artifactPrefix + "chrome-" + mode + ".log"),
          diagnostics,
        );
        code === 0 ? resolve() : reject(new Error(`Chrome exited ${code}`));
      });
    });
    await writeFile(
      join(output, artifactPrefix + "browser-report.json"),
      JSON.stringify(Object.fromEntries(reports), null, 2),
    );
    const report = reports.get(mode);
    assert.ok(report, `No ${mode} report`);
    assert.equal(
      report.error,
      undefined,
      JSON.stringify({ ...report, failedRequests }),
    );
    for (const [name, value] of Object.entries(report))
      assert.equal(value, true, `${mode}: ${name}`);
    if (mode === "fallback") assert.ok(blockedHighlighterRequests > 0);
  }
  console.log(JSON.stringify(Object.fromEntries(reports), null, 2));
  assert.equal(preferencesRequests, 0, 'Theme and language must not use the server preference API');
} finally {
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
  for (const cleanup of viteCleanup.reverse()) await cleanup();
  await rm(scratch, { recursive: true, force: true });
}
