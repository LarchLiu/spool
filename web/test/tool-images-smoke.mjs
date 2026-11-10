import { requireRunTemp } from "../../tools/test-scratch.mjs";
// Run after the web build with an owned system-temp run and CHROME_BIN set.
// Uses synthetic Codex/Claude history and an owned browser profile.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
assert.ok(process.env.CHROME_BIN, "Set CHROME_BIN to headless Chromium");
requireRunTemp();
const scratch = await mkdtemp(join(process.env.TMPDIR, "tool-images-browser-"));
const output = join(root, "build/artifacts/tool-images");
const image = await readFile(join(root, "assets/logo.png"));
const now = Date.now();
const parts = [];
const turns = [];
for (const backend of ["codex", "claude"]) {
  const start = parts.length;
  parts.push({ role: "user", kind: "text", text: `Inspect ${backend} screenshots` });
  for (let i = 0; i < (backend === "codex" ? 6 : 2); i++) {
    const path = `/work/build/artifacts/${backend} page ${i + 1}.png`;
    parts.push({ id: `${backend}-${i}`, role: "assistant", kind: "tool_use", name: backend === "codex" ? "functions.view_image" : "Read", text: "", tool: {
      kind: "image", image_paths: [path], version: "v1", bytes: 70, ref: `${backend}-${i}`, href: `/chats/image-chat/tool-detail?ref=${backend}-${i}`,
    } });
  }
  // Claude can complete parallel Reads in a different order. Persisted pixels
  // must remain paired with their own original file names.
  if (backend === "claude") for (const i of [1, 0]) parts.push({
    id: `claude-${i}`, role: "tool", kind: "tool_result", text: "", tool: { kind: "other", version: "v1", image_count: 1, image_urls: [`/api/chats/image-chat/tool-image?ref=claude-${i}&index=0`] },
  });
  parts.push({ role: "assistant", kind: "text", text: `Checked ${backend} images.`, phase: "final_answer" });
  turns.push({ id: backend, backend, part_start: start, input_end: start + 1, part_end: parts.length, started_at: now - 5000, finished_at: now, status: "completed" });
}
const chat = { id: "image-chat", project_id: "project", user_id: "owner", title: "Image tool history", backend: "codex", status: "completed", updated_at: now, transcript: { parts }, turns };
const reports = new Map();
const requests = [];
const probe = `
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check) {
  for (let i = 0; i < 240; i++) { const result = check(); if (result) return result; await pause(25); }
  throw new Error('Timed out: ' + check.toString());
}
const errors = [];
window.addEventListener('error', event => errors.push(event.message));
window.addEventListener('unhandledrejection', event => errors.push(String(event.reason)));
(async () => {
  const report = {};
  try {
    await until(() => document.querySelectorAll('.work-label-toggle').length === 2);
    document.querySelectorAll('.work-label-toggle').forEach(button => button.click());
    await until(() => document.querySelectorAll('.viewed-images').length === 2);
    report.historicalGroupsStartCollapsed = [...document.querySelectorAll('.viewed-images > button')].every(button => button.getAttribute('aria-expanded') === 'false') && !document.querySelector('.viewed-image');
    document.querySelectorAll('.viewed-images > button').forEach(button => button.click());
    await until(() => document.querySelectorAll('.viewed-image').length === 8);
    await until(() => [...document.querySelectorAll('.viewed-image img')].filter(image => image.complete && image.naturalWidth > 0).length === 7);
    await until(() => document.querySelector('.viewed-image-unavailable'));
    report.missingImageHasFallback = document.querySelector('.viewed-image[aria-label="Image unavailable: codex page 6.png"]')?.textContent === 'codex page 6.png';
    report.correctGroups = [...document.querySelectorAll('.viewed-images > button')].map(button => button.textContent.trim()).join('|') === 'Viewed 6 images|Viewed 2 images';
    report.noRawToolBodies = !document.querySelector('.tool-detail, .tool-detail-block, .tool-result-source');
    const claude = document.querySelectorAll('.viewed-images')[1];
    report.claudeResultsPaired = claude.querySelector('img').alt === 'claude page 1.png' && claude.querySelector('img').src.includes('ref=claude-0');
    const group = document.querySelector('.viewed-images');
    group.querySelector('button').click();
    await until(() => !group.querySelector('.viewed-image-thumbnails'));
    report.groupCollapses = group.querySelector('button').getAttribute('aria-expanded') === 'false';
    group.querySelector('button').click();
    await until(() => group.querySelectorAll('.viewed-image').length === 6);
    group.querySelector('.viewed-image').click();
    const dialog = await until(() => document.querySelector('dialog.image-preview[open]'));
    report.clickOpensPreview = dialog.querySelector('.image-preview-toolbar span').textContent === 'codex page 1.png';
    report.downloadUsesAuthorizedEndpoint = dialog.querySelector('a[download]').getAttribute('href').startsWith('/api/chats/image-chat/file?');
    dialog.querySelector('[aria-label="Close preview"]').click();
    await until(() => !document.querySelector('dialog.image-preview'));
    claude.querySelector('.viewed-image').click();
    await until(() => document.querySelector('dialog.image-preview[open]'));
    report.claudePreviewUsesPersistedImage = document.querySelector('dialog.image-preview > img').src.includes('/tool-image?ref=claude-0');
    document.querySelector('dialog [aria-label="Close preview"]').click();
    await until(() => !document.querySelector('dialog'));
    await until(() => [...document.querySelectorAll('.viewed-image img')].every(image => image.complete && image.naturalWidth > 0));
    report.thumbnailsSurviveClosingPreview = [...document.querySelectorAll('.viewed-image img')].every(image => image.complete && image.naturalWidth > 0);
    report.noPageOverflow = document.documentElement.scrollWidth <= innerWidth;
    report.noJavascriptErrors = errors.length === 0;
    const history = document.querySelector('.history');
    history.scrollTop = 0;
    history.dispatchEvent(new Event('scroll'));
    await pause(300);
  } catch (error) { report.error = String(error); report.errors = errors; report.document = document.body.innerHTML.slice(-5000); }
  await fetch('/smoke/report?mode=' + new URL(location.href).searchParams.get('mode'), { method: 'POST', body: JSON.stringify(report) });
})();
`;
const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost");
  const path = url.pathname;
  const json = (data) => { res.setHeader("Content-Type", "application/json"); res.end(JSON.stringify(data)); };
  res.setHeader("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; worker-src 'self'; manifest-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'");
  if (path === "/smoke/report") {
    let body = "";
    for await (const chunk of req) body += chunk;
    reports.set(url.searchParams.get("mode"), JSON.parse(body));
    return json({ ok: true });
  }
  if (path === "/smoke/probe.js") { res.setHeader("Content-Type", "text/javascript"); return res.end(probe); }
  if (path === "/api/me") return json({ user: { id: "owner", username: "owner", nickname: "Owner" }, csrf: "fixture", preferences: { language: "en", theme: "light" } });
  if (path === "/api/version") return json({ version: "fixture" });
  if (path === "/api/profiles") return json({ revision: 1, people: [{ id: "owner", nickname: "Owner" }] });
  if (path === "/api/navigation") return json({ projects: [{ id: "project", name: "Image previews", shared_chats: false }], chats: [chat] });
  if (path === "/api/agents") return json([{ backend: "codex", enabled: true, installed: true, catalog: { models: [{ model: "demo", displayName: "Demo", supportedReasoningEfforts: [] }], defaultModel: "demo" } }]);
  if (path === "/api/permissions") return json({ backend: "codex", default: "ask", modes: [{ id: "ask", available: true }] });
  if (path.endsWith("/events")) { res.writeHead(200, { "Content-Type": "text/event-stream" }); return res.end("event: snapshot\ndata: " + JSON.stringify(path.includes("image-chat") ? chat : []) + "\n\n"); }
  if (path === "/api/chats/image-chat") return json(chat);
  if (path.endsWith("/workspace")) return json({ path: "/work", kind: "main", available: true });
  if (path.endsWith("/tool-detail")) { requests.push("unexpected tool body"); return json({ text: "raw input should stay unloaded" }); }
  if (path === "/api/chats/image-chat/tool-image" || path === "/api/chats/image-chat/file" && url.searchParams.get("image") === "1") {
    requests.push(url.pathname + url.search);
    if (url.searchParams.get("path")?.endsWith("codex page 6.png")) { res.writeHead(404); return res.end(); }
    res.setHeader("Content-Type", "image/png");
    return res.end(image);
  }
  if (path.startsWith("/api/")) return json({ path: ".", entries: [], truncated: false });
  try {
    assert.ok(!path.includes(".."));
    const file = path === "/" ? "index.html" : path.slice(1);
    let body = await readFile(join(root, "frontend", file));
    if (file === "index.html") body = Buffer.from(body.toString().replace("</body>", '<script src="/smoke/probe.js"></script></body>'));
    res.setHeader("Content-Type", { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".png": "image/png", ".svg": "image/svg+xml" }[extname(file)] || "application/octet-stream");
    res.end(body);
  } catch { res.writeHead(404); res.end(); }
});

try {
  await mkdir(output, { recursive: true });
  await new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
  for (const [mode, width, height] of [["desktop", 1440, 1000], ["mobile", 500, 844]]) {
    await new Promise((resolve, reject) => {
      const child = spawn(process.env.CHROME_BIN, ["--headless", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions", "--disable-breakpad", `--user-data-dir=${join(scratch, mode)}`, `--window-size=${width},${height}`, "--force-device-scale-factor=1", "--virtual-time-budget=15000", `--screenshot=${join(output, mode + ".png")}`, `http://127.0.0.1:${server.address().port}/?mode=${mode}`], { stdio: ["ignore", "pipe", "pipe"] });
      let diagnostics = "";
      for (const stream of [child.stdout, child.stderr]) stream.on("data", (data) => { diagnostics = (diagnostics + data).slice(-8000); });
      const timeout = setTimeout(() => child.kill("SIGTERM"), 30000);
      child.once("error", reject);
      child.once("close", async (code) => { clearTimeout(timeout); await writeFile(join(output, mode + ".log"), diagnostics); code === 0 ? resolve() : reject(new Error(`Chrome exited ${code}`)); });
    });
    await writeFile(join(output, "browser-report.json"), JSON.stringify(Object.fromEntries(reports), null, 2));
    const report = reports.get(mode);
    assert.ok(report, `No ${mode} report`);
    assert.equal(report.error, undefined, JSON.stringify(report));
    for (const [key, value] of Object.entries(report)) assert.equal(value, true, `${mode}: ${key}`);
  }
  assert.ok(!requests.includes("unexpected tool body"));
  console.log(JSON.stringify(Object.fromEntries(reports), null, 2));
} finally {
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
  await rm(scratch, { recursive: true, force: true });
}
