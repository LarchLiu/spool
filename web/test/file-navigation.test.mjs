import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { FileTree } from "@pierre/trees";
import { FilePreviewSplit } from "../.test-build/components/web/src/file-preview-split.js";
import { FileSidebarHeader } from "../.test-build/components/web/src/file-sidebar-header.js";
import { SessionWorkspaceInfo } from "../.test-build/components/web/src/session-workspace.js";
import { receiveSessionWorkspace, selectSessionWorkspace } from "../.test-build/components/web/src/session-workspace-state.js";
import {
  localFileTarget,
  projectFileURL,
  fileAPIBase,
} from "../.test-build/local-file-links.js";
import {
  parsePanelLayout,
  panelWidth,
  defaultPanelLayout,
  defaultFileTreeFraction,
  parsePreviewSplit,
  previewSplitLimits,
  previewTreeFraction,
} from "../.test-build/panel-layout.js";

test("file links preserve native paths and parse Codex line/column suffixes", () => {
  assert.deepEqual(
    localFileTarget("/Users/k/code/muye/docs/development-plan.md:55"),
    { path: "/Users/k/code/muye/docs/development-plan.md", line: 55 },
  );
  assert.deepEqual(localFileTarget("src/main.tsx#L12C3"), {
    path: "src/main.tsx",
    line: 12,
    column: 3,
  });
  assert.deepEqual(localFileTarget("README.md:55"), {
    path: "README.md",
    line: 55,
  });
  assert.deepEqual(localFileTarget("C:\\repo\\main.rs:12:5"), {
    path: "C:/repo/main.rs",
    line: 12,
    column: 5,
  });
  assert.deepEqual(
    localFileTarget("file:///Users/k/Project%20Files/plan.md#L2"),
    { path: "/Users/k/Project Files/plan.md", line: 2 },
  );
  assert.deepEqual(localFileTarget("../README.md", "docs/plan.md"), {
    path: "docs/../README.md",
  });
});

test("session file URLs retain chat scope for previews, images and downloads", () => {
  assert.equal(fileAPIBase("project", "chat"), "/api/chats/chat");
  assert.equal(fileAPIBase("project"), "/api/projects/project");
  for (const mode of [undefined, "image", "download", "preview"]) {
    const url = new URL(
      projectFileURL("project", "/worktree/a b.md", mode, "chat"),
      "https://spool.invalid",
    );
    assert.equal(url.pathname, "/api/chats/chat/file");
    assert.equal(url.searchParams.get("path"), "/worktree/a b.md");
    if (mode) assert.equal(url.searchParams.get(mode), "1");
    const selected = new URL(projectFileURL("project", "same.txt", mode, "chat", "/work/other tree"), "https://spool.invalid");
    assert.equal(selected.searchParams.get("workspace"), "/work/other tree");
    assert.equal(selected.searchParams.get("path"), "same.txt");
    if (mode) assert.equal(selected.searchParams.get(mode), "1");
  }
});

test("workspace selection survives refresh and follows the next native turn or directory change", () => {
  const main = { path: "/repo", available: true, kind: "main" };
  const first = { path: "/trees/first", available: true, kind: "worktree" };
  const second = { path: "/trees/second", available: true, kind: "worktree" };
  const latest = { ...first, turn_id: "turn-1", workspaces: [main, first, second] };
  let state = receiveSessionWorkspace(undefined, latest);
  assert.equal(state.workspace.path, first.path);
  state = selectSessionWorkspace(state, main.path);
  assert.equal(state.workspace.path, main.path);
  state = receiveSessionWorkspace(state, { ...latest, branch: "new-branch" });
  assert.equal(state.workspace.path, main.path);
  assert.equal(selectSessionWorkspace(state, "/private").workspace.path, main.path);
  state = receiveSessionWorkspace(state, { ...latest, turn_id: "turn-2" });
  assert.equal(state.workspace.path, first.path);
  state = selectSessionWorkspace(state, main.path);
  state = receiveSessionWorkspace(state, { ...latest, ...second, turn_id: "turn-2" });
  assert.equal(state.workspace.path, second.path);
  state = selectSessionWorkspace(state, main.path);
  state = receiveSessionWorkspace(state, { ...latest, workspaces: [first, second] });
  assert.equal(state.workspace.path, first.path);
});

test("directory title shows a chooser only when multiple workspaces are available", () => {
  const main = { path: "/repo/main", available: true, kind: "main" };
  const tree = { path: "/repo/feature", available: true, kind: "worktree" };
  const render = (workspaces) => renderToStaticMarkup(createElement(FileSidebarHeader, {
    name: "Project", workspace: main, workspaces, onSelectWorkspace: () => {}, t: key => key, onClose: () => {},
  }));
  assert.match(render([main]), /<strong title="\/repo\/main">main<\/strong>/);
  assert.doesNotMatch(render([main]), /Switch directory|custom-select/);
  assert.match(render([main, tree]), /aria-label="Switch directory"/);
  assert.match(render([main, tree]), /aria-haspopup="listbox"/);
});

test("session workspace shows its full path, checkout type, branch and missing state", () => {
  const workspace = {
    path: "/work/trees/feature with spaces",
    kind: "worktree",
    branch: "feature/files",
    available: true,
  };
  const render = (value) =>
    renderToStaticMarkup(
      createElement(SessionWorkspaceInfo, {
        workspace: value,
        t: (key, values) => key.replace("{commit}", values?.commit || ""),
      }),
    );
  const html = render(workspace);
  assert.match(html, /aria-label="Session directory"/);
  assert.match(html, /Worktree/);
  assert.ok(html.includes(workspace.path));
  assert.ok(html.includes(workspace.branch));
  assert.doesNotMatch(html, /unavailable/);
  const detached = render({
    ...workspace,
    branch: undefined,
    commit: "abc1234",
  });
  assert.match(detached, /Detached HEAD · abc1234/);
  assert.doesNotMatch(detached, /feature\/files/);
  const main = render({ ...workspace, kind: "main", branch: "dev" });
  assert.match(main, /Main checkout/);
  const missing = render({ ...workspace, available: false });
  assert.match(missing, /role="status">Session directory is unavailable/);
});

test("web links and unsafe schemes cannot become local file actions", () => {
  for (const url of [
    "https://example.com/a.md",
    "http://localhost/test",
    "//example.com/file",
    "#heading",
    "mailto:a@b.com",
    "javascript:alert(1)",
    "javascript%3Aalert(1)",
    "data:text/html,test",
    "file://other-host/etc/passwd",
    "bad%00file",
    "bad%escape",
  ]) {
    assert.equal(localFileTarget(url), null, url);
  }
  assert.equal(
    projectFileURL("project", "docs/a b.md", "download"),
    "/api/projects/project/file?path=docs%2Fa+b.md&download=1",
  );
});

test("sidebar widths clamp invalid persisted values and retain independent collapse state", () => {
  assert.deepEqual(parsePanelLayout(null), defaultPanelLayout);
  assert.deepEqual(parsePanelLayout("garbage"), defaultPanelLayout);
  const layout = parsePanelLayout(
    '{"leftOpen":false,"rightOpen":true,"leftWidth":10,"rightWidth":99999}',
  );
  assert.deepEqual(layout, {
    leftOpen: false,
    rightOpen: true,
    leftWidth: 220,
    rightWidth: 720,
  });
  assert.equal(panelWidth("left", NaN), 298);
  assert.equal(panelWidth("right", 450), 450);
});

test("the file sidebar title contains the project name and refresh before close", () => {
  const html = renderToStaticMarkup(
    createElement(FileSidebarHeader, {
      name: "Project name",
      t: (key) => key,
      onRefresh: () => {},
      onClose: () => {},
    }),
  );
  assert.match(html, /<strong title="Project name">Project name<\/strong>/);
  assert.ok(
    html.indexOf('aria-label="Refresh files"') <
      html.indexOf('aria-label="Collapse file sidebar"'),
  );
  assert.doesNotMatch(html, /<form|<input/);
});

test("file search switches the project title to a live search input without a submit button", () => {
  const props = {
    name: "Project name",
    t: (key) => key,
    onRefresh: () => {},
    onClose: () => {},
    search: {
      isOpen: false,
      onOpen: () => {},
      onChange: () => {},
      onClose: () => {},
    },
  };
  const closed = renderToStaticMarkup(createElement(FileSidebarHeader, props));
  assert.match(closed, /<strong title="Project name">/);
  assert.match(closed, /aria-expanded="false"[^>]*aria-label="Search files"/);
  assert.doesNotMatch(closed, /<input/);
  const open = renderToStaticMarkup(
    createElement(FileSidebarHeader, {
      ...props,
      search: { ...props.search, isOpen: true },
    }),
  );
  assert.match(open, /<div[^>]*role="search"/);
  assert.match(open, /<input[^>]*type="search"[^>]*aria-label="Search files"/);
  assert.match(open, /<input[^>]*\/>\s*<\/div>/);
  assert.doesNotMatch(
    open,
    /type="submit"|aria-label="Search files"[^>]*>\s*<svg/,
  );
  assert.match(open, /aria-label="Close file search"/);
  assert.doesNotMatch(open, /<strong/);
  const empty = renderToStaticMarkup(
    createElement(FileSidebarHeader, {
      t: props.t,
      onClose: props.onClose,
    }),
  );
  assert.match(empty, /<strong>Files<\/strong>/);
  assert.doesNotMatch(empty, /Search files|Refresh files/);
});

test("the external search keeps tree filtering, selection and cancel behavior without the built-in input", () => {
  const model = new FileTree({
    paths: ["README.md", "docs/plan.md", "docs/nested/notes.txt"],
    search: false,
    initialExpansion: "closed",
  });
  try {
    const visiblePaths = () =>
      model.getVisibleRows(0, model.getVisibleCount()).map((row) => row.path);
    const initialPaths = visiblePaths();
    model.openSearch();
    model.setSearch("PLAN");
    assert.deepEqual(model.getSearchMatchingPaths(), ["docs/plan.md"]);
    assert.deepEqual(visiblePaths(), ["docs/", "docs/plan.md"]);
    model.setSearch("");
    assert.deepEqual(visiblePaths(), initialPaths);
    model.setSearch("plan");
    model.getItem("docs/plan.md").select();
    assert.deepEqual(model.getSelectedPaths(), ["docs/plan.md"]);
    model.closeSearch();
    assert.equal(model.isSearchOpen(), false);
    assert.deepEqual(visiblePaths(), [
      "docs/",
      "docs/nested/",
      "docs/plan.md",
      "README.md",
    ]);
  } finally {
    model.cleanUp();
  }
});

test("file preview split restores and clamps its saved ratio", () => {
  assert.equal(parsePreviewSplit(null), defaultFileTreeFraction);
  assert.equal(parsePreviewSplit("invalid"), defaultFileTreeFraction);
  assert.equal(parsePreviewSplit('"0.5"'), defaultFileTreeFraction);
  assert.equal(parsePreviewSplit("0.5"), 0.5);
  assert.equal(parsePreviewSplit("999"), 0.8);
  assert.equal(parsePreviewSplit("-1"), 0.15);
  assert.equal(previewTreeFraction(NaN), defaultFileTreeFraction);
  const { min, max } = previewSplitLimits(608);
  assert.equal(min, 0.2);
  assert.equal(max, 1 - 160 / 600);
  assert.equal(previewTreeFraction(0, 608), min);
  assert.equal(previewTreeFraction(1, 608), max);
  const compact = previewSplitLimits(180);
  assert.ok(compact.min <= compact.max);
  assert.ok(previewTreeFraction(0.1, 180) >= compact.min);
  assert.ok(previewTreeFraction(0.9, 180) <= compact.max);
});

test("file preview exposes a keyboard-focusable horizontal separator only when open", () => {
  const props = {
    tree: createElement("div", null, "Tree"),
    t: (key) => key,
  };
  const closed = renderToStaticMarkup(
    createElement(FilePreviewSplit, { ...props, preview: null }),
  );
  assert.doesNotMatch(closed, /role="separator"/);
  assert.doesNotMatch(closed, /file-panel-hint|Select a file/);
  assert.match(closed, /class="file-tree-pane"/);
  const open = renderToStaticMarkup(
    createElement(FilePreviewSplit, {
      ...props,
      preview: createElement("section", null, "Preview"),
    }),
  );
  assert.match(open, /role="separator" aria-orientation="horizontal"/);
  assert.match(open, /aria-label="Resize file preview"/);
  assert.match(open, /tabindex="0"/);
  assert.match(open, /grid-template-rows:minmax\(0, 0\.32fr\) 8px minmax\(0, /);
});
