// Production file/Git panels with isolated synthetic directories and browser profiles.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { requireRunTemp } from "../../tools/test-scratch.mjs";
import { createTestViteServer } from "./vite-fixture.mjs";

requireRunTemp();
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const require = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__git_panel__.cjs"));
const { chromium } = require("playwright");
const output = path.join(root, "build/artifacts/git-panel");
const cleanup = [], reports = [], requests = [];
const commit = "a".repeat(40);
const source = `
import React,{useState} from 'react';import {createRoot} from 'react-dom/client';
import {FileSidebar} from '/src/file-sidebar.tsx';import {translator} from '/src/i18n.ts';import '/src/style.css';
const main={path:'/repo/main',available:true,kind:'main',branch:'dev'};
const tree={path:'/repo/feature',available:true,kind:'worktree',branch:'feature/git'};
const sceneParams=new URL(location.href).searchParams;
const directories=[main,tree,...Array.from({length:Math.max(0,Number(sceneParams.get('directories')||2)-2)},(_,i)=>({path:'/repo/worktree-'+(i+1),available:true,kind:'worktree',branch:'feature/directory-'+(i+1)}))];
const t=translator('en');
function Scene(){const [workspace,setWorkspace]=useState(sceneParams.get('selected')==='last'?directories.at(-1):main),[selection,setSelection]=useState(null);
return <div className="app left-collapsed" style={{'--right-panel-width':'460px'}}><main className="workspace"/><FileSidebar
 project={{id:'p',name:'Project'}} chatID="c" workspace={workspace} workspaces={directories} onSelectWorkspace={path=>{setWorkspace(directories.find(item=>item.path===path)||main);setSelection(null)}} onRefreshWorkspace={()=>{}}
 active={true} mobileOpen={true} width={460} selection={selection} t={t} theme={new URL(location.href).searchParams.get('theme')||'light'}
 onOpen={target=>setSelection(old=>({target,scope:workspace.path,request:(old?.request||0)+1}))} onClose={()=>{}} onResize={()=>{}} onClear={()=>setSelection(null)}/></div>}
document.documentElement.dataset.theme=new URL(location.href).searchParams.get('theme')||'light';createRoot(document.getElementById('root')).render(<Scene/>);`;
const plugin = {
  name: "git-panel-fixture",
  resolveId: id => id === "/__git-panel.tsx" ? id : undefined,
  load: id => id === "/__git-panel.tsx" ? source : undefined,
  configureServer(server) {
    server.middlewares.use(async (req, res, next) => {
      if (!req.url.startsWith("/__git-panel?")) return next();
      res.setHeader("Content-Type", "text/html");
      res.end(await server.transformIndexHtml(req.url, '<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="/__git-panel.tsx"></script>'));
    });
  },
};
const server = await createTestViteServer({ after: fn => cleanup.push(fn) }, {
  configFile: false, root: path.join(root, "web"), plugins: [react(), plugin], server: { host: "127.0.0.1", port: 0 },
});
let browser;
try {
  await mkdir(output, { recursive: true }); await server.listen();
  browser = await chromium.launch({ channel: "chrome", headless: true });
  for (const [width, theme] of [[1280, "light"], [390, "light"], [1280, "dark"], [390, "dark"]]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, serviceWorkers: "block" });
    const page = await context.newPage(); const errors = [];
    try {
      page.setDefaultTimeout(10000);
      page.on("pageerror", error => errors.push(error.stack || String(error)));
      let fail = false, blocked;
      let fileRevision = 0, diffRevision = 0;
      await page.route("**/api/**", async route => {
        const url = new URL(route.request().url()), scope = url.searchParams.get("workspace"); requests.push(url.pathname + url.search);
        const json = (value, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
        if (url.pathname.endsWith("/files")) return json({ entries: [{ path: "readme.txt", name: "readme.txt", kind: "file" }], truncated: false });
        if (url.pathname.endsWith("/file")) return json({ path: "readme.txt", name: "readme.txt", kind: "text", media_type: "text/plain", content: `File preview contents ${fileRevision}`, size: 20 });
        if (fail) return json({ error: "Git temporarily unavailable" }, 500);
        if (url.searchParams.get("view") === "diff") {
          if (blocked) { await blocked; }
          if (url.searchParams.get("path") === "image.png") return json({ path: "image.png", binary: true, patch: "" });
          return json({ path: "src/example.ts", patch: `diff --git a/src/example.ts b/src/example.ts\n--- a/src/example.ts\n+++ b/src/example.ts\n@@ -1,2 +1,3 @@\n first\n-old\n+new-${scope === "/repo/feature" ? "feature" : "main"}\n+revision-${diffRevision}\n`, truncated: false });
        }
        const base = { repository: true, branch: scope === "/repo/feature" ? "feature/git" : "dev", files: [], commits: [] };
        if (url.searchParams.get("view") === "history") return json({ ...base, commits: [{ id: commit, subject: "Fix Git panel preview", author: "Developer", time: 1794900000 }] });
        return json({ ...base, files: [
          { path: "src/example.ts", status: "MM", staged: true, additions: 12, deletions: 4 },
          { path: "image.png", status: "M", binary: true, additions: 0, deletions: 0 },
          { path: "notes.txt", status: "??", untracked: true, additions: 0, deletions: 0 },
        ] });
      });
      await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__git-panel?theme=${theme}`);
      const project = page.getByRole("tab", { name: "Project panel", exact: true }), git = page.getByRole("tab", { name: "Git panel", exact: true });
      await project.waitFor();
      await page.locator('[data-item-path="readme.txt"]').click();
      await page.getByRole("region", { name: "File preview", exact: true }).waitFor();
      await page.getByRole("link", { name: "Download file", exact: true }).waitFor();
      const fileRefreshBox = await page.getByRole("button", { name: "Refresh file preview", exact: true }).boundingBox();
      const fileCloseBox = await page.getByRole("button", { name: "Close file preview", exact: true }).boundingBox();
      assert.ok(Math.abs(fileCloseBox.x - fileRefreshBox.x - fileRefreshBox.width) <= 1, "Preview refresh and close are adjacent");
      await page.locator(".file-sidebar").screenshot({ path: path.join(output, `project-${width}-${theme}.png`) });
      let offset = requests.length;
      await Promise.all([page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/files")), page.getByRole("button", { name: "Refresh files", exact: true }).click()]);
      assert.ok(requests.slice(offset).every(url => !url.includes("/file?")), "Tree refresh must leave preview alone");
      offset = requests.length; fileRevision++;
      await Promise.all([page.waitForResponse(response => new URL(response.url()).pathname.endsWith("/file")), page.getByRole("button", { name: "Refresh file preview", exact: true }).click()]);
      assert.ok(requests.slice(offset).every(url => url.includes("/file?")), "File preview refresh must leave tree alone");
      await git.click();
      await page.getByRole("tab", { name: "Changes", exact: true }).click();
      const row = page.locator(".git-file-row").filter({ hasText: "example.ts" }); await row.click();
      await page.locator(".git-diff-source").getByText("new-main", { exact: true }).waitFor();
      const diffRefreshBox = await page.getByRole("button", { name: "Refresh diff preview", exact: true }).boundingBox();
      const diffCloseBox = await page.getByRole("button", { name: "Close diff", exact: true }).boundingBox();
      assert.ok(Math.abs(diffCloseBox.x - diffRefreshBox.x - diffRefreshBox.width) <= 1, "Diff refresh and close are adjacent");
      assert.doesNotMatch(await page.locator(".git-diff-source").textContent(), /diff --git|--- a\/|\+\+\+ b\/|@@/);
      // Clicking the selected row must keep the loaded diff, rather than get stuck loading.
      await row.click(); assert.ok(await page.locator(".git-diff-source").textContent());
      await page.getByRole("tab", { name: "Changes", exact: true }).click();
      await row.waitFor(); await page.locator(".git-diff-source").waitFor();
      offset = requests.length;
      await Promise.all([page.waitForResponse(response => new URL(response.url()).searchParams.get("view") === "changes"), page.getByRole("button", { name: "Refresh Git information", exact: true }).click()]);
      assert.ok(requests.slice(offset).every(url => url.includes("/git?") && !url.includes("view=diff")), "Git list refresh must leave diff and Project alone");
      offset = requests.length; diffRevision++;
      await page.getByRole("button", { name: "Refresh diff preview", exact: true }).click();
      await page.locator(".git-diff-source").getByText("revision-1", { exact: true }).waitFor();
      assert.ok(requests.slice(offset).every(url => url.includes("view=diff")), "Diff refresh must leave lists alone");
      const grip = page.getByRole("separator", { name: "Resize file preview", exact: true }).filter({ visible: true });
      const handle = await grip.boundingBox(); assert.ok(handle);
      await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2); await page.mouse.down();
      await page.mouse.move(handle.x + handle.width / 2, handle.y + 95, { steps: 8 }); await page.mouse.up();
      const moved = await grip.boundingBox(); assert.ok(moved.y > handle.y + 40);
      await grip.focus(); const before = Number(await grip.getAttribute("aria-valuenow")); await page.keyboard.press("ArrowUp");
      assert.ok(Number(await grip.getAttribute("aria-valuenow")) < before);
      await page.locator(".git-file-row").filter({ hasText: "image.png" }).click(); await page.locator(".git-diff-preview").getByText("Binary file", { exact: true }).waitFor();
      await page.getByRole("tab", { name: "History", exact: true }).click(); await page.getByText("Fix Git panel preview", { exact: true }).click();
      await row.click(); await page.locator(".git-diff-source").getByText("new-main", { exact: true }).waitFor();
      await page.getByRole("tab", { name: "Changes", exact: true }).click(); await row.waitFor();
      await page.getByRole("button", { name: "Switch directory", exact: true }).click();
      await page.getByRole("option").filter({ hasText: "feature/git" }).click();
      await row.click(); await page.locator(".git-diff-source").getByText("new-feature", { exact: true }).waitFor();
      assert.equal(await page.locator(".git-diff-source").getByText("new-main", { exact: true }).count(), 0);
      await project.click(); await page.locator('[data-item-path="readme.txt"]').waitFor(); await git.click(); await row.click();
      await page.locator(".git-diff-source").getByText("new-feature", { exact: true }).waitFor();
      await page.locator(".file-sidebar").screenshot({ path: path.join(output, `git-${width}-${theme}.png`) });
      // Failure, retry and late responses use the same directory-scoped requests.
      fail = true; await page.getByRole("button", { name: "Refresh Git information", exact: true }).click(); await page.getByText("Git temporarily unavailable", { exact: false }).waitFor();
      fail = false; await page.getByRole("button", { name: "Retry", exact: true }).click(); await row.waitFor();
      let release; blocked = new Promise(resolve => { release = resolve; }); await row.click();
      await page.getByRole("button", { name: "Switch directory", exact: true }).click(); await page.getByRole("option").filter({ hasText: "Main checkout" }).click();
      release(); blocked = undefined;
      await row.click(); await page.locator(".git-diff-source").getByText("new-main", { exact: true }).waitFor();
      assert.deepEqual(errors, []); reports.push({ width, theme, passed: true });
    } catch (error) {
      await page.screenshot({ path: path.join(output, "failure.png") });
      await writeFile(path.join(output, "failure.json"), JSON.stringify({ errors, requests, text: await page.locator("body").innerText() }, null, 2));
      throw error;
    } finally { await context.close(); }
  }
  for (const width of [1280, 390]) for (const count of [2, 8]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, serviceWorkers: "block" });
    try {
      const page = await context.newPage();
      await page.route("**/api/**", route => route.fulfill({ json: { entries: [] } }));
      await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__git-panel?theme=light&directories=${count}&selected=last`);
      await page.getByRole("button", { name: "Switch directory", exact: true }).click();
      const menu = page.getByRole("listbox", { name: "Switch directory", exact: true }); await menu.waitFor();
      await page.waitForFunction(() => {
        const menu = document.querySelector('[role="listbox"][aria-label="Switch directory"]');
        const selected = menu?.querySelector('[aria-selected="true"]');
        if (!selected) return false;
        const bounds = menu.getBoundingClientRect(), row = selected.getBoundingClientRect();
        return row.top >= bounds.top && row.bottom <= bounds.bottom;
      });
      const geometry = await menu.evaluate(list => {
        const style = getComputedStyle(list), bounds = list.getBoundingClientRect();
        const chrome = parseFloat(style.paddingTop) + parseFloat(style.paddingBottom) + parseFloat(style.borderTopWidth) + parseFloat(style.borderBottomWidth);
        const rows = [...list.children].map(row => row.getBoundingClientRect().height);
        return { height: bounds.height, desired: chrome + rows.slice(0, 5).reduce((sum, value) => sum + value, 0), scrollHeight: list.scrollHeight, clientHeight: list.clientHeight, count: rows.length };
      });
      assert.equal(geometry.count, count);
      assert.ok(Math.abs(geometry.height - geometry.desired) <= 1, JSON.stringify(geometry));
      assert.equal(geometry.scrollHeight > geometry.clientHeight + 1, count > 5);
      await page.screenshot({ path: path.join(output, `directories-${count}-${width}.png`) });
      reports.push({ width, directoryCount: count, passed: true });
    } finally { await context.close(); }
  }
  await writeFile(path.join(output, "browser-report.json"), JSON.stringify({ reports, requests }, null, 2) + "\n");
  console.log(`Verified Git panels, unified diff resizing, history, directory changes and retries in ${reports.length} browser scenes.`);
} finally {
  await browser?.close(); for (const finish of cleanup.reverse()) await finish();
}
