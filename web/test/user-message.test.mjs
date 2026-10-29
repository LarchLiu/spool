import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  observeUserMessageOverflow,
  UserMessage,
} from "../.test-build/components/web/src/user-message.js";

function overflowFixture(t, height = 40) {
  const previousStyle = globalThis.getComputedStyle;
  const previousObserver = globalThis.ResizeObserver;
  const observed = [];
  let resize;
  let disconnected = false;
  globalThis.getComputedStyle = () => ({ lineHeight: "24.5px" });
  globalThis.ResizeObserver = class {
    constructor(callback) {
      resize = callback;
    }
    observe(element) {
      observed.push(element);
    }
    disconnect() {
      disconnected = true;
    }
  };
  t.after(() => {
    if (previousStyle) globalThis.getComputedStyle = previousStyle;
    else delete globalThis.getComputedStyle;
    if (previousObserver) globalThis.ResizeObserver = previousObserver;
    else delete globalThis.ResizeObserver;
  });
  const markdown = {};
  const content = { scrollHeight: height, firstElementChild: markdown };
  const changes = [];
  const cleanup = observeUserMessageOverflow(content, (value) =>
    changes.push(value),
  );
  return {
    content,
    markdown,
    changes,
    observed,
    cleanup,
    resize: () => resize(),
    disconnected: () => disconnected,
  };
}

test("user messages preserve full Markdown and start collapsed without a premature toggle", () => {
  const text = "**Keep formatting**\n\n" + "Message line\n".repeat(30);
  const html = renderToStaticMarkup(
    createElement(UserMessage, { text, t: (key) => key }),
  );
  assert.match(html, /class="user-bubble"/);
  assert.match(html, /class="user-bubble-content"[^>]*data-expanded="false"/);
  assert.match(html, /<strong>Keep formatting<\/strong>/);
  assert.equal((html.match(/Message line/g) || []).length, 30);
  assert.doesNotMatch(html, /user-message-toggle|Show more|Show less/);
});

test("only messages taller than eight lines overflow, allowing for pixel rounding", async (t) => {
  for (const [height, expected] of [
    [24, false],
    [196, false],
    [197, false],
    [198, true],
    [900, true],
  ]) {
    await t.test(`${height}px`, (t) => {
      const f = overflowFixture(t, height);
      assert.deepEqual(f.changes, [expected]);
      f.cleanup();
    });
  }
});

test("overflow follows wrapping and Markdown height changes and disconnects on cleanup", (t) => {
  const f = overflowFixture(t);
  assert.deepEqual(f.observed, [f.content, f.markdown]);
  f.content.scrollHeight = 800;
  f.resize();
  f.content.scrollHeight = 100;
  f.resize();
  assert.deepEqual(f.changes, [false, true, false]);
  f.cleanup();
  assert.equal(f.disconnected(), true);
});

test("only user text is collapsible; attachments and copy retain the complete message", async () => {
  const main = await readFile(
    new URL("../src/main.tsx", import.meta.url),
    "utf8",
  );
  const source = await readFile(
    new URL("../src/user-message.tsx", import.meta.url),
    "utf8",
  );
  const css = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  assert.match(main, /userText && <UserMessage text=\{userText\} t=\{t\}/);
  assert.match(main, /<MessageFooter\s+role="user"\s+text=\{userText\}/);
  assert.match(source, /aria-controls=\{id\}/);
  assert.match(source, /aria-expanded=\{expanded\}/);
  assert.match(source, /setExpanded\(\(value\) => !value\)/);
  assert.match(source, /t\(expanded \? "Show less" : "Show more"\)/);
  assert.match(
    css,
    /\.user-bubble-content \{\s*max-height: 8lh;\s*overflow: hidden;/,
  );
  assert.match(
    css,
    /\.user-bubble-content\[data-expanded="true"\] \{\s*max-height: none;/,
  );
  assert.match(css, /\.user-message-toggle \{[^}]*margin: 8px 0 0;/);
});
