import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";
import { takeDesktopNotification } from "../.test-build/runtime.js";

const chrome = await readFile(new URL("../../cmd/desktop/window_chrome.go", import.meta.url), "utf8");
const smoke = await readFile(new URL("../../cmd/desktop/notifications_smoke.go", import.meta.url), "utf8");
const preload = chrome.match(/const desktopWindowPreload = `([^`]+)`/)?.[1];
const observer = smoke.match(/const smokeNotificationObserverScript = `([^`]+)`/)?.[1];
assert.ok(preload && observer, "exercise the actual native preload and smoke observer");

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
  const context = vm.createContext({ window, Event, performance });
  vm.runInContext(preload, context);
  const received = [];
  window.addEventListener("spool:notification-changed", () => {
    received.push(takeDesktopNotification(window));
  });
  vm.runInContext(observer, context);
  const emit = (target) => {
    for (const listener of listeners.get("spool:notification-open") ?? []) listener(target);
  };
  return { window, performance, received, emit };
}

test("native smoke observes clicks after the frontend consumes the one-shot target", () => {
  const { window, performance, received, emit } = fixture();
  assert.equal(window.spoolSmokeNotification, null);
  // Direct window dispatch does not put a later capture listener ahead of the
  // already registered consumer on every WebView engine.
  let stale;
  window.addEventListener("spool:notification-changed", () => {
    stale = window.spoolNotificationTarget;
  }, true);
  for (const target of [
    { url: "/?inbox=user-sender123&recipient=user-receiver123", user: "user-receiver123" },
    { url: "/?inbox=group-12345678&recipient=user-receiver123", user: "user-receiver123" },
  ]) {
    emit(target);
    assert.equal(stale, undefined, "the old buffer observer misses this click");
    assert.equal("spoolNotificationTarget" in window, false);
    assert.deepEqual(received.at(-1), target, "the real frontend still consumes each target");
    assert.deepEqual(window.spoolSmokeNotification, target, "the smoke sees the native payload independently");
    assert.equal(window.spoolSmokeTimeOrigin, performance.timeOrigin);
  }
  assert.equal(received.length, 2);
});

test("a DOM notification alone cannot satisfy the native click smoke observer", () => {
  const { window, received } = fixture();
  window.dispatchEvent(new Event("spool:notification-changed"));
  assert.deepEqual(received, [null]);
  assert.equal(window.spoolSmokeNotification, null);
});
