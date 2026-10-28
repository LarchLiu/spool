import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  MarkdownMessage,
  MarkdownPreview,
} from "../.test-build/markdown-message.js";
import { LocalFileContext } from "../.test-build/local-file-context.js";

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

test("local links become file actions, never URLs relative to the website", () => {
  const text =
    "[开发计划](/Users/k/code/muye/docs/development-plan.md:55) [source](src/main.tsx#L12C3) [file](file:///Users/k/code/muye/README.md) [external](https://example.com/plan)";
  const html = renderToStaticMarkup(
    createElement(
      LocalFileContext.Provider,
      { value: { openFile() {} } },
      createElement(MarkdownMessage, null, text),
    ),
  );
  assert.equal((html.match(/class="local-file-link"/g) || []).length, 3);
  assert.match(html, /<button[^>]*>开发计划<\/button>/);
  assert.doesNotMatch(html, /href="(?:\/Users|src\/|file:)/);
  assert.match(html, /href="https:\/\/example.com\/plan"/);
  assert.doesNotMatch(html, /disabled/);
});

test("local images never auto-load and encoded unsafe schemes stay inert", () => {
  const html = render(
    "![Local](/Users/k/code/muye/diagram.png) [bad](javascript%3Aalert%281%29) [badfile](file://remote-host/etc/passwd)",
  );
  assert.doesNotMatch(html, /<img|href="(?:javascript|file|\/Users)/);
  assert.match(html, /class="local-file-link"/);
});

test("external links keep their encoded query and path semantics", () => {
  const html = render("[Website](https://example.com/a%2Fb?q=a%26b)");
  assert.match(html, /href="https:\/\/example\.com\/a%2Fb\?q=a%26b"/);
});

test("plain text and newlines remain readable without Markdown syntax", () => {
  const html = render("First line\n第二行\n\nNext paragraph & text");
  assert.match(html, /<p>First line\n第二行<\/p>/);
  assert.match(html, /<p>Next paragraph &amp; text<\/p>/);
});

function preview(content) {
  return renderToStaticMarkup(createElement(MarkdownPreview, null, content));
}

test("Markdown previews parse full emphasis before visual truncation", () => {
  const title = "A long thought title ".repeat(30).trim();
  const html = preview(
    `**${title}** and *emphasis* with \`inline code\` and ~~old~~`,
  );
  assert.match(html, /^<span class="markdown markdown-preview">/);
  assert.ok(html.includes(`<strong>${title}</strong>`));
  assert.match(html, /<em>emphasis<\/em>/);
  assert.match(html, /<code>inline code<\/code>/);
  assert.match(html, /<del>old<\/del>/);
  assert.doesNotMatch(html, /\*\*|~~/);
});

test("Markdown previews flatten blocks, hard breaks, tables and code into inline content", () => {
  const html = preview(
    "# Heading\n\nParagraph  \nNext line\n\n> Quote\n\n- First\n  - Nested\n- Second\n\n| Name | Status |\n| --- | --- |\n| Spool | Ready |\n\n```js\nconst x = 1;\nconst y = 2;\n```\n\n---\n\nLast paragraph",
  );
  assert.match(html, /<strong>Heading <\/strong>/);
  assert.doesNotMatch(
    html,
    /<(?:div|p|h[1-6]|blockquote|ul|ol|li|table|thead|tbody|tr|th|td|pre|br|hr)\b/,
  );
  for (const text of [
    "Heading",
    "Paragraph",
    "Next line",
    "Quote",
    "First",
    "Nested",
    "Second",
    "Name",
    "Status",
    "Spool",
    "Ready",
    "const x = 1;",
    "const y = 2;",
    "Last paragraph",
  ])
    assert.ok(html.includes(text), text);
  const text = html
    .replace(/<[^>]*>/g, "")
    .replace(/\s+/g, " ")
    .trim();
  assert.equal(
    text,
    "Heading Paragraph Next line Quote First Nested Second Name Status Spool Ready const x = 1; const y = 2; Last paragraph",
  );
});

test("Markdown previews cannot create nested controls or load remote content", () => {
  const html = preview(
    "[Reference](https://example.com)\n\n![Diagram](https://example.com/track.png)\n\n- [x] Done\n- [ ] Next\n\n<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>",
  );
  assert.doesNotMatch(
    html,
    /<(?:a|input|button|img|script|div)\b|href=|src=|onerror=/,
  );
  for (const text of ["Reference", "Diagram", "Done", "Next"])
    assert.ok(html.includes(text), text);
});
