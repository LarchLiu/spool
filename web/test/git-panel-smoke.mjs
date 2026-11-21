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
const detailMessage = "Fix Git panel preview\n\nPreserve the full commit message and its paragraphs.\n第二行提交说明\n\nFinal paragraph.";
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
      let staged = true, unstaged = true, notes = true, lastSubject = "Latest commit summary", detailPushed = false;
      const mutations = [];
      await page.route("**/api/**", async route => {
        const url = new URL(route.request().url()), scope = url.searchParams.get("workspace"); requests.push(url.pathname + url.search);
        const json = (value, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
        if (url.pathname.endsWith("/files")) return json({ entries: [{ path: "readme.txt", name: "readme.txt", kind: "file" }], truncated: false });
        if (url.pathname.endsWith("/file")) return json({ path: "readme.txt", name: "readme.txt", kind: "text", media_type: "text/plain", content: `File preview contents ${fileRevision}`, size: 20 });
        if (route.request().method() === "POST") {
          const body = route.request().postDataJSON(); mutations.push(body);
          assert.equal(body.branch, scope === "/repo/feature" ? "feature/git" : "dev");
          assert.equal(body.head, commit);
          if (body.action === "stage") { staged = true; unstaged = false; }
          if (body.action === "unstage") { staged = false; unstaged = true; }
          if (body.action === "commit") { lastSubject = body.message; staged = false; }
          if (body.action === "discard") {
            if (body.paths.includes("src/example.ts")) unstaged = false;
            if (body.paths.includes("notes.txt")) notes = false;
          }
          if (body.action === "push") return json({ error: "Push rejected: remote branch is ahead" }, 409);
          return json({ ok: true });
        }
        if (fail) return json({ error: "Git temporarily unavailable" }, 500);
        if (url.searchParams.get("view") === "diff") {
          if (blocked) { await blocked; }
          if (url.searchParams.get("path") === "image.png") return json({ path: "image.png", binary: true, patch: "" });
          return json({ path: "src/example.ts", patch: `diff --git a/src/example.ts b/src/example.ts\n--- a/src/example.ts\n+++ b/src/example.ts\n@@ -1,2 +1,3 @@\n first\n-old\n+new-${scope === "/repo/feature" ? "feature" : "main"}\n+revision-${diffRevision}\n`, truncated: false });
        }
        const base = { repository: true, writable: true, index: "index-snapshot", commit, remote: "origin", remote_branch: "dev", remote_allowed: true, upstream: "origin/dev", ahead: 2, behind: 0,
          last_commit: { id: commit, subject: lastSubject, author: "Developer", time: 1794900000 },
          branch: scope === "/repo/feature" ? "feature/git" : "dev", files: [], commits: [] };
        if (url.searchParams.get("view") === "history") return json({ ...base, commits: [{ id: commit, subject: "Fix Git panel preview", message: detailMessage, author: "Developer", time: 1794900000, tags: ["v1.2.0"], unpushed: true }] });
        return json({ ...base, ...(url.searchParams.has("commit") ? { selected_commit: {
          id: commit, subject: "Fix Git panel preview", message: detailMessage, author: "Developer", time: 1794900000,
          tags: [detailPushed ? "v1.2.1" : "v1.2.0"], unpushed: !detailPushed,
        } } : {}), files: [
          { path: "src/example.ts", status: staged && unstaged ? "MM" : "M", staged, unstaged, additions: 12, deletions: 4,
            staged_additions: 10, staged_deletions: 3, unstaged_additions: 2, unstaged_deletions: 1 },
          { path: "image.png", status: "M", binary: true, additions: 0, deletions: 0 },
          ...(notes ? [{ path: "notes.txt", status: "??", untracked: true, additions: 0, deletions: 0 }] : []),
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
      const row = page.locator(".git-file-row").filter({ hasText: "example.ts" }).first();
      assert.equal(await page.getByRole("button", { name: "Stage src/example.ts", exact: true }).textContent(), "+");
      assert.equal(await page.getByRole("button", { name: "Unstage src/example.ts", exact: true }).textContent(), "−");
      const stagedGroup = page.locator(".git-file-group").filter({ has: page.locator(".git-group-toggle", { hasText: /^Staged/ }) });
      const unstagedGroup = page.locator(".git-file-group").filter({ has: page.locator(".git-group-toggle", { hasText: /^Unstaged/ }) });
      const untrackedGroup = page.locator(".git-file-group").filter({ has: page.locator(".git-group-toggle", { hasText: /^Untracked/ }) });
      assert.equal(await stagedGroup.getByRole("button", { name: "Unstage all", exact: true }).textContent(), "−");
      assert.equal(await unstagedGroup.getByRole("button", { name: "Stage all", exact: true }).textContent(), "+");
      assert.equal(await unstagedGroup.getByRole("button", { name: "Discard all changes", exact: true }).textContent(), "−");
      // A focus refresh must validate the currently selected side, even when the
      // list request's effect was installed before that side was selected.
      for (const selectedSide of ["staged", "unstaged"]) {
        const group = selectedSide === "staged" ? stagedGroup : unstagedGroup;
        const beforeSelection = requests.length;
        await group.locator(".git-file-row").filter({ hasText: "example.ts" }).click();
        await page.locator(".git-diff-source").waitFor();
        assert.ok(requests.slice(beforeSelection).some(url => url.includes(`side=${selectedSide}`)));
        assert.ok(requests.slice(beforeSelection).every(url => url.includes("view=diff")), "Selecting a side only loads its diff");
        if (selectedSide === "staged") staged = false; else unstaged = false;
        await Promise.all([
          page.waitForResponse(response => new URL(response.url()).searchParams.get("view") === "changes"),
          page.evaluate(() => window.dispatchEvent(new Event("focus"))),
        ]);
        await page.locator(".git-diff-preview").waitFor({ state: "detached" });
        assert.equal(await page.locator('.git-file-row[aria-pressed="true"]').count(), 0, "A removed side cannot keep its old selection");
        assert.ok(await page.locator(".git-file-row").filter({ hasText: "example.ts" }).isVisible(), "The other side remains available");
        staged = unstaged = true;
        await Promise.all([
          page.waitForResponse(response => new URL(response.url()).searchParams.get("view") === "changes"),
          page.evaluate(() => window.dispatchEvent(new Event("focus"))),
        ]);
        await page.getByRole("button", { name: "Unstage src/example.ts", exact: true }).waitFor();
        await page.getByRole("button", { name: "Stage src/example.ts", exact: true }).waitFor();
      }
      await page.getByRole("button", { name: "Discard src/example.ts", exact: true }).click();
      const confirmation = page.getByRole("dialog", { name: "Discard changes?", exact: true });
      await confirmation.getByText("src/example.ts", { exact: true }).waitFor();
      assert.match(await confirmation.innerText(), /Staged changes are kept/);
      await page.screenshot({ path: path.join(output, `discard-${width}-${theme}.png`) });
      await page.keyboard.press("Escape"); assert.equal(mutations.length, 0, "Cancel must not write to Git");
      await page.locator(".git-last-commit").getByText("Latest commit summary", { exact: true }).waitFor();
      const collapsedLabel = await page.locator(".git-commit-toggle").innerText();
      const branchBounds = await page.locator(".git-summary").getByText("dev", { exact: true }).boundingBox();
      const remoteBounds = await page.locator(".git-remote-area").boundingBox();
      const commitBounds = await page.locator(".git-commit-area").boundingBox();
      assert.ok(branchBounds.y < commitBounds.y && commitBounds.y + commitBounds.height <= remoteBounds.y + 1, "Branch stays above the list and the commit area sits above remote actions");
      assert.equal(await page.getByRole("button", { name: "Fetch", exact: true }).count(), 0, "Fetch is in the remote dropdown");
      await page.getByRole("button", { name: "Expand commit area", exact: true }).hover();
      await page.locator(".file-sidebar").screenshot({ path: path.join(output, `collapsed-${width}-${theme}.png`) });
      await page.getByRole("button", { name: "Git remote actions", exact: true }).click();
      const remoteMenu = page.getByRole("listbox", { name: "Git remote actions", exact: true });
      await remoteMenu.getByRole("option", { name: "Fetch", exact: true }).waitFor();
      const menuBounds = await remoteMenu.boundingBox();
      assert.ok(menuBounds.y < remoteBounds.y && menuBounds.y + menuBounds.height <= 900, "Bottom remote menu opens into the available viewport");
      await page.screenshot({ path: path.join(output, `remote-menu-${width}-${theme}.png`) });
      await remoteMenu.getByRole("option", { name: "Fetch", exact: true }).click();
      await page.getByRole("button", { name: "Fetch", exact: true }).waitFor();
      assert.equal(mutations.length, 0, "Selecting Fetch changes the main action without executing it");
      await page.getByRole("button", { name: "Git remote actions", exact: true }).click();
      assert.equal(await remoteMenu.getByRole("option", { name: "Fetch", exact: true }).getAttribute("aria-selected"), "true");
      await remoteMenu.getByRole("option", { name: "Push", exact: true }).click();
      await page.getByRole("button", { name: "Push", exact: true }).waitFor();
      assert.equal(mutations.length, 0, "Selecting Push also leaves Git unchanged");
      await page.locator(".git-commit-label").click();
      assert.equal(await page.getByRole("textbox", { name: "Commit message", exact: true }).count(), 0, "Commit information is passive");
      await page.getByRole("button", { name: "Expand commit area", exact: true }).click();
      const commitInput = page.getByRole("textbox", { name: "Commit message", exact: true });
      const inputHeight = async () => (await commitInput.boundingBox()).height;
      const lineMessage = lines => Array.from({ length: lines }, (_, i) => `Commit line ${i + 1}`).join("\n");
      const oneLineHeight = await inputHeight();
      await commitInput.fill(lineMessage(3)); const threeLineHeight = await inputHeight();
      assert.ok(threeLineHeight > oneLineHeight, "Commit input grows with its contents");
      await commitInput.fill(lineMessage(6)); const maximumHeight = await inputHeight();
      assert.ok(maximumHeight > threeLineHeight);
      await commitInput.fill(lineMessage(12));
      assert.equal(await inputHeight(), maximumHeight, "Commit input stops growing at the default six-line maximum");
      assert.ok(await commitInput.evaluate(element => element.scrollHeight > element.clientHeight), "Long commit messages scroll inside the input");
      await page.getByRole("button", { name: "Collapse commit area", exact: true }).hover();
      await page.locator(".file-sidebar").screenshot({ path: path.join(output, `commit-max-${width}-${theme}.png`) });
      await commitInput.fill("Commit from Git panel");
      assert.equal(await inputHeight(), oneLineHeight, "Commit input shrinks when text is removed");
      assert.equal(await page.locator(".git-commit-actions").getByRole("button", { name: "Commit staged changes (1)", exact: true }).innerText(), collapsedLabel);
      assert.equal(await page.locator(".git-commit-area").getByRole("button", { name: "Cancel", exact: true }).count(), 0);
      const collapseBounds = await page.getByRole("button", { name: "Collapse commit area", exact: true }).boundingBox();
      const submitBounds = await page.locator(".git-commit-actions").getByRole("button", { name: "Commit staged changes (1)", exact: true }).boundingBox();
      assert.ok(collapseBounds.x < submitBounds.x, "Collapse icon is to the left of the commit action");
      await page.getByRole("button", { name: "Collapse commit area", exact: true }).click();
      assert.equal(await page.getByRole("textbox", { name: "Commit message", exact: true }).count(), 0);
      assert.equal(await page.locator(".git-commit-toggle").innerText(), collapsedLabel);
      await page.getByRole("button", { name: "Expand commit area", exact: true }).click();
      assert.equal(await page.getByRole("textbox", { name: "Commit message", exact: true }).inputValue(), "Commit from Git panel", "Collapsing preserves the draft");
      await page.getByRole("textbox", { name: "Commit message", exact: true }).focus();
      await page.keyboard.press("ControlOrMeta+A");
      await page.locator(".file-sidebar").screenshot({ path: path.join(output, `staging-${width}-${theme}.png`) });
      await row.click();
      await page.locator(".git-diff-source").getByText("new-main", { exact: true }).waitFor();
      assert.equal(await page.locator(".git-commit-area").count(), 0, "Commit area is hidden during diff preview");
      assert.ok(await page.locator(".git-last-commit").isVisible(), "Latest commit remains at the bottom during diff preview");
      await page.getByRole("button", { name: "Close diff", exact: true }).click();
      assert.equal(await page.getByRole("textbox", { name: "Commit message", exact: true }).inputValue(), "Commit from Git panel");
      await row.click(); await page.locator(".git-diff-source").waitFor();
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
      await page.getByRole("tab", { name: "History", exact: true }).click();
      await page.locator(".git-history-list").getByText("v1.2.0", { exact: true }).waitFor();
      const historyRow = page.locator(".git-history-list > .file-panel-button").first();
      await historyRow.locator("strong").hover();
      assert.equal(await historyRow.getAttribute("title"), `${commit}\n\n${detailMessage}`, "History hover includes the full SHA and complete commit message");
      assert.deepEqual(await page.locator(".git-history-list .git-history-badge").evaluateAll(badges => badges.map(badge => badge.getAttribute("aria-label") || badge.textContent)), ["v1.2.0", "Not pushed"]);
      await page.locator(".file-sidebar").screenshot({ path: path.join(output, `history-${width}-${theme}.png`) });
      await page.getByText("Fix Git panel preview", { exact: true }).click();
      const detail = page.locator(".git-commit-detail");
      await detail.getByText("v1.2.0", { exact: true }).waitFor();
      assert.equal(await detail.getByLabel("Not pushed", { exact: true }).count(), 1);
      assert.deepEqual(await detail.locator(".git-history-badge").evaluateAll(badges => badges.map(badge => badge.getAttribute("aria-label") || badge.textContent)), ["v1.2.0", "Not pushed"]);
      assert.equal(await detail.locator(".git-commit-metadata span").innerText(), "Developer");
      assert.equal(await detail.locator("time").getAttribute("datetime"), new Date(1794900000 * 1000).toISOString());
      assert.equal(await detail.locator("time").innerText(), await page.evaluate(time => new Date(time * 1000).toLocaleString(), 1794900000));
      assert.equal(await detail.locator(".git-commit-detail-message").innerText(), detailMessage);
      assert.ok(await detail.locator(".git-commit-detail-message").evaluate(element => element.getBoundingClientRect().height > parseFloat(getComputedStyle(element).lineHeight) * 5), "Commit message paragraphs remain visible");
      assert.ok(await detail.evaluate(element => element.scrollWidth <= element.clientWidth), "Commit detail fits narrow panels");
      assert.equal(await detail.locator(".git-commit-sha code").innerText(), commit);
      await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: `http://127.0.0.1:${server.httpServer.address().port}` });
      await page.locator(".file-sidebar").screenshot({ path: path.join(output, `commit-detail-${width}-${theme}.png`) });
      await detail.getByRole("button", { name: "Copy SHA", exact: true }).click();
      await detail.getByRole("button", { name: "Copied", exact: true }).waitFor();
      assert.equal(await page.evaluate(() => navigator.clipboard.readText()), commit, "Copy writes the full SHA");
      detailPushed = true;
      await page.getByRole("button", { name: "Refresh Git information", exact: true }).click();
      await detail.getByText("v1.2.1", { exact: true }).waitFor();
      assert.equal(await detail.getByLabel("Not pushed", { exact: true }).count(), 0, "Commit details refresh tag and outgoing state");
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
      await page.getByRole("button", { name: "Close diff", exact: true }).click();
      await page.getByRole("button", { name: "Expand commit area", exact: true }).click();
      await page.getByRole("textbox", { name: "Commit message", exact: true }).waitFor();
      await page.getByRole("textbox", { name: "Commit message", exact: true }).fill("Commit from Git panel");
      await page.getByRole("button", { name: "Unstage src/example.ts", exact: true }).click();
      await page.waitForFunction(() => !document.querySelector('[aria-label="Unstage src/example.ts"]'));
      await page.getByRole("button", { name: "Stage src/example.ts", exact: true }).click();
      await page.waitForFunction(() => document.querySelector('[aria-label="Unstage src/example.ts"]') && !document.querySelector('[aria-label="Stage src/example.ts"]'));
      await page.getByRole("button", { name: "Commit staged changes (1)", exact: true }).click();
      await page.locator(".git-last-commit").getByText("Commit from Git panel", { exact: true }).waitFor();
      await page.getByRole("button", { name: "Git remote actions", exact: true }).click();
      await page.getByRole("option", { name: "Fetch", exact: true }).click();
      await page.getByRole("button", { name: "Fetch", exact: true }).waitFor();
      assert.equal(mutations.length, 3, "Choosing Fetch must not start a request");
      await page.getByRole("button", { name: "Fetch", exact: true }).click();
      await page.waitForFunction(() => !document.querySelector('.git-remote-split > button').disabled);
      assert.equal(await page.locator(".git-operation-notice").count(), 0, "Successful Git operations show no notice");
      await page.getByRole("button", { name: "Git remote actions", exact: true }).click();
      await page.getByRole("option", { name: "Push", exact: true }).click();
      await page.getByRole("button", { name: "Push", exact: true }).waitFor();
      assert.equal(mutations.length, 4, "Choosing Push must not start a request");
      await page.getByRole("button", { name: "Push", exact: true }).click();
      await page.getByRole("alert").getByText("Push rejected: remote branch is ahead", { exact: true }).waitFor();
      assert.deepEqual(mutations.map(item => item.action), ["unstage", "stage", "commit", "fetch", "push"]);
      assert.equal(mutations[2].message, "Commit from Git panel");
      assert.equal(mutations[2].index, "index-snapshot");
      const operationAlert = page.locator(".git-operation-notice");
      await operationAlert.getByRole("button", { name: "Dismiss", exact: true }).click();
      assert.equal(await operationAlert.count(), 0, "An operation error can be dismissed");
      await Promise.all([page.waitForResponse(response => new URL(response.url()).searchParams.get("view") === "changes"), page.getByRole("button", { name: "Refresh Git information", exact: true }).click()]);
      assert.equal(await operationAlert.count(), 0, "Refreshing the list does not restore a dismissed error");
      await page.getByRole("button", { name: "Push", exact: true }).click();
      await operationAlert.getByText("Push rejected: remote branch is ahead", { exact: true }).waitFor();
      assert.equal(mutations.at(-1).action, "push", "The same error reappears after a new failed operation");
      await page.locator(".file-sidebar").screenshot({ path: path.join(output, `operations-${width}-${theme}.png`) });
      await operationAlert.getByRole("button", { name: "Dismiss", exact: true }).click();
      const bulkOffset = mutations.length;
      staged = true; unstaged = true;
      await page.getByRole("button", { name: "Refresh Git information", exact: true }).click();
      await page.getByRole("button", { name: "Discard src/example.ts", exact: true }).click();
      await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
      await page.waitForFunction(() => !document.querySelector('[aria-label="Discard src/example.ts"]'));
      assert.ok(await page.getByRole("button", { name: "Unstage src/example.ts", exact: true }).isVisible(), "Discard preserves staged content");
      await stagedGroup.getByRole("button", { name: "Unstage all", exact: true }).click();
      await page.getByRole("button", { name: "Stage src/example.ts", exact: true }).waitFor();
      await unstagedGroup.getByRole("button", { name: "Stage all", exact: true }).click();
      await page.waitForFunction(() => !document.querySelector('[aria-label="Stage src/example.ts"]'));
      await untrackedGroup.getByRole("button", { name: "Discard all changes", exact: true }).click();
      assert.deepEqual(await confirmation.locator("li").allTextContents(), ["notes.txt"]);
      await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
      await page.waitForFunction(() => !document.querySelector('[aria-label="Discard notes.txt"]'));
      await unstagedGroup.getByRole("button", { name: "Discard all changes", exact: true }).click();
      assert.deepEqual(await confirmation.locator("li").allTextContents(), ["image.png"]);
      await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
      await page.waitForFunction(() => !document.querySelector('.git-remote-split > button').disabled);
      assert.equal(await operationAlert.count(), 0, "Later successful operations leave the error notice hidden");
      assert.deepEqual(mutations.slice(bulkOffset).map(item => [item.action, item.paths]), [
        ["discard", ["src/example.ts"]], ["unstage", ["src/example.ts"]],
        ["stage", ["src/example.ts", "image.png"]], ["discard", ["notes.txt"]], ["discard", ["image.png"]],
      ]);
      assert.deepEqual(errors, []); reports.push({ width, theme, passed: true });
    } catch (error) {
      await page.screenshot({ path: path.join(output, "failure.png") });
      await writeFile(path.join(output, "failure.json"), JSON.stringify({ errors, requests, text: await page.locator("body").innerText() }, null, 2));
      throw error;
    } finally { await context.close(); }
  }
  {
    const context = await browser.newContext({ viewport: { width: 390, height: 900 }, serviceWorkers: "block" });
    const page = await context.newPage(), writes = [];
    const listing = { repository: true, writable: true, remote_allowed: true, branch: "dev", commit, index: "snapshot", files: [], commits: [],
      remote: "origin", remote_branch: "main", upstream: "origin/main", push_remote: "fork", push_remote_branch: "dev", push_upstream: "", push_ahead: 0 };
    try {
      page.setDefaultTimeout(10000);
      await page.route("**/api/**", route => {
        if (route.request().method() === "POST") {
          writes.push(route.request().postDataJSON());
          return route.fulfill({ json: { ok: true } });
        }
        return route.fulfill({ json: new URL(route.request().url()).pathname.endsWith("/git") ? listing : { entries: [] } });
      });
      await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__git-panel?theme=light`);
      await page.getByRole("tab", { name: "Git panel", exact: true }).click();
      await page.locator(".git-remote-name").getByText("fork/dev", { exact: true }).waitFor();
      await page.getByRole("button", { name: "Publish branch", exact: true }).click();
      await page.waitForFunction(() => !document.querySelector(".git-remote-split > button").disabled);
      assert.equal(writes[0].remote, "fork"); assert.equal(writes[0].remote_branch, "dev");
      await page.getByRole("button", { name: "Git remote actions", exact: true }).click();
      await page.getByRole("option", { name: "Fetch", exact: true }).click();
      await page.locator(".git-remote-name").getByText("origin/main", { exact: true }).waitFor();
      await page.getByRole("button", { name: "Fetch", exact: true }).click();
      await page.waitForFunction(() => !document.querySelector(".git-remote-split > button").disabled);
      assert.equal(writes[1].remote, "origin"); assert.equal(writes[1].remote_branch, "main");
      listing.push_remote = "";
      await Promise.all([page.waitForResponse(response => new URL(response.url()).searchParams.get("view") === "changes"), page.getByRole("button", { name: "Refresh Git information", exact: true }).click()]);
      await page.getByRole("button", { name: "Git remote actions", exact: true }).click();
      assert.equal(await page.getByRole("option", { name: "Publish branch", exact: true }).isDisabled(), true);
      await page.keyboard.press("Escape");
      assert.equal(await page.getByRole("button", { name: "Fetch", exact: true }).isEnabled(), true);
      reports.push({ remoteTargets: true, passed: true });
    } finally { await context.close(); }
  }
  for (const [width, theme] of [[390, "light"], [1280, "dark"]]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, serviceWorkers: "block" });
    const page = await context.newPage(), pollRequests = [], errors = [];
    let release;
    try {
      page.setDefaultTimeout(10000); page.on("pageerror", error => errors.push(String(error)));
      const clockStart = new Date("2026-11-17T08:00:00Z");
      await page.clock.install({ time: clockStart }); await page.clock.pauseAt(new Date(clockStart.getTime() + 1000));
      let revision = 0, blocked, enteredBlocked, listRevision = 0, diffFailure = false, removed = false;
      const patch = () => `--- a/src/example.ts\n+++ b/src/example.ts\n@@ -1,100 +1,100 @@\n${Array.from({ length: 100 }, (_, i) => `+revision-${revision} line ${i} ${"long text ".repeat(40)}`).join("\n")}\n`;
      await page.route("**/api/**", async route => {
        const url = new URL(route.request().url()), view = url.searchParams.get("view");
        if (!url.pathname.endsWith("/git")) return route.fulfill({ json: { entries: [] } });
        pollRequests.push(url.pathname + url.search);
        if (view === "diff") {
          const body = patch();
          if (blocked) { enteredBlocked?.(); await blocked; }
          return route.fulfill(diffFailure ? { status: 500, json: { error: "Diff temporarily unavailable" } } : { json: { path: "src/example.ts", patch: body } });
        }
        return route.fulfill({ json: { repository: true, branch: `dev-${++listRevision}`, commit, files: removed ? [] : [
          { path: "src/example.ts", status: "MM", staged: true, unstaged: true, additions: 100, deletions: 100 },
          { path: "added.txt", status: "AM", staged: true, unstaged: true, additions: 1, deletions: 0 },
          { path: "deleted.txt", status: "D", staged: true, unstaged: false, additions: 0, deletions: 1 },
          { path: "notes.txt", status: "??", untracked: true, unstaged: true, additions: 0, deletions: 0 },
        ], commits: [] } });
      });
      await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__git-panel?theme=${theme}`);
      await page.getByRole("tab", { name: "Git panel", exact: true }).click();
      const row = page.locator(".git-file-row").filter({ hasText: "example.ts" }); await row.first().waitFor();
      const staged = page.locator(".git-file-group").filter({ has: page.locator(".git-group-toggle", { hasText: /^Staged/ }) });
      const marker = (group, name) => group.locator(".git-file-row").filter({ hasText: name }).locator(".git-file-status");
      const yellow = "rgb(188, 148, 0)";
      const [green, red] = theme === "dark" ? ["rgb(7, 196, 128)", "rgb(255, 46, 63)"] : ["rgb(24, 164, 108)", "rgb(213, 44, 54)"];
      assert.deepEqual(await marker(staged, "added.txt").evaluate(node => [...node.childNodes].map(child => [
        child.textContent, getComputedStyle(child.nodeType === Node.ELEMENT_NODE ? child : node).color,
      ])), [["A", green], ["M", yellow]]);
      for (const [name, status, color] of [["deleted.txt", "D", red], ["example.ts", "M", yellow]]) {
        assert.equal(await marker(staged, name).getByText(status, { exact: true }).evaluate(node => getComputedStyle(node).color), color);
      }
      const untracked = page.locator(".git-file-group").filter({ has: page.locator(".git-group-toggle", { hasText: /^Untracked/ }) });
      assert.deepEqual(await marker(untracked, "notes.txt").evaluate(node => [node.textContent, getComputedStyle(node).color]), ["A", green]);
      const diffRequests = () => pollRequests.filter(url => url.includes("view=diff"));
      const poll = async () => {
        const response = page.waitForResponse(response => new URL(response.url()).searchParams.get("view") === "changes");
        await page.clock.runFor(10_000); await response;
        await page.locator(".git-summary").getByText(`dev-${listRevision}`, { exact: true }).waitFor();
      };
      await poll(); assert.equal(diffRequests().length, 0, "No preview means no automatic diff request");
      await row.first().click(); await page.locator(".git-diff-source").getByText(/^revision-0 line 0 /).waitFor();
      const preview = page.locator(".git-diff-preview .file-preview-scroll");
      await preview.evaluate(element => { element.scrollTop = 220; element.scrollLeft = 120; window.__diffScroll = element; });
      const assertScroll = async () => assert.deepEqual(await preview.evaluate(element => ({ same: element === window.__diffScroll, y: element.scrollTop, x: element.scrollLeft })), { same: true, y: 220, x: 120 });
      revision = 1; await poll(); await page.locator(".git-diff-source").getByText(/^revision-1 line 0 /).waitFor(); await assertScroll();
      assert.equal(new URL(diffRequests().at(-1), "http://fixture").searchParams.get("side"), "staged");
      const beforeUnchanged = diffRequests().length;
      const unchanged = page.waitForResponse(response => new URL(response.url()).searchParams.get("view") === "diff"); await poll(); await unchanged;
      assert.equal(diffRequests().length, beforeUnchanged + 1); await assertScroll();
      blocked = new Promise(resolve => { release = resolve; });
      const blockedStarted = new Promise(resolve => { enteredBlocked = resolve; });
      const slow = page.waitForRequest(request => new URL(request.url()).searchParams.get("view") === "diff");
      await poll(); await slow; await blockedStarted; const beforeSlow = diffRequests().length;
      await poll(); assert.equal(diffRequests().length, beforeSlow, "A slow automatic diff read is not duplicated");
      const released = page.waitForResponse(response => new URL(response.url()).searchParams.get("view") === "diff");
      revision = 2; release(); blocked = undefined; await released;
      await poll(); await page.locator(".git-diff-source").getByText(/^revision-2 line 0 /).waitFor(); await assertScroll();
      diffFailure = true; await poll(); await page.getByRole("alert").getByText("Diff temporarily unavailable", { exact: false }).waitFor(); await assertScroll();
      diffFailure = false; await poll(); await page.waitForFunction(() => !document.querySelector(".git-diff-preview [role=alert]")); await assertScroll();
      const selectedRead = page.waitForResponse(response => new URL(response.url()).searchParams.get("view") === "diff");
      await row.nth(1).click(); await selectedRead; await page.locator(".git-diff-source").waitFor();
      const unstaged = page.waitForResponse(response => new URL(response.url()).searchParams.get("view") === "diff");
      await poll(); assert.equal(new URL((await unstaged).url()).searchParams.get("side"), "unstaged");
      removed = true; const beforeRemoved = diffRequests().length; await poll();
      await page.waitForFunction(() => !document.querySelector(".git-diff-preview"));
      assert.equal(diffRequests().length, beforeRemoved, "A file removed from the list is not re-read");
      await page.getByRole("tab", { name: "Project panel", exact: true }).click();
      const inactiveRequests = pollRequests.length; await page.clock.runFor(20_000);
      assert.equal(pollRequests.length, inactiveRequests, "Inactive Git panels stop polling");
      assert.deepEqual(errors, []); reports.push({ width, theme, diffPolling: true, passed: true });
    } catch (error) {
      await page.screenshot({ path: path.join(output, "failure.png") });
      await writeFile(path.join(output, "failure.json"), JSON.stringify({ errors, pollRequests, text: await page.locator("body").innerText() }, null, 2));
      throw error;
    } finally { release?.(); await context.close(); }
  }
  for (const [width, theme] of [[390, "light"], [1280, "dark"]]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, serviceWorkers: "block" });
    const page = await context.newPage(); const errors = [], historyRequests = [];
    let release;
    try {
      page.setDefaultTimeout(10000); page.on("pageerror", error => errors.push(String(error)));
      let failMore = true, updated = false, generation = 0, blockedPage;
      await page.route("**/api/**", async route => {
        const url = new URL(route.request().url());
        if (!url.pathname.endsWith("/git")) return route.fulfill({ json: { entries: [] } });
        const feature = url.searchParams.get("workspace") === "/repo/feature";
        const snapshot = { repository: true, branch: feature ? "feature/git" : "dev", commit: String(feature ? "f" : generation ? "b" : "a").repeat(40), files: [], commits: [] };
        if (url.searchParams.get("view") !== "history") return route.fulfill({ json: snapshot });
        const offset = Number(url.searchParams.get("cursor")?.split(":")[1] || 0);
        historyRequests.push({ feature, offset, generation });
        if (feature) return route.fulfill({ json: { ...snapshot, commits: [{ id: "f".repeat(40), subject: "Feature-only history", author: "Developer", time: 1794900000, tags: ["feature-release"] }] } });
        if (offset && blockedPage) await blockedPage;
        if (offset === 50 && failMore) { failMore = false; return route.fulfill({ status: 500, json: { error: "History page temporarily unavailable" } }); }
        const commits = Array.from({ length: Math.min(50, 125 - offset) }, (_, i) => ({
          id: (offset + i + 1).toString(16).padStart(40, "0"), subject: `History row ${offset + i + 1}`, author: "Developer", time: 1794900000 - (offset + i) * 3600,
          unpushed: !updated && offset + i < 3, tags: offset + i === 0 ? [updated ? "v1.2.1" : "v1.2.0"] : offset + i === 55 ? ["release/v1.1"] : [],
        }));
        return route.fulfill({ json: { ...snapshot, commits, ...(offset + 50 < 125 ? { next_cursor: snapshot.commit + ":" + (offset + 50) } : {}) } });
      });
      await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__git-panel?theme=${theme}`);
      await page.getByRole("tab", { name: "Git panel", exact: true }).click();
      await page.getByRole("tab", { name: "History", exact: true }).click();
      const rows = page.locator(".git-history-list > .file-panel-button");
      await rows.nth(49).waitFor(); assert.equal(await rows.count(), 50);
      assert.equal(await page.getByLabel("Not pushed", { exact: true }).count(), 3);
      await rows.first().getByText("v1.2.0", { exact: true }).waitFor();
      await page.locator(".file-sidebar").screenshot({ path: path.join(output, `history-${width}-${theme}.png`) });
      const scrollBottom = () => page.locator(".git-panel .file-tree-pane").evaluate(element => { element.scrollTop = element.scrollHeight; });
      await scrollBottom(); await page.getByRole("alert").getByText("History page temporarily unavailable", { exact: false }).waitFor();
      assert.equal(await rows.count(), 50, "A failed next page preserves existing commits");
      await page.getByRole("button", { name: "Retry", exact: true }).click();
      await rows.nth(99).waitFor(); assert.equal(await rows.count(), 100);
      await rows.nth(55).getByText("release/v1.1", { exact: true }).waitFor();
      await scrollBottom(); await rows.nth(124).waitFor();
      assert.equal(await rows.count(), 125);
      assert.equal(new Set(await rows.locator("strong").allTextContents()).size, 125, "Pages append without duplicate rows");
      assert.equal(await page.locator(".git-history-end").count(), 0, "Loading stops at the end of history");
      updated = true;
      await page.getByRole("button", { name: "Refresh Git information", exact: true }).click();
      await page.getByText("v1.2.1", { exact: true }).waitFor();
      assert.equal(await rows.count(), 125, "Refreshing retains the loaded history length");
      assert.equal(await page.getByLabel("Not pushed", { exact: true }).count(), 0, "Refreshing updates outgoing markers");
      await rows.first().click(); await page.locator(".git-history-back").waitFor();
      await page.locator(".git-history-back").click(); await rows.nth(124).waitFor();
      generation = 1;
      blockedPage = new Promise(resolve => { release = resolve; });
      const pendingPage = page.waitForRequest(request => new URL(request.url()).searchParams.get("cursor")?.startsWith("b".repeat(40)));
      await page.getByRole("button", { name: "Refresh Git information", exact: true }).click();
      await page.waitForFunction(() => document.querySelectorAll(".git-history-list > .file-panel-button").length === 50);
      await scrollBottom();
      await pendingPage;
      await page.getByRole("button", { name: "Switch directory", exact: true }).click();
      await page.getByRole("option").filter({ hasText: "feature/git" }).click();
      await page.getByRole("tab", { name: "Git panel", exact: true }).click();
      await page.getByRole("tab", { name: "History", exact: true }).click();
      await page.getByText("Feature-only history", { exact: true }).waitFor();
      release(); blockedPage = undefined;
      await page.getByText("feature-release", { exact: true }).waitFor();
      assert.equal(await rows.count(), 1, "A stale page cannot enter another directory's history");
      assert.deepEqual(errors, []); reports.push({ width, theme, historyPagination: true, passed: true });
    } catch (error) {
      await page.screenshot({ path: path.join(output, "failure.png") });
      await writeFile(path.join(output, "failure.json"), JSON.stringify({ errors, historyRequests, text: await page.locator("body").innerText() }, null, 2));
      throw error;
    } finally { release?.(); await context.close(); }
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
