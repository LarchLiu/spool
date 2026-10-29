import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  copyMessageText,
  MessageFooter,
  messageTime,
} from "../.test-build/components/web/src/message-footer.js";
import { TurnResponse } from "../.test-build/components/web/src/tool-activity.js";
import { turnPresentation } from "../.test-build/components/web/src/turn-presentation.js";

const t = (key) => key;
const timestamp = Date.parse("2026-10-29T02:34:56Z");
const renderFooter = (
  role,
  text = "**Original**\n\nMessage",
  time = timestamp,
) =>
  renderToStaticMarkup(
    createElement(MessageFooter, {
      role,
      text,
      timestamp: time,
      locale: "en",
      t,
    }),
  );

test("user footers put time before copy, while agent footers put copy before time", () => {
  const user = renderFooter("user");
  assert.match(user, /class="message-footer message-footer-user"/);
  assert.ok(user.indexOf("<time") < user.indexOf("<button"));
  const assistant = renderFooter("assistant");
  assert.match(assistant, /class="message-footer message-footer-assistant"/);
  assert.ok(assistant.indexOf("<button") < assistant.indexOf("<time"));
  for (const html of [user, assistant]) {
    assert.match(html, /dateTime="2026-10-29T02:34:56.000Z"/);
    assert.match(html, /<time[^>]*title="[^"]+"/);
    assert.match(html, /type="button"[^>]*aria-label="Copy message"/);
    assert.match(html, /lucide-copy/);
    assert.match(html, /role="status"/);
    assert.doesNotMatch(html, /\*\*Original\*\*/);
  }
});

test("message times follow the viewer locale and omit missing or invalid timestamps", () => {
  for (const locale of ["en", "zh-CN", "ja"]) {
    const time = messageTime(timestamp, locale);
    assert.equal(time.dateTime, new Date(timestamp).toISOString());
    assert.equal(
      time.label,
      new Intl.DateTimeFormat(locale, {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      }).format(timestamp),
    );
  }
  for (const time of [undefined, 0, -1, NaN, Infinity, 1e20]) {
    assert.equal(messageTime(time, "en"), null);
    assert.doesNotMatch(renderFooter("assistant", "Reply", time || 0), /<time/);
  }
});

test("attachment-only messages disable copy without losing their send time", () => {
  const html = renderFooter("user", "");
  assert.match(html, /<time/);
  assert.match(html, /<button[^>]*disabled=""/);
});

test("agent footers use completion time and copy only the final answer, not hidden activity", async () => {
  const parts = [
    { role: "assistant", kind: "text", phase: "commentary", text: "Progress" },
    { role: "assistant", kind: "thinking", text: "Private reasoning" },
    { role: "assistant", kind: "tool_use", name: "Read", text: "tool input" },
    {
      role: "assistant",
      kind: "text",
      phase: "final_answer",
      text: "**Done**\n\n```js\n  code();\n```",
    },
  ];
  const html = renderToStaticMarkup(
    createElement(TurnResponse, {
      parts,
      t,
      running: false,
      summary: "Worked",
      timestamp,
      locale: "en",
    }),
  );
  assert.match(html, /class="message-footer message-footer-assistant"/);
  assert.match(html, /dateTime="2026-10-29T02:34:56.000Z"/);
  assert.ok(
    html.indexOf("<strong>Done</strong>") < html.indexOf("message-footer"),
  );
  const copies = [];
  await copyMessageText(
    turnPresentation(parts, false).answer,
    { writeText: async (text) => copies.push(text) },
    {},
  );
  assert.deepEqual(copies, [parts.at(-1).text]);
  const working = renderToStaticMarkup(
    createElement(TurnResponse, {
      parts: parts.slice(0, 3),
      t,
      running: true,
      summary: "Working",
    }),
  );
  assert.doesNotMatch(working, /message-footer|Copy message/);
});

function legacyClipboard(copied = true) {
  const events = [];
  const range = {};
  const textarea = {
    style: {},
    focus: (options) => events.push(["focus textarea", options]),
    select: () => events.push("select"),
    remove: () => events.push("remove"),
  };
  const doc = {
    activeElement: {
      focus: (options) => events.push(["restore focus", options]),
    },
    getSelection: () => ({
      rangeCount: 1,
      getRangeAt: () => ({ cloneRange: () => range }),
      removeAllRanges: () => events.push("clear selection"),
      addRange: (value) => events.push(["restore selection", value]),
    }),
    createElement: (tag) => {
      assert.equal(tag, "textarea");
      return textarea;
    },
    body: { append: (value) => assert.equal(value, textarea) },
    execCommand: (command) => {
      assert.equal(command, "copy");
      events.push("copy");
      return copied;
    },
  };
  return { events, range, textarea, doc };
}

test("clipboard API copies the exact Markdown without touching the document", async () => {
  const text = "**Hello**\n\n```html\n  <div>original</div>\n```\n";
  const copies = [];
  await copyMessageText(
    text,
    { writeText: async (value) => copies.push(value) },
    {},
  );
  assert.deepEqual(copies, [text]);
});

test("HTTP/webview fallback restores focus and selected text after copying", async () => {
  for (const clipboard of [
    {},
    {
      writeText: async () => {
        throw new Error("Not allowed");
      },
    },
  ]) {
    const f = legacyClipboard();
    await copyMessageText("  exact\ntext\n", clipboard, f.doc);
    assert.equal(f.textarea.value, "  exact\ntext\n");
    assert.equal(f.textarea.readOnly, true);
    assert.match(f.textarea.style.cssText, /position:fixed/);
    assert.deepEqual(f.events, [
      ["focus textarea", { preventScroll: true }],
      "select",
      "copy",
      "remove",
      ["restore focus", { preventScroll: true }],
      "clear selection",
      ["restore selection", f.range],
    ]);
  }
});

test("failed copies propagate failure and still clean up the fallback", async () => {
  const f = legacyClipboard(false);
  await assert.rejects(
    copyMessageText("text", {}, f.doc),
    /Clipboard copy failed/,
  );
  assert.ok(f.events.includes("remove"));
  assert.ok(f.events.some((event) => event[0] === "restore focus"));
  assert.ok(f.events.some((event) => event[0] === "restore selection"));
});

test("chat wiring uses send/completion timestamps and role-specific footer alignment", async () => {
  const source = await readFile(
    new URL("../src/main.tsx", import.meta.url),
    "utf8",
  );
  assert.match(
    source,
    /<MessageFooter\s+role="user"\s+text=\{userText\}\s+timestamp=\{turn.started_at\}/,
  );
  assert.match(
    source,
    /<TurnResponse[\s\S]*?timestamp=\{turn.finished_at\}\s+locale=\{locale\}/,
  );
  const css = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  assert.match(
    css,
    /\.message-footer-user \{[^}]*align-self: flex-end;[^}]*justify-content: flex-end;/,
  );
  assert.match(
    css,
    /\.message-footer-assistant \{[^}]*justify-content: flex-start;/,
  );
});
