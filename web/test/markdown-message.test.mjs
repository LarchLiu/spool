import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MarkdownMessage } from "../.test-build/markdown-message.js";

function render(content) {
  return renderToStaticMarkup(createElement(MarkdownMessage, null, content));
}

test("chat Markdown renders headings, emphasis, quotes, lists and inline code", () => {
  const html = render(
    "## Heading\n\n**Bold** and *italic* with `inline code`.\n\n> A quote\n\n1. First\n2. Second\n\n- Item",
  );
  for (const part of [
    "<h2>Heading</h2>",
    "<strong>Bold</strong>",
    "<em>italic</em>",
    "<code>inline code</code>",
    "<blockquote>",
    "<ol>",
    "<li>First</li>",
    "<ul>",
  ])
    assert.ok(html.includes(part), part);
});

test("GFM tables, task lists, strikethrough and autolinks are supported", () => {
  const html = render(
    "| Name | Status |\n| --- | --- |\n| Spool | Ready |\n\n- [x] Done\n- [ ] Next\n\n~~Old~~ https://example.com",
  );
  assert.match(html, /class="markdown-table"><table>/);
  assert.match(html, /<th>Name<\/th>/);
  assert.match(html, /<td>Ready<\/td>/);
  assert.match(html, /type="checkbox"[^>]*checked=""/);
  assert.match(html, /class="task-list-item"/);
  assert.match(html, /<del>Old<\/del>/);
  assert.match(html, /href="https:\/\/example.com"/);
  assert.match(html, /target="_blank" rel="noopener noreferrer"/);
});

test("fenced code preserves whitespace and escapes HTML, including partial streamed fences", () => {
  const partial = render("```html\n<div>\n  hello");
  assert.match(partial, /<pre><code class="language-html">/);
  assert.match(partial, /&lt;div&gt;\n  hello/);
  assert.doesNotMatch(partial, /<div>\n  hello/);
  const complete = render(
    "```html\n<div>\n  hello\n</div>\n```\n\n**Finished**",
  );
  assert.match(complete, /&lt;\/div&gt;/);
  assert.match(complete, /<strong>Finished<\/strong>/);
});

test("raw HTML and unsafe link protocols cannot create executable content", () => {
  const html = render(
    "<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[unsafe](javascript:alert%281%29)\n\n[encoded](jav&#x61;script:alert%281%29)\n\n[inline](data:text/html,test)\n\n[safe](https://example.com)",
  );
  assert.doesNotMatch(
    html,
    /<script|<img|onerror=|href="(?:javascript|data):/i,
  );
  assert.match(html, /<span>unsafe<\/span>/);
  assert.match(html, /<span>encoded<\/span>/);
  assert.match(html, /<span>inline<\/span>/);
  assert.match(html, /href="https:\/\/example.com"/);
});

test("Markdown images are safe links, not automatic remote requests", () => {
  const html = render(
    "![Diagram](https://example.com/track.png)\n\n![Blocked](data:image/svg+xml,test)",
  );
  assert.doesNotMatch(html, /<img|<link|src=|href="data:/);
  assert.match(
    html,
    /href="https:\/\/example.com\/track.png"[^>]*>Diagram<\/a>/,
  );
  assert.match(html, /<span>Blocked<\/span>/);
});

test("plain text and newlines remain readable without Markdown syntax", () => {
  const html = render("First line\n第二行\n\nNext paragraph & text");
  assert.match(html, /<p>First line\n第二行<\/p>/);
  assert.match(html, /<p>Next paragraph &amp; text<\/p>/);
});
