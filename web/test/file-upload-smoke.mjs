// Real project tree and settings controls, with isolated uploads and browser data.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

const scratch = requireRunTemp();
const root = fileURLToPath(new URL("../", import.meta.url));
const output = fileURLToPath(new URL("../../build/artifacts/file-upload/", import.meta.url));
const { chromium } = createRequire(join(process.env.SPOOL_NODE_MODULES || join(root, "node_modules"), "__upload__.cjs"))("playwright");
const cleanups = [];
const source = `
import React,{useState} from 'react'; import {createRoot} from 'react-dom/client';
import {FileSidebar} from '/src/file-sidebar.tsx'; import {ProjectsPage} from '/src/settings-projects.tsx';
import {newProject} from '/src/settings-client.ts'; import {translator} from '/src/i18n.ts';
import {setCSRF} from '/src/api.ts'; import '/src/style.css'; import '/src/settings.css';
const params=new URL(location.href).searchParams; const t=translator('en'); setCSRF('upload-csrf');
const main={path:'/repo/main',available:true,kind:'main',branch:'dev'};
const tree={path:'/repo/feature',available:true,kind:'worktree',branch:'feature/upload'};
function Files(){const [workspace,setWorkspace]=useState(main),[selection,setSelection]=useState(null);
return <div className="app left-collapsed" style={{'--right-panel-width':'460px'}}><main className="workspace"/><FileSidebar
project={{id:'project',name:'Project',can_upload:params.get('enabled')!=='false'}} chatID={params.has('project')?undefined:'chat'}
workspace={params.has('project')?undefined:workspace} workspaces={[main,tree]} onSelectWorkspace={path=>{setWorkspace(path===tree.path?tree:main);setSelection(null)}} onRefreshWorkspace={()=>{}}
active={true} mobileOpen={true} width={460} selection={selection} t={t} theme={params.get('theme')||'light'}
onOpen={target=>setSelection(old=>({target,scope:workspace.path,request:(old?.request||0)+1}))} onClose={()=>{}} onResize={()=>{}} onClear={()=>setSelection(null)}/></div>}
function Settings(){const [data,setData]=useState({projects:[{...newProject(),id:'project',name:'Project',path:'/repo/main',members:['alice']}],users:[{id:'alice',username:'alice',nickname:'Alice',disabled:false}],agents:[],github_path:''});
async function run(command){window.commands.push(structuredClone(command)); const next={...data,projects:[command.project],saved_id:'project'};setData(next);return next;}
return <div className="desktop-settings-shell"><main className="settings-main"><div className="settings-page"><ProjectsPage data={data} run={run} t={t}/></div></main></div>}
window.commands=[];document.documentElement.dataset.theme=params.get('theme')||'light';createRoot(document.getElementById('root')).render(params.has('settings')?<Settings/>:<Files/>);`;
const plugin = {
  name: "file-upload-fixture",
  resolveId: id => id === "/__upload.tsx" ? id : undefined,
  load: id => id === "/__upload.tsx" ? source : undefined,
  configureServer(server) {
    server.middlewares.use(async (req, res, next) => {
      if (!req.url.startsWith("/__upload?")) return next();
      res.setHeader("Content-Type", "text/html");
      res.end(await server.transformIndexHtml(req.url, '<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="/__upload.tsx"></script>'));
    });
  },
};
const server = await createTestViteServer({ after: callback => cleanups.push(callback) }, {
  configFile: false, root, plugins: [react(), plugin], server: { host: "127.0.0.1", port: 0, strictPort: true },
});
let browser;
const reports = [];
try {
  await server.listen();
  await mkdir(output, { recursive: true });
  browser = await chromium.launchPersistentContext(join(scratch, "browser"), { channel: "chrome", headless: true, viewport: { width: 1280, height: 900 } });
  const page = browser.pages()[0];
  const errors = [];
  page.on("pageerror", error => errors.push(error.stack || String(error)));
  const base = `http://127.0.0.1:${server.httpServer.address().port}/__upload?`;
  const uploads = [];
  const uploaded = new Map();
  await page.route("**/api/**", async route => {
    const request = route.request(), url = new URL(request.url());
    const directory = url.searchParams.get("path") || ".";
    const scope = url.searchParams.get("workspace") || "/project";
    const json = (value, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
    if (url.pathname.endsWith("/upload")) {
      assert.equal(request.headers()["x-csrf-token"], "upload-csrf");
      const form = await new Request(url, { method: "POST", headers: { "Content-Type": request.headers()["content-type"] }, body: request.postDataBuffer() }).formData();
      const file = form.get("file");
      const path = directory === "." ? file.name : directory + "/" + file.name;
      uploads.push({ endpoint: url.pathname, scope, directory, name: file.name, contents: await file.text() });
      const key = scope + ":" + directory;
      const entries = uploaded.get(key) || [];
      if (entries.some(entry => entry.path === path)) return json({ error: "A file with this name already exists" }, 409);
      const entry = { path, name: file.name, kind: "file", size: file.size };
      uploaded.set(key, [...entries, entry]);
      return json(entry, 201);
    }
    if (url.pathname.endsWith("/files")) {
      const entries = directory === "." ? [{ path: "docs", name: "docs", kind: "directory" }, { path: "empty", name: "empty", kind: "directory" }, { path: "root.txt", name: "root.txt", kind: "file" }]
        : directory === "docs" ? [{ path: "docs/nested.txt", name: "nested.txt", kind: "file" }] : [];
      return json({ path: directory, entries: [...entries, ...(uploaded.get(scope + ":" + directory) || [])], truncated: false });
    }
    if (url.pathname.endsWith("/file")) return json({ path: directory, name: directory.split("/").pop(), kind: "text", media_type: "text/plain", content: "fixture", size: 7 });
    return json({ error: "Unexpected request" }, 404);
  });
  const row = path => page.locator(`[data-item-path="${path}"]`).first();
  async function openMenu(target, position) {
    await target.click({ button: "right", ...(position ? { position } : {}) });
    await page.getByRole("menu", { name: "Project file actions" }).waitFor();
  }
  async function choose(files, expectError = false) {
    const picker = page.waitForEvent("filechooser");
    await page.getByRole("menuitem", { name: "Upload files" }).click();
    await (await picker).setFiles(files.map(name => ({ name, mimeType: "text/plain", buffer: Buffer.from("contents of " + name) })));
    await (expectError ? page.getByRole("alert") : page.getByRole("status").filter({ hasText: /Uploaded/ })).waitFor();
  }
  await page.goto(base + "files=1");
  await row("docs/").waitFor();
  const folderBox = await row("docs/").boundingBox();
  await openMenu(row("docs/"), { x: folderBox.width - 20, y: folderBox.height / 2 });
  assert.ok(await page.getByRole("menu").getByText("Upload to docs", { exact: true }).isVisible());
  await choose(["one.txt", "中文 two.txt"]);
  await row("docs/one.txt").waitFor();
  await row("docs/中文 two.txt").waitFor();
  assert.deepEqual(uploads.map(item => [item.directory, item.scope]), [["docs", "/repo/main"], ["docs", "/repo/main"]]);
  assert.equal(await page.locator(".file-preview").count(), 0, "Right-click must not open the preview");
  const fileBox = await row("docs/nested.txt").boundingBox();
  await openMenu(row("docs/nested.txt"), { x: fileBox.width - 12, y: fileBox.height / 2 });
  assert.ok(await page.getByRole("menu").getByText("Upload to docs", { exact: true }).isVisible());
  await page.keyboard.press("Escape");
  assert.equal(await page.getByRole("menu").count(), 0);
  await openMenu(row("empty/"));
  await choose(["empty-folder.txt"]);
  try {
    await row("empty/empty-folder.txt").waitFor({ timeout: 5000 });
  } catch (error) {
    await page.screenshot({ path: join(output, "failure.png") });
    console.log(JSON.stringify({ uploads, errors }));
    throw error;
  }
  const tree = page.locator(".file-tree-section");
  const treeBox = await tree.boundingBox();
  await openMenu(tree, { x: 100, y: treeBox.height - 4 });
  assert.ok(await page.getByRole("menu").getByText("Upload to /", { exact: true }).isVisible());
  await choose(["root-upload.txt"]);
  await row("root-upload.txt").waitFor();
  assert.equal(uploads.at(-1).directory, ".");
  await openMenu(row("docs/"));
  await choose(["one.txt"], true);
  await page.getByRole("alert").filter({ hasText: "A file with this name already exists" }).waitFor();
  await page.screenshot({ path: join(output, "context-and-conflict.png"), animations: "disabled" });
  await page.goto(base + "files=1&enabled=false");
  await openMenu(row("docs/"));
  assert.ok(await page.getByRole("menuitem", { name: "Upload files" }).isDisabled());
  assert.ok(await page.getByText("File upload permission is disabled", { exact: true }).isVisible());
  await page.keyboard.press("Escape");
  assert.equal(await page.getByRole("menu").count(), 0);
  await page.goto(base + "files=1&theme=dark");
  await page.getByRole("button", { name: "Switch directory", exact: true }).click();
  await page.getByRole("option").filter({ hasText: "feature/upload" }).click();
  await openMenu(row("docs/"));
  await choose(["worktree.txt"]);
  assert.equal(uploads.at(-1).scope, "/repo/feature");
  assert.equal(uploads.at(-1).directory, "docs");
  await page.screenshot({ path: join(output, "worktree-dark.png"), animations: "disabled" });
  await page.goto(base + "project=1");
  await openMenu(row("docs/"));
  await choose(["project.txt"]);
  assert.equal(uploads.at(-1).endpoint, "/api/projects/project/upload");
  assert.equal(uploads.at(-1).scope, "/project");

  await page.goto(base + "settings=1");
  await page.getByRole("button", { name: /Project.*repo\/main/ }).click();
  const permission = page.getByRole("switch", { name: "Alice · Allow file uploads", exact: true });
  assert.ok(!(await permission.isChecked()));
  await permission.focus();
  await permission.press("Space");
  assert.ok(await permission.isChecked());
  await page.locator("form").evaluate(form => form.requestSubmit());
  await page.waitForFunction(() => window.commands.some(command => command.kind === "save-project"));
  assert.equal(await page.evaluate(() => window.commands.at(-1).project.member_upload.alice), true);
  assert.ok(await permission.isChecked());
  await permission.click();
  await page.locator("form").evaluate(form => form.requestSubmit());
  await page.waitForFunction(() => window.commands.filter(command => command.kind === "save-project").length === 2);
  assert.equal(await page.evaluate(() => window.commands.at(-1).project.member_upload.alice), false);
  await permission.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(output, "member-permission.png"), animations: "disabled" });
  assert.deepEqual(errors, []);
  reports.push({ uploads, memberPermissionSaved: true, errors });
  await writeFile(join(output, "report.json"), JSON.stringify(reports, null, 2) + "\n");
  console.log("File upload browser checks passed: folders, file whitespace, root whitespace, empty folders, batches, conflict, permission, worktree, project root, settings save.");
} finally {
  await browser?.close();
  for (const cleanup of cleanups.reverse()) await cleanup();
}
