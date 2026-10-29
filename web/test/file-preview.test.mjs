import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { readFile } from "node:fs/promises";
import {
  filePreviewKind,
  HtmlFilePreview,
} from "../.test-build/components/web/src/file-preview.js";

test("text HTML files support preview by extension or detected media type", () => {
  for (const name of ["index.html", "page.htm", "PAGE.HTML", "empty.HTM"]) {
    assert.equal(
      filePreviewKind({ name, kind: "text", media_type: "text/plain" }),
      "html",
    );
  }
  assert.equal(
    filePreviewKind({
      name: "page.txt",
      kind: "text",
      media_type: "text/html; charset=utf-8",
    }),
    "html",
  );
  for (const kind of ["image", "binary"]) {
    assert.equal(
      filePreviewKind({ name: "page.html", kind, media_type: "text/html" }),
      null,
    );
  }
});

test("Markdown keeps its renderer and other text remains source-only", () => {
  for (const name of ["README.md", "notes.mdown", "page.mdx", "NOTES.MD"]) {
    assert.equal(
      filePreviewKind({ name, kind: "text", media_type: "text/html" }),
      "markdown",
    );
  }
  for (const name of ["script.js", "notes.txt", "page.html.txt"]) {
    assert.equal(
      filePreviewKind({ name, kind: "text", media_type: "text/plain" }),
      null,
    );
  }
});

test("HTML is contained in an opaque sandbox with a policy before all file content", () => {
  const contents = `<!doctype html><html><head><style>h1 { color: red; }</style></head><body><h1>Preview</h1><script>parent.document.body.remove()</script><img src="/api/private"><iframe src="https://example.com"></iframe></body></html>`;
  const frame = HtmlFilePreview({ contents, title: "Preview page.html" });
  assert.equal(frame.type, "iframe");
  assert.equal(frame.props.sandbox, "");
  assert.equal(frame.props.referrerPolicy, "no-referrer");
  assert.equal(frame.props.src, undefined);
  assert.equal(frame.props.children, undefined);
  assert.equal(frame.props.title, "Preview page.html");
  const document = frame.props.srcDoc;
  assert.ok(document.endsWith(contents));
  const policy = document.match(
    /<meta http-equiv="Content-Security-Policy" content="([^"]+)">/,
  )[1];
  assert.ok(document.indexOf(policy) < document.indexOf(contents));
  for (const directive of [
    "default-src 'none'",
    "style-src 'unsafe-inline'",
    "img-src data:",
    "base-uri 'none'",
    "form-action 'none'",
  ])
    assert.ok(policy.includes(directive), directive);
  assert.doesNotMatch(policy, /https?:|blob:|'self'|script-src/);
  const html = renderToStaticMarkup(frame);
  assert.match(html, /^<iframe[^>]*sandbox=""/);
  assert.doesNotMatch(html, /<script|<style|<h1|<img/);
  assert.match(html, /&lt;h1&gt;Preview&lt;\/h1&gt;/);
});

test("empty HTML previews still have a sandbox, document and accessible title", () => {
  const html = renderToStaticMarkup(
    createElement(HtmlFilePreview, { contents: "", title: "Empty preview" }),
  );
  assert.match(html, /title="Empty preview"/);
  assert.match(html, /srcDoc="&lt;!doctype html&gt;/);
  assert.match(html, /sandbox=""/);
});

test("the viewer shares Source/Preview controls without losing source line navigation", async () => {
  const source = await readFile(
    new URL("../src/file-sidebar.tsx", import.meta.url),
    "utf8",
  );
  assert.match(source, /const previewKind = filePreviewKind\(file\)/);
  assert.match(
    source,
    /\[renderPreview, setRenderPreview\] = useState\(false\)/,
  );
  assert.match(source, /aria-pressed=\{!renderPreview\}/);
  assert.match(source, /aria-pressed=\{renderPreview\}/);
  assert.match(source, /onClick=\{\(\) => setRenderPreview\(true\)\}/);
  assert.match(source, /onClick=\{\(\) => setRenderPreview\(false\)\}/);
  assert.match(source, /previewKind === "html" \? \(\s*<HtmlFilePreview/);
  assert.match(source, /previewKind === "markdown" \? \(\s*<MarkdownMessage/);
  assert.match(source, /<FileSource[\s\S]*?line=\{target\.line\}/);
});
