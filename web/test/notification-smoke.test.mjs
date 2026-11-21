import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";
import {
  takeDesktopNotification,
  takeDesktopSessionNotifications,
  subscribeDesktopSessionNotifications,
  desktopViewedChat,
  subscribeDesktopSessionView,
} from "../.test-build/runtime.js";

const chrome = await readFile(new URL("../../cmd/desktop/window_chrome.go", import.meta.url), "utf8");
const smoke = await readFile(new URL("../../cmd/desktop/notifications_smoke.go", import.meta.url), "utf8");
const preload = chrome.match(/const desktopWindowPreload = `([^`]+)`/)?.[1];
const observer = smoke.match(/const smokeNotificationObserverScript = `([^`]+)`/)?.[1];
const checks = smoke.match(/func waitSmokeNotification[\s\S]*?fmt\.Sprintf\(`([^`]+)`/)?.[1];
assert.ok(preload && observer && checks, "exercise the actual native preload, smoke observer and assertions");

function fixture() {
  const window = new EventTarget();
  const listeners = new Map();
  window.mygo = {
    platform: "linux",
    on(name, listener) {
      const callbacks = listeners.get(name) ?? [];
      callbacks.push(listener);
      listeners.set(name, callbacks);
    },
  };
  const performance = { timeOrigin: 12345 };
  const document = {
    querySelector(selector) {
      assert.equal(selector, 'input[name="password"]');
      return {};
    },
  };
  const context = vm.createContext({ window, Event, performance, document });
  vm.runInContext(preload, context);
  const received = [];
  window.addEventListener("spool:notification-changed", () => {
    received.push(takeDesktopNotification(window));
  });
  vm.runInContext(observer, context);
  const emitNative = (name, value) => {
    for (const listener of listeners.get(name) ?? []) listener(value);
  };
  const emit = (target) => emitNative("spool:notification-open", target);
  const check = (target) => {
    const args = [target.url, target.user];
    const script = checks.replace(/%q/g, () => JSON.stringify(args.shift()));
    return vm.runInContext(`(function () { ${script} })()`, context);
  };
  return { window, performance, document, context, received, emit, emitNative, check };
}

test("native session notifications buffer until login and deliver each status without opening a chat", () => {
  const { window, emitNative, received } = fixture();
  const pending = ["completed", "failed", "approval"].map((kind, i) => ({
    chat: `chat-1234567${i}`, user: "user-12345678", kind,
  }));
  emitNative("spool:session-notification", pending[0]);
  const seen = [];
  const off = subscribeDesktopSessionNotifications(() => seen.push(...takeDesktopSessionNotifications(window)), window);
  seen.push(...takeDesktopSessionNotifications(window));
  for (const notification of pending.slice(1)) emitNative("spool:session-notification", notification);
  assert.deepEqual(seen, pending);
  assert.deepEqual(takeDesktopSessionNotifications(window), []);
  assert.deepEqual(received, [], "showing notifications must not navigate");
  off();
  emitNative("spool:session-notification", pending[0]);
  assert.equal(seen.length, 3);
  assert.deepEqual(takeDesktopSessionNotifications(window), pending.slice(0, 1));
});

test("session notification buffers reject malformed data and browser surfaces", () => {
  const valid = { chat: "chat-12345678", user: "user-12345678", kind: "completed" };
  const invalid = [null, {}, { ...valid, chat: "short" }, { ...valid, user: "bad" }, { ...valid, kind: "test" }, { ...valid, kind: "direct_message" }];
  const native = { spoolDesktop: true, spoolSessionNotifications: [...invalid, valid] };
  assert.deepEqual(takeDesktopSessionNotifications(native), [valid]);
  assert.equal("spoolSessionNotifications" in native, false);
  assert.deepEqual(takeDesktopSessionNotifications({ spoolSessionNotifications: [valid] }), []);
  assert.deepEqual(takeDesktopSessionNotifications({ spoolDesktop: true, spoolSessionNotifications: {} }), []);
  subscribeDesktopSessionNotifications(() => assert.fail("browser subscribed"), {})();
});

test("native session views retain the current read state across subscriptions and validate payloads", () => {
  const { window, emitNative } = fixture();
  emitNative("spool:session-view", "chat-12345678");
  assert.equal(desktopViewedChat(window), "chat-12345678");
  const seen = [];
  const off = subscribeDesktopSessionView(() => seen.push(desktopViewedChat(window)), window);
  for (const chat of ["", "chat-87654321", "short", null, {}, "../api/me"])
    emitNative("spool:session-view", chat);
  assert.deepEqual(seen, ["", "chat-87654321", "", "", "", ""]);
  off();
  emitNative("spool:session-view", "chat-12345678");
  assert.equal(seen.length, 6);
  assert.equal(desktopViewedChat({ spoolViewedChat: "chat-12345678" }), "");
  subscribeDesktopSessionView(() => assert.fail("browser subscribed"), {})();
});

test("native smoke observes clicks after the frontend consumes the one-shot target", () => {
  const { window, document, received, emit, check } = fixture();
  assert.equal(window.spoolSmokeNotification, null);
  // An observer registered after the consumer sees an already consumed buffer.
  // Keep both in the same phase so this ordering holds across event engines.
  let stale;
  window.addEventListener("spool:notification-changed", () => {
    stale = window.spoolNotificationTarget;
  });
  for (const target of [
    { url: "/?inbox=user-sender123&recipient=user-receiver123", user: "user-receiver123" },
    { url: "/?inbox=group-12345678&recipient=user-receiver123", user: "user-receiver123" },
  ]) {
    emit(target);
    assert.equal(stale, undefined, "the old buffer observer misses this click");
    assert.equal("spoolNotificationTarget" in window, false);
    assert.deepEqual(received.at(-1), target, "the real frontend still consumes each target");
    assert.deepEqual(window.spoolSmokeNotification, target, "the smoke sees the native payload independently");
    assert.equal(window.spoolSmokeDocument, document);
    assert.equal(check(target).ready, true);
  }
  assert.equal(received.length, 2);
});

test("a DOM notification alone cannot satisfy the native click smoke observer", () => {
  const { window, received } = fixture();
  window.dispatchEvent(new Event("spool:notification-changed"));
  assert.deepEqual(received, [null]);
  assert.equal(window.spoolSmokeNotification, null);
});

test("same-page notification checks use document identity, not a floating point clock", () => {
  const { performance, document, context, emit, check } = fixture();
  const target = { url: "/?chat=chat-12345678", user: "user-12345678" };
  emit(target);
  assert.equal(check(target).ready, true);
  // WebKit can expose tiny timeOrigin variations within the same document.
  performance.timeOrigin += 0.001;
  assert.equal(check(target).ready, true);
  context.document = { ...document };
  const result = check(target);
  assert.equal(result.checks.target, true);
  assert.equal(result.checks.consumed, true);
  assert.equal(result.checks.same_page, false, "a replaced document must still fail the smoke");
  assert.equal(result.ready, false);
});
