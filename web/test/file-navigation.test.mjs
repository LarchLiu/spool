import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { FileTree } from "@pierre/trees";
import { FilePreviewSplit } from "../.test-build/components/web/src/file-preview-split.js";
import { FileSidebarHeader } from "../.test-build/components/web/src/file-sidebar-header.js";
import {
  localFileTarget,
  projectFileURL,
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

test("sidebar separators keep fixed geometry while pressed and all resize handles use neutral feedback", async () => {
  const css = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  assert.match(css, /\.panel-resize-handle \{[^}]*width: 8px/);
  assert.doesNotMatch(css, /\.panel-resize-handle\.resizing \{/);
  for (const selector of [
    "panel-resize-handle",
    "file-preview-resize-handle",
  ]) {
    assert.match(
      css,
      new RegExp(
        `\\.${selector}\\.resizing::after \\{[^}]*background: var\\(--muted\\)`,
      ),
    );
    assert.doesNotMatch(
      css,
      new RegExp(
        `\\.${selector}[^}]*\\{[^}]*background: var\\(--(?:focus-ring|color-primary)\\)`,
      ),
    );
  }
  const handle = await readFile(
    new URL("../src/panel-resize-handle.tsx", import.meta.url),
    "utf8",
  );
  assert.match(
    handle,
    /event\.clientX !== start\.current\.x \|\| start\.current\.moved/,
  );
  assert.match(
    handle,
    /onLostPointerCapture=\{\(event\) =>\s*event\.currentTarget\.classList\.remove\("resizing"\)/,
  );
});

test("header sidebar open buttons are hidden while their sidebar is open", async () => {
  const main = await readFile(
    new URL("../src/main.tsx", import.meta.url),
    "utf8",
  );
  assert.match(
    main,
    /!\(narrowLayout \? sidebar : panels\.leftOpen\) && \(\s*<button\s+className="icon panel-toggle-left"/,
  );
  assert.match(
    main,
    /!\(narrowLayout \? fileSidebarMobile : panels\.rightOpen\) && \(\s*<button/,
  );
  assert.doesNotMatch(main, /PanelLeftClose|Collapse sidebar/);
});

test("the file sidebar title contains the project name and refresh before close", async () => {
  const source = await readFile(
    new URL("../src/file-sidebar.tsx", import.meta.url),
    "utf8",
  );
  assert.match(source, /<FileSidebarHeader\s+name=\{project\.name\}/);
  assert.doesNotMatch(source, /file-project-bar/);
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

test("file search switches the project title to a live search input without a submit button", async () => {
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
  const source = await readFile(
    new URL("../src/file-sidebar.tsx", import.meta.url),
    "utf8",
  );
  assert.match(source, /search: false/);
  assert.match(source, /useFileTreeSearch\(model\)/);
  assert.match(source, /onChange: search\.setValue/);
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

test("file loading retains the displayed file and its own line target without a loading flash", async () => {
  const source = await readFile(
    new URL("../src/file-sidebar.tsx", import.meta.url),
    "utf8",
  );
  assert.doesNotMatch(source, /loadingFile|setLoadingFile|Loading file…/);
  assert.match(source, /setLoaded\(\{ file: next, selection \}\)/);
  assert.match(source, /key=\{loaded\.selection\.request\}/);
  assert.match(source, /target=\{loaded\.selection\.target\}/);
  const pending = source.slice(
    source.indexOf("if (!active || !selection) return;"),
    source.indexOf(".then(async (next)"),
  );
  assert.doesNotMatch(pending, /setLoaded\(null\)/);
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
