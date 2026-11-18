// Production App navigation with delayed snapshots and workspace discovery.
// Run through tools/run.sh with SPOOL_NODE_MODULES pointing at Playwright.
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
const require = createRequire(path.join(process.env.SPOOL_NODE_MODULES || path.join(root, "web/node_modules"), "__navigation__.cjs"));
const { chromium } = require("playwright");
const output = path.join(root, "build/artifacts/chat-navigation");
const cleanup = [], requests = [], errors = [], gates = new Map();
const main = { path: "/repo/main", kind: "main", branch: "dev", available: true };
const tree = { path: "/repo/feature", kind: "worktree", branch: "feature", available: true };
const chats = ["alpha", "beta", "gamma", "delta"].map(id => ({
  id, title: `Conversation ${id}`, project_id: id === "delta" ? "other" : "project", user_id: "owner",
  backend: id === "beta" ? "claude" : "codex", status: "completed", updated_at: 1,
  turns: [{ id: `turn-${id}`, status: "completed", part_start: 0, input_end: 0, part_end: 1 }],
  transcript: { parts: [{ role: "assistant", kind: "text", text: `Response from ${id}` }] },
}));
const hold = endpoint => {
  let release;
  const promise = new Promise(resolve => { release = resolve; });
  const gate = { promise, release };
  gates.set(endpoint, gate);
  return gate;
};
const server = await createTestViteServer({ after: fn => cleanup.push(fn) }, {
  configFile: false, root: path.join(root, "web"), plugins: [react()], server: { host: "127.0.0.1", port: 0 },
});
let browser;
let passed = false;
try {
  await mkdir(output, { recursive: true });
  await server.listen();
  browser = await chromium.launch({ channel: "chrome", headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: "block" });
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  page.on("pageerror", error => errors.push(String(error)));
  await page.addInitScript(() => {
    localStorage.setItem("spool.ui-preferences.v1", JSON.stringify({ language: "en", theme: "light" }));
    window.WebSocket = class extends EventTarget { readyState = 0; send() {} close() { this.readyState = 3; } };
    // Polls are exercised explicitly through focus, keeping request counts deterministic.
    const interval = window.setInterval;
    window.setInterval = (callback, delay, ...args) => interval(callback, delay === 10000 ? 3600000 : delay, ...args);
  });
  await page.route("**/api/**", async route => {
    const request = route.request(), url = new URL(request.url()), endpoint = url.pathname;
    requests.push(endpoint + url.search);
    const gate = gates.get(endpoint);
    if (gate) await gate.promise;
    const json = (value, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
    if (endpoint === "/api/me") return json({ user: { id: "owner", nickname: "Owner" }, csrf: "fixture" });
    if (endpoint === "/api/version") return json({ version: "fixture" });
    if (endpoint === "/api/profiles") return json({ revision: 1, people: [{ id: "owner", nickname: "Owner" }] });
    if (endpoint === "/api/navigation") return json({ projects: [{ id: "project", name: "Project" }, { id: "other", name: "Other project" }], chats });
    if (endpoint === "/api/agents") return json(["codex", "claude"].map(backend => ({ backend, enabled: true, installed: true, catalog: { models: [] } })));
    if (endpoint === "/api/permissions") return json({ backend: "codex", default: "ask", modes: [{ id: "ask", available: true }] });
    if (endpoint === "/api/chats" && request.method() === "POST") {
      const created = { ...request.postDataJSON(), id: "created", title: "Conversation created", user_id: "owner", status: "ready", updated_at: 1,
        turns: [], transcript: { parts: [] } };
      chats.unshift(created);
      return json(created);
    }
    const chat = chats.find(item => endpoint.startsWith(`/api/chats/${item.id}`));
    const workspace = [main, tree].find(item => item.path === url.searchParams.get("workspace")) || (chat?.id === "gamma" ? tree : main);
    if (endpoint.endsWith("/messages") && request.method() === "POST") {
      Object.assign(chat, { status: "completed", updated_at: 2,
        turns: [{ id: "turn-created", status: "completed", part_start: 0, input_end: 1, part_end: 2 }],
        transcript: { parts: [{ role: "user", kind: "text", text: request.postDataJSON().message },
          { role: "assistant", kind: "text", text: "Response from created" }] } });
      return json(chat);
    }
    if (endpoint.endsWith("/workspace")) {
      if (gate?.fail) return json({ error: "Workspace unavailable" }, 500);
      return json({ ...workspace, workspaces: [main, tree] });
    }
    if (endpoint.endsWith("/events")) return route.fulfill({ contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(chat)}\n\n` });
    if (endpoint.endsWith("/files")) return json({ path: url.searchParams.get("path"), entries: url.searchParams.get("path") === "src" ? [
      { name: "nested.txt", path: "src/nested.txt", kind: "file" },
    ] : [
      { name: "src", path: "src", kind: "directory" },
      { name: "readme.txt", path: "readme.txt", kind: "file" },
    ], truncated: false });
    if (endpoint.endsWith("/file")) return json({ path: "readme.txt", name: "readme.txt", kind: "text", content: `File from ${chat.id}`, size: 20, media_type: "text/plain" });
    if (endpoint.endsWith("/git")) {
      if (url.searchParams.get("view") === "diff") return json({ path: "readme.txt", patch: "@@ -1 +1 @@\n-old\n+new contents\n" });
      return json({ repository: true, writable: true, branch: workspace.branch, commit: "a".repeat(40), files: [{ path: "readme.txt", status: "M", unstaged: true, additions: 1, deletions: 1 }],
        commits: [{ id: "a".repeat(40), subject: "Latest commit", author: "Developer", time: 1794900000 }] });
    }
    if (chat) return json(chat);
    return json([]);
  });
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/`);
  await page.getByText("Response from alpha", { exact: true }).waitFor();
  const openFiles = page.getByRole("button", { name: "Open file sidebar", exact: true });
  if (await openFiles.isVisible()) await openFiles.click();
  const directory = page.locator('[data-item-path="src/"]');
  await directory.click();
  await page.locator('[data-item-path="src/nested.txt"]').waitFor();
  await page.locator('[data-item-path="readme.txt"]').click();
  await page.getByRole("link", { name: "Download file", exact: true }).waitFor();
  await directory.evaluate(element => { window.originalDirectory = element; });
  const workspaceGate = hold("/api/chats/beta/workspace"), snapshotGate = hold("/api/chats/beta/events");
  const beforeSwitch = requests.length;
  await page.locator(".nav-chat-open").filter({ hasText: "Conversation beta" }).first().click();
  await page.waitForFunction(() => document.querySelector(".chat-heading")?.textContent.includes("Conversation beta") && document.querySelector(".history")?.textContent.includes("Loading conversation…"));
  assert.equal(await page.locator(".welcome").count(), 0, "Loading an existing chat cannot show the new-chat welcome screen");
  assert.equal(await page.getByText("Response from alpha", { exact: true }).count(), 0, "The previous transcript is hidden immediately");
  assert.equal(await page.locator(".file-sidebar").getAttribute("inert"), "", "The retained directory cannot be used before discovery finishes");
  assert.equal(await page.locator(".file-preview").count(), 0, "Chat-scoped previews clear during navigation");
  workspaceGate.release(); snapshotGate.release(); gates.clear();
  await page.getByText("Response from beta", { exact: true }).waitFor();
  await page.waitForFunction(() => !document.querySelector(".file-sidebar").inert);
  assert.equal(await directory.evaluate(element => window.originalDirectory === element), true, "Same-directory navigation retains the file tree DOM");
  assert.ok(await page.locator('[data-item-path="src/nested.txt"]').isVisible(), "Expanded directories survive navigation");
  assert.ok(requests.slice(beforeSwitch).every(url => !url.includes("/files?")), "Same-directory navigation does not reload files");
  await page.locator('[data-item-path="readme.txt"]').click();
  await page.getByRole("link", { name: "Download file", exact: true }).waitFor();
  assert.match(await page.getByRole("link", { name: "Download file", exact: true }).getAttribute("href"), /\/chats\/beta\/file/);
  await page.getByRole("tab", { name: "Git panel", exact: true }).click();
  await page.locator(".git-file-row").click();
  await page.locator(".git-diff-source").waitFor();
  const navigate = async id => {
    await page.locator(".nav-chat-open").filter({ hasText: `Conversation ${id}` }).first().click();
    await page.getByText(`Response from ${id}`, { exact: true }).waitFor();
    await page.waitForFunction(() => !document.querySelector(".file-sidebar").inert);
  };
  let offset = requests.length;
  await navigate("alpha");
  assert.ok(await page.locator(".git-diff-source").isVisible(), "Git selection and diff survive switching within a directory");
  assert.ok(requests.slice(offset).every(url => !url.includes("/git?") && !url.includes("/files?")), "Switching chats reloads neither Git nor the tree");
  await Promise.all([
    page.waitForResponse(response => new URL(response.url()).pathname === "/api/chats/alpha/git" && new URL(response.url()).searchParams.get("view") === "diff"),
    page.getByRole("button", { name: "Refresh diff preview", exact: true }).click(),
  ]);
  await page.getByRole("button", { name: "Close diff", exact: true }).click();
  await page.getByRole("tab", { name: "History", exact: true }).click();
  await page.getByRole("button", { name: /Latest commit/ }).waitFor();
  offset = requests.length;
  await navigate("beta");
  assert.ok(await page.getByRole("button", { name: /Latest commit/ }).isVisible(), "Git history survives same-directory navigation");
  assert.ok(requests.slice(offset).every(url => !url.includes("/git?")), "History is not requested again on a chat switch");
  await Promise.all([
    page.waitForResponse(response => new URL(response.url()).pathname === "/api/chats/beta/git" && new URL(response.url()).searchParams.get("view") === "history"),
    page.evaluate(() => window.dispatchEvent(new Event("focus"))),
  ]);
  await page.getByRole("tab", { name: "Changes", exact: true }).click();
  await page.locator(".git-file-row").waitFor();
  await page.getByRole("button", { name: "Expand commit area", exact: true }).click();
  await page.getByRole("textbox", { name: "Commit message", exact: true }).fill("Unsent commit draft");
  offset = requests.length;
  await navigate("alpha");
  assert.equal(await page.getByRole("textbox", { name: "Commit message", exact: true }).inputValue(), "Unsent commit draft");
  assert.ok(requests.slice(offset).every(url => !url.includes("/git?")), "Commit drafts survive without refreshing Git");
  await navigate("gamma");
  await page.locator(".git-summary").getByText("feature", { exact: true }).waitFor();
  assert.equal(await page.getByRole("textbox", { name: "Commit message", exact: true }).count(), 0, "A different directory resets Git state");
  await navigate("alpha");
  offset = requests.length;
  await navigate("delta");
  await page.locator(".git-summary").getByText("dev", { exact: true }).waitFor();
  assert.ok(requests.slice(offset).some(url => url.startsWith("/api/chats/delta/files?")), "Different projects never share directory data");
  assert.ok(requests.slice(offset).some(url => url.startsWith("/api/chats/delta/git?")), "Git is scoped by project as well as directory");
  await navigate("alpha");
  const failed = hold("/api/chats/beta/workspace"); failed.fail = true; failed.release();
  await page.locator(".nav-chat-open").filter({ hasText: "Conversation beta" }).first().click();
  await page.getByRole("tab", { name: "Project panel", exact: true }).click();
  await page.getByRole("alert").filter({ hasText: "Workspace unavailable" }).waitFor();
  assert.equal(await page.locator('[data-item-path="readme.txt"]').count(), 0, "Failed discovery clears stale directory data");
  gates.clear();
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await page.locator('[data-item-path="readme.txt"]').waitFor();
  offset = requests.length;
  await Promise.all([
    page.waitForResponse(response => new URL(response.url()).pathname === "/api/chats/beta/files"),
    page.getByRole("button", { name: "Refresh files", exact: true }).click(),
  ]);
  assert.ok(requests.slice(offset).some(url => url.startsWith("/api/chats/beta/files?")), "Explicit refresh still reloads the current directory");
  const newChat = async (name = "Project") => {
    await page.getByRole("button", { name: `New chat in ${name}`, exact: true }).click();
    await page.locator(".welcome").waitFor();
    assert.equal(await page.locator(".chat-heading").innerText(), "New chat", "Explicit new-chat navigation keeps its welcome state");
  };
  const assertNoDirectoryReload = offset => assert.ok(
    requests.slice(offset).every(url => !/\/(files|git)\?/.test(url)),
    "A same-project draft does not reload the file tree or Git",
  );
  await directory.click();
  await page.locator('[data-item-path="src/nested.txt"]').waitFor();
  await directory.evaluate(element => { window.draftDirectory = element; });
  await page.locator('[data-item-path="readme.txt"]').click();
  await page.getByRole("link", { name: "Download file", exact: true }).waitFor();
  offset = requests.length;
  await newChat();
  assertNoDirectoryReload(offset);
  assert.equal(await directory.evaluate(element => window.draftDirectory === element), true, "A new draft retains the file tree DOM");
  assert.ok(await page.locator('[data-item-path="src/nested.txt"]').isVisible(), "A new draft retains expanded directories");
  assert.equal(await page.locator(".file-sidebar").getAttribute("inert"), null, "The retained directory remains usable in a draft");
  assert.equal(await page.locator(".file-preview").count(), 0, "Chat-scoped file previews clear when opening a draft");
  await newChat();
  assertNoDirectoryReload(offset);
  await page.locator('[data-item-path="readme.txt"]').click();
  await page.getByRole("link", { name: "Download file", exact: true }).waitFor();
  assert.match(await page.getByRole("link", { name: "Download file", exact: true }).getAttribute("href"), /\/chats\/beta\/file/,
    "Draft file actions still address the retained directory");
  await navigate("beta");
  await page.getByRole("tab", { name: "Git panel", exact: true }).click();
  await page.locator(".git-file-row").click();
  await page.locator(".git-diff-source").waitFor();
  offset = requests.length;
  await newChat();
  assertNoDirectoryReload(offset);
  assert.ok(await page.locator(".git-diff-source").isVisible(), "A new draft retains the selected Git diff");
  await page.getByRole("button", { name: "Close diff", exact: true }).click();
  await page.getByRole("button", { name: "Expand commit area", exact: true }).click();
  await page.getByRole("textbox", { name: "Commit message", exact: true }).fill("Keep this commit draft");
  await newChat();
  assertNoDirectoryReload(offset);
  assert.equal(await page.getByRole("textbox", { name: "Commit message", exact: true }).inputValue(), "Keep this commit draft");
  const createdWorkspace = hold("/api/chats/created/workspace");
  await page.locator("#chat-message").fill("First message in the new chat");
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await page.getByText("Response from created", { exact: true }).waitFor();
  assert.equal(await page.locator(".file-sidebar").getAttribute("inert"), "", "Workspace discovery still gates the newly created chat");
  createdWorkspace.release(); gates.clear();
  await page.waitForFunction(() => !document.querySelector(".file-sidebar").inert);
  assertNoDirectoryReload(offset);
  assert.equal(await directory.evaluate(element => window.draftDirectory === element), true, "First-message creation retains the same directory DOM");
  assert.equal(await page.getByRole("textbox", { name: "Commit message", exact: true }).inputValue(), "Keep this commit draft");
  await Promise.all([
    page.waitForResponse(response => new URL(response.url()).pathname === "/api/chats/created/git"),
    page.getByRole("button", { name: "Refresh Git information", exact: true }).click(),
  ]);
  offset = requests.length;
  await newChat("Other project");
  await page.locator(".git-summary").getByText("dev", { exact: true }).waitFor();
  assert.ok(requests.slice(offset).some(url => url.startsWith("/api/projects/other/files?")), "A draft in another project loads its own files");
  assert.ok(requests.slice(offset).some(url => url.startsWith("/api/projects/other/git?")), "A draft in another project loads its own Git state");
  assert.equal(await page.getByRole("textbox", { name: "Commit message", exact: true }).count(), 0, "Another project cannot inherit a commit draft");
  await navigate("gamma");
  await page.locator(".git-summary").getByText("feature", { exact: true }).waitFor();
  offset = requests.length;
  await newChat();
  assertNoDirectoryReload(offset);
  assert.equal(await page.locator(".file-sidebar-title strong").innerText(), "feature", "A same-project draft keeps the displayed worktree");
  await page.getByRole("button", { name: "Switch directory", exact: true }).click();
  await page.getByRole("option", { name: /main/ }).click();
  await page.locator(".git-summary").getByText("dev", { exact: true }).waitFor();
  assert.equal(await page.locator(".file-sidebar-title strong").innerText(), "main", "Directory selection remains functional in a draft");
  assert.deepEqual(errors, []);
  await page.screenshot({ path: path.join(output, "navigation.png") });
  passed = true;
  console.log("Chat navigation passed: delayed loading, same-directory file/Git reuse, new-chat drafts and first-message creation, current-chat requests, directory/project changes, retry and explicit refresh.");
} finally {
  for (const gate of gates.values()) gate.release();
  await browser?.close();
  for (const close of cleanup.reverse()) await close();
  await writeFile(path.join(output, "report.json"), JSON.stringify({ passed, errors, requests }, null, 2));
}
