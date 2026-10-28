import { test } from "node:test";
import assert from "node:assert/strict";
import {
  fetchOlderHistory,
  restoreHistoryScroll,
} from "../.test-build/history-loading.js";

const snapshot = (before = "older/cursor", has_more = true) => ({
  id: "chat-1",
  history: { before, has_more },
});
const yieldNow = async () => {};

test("the browser scheduler cancels a pending animation-frame yield on navigation", async (t) => {
  const previousFrame = globalThis.requestAnimationFrame;
  const previousCancel = globalThis.cancelAnimationFrame;
  t.after(() => {
    if (previousFrame) globalThis.requestAnimationFrame = previousFrame;
    else delete globalThis.requestAnimationFrame;
    if (previousCancel) globalThis.cancelAnimationFrame = previousCancel;
    else delete globalThis.cancelAnimationFrame;
  });
  let canceled;
  globalThis.requestAnimationFrame = () => 42;
  globalThis.cancelAnimationFrame = (frame) => {
    canceled = frame;
  };
  const controller = new AbortController();
  let requests = 0;
  const pending = fetchOlderHistory(
    snapshot(),
    async () => {
      requests++;
    },
    controller.signal,
  );
  controller.abort();
  assert.equal(await pending, null);
  assert.equal(canceled, 42);
  assert.equal(requests, 0);
});

test("background history requests wait for first paint and page sequentially", async () => {
  const controller = new AbortController();
  const requests = [];
  const yields = [];
  const yieldToBrowser = () => new Promise((resolve) => yields.push(resolve));
  const fetchPage = async (path, signal) => {
    assert.equal(signal, controller.signal);
    requests.push(path);
    return requests.length === 1
      ? snapshot("oldest")
      : snapshot(undefined, false);
  };
  const first = fetchOlderHistory(
    snapshot(),
    fetchPage,
    controller.signal,
    yieldToBrowser,
  );
  assert.deepEqual(requests, []);
  yields.shift()();
  const firstPage = await first;
  assert.deepEqual(requests, ["/chats/chat-1/history?before=older%2Fcursor"]);
  const second = fetchOlderHistory(
    firstPage,
    fetchPage,
    controller.signal,
    yieldToBrowser,
  );
  assert.equal(requests.length, 1);
  yields.shift()();
  const lastPage = await second;
  assert.equal(requests.length, 2);
  assert.equal(lastPage.history.has_more, false);
  assert.equal(
    await fetchOlderHistory(lastPage, fetchPage, controller.signal, yieldNow),
    null,
  );
  assert.equal(requests.length, 2);
});

test("switching chats cancels scheduled and in-flight history loads", async () => {
  const controller = new AbortController();
  let release;
  const yieldToBrowser = () =>
    new Promise((resolve) => {
      release = resolve;
    });
  let requests = 0;
  const pending = fetchOlderHistory(
    snapshot(),
    async () => {
      requests++;
    },
    controller.signal,
    yieldToBrowser,
  );
  controller.abort();
  release();
  assert.equal(await pending, null);
  assert.equal(requests, 0);
  const active = new AbortController();
  let finish;
  const loaded = fetchOlderHistory(
    snapshot(),
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
    active.signal,
    yieldNow,
  );
  await Promise.resolve();
  active.abort();
  finish(snapshot(undefined, false));
  assert.equal(await loaded, null);
});

test("invalid cursors stop background loading instead of looping or mixing chats", async () => {
  const signal = new AbortController().signal;
  for (const page of [
    snapshot(),
    snapshot(""),
    { ...snapshot("next"), id: "other-chat" },
  ]) {
    await assert.rejects(
      fetchOlderHistory(snapshot(), async () => page, signal, yieldNow),
      /Invalid history cursor/,
    );
  }
  await assert.rejects(
    fetchOlderHistory(
      snapshot(),
      async () => {
        throw new Error("offline");
      },
      signal,
      yieldNow,
    ),
    /offline/,
  );
  const retry = await fetchOlderHistory(
    snapshot(),
    async () => snapshot(undefined, false),
    signal,
    yieldNow,
  );
  assert.equal(retry.history.has_more, false);
});

test("unavailable history does not start background requests", async () => {
  let requests = 0;
  const chat = {
    ...snapshot(),
    history: { ...snapshot().history, error: "unavailable" },
  };
  assert.equal(
    await fetchOlderHistory(
      chat,
      async () => {
        requests++;
      },
      new AbortController().signal,
      yieldNow,
    ),
    null,
  );
  assert.equal(requests, 0);
});

test("opening at the bottom and prepending pages keep the intended viewport", () => {
  const element = { scrollTop: 0, scrollHeight: 1200 };
  restoreHistoryScroll(element, true, null);
  assert.equal(element.scrollTop, 1200);
  const anchor = { id: "chat-1", height: 1200, top: 150 };
  element.scrollHeight = 3400;
  restoreHistoryScroll(element, false, anchor);
  assert.equal(element.scrollTop, 2350);
  element.scrollHeight = 5000;
  restoreHistoryScroll(element, true, anchor);
  assert.equal(element.scrollTop, 5000);
  restoreHistoryScroll(element, false, null);
  assert.equal(element.scrollTop, 5000);
});
