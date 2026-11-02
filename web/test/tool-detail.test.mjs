import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ToolDetailCache, mergeToolDetailPage } from "../.test-build/components/web/src/tool-detail.js";
import { activitiesFromParts, changeStats, ToolActivityList, ActivityDetail } from "../.test-build/components/web/src/tool-activity.js";

const t = (message, values = {}) => message.replace(/\{(\w+)\}/g, (_, key) => String(values[key] ?? key));
const page = (text, offset = 0, total = offset + text.length) => ({ ref: "tool", version: "v1", text, offset, next: offset + text.length, total, prefix: "hash" });

test("lazy tool summaries preserve labels, pairing, icons and change counts without bodies", () => {
  const input = { role: "assistant", kind: "tool_use", name: "apply_patch", text: "", tool: {
    ref: "tool", href: "/chats/chat/tool-detail?ref=tool", version: "v1", bytes: 900000, kind: "create", path: "src/a.go", changes: { additions: 12, deletions: 2 },
  } };
  const result = { role: "tool", kind: "tool_result", text: "", tool: { ...input.tool, ref: "output", changes: { additions: 20, deletions: 3 } } };
  const activities = activitiesFromParts([input, result], "codex", t);
  assert.equal(activities[0].label, "Created src/a.go");
  assert.equal(activities[0].result, result);
  assert.equal(changeStats(activities), "+20 -3");
  const html = renderToStaticMarkup(createElement(ToolActivityList, { parts: [input, result], backend: "codex", t, live: true }));
  assert.match(html, /Created src\/a.go/);
  assert.match(html, /lucide-file-plus/);
  assert.match(html, /\+20 -3/);
  assert.doesNotMatch(html, /tool-detail-block/);
  const detail = renderToStaticMarkup(createElement(ActivityDetail, { activity: activities[0], t }));
  assert.match(detail, /Command/); assert.match(detail, /Result/);
  assert.doesNotMatch(detail, /900000/);
});

test("incremental pages append once, reset replacements, and bound rendered source", () => {
  const first = page("abc");
  const appended = mergeToolDetailPage(first, page("def", 3));
  assert.equal(appended.text, "abcdef"); assert.equal(appended.next, 6);
  assert.deepEqual(mergeToolDetailPage(appended, { ...page("replace"), reset: true }), { ...page("replace"), reset: true });
  const full = page("x".repeat(32768));
  const large = mergeToolDetailPage(full, page("y".repeat(32768), 32768));
  assert.equal(large.text.length, 32768); assert.equal(large.next, 32768); assert.equal(large.total, 65536);
  assert.equal(mergeToolDetailPage(large, page("next", 65536)).text, "next");
});

test("detail pages use bounded LRU and invalidate on version/account changes", async () => {
  const cache = new ToolDetailCache(4096, 2);
  const calls = [];
  const source = async (path) => { calls.push(path); return page("hello"); };
  const signal = new AbortController().signal;
  const cursor = { offset: 5, prefix: "a+b" };
  await cache.load(source, "/detail?ref=one", "v1", cursor, signal);
  await cache.load(source, "/detail?ref=one", "v1", cursor, signal);
  assert.equal(calls.length, 1); assert.match(calls[0], /offset=5&prefix=a%2Bb/);
  await cache.load(source, "/detail?ref=one", "v2", cursor, signal);
  await cache.load(source, "/detail?ref=two", "v1", cursor, signal);
  await cache.load(source, "/detail?ref=one", "v1", cursor, signal);
  assert.equal(calls.length, 4);
  cache.clear(); await cache.load(source, "/detail?ref=one", "v1", cursor, signal);
  assert.equal(calls.length, 5);
});

test("closing a tool or clearing the account prevents inflight bodies entering cache", async () => {
  const cache = new ToolDetailCache();
  let resolve;
  const controller = new AbortController();
  const load = cache.load(() => new Promise((done) => { resolve = done; }), "/detail?ref=one", "v1", { offset: 0, prefix: "" }, controller.signal);
  controller.abort(); cache.clear(); resolve(page("sensitive")); await load;
  let called = false;
  await cache.load(async () => { called = true; return page("fresh"); }, "/detail?ref=one", "v1", { offset: 0, prefix: "" }, new AbortController().signal);
  assert.equal(called, true);
});
