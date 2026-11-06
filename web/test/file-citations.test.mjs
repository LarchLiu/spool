import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { parseFragment } from "parse5";
import {
  MarkdownMessage,
  MarkdownPreview,
} from "../.test-build/markdown-message.js";
import { LocalFileContext } from "../.test-build/local-file-context.js";
import { localFileTarget } from "../.test-build/local-file-links.js";

const cases = JSON.parse(
  readFileSync(
    new URL(
      "../../android/app/src/test/resources/file-citations.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
for (const item of cases)
  test(`Codex file citation: ${item.id}`, () => {
    const html = renderToStaticMarkup(
      createElement(
        LocalFileContext.Provider,
        { value: { openFile() {} } },
        createElement(MarkdownMessage, null, item.source),
      ),
    );
    const links = [];
    const text = (node) =>
      node.nodeName === "#text"
        ? node.value
        : (node.childNodes || []).map(text).join("");
    const visit = (node) => {
      const attrs = Object.fromEntries(
        (node.attrs || []).map((attr) => [attr.name, attr.value]),
      );
      if (attrs.class === "local-file-link") {
        links.push({
          name: text(node),
          path: localFileTarget(attrs.title)?.path,
        });
        assert.equal(attrs.disabled, undefined);
      }
      for (const child of node.childNodes || []) visit(child);
    };
    visit(parseFragment(html));
    assert.deepEqual(
      links,
      item.links.map((link) => ({
        ...link,
        path: localFileTarget(link.path).path,
      })),
    );
    assert.doesNotMatch(html, /<img|<iframe|href="(?:javascript|file):/);
    const preview = renderToStaticMarkup(
      createElement(MarkdownPreview, null, item.source),
    );
    assert.doesNotMatch(preview, /<(?:a|button)\b/);
    if (item.links.length) {
      assert.doesNotMatch(html, /:codex-file-citation|purpose=/);
      assert.doesNotMatch(preview, /:codex-file-citation|purpose=/);
    }
  });
