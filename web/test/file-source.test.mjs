import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { readFile } from "node:fs/promises";
import {
  FileSource,
  FileSourceBoundary,
  PlainFileSource,
  centerSourceLine,
} from "../.test-build/components/web/src/file-source.js";

test("source previews use the existing single-file renderer in both themes", async () => {
  for (const theme of ["light", "dark"]) {
    const html = renderToStaticMarkup(
      createElement(FileSource, {
        path: "src/example.tsx",
        contents: "export const label = <span>Hello</span>;",
        line: 1,
        theme,
      }),
    );
    assert.match(html, /<diffs-container[^>]*class="file-code-preview"/);
    assert.doesNotMatch(html, /contenteditable|textarea/);
  }
  const source = await readFile(
    new URL("../src/file-source.tsx", import.meta.url),
    "utf8",
  );
  assert.match(source, /disableFileHeader: true/);
  assert.match(source, /themeType: theme/);
  assert.match(source, /edit=\{false\}/);
  assert.match(source, /name: path, contents/);
});

test("the plain source fallback preserves blank lines, indentation and inert markup", () => {
  const html = renderToStaticMarkup(
    createElement(PlainFileSource, {
      contents: "  const value = 1;\n\n<script>alert('test')</script>\n",
      line: 3,
    }),
  );
  assert.equal((html.match(/data-line="/g) || []).length, 4);
  assert.match(html, /<code>  const value = 1;<\/code>/);
  assert.match(html, /file-source-line highlighted[^>]*data-line="3"/);
  assert.match(html, /&lt;script&gt;alert/);
  assert.doesNotMatch(html, /<script/);
  const empty = renderToStaticMarkup(
    createElement(PlainFileSource, { contents: "" }),
  );
  assert.match(empty, /data-line="1"/);
});

test("renderer failures fall back to complete readable source with the target line", () => {
  const contents = "first\nsecond\nthird";
  const boundary = new FileSourceBoundary({
    contents,
    line: 2,
    children: createElement("div", null, "Highlighted"),
  });
  assert.equal(boundary.render().props.children, "Highlighted");
  boundary.state = FileSourceBoundary.getDerivedStateFromError(
    new Error("render failed"),
  );
  const html = renderToStaticMarkup(boundary.render());
  assert.match(html, /<code>first<\/code>/);
  assert.match(html, /<code>second<\/code>/);
  assert.match(html, /<code>third<\/code>/);
  assert.match(html, /highlighted[^>]*data-line="2"/);
});

test("line navigation centers inside the preview without scrolling the conversation", () => {
  const scroller = {
    scrollTop: 100,
    clientHeight: 300,
    getBoundingClientRect: () => ({ top: 200 }),
  };
  const row = { getBoundingClientRect: () => ({ top: 600, height: 20 }) };
  centerSourceLine(scroller, row);
  assert.equal(scroller.scrollTop, 360);
});
