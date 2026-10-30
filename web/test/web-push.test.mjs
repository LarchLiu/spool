import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";
import ts from "typescript";

const source = readFileSync(
  new URL("../src/web-push.ts", import.meta.url),
  "utf8",
);
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    target: ts.ScriptTarget.ES2022,
    module: ts.ModuleKind.CommonJS,
  },
}).outputText;
const keyBytes = Uint8Array.from({ length: 65 }, (_, index) => index + 1);
const publicKey = Buffer.from(keyBytes).toString("base64url");

function device({
  permission = "granted",
  existing = false,
  failSave = false,
} = {}) {
  const calls = [];
  let current;
  const subscription = {
    endpoint: "https://fcm.googleapis.com/fcm/send/device",
    options: { applicationServerKey: keyBytes.buffer },
    toJSON: () => ({
      endpoint: subscription.endpoint,
      expirationTime: null,
      keys: { auth: "auth", p256dh: "public" },
    }),
    unsubscribe: async () => {
      calls.push("unsubscribe");
      current = null;
      return true;
    },
  };
  current = existing ? subscription : null;
  const registration = {
    active: {
      state: "activated",
      addEventListener() {},
      removeEventListener() {},
    },
    pushManager: {
      getSubscription: async () => current,
      subscribe: async (options) => {
        calls.push(["subscribe", options]);
        current = subscription;
        return subscription;
      },
    },
    getNotifications: async () => [
      {
        close() {
          calls.push("close-notification");
        },
      },
    ],
  };
  const notification = {
    permission,
    requestPermission() {
      calls.push("permission");
      return Promise.resolve(permission);
    },
  };
  const window = {
    isSecureContext: true,
    Notification: notification,
    PushManager: {},
  };
  const navigator = {
    serviceWorker: {
      getRegistration: async () => registration,
      register: async () => registration,
    },
  };
  const api = async (path, body, signal) => {
    calls.push([path, body, signal]);
    if (path === "/push/config") return { public_key: publicKey };
    if (path === "/push/subscribe" && failSave) throw new Error("save failed");
    return { subscribed: true };
  };
  const module = { exports: {} };
  vm.runInNewContext(compiled, {
    module,
    exports: module.exports,
    require(name) {
      assert.equal(name, "./api");
      return { api };
    },
    window,
    navigator,
    Notification: notification,
    Uint8Array,
    URL,
    atob,
    setTimeout,
    clearTimeout,
  });
  return {
    push: module.exports,
    calls,
    subscription,
    window,
    navigator,
    notification,
    registration,
  };
}

test("push capability distinguishes insecure origins from unsupported browsers", () => {
  const d = device();
  assert.equal(d.push.pushSupport(), "supported");
  d.window.isSecureContext = false;
  assert.equal(d.push.pushSupport(), "insecure");
  d.window.isSecureContext = true;
  delete d.window.PushManager;
  assert.equal(d.push.pushSupport(), "unsupported");
});

test("enable requests permission in the click gesture, then saves only standard subscription fields", async () => {
  const d = device();
  const enabling = d.push.enablePush("zh-CN");
  assert.deepEqual(d.calls, ["permission"]);
  await enabling;
  const options = d.calls.find((call) => call[0] === "subscribe")[1];
  assert.equal(options.userVisibleOnly, true);
  assert.deepEqual([...options.applicationServerKey], [...keyBytes]);
  const body = d.calls.find((call) => call[0] === "/push/subscribe")[1];
  assert.equal(body.language, "zh-CN");
  assert.equal(body.subscription.endpoint, d.subscription.endpoint);
  assert.deepEqual(Object.keys(body.subscription).sort(), ["endpoint", "keys"]);
  assert.equal(d.calls.filter((call) => call[0] === "subscribe").length, 1);
});

test("denied permission never contacts the backend or creates a subscription", async () => {
  const d = device({ permission: "denied" });
  await assert.rejects(d.push.enablePush("en"), /Notifications are blocked/);
  assert.deepEqual(d.calls, ["permission"]);
});

test("existing subscriptions are reused and failed saves do not leave an unsaved opt-in", async () => {
  const d = device({ existing: true });
  await d.push.enablePush("ja");
  assert.equal(
    d.calls.some((call) => call[0] === "subscribe"),
    false,
  );
  const failed = device({ failSave: true });
  await assert.rejects(failed.push.enablePush("en"), /save failed/);
  assert.equal(failed.calls.at(-1), "unsubscribe");
});

test("normal page loads only refresh existing subscriptions and never request permission or enable a device", async () => {
  for (const existing of [true, false]) {
    const d = device({ existing });
    await d.push.syncExistingPush("ja");
    assert.equal(
      d.calls.some(
        (call) =>
          call === "permission" ||
          call[0] === "subscribe" ||
          call[0] === "/push/subscribe",
      ),
      false,
    );
    assert.equal(
      d.calls.filter((call) => call[0] === "/push/refresh").length,
      existing ? 1 : 0,
    );
  }
  const denied = device({ permission: "denied", existing: true });
  await denied.push.syncExistingPush("en");
  assert.deepEqual(denied.calls, []);
});

test("disable removes the server binding before unsubscribing; logout also closes displayed notifications", async () => {
  const d = device({ existing: true });
  await d.push.disablePush();
  assert.equal(d.calls[0][0], "/push/unsubscribe");
  assert.equal(d.calls[1], "unsubscribe");
  const logout = device({ existing: true });
  await logout.push.clearDevicePush();
  assert.deepEqual(logout.calls, ["unsubscribe", "close-notification"]);
});

test("test and status operate on this browser's subscription only", async () => {
  const d = device({ existing: true });
  assert.equal(await d.push.pushEnabled(), true);
  await d.push.testPush();
  assert.deepEqual(
    d.calls.map((call) => call[0]),
    ["/push/status", "/push/test"],
  );
  for (const call of d.calls)
    assert.equal(call[1].endpoint, d.subscription.endpoint);
});

test("notification deep links accept native chat IDs but not paths or script fragments", () => {
  const { push } = device();
  assert.equal(
    push.notificationChatFromURL("https://spool.test/?chat=chat_123-456"),
    "chat_123-456",
  );
  for (const id of [
    "short",
    "../api/me",
    "javascript:alert(1)",
    "-chat12345",
    "a".repeat(129),
  ]) {
    assert.equal(
      push.notificationChatFromURL(
        `https://spool.test/?chat=${encodeURIComponent(id)}`,
      ),
      "",
    );
  }
});

test("people notification links require a valid peer and bind navigation to the recipient account", () => {
  const { push } = device();
  for (const peer of ["sender-123456", "group-12345678", "online-room"]) {
    const target = push.notificationInboxFromURL(
      `https://spool.test/?inbox=${peer}&recipient=user-123456`,
    );
    assert.equal(target.peer, peer);
    assert.equal(target.user, "user-123456");
  }
  for (const query of [
    "inbox=online-room",
    "inbox=short&recipient=user-123456",
    "inbox=../api/me&recipient=user-123456",
    "inbox=user-123456&recipient=user-123456",
    "inbox=online-room&recipient=bad",
  ]) {
    assert.equal(
      push.notificationInboxFromURL("https://spool.test/?" + query),
      null,
    );
  }
});

function serviceWorker(windows = []) {
  const handlers = {};
  const shown = [],
    opened = [];
  const self = {
    location: { origin: "https://spool.test" },
    addEventListener(type, handler) {
      handlers[type] = handler;
    },
    registration: {
      async showNotification(title, options) {
        shown.push({ title, options });
      },
    },
    clients: {
      matchAll: async () => windows,
      openWindow: async (url) => opened.push(url),
    },
  };
  class MessageChannel {
    port1 = { onmessage: null, close() {} };
    port2 = {
      postMessage: (value) => this.port1.onmessage?.({ data: value }),
      close() {},
    };
  }
  vm.runInNewContext(
    readFileSync(new URL("../public/sw.js", import.meta.url), "utf8"),
    {
      self,
      URL,
      MessageChannel,
      setTimeout: (callback) => setTimeout(callback, 10),
      clearTimeout,
    },
  );
  async function dispatch(type, data) {
    let work;
    handlers[type]({
      ...data,
      waitUntil(promise) {
        work = promise;
      },
    });
    await work;
  }
  return { dispatch, shown, opened };
}

test("service worker renders push data, keeps navigation same-origin, and tolerates malformed payloads", async () => {
  const worker = serviceWorker();
  await worker.dispatch("push", {
    data: {
      json: () => ({
        title: "Spool · 任务成功",
        body: "任务已完成",
        url: "/?chat=12345678",
        tag: "task-1",
      }),
    },
  });
  assert.equal(worker.shown[0].title, "Spool · 任务成功");
  assert.equal(worker.shown[0].options.body, "任务已完成");
  assert.equal(
    worker.shown[0].options.data.url,
    "https://spool.test/?chat=12345678",
  );
  await worker.dispatch("push", {
    data: { json: () => ({ body: "status", url: "https://evil.test/" }) },
  });
  assert.equal(worker.shown[1].options.data.url, "https://spool.test/");
  await worker.dispatch("push", {
    data: {
      json() {
        throw new Error("bad JSON");
      },
    },
  });
  assert.equal(worker.shown[2].title, "Spool");
  assert.ok(worker.shown[2].options.body);
});

test("service worker displays status and sender/group titles with a bounded fallback", async () => {
  const worker = serviceWorker();
  for (const title of [
    "Spool · 需要审核",
    "Spool · 任务失败",
    "Spool · Alice 发来消息",
    "Spool · 群聊：产品组",
  ]) {
    await worker.dispatch("push", { data: { json: () => ({ title }) } });
    assert.equal(worker.shown.at(-1).title, title);
  }
  for (const title of [undefined, null, 123, "", " \r\n "]) {
    await worker.dispatch("push", { data: { json: () => ({ title }) } });
    assert.equal(worker.shown.at(-1).title, "Spool");
  }
  await worker.dispatch("push", {
    data: { json: () => ({ title: "😀".repeat(170) }) },
  });
  assert.equal(worker.shown.at(-1).title, "😀".repeat(160));
});

test("notification click focuses an existing app without reload or opens the target chat", async () => {
  const calls = [];
  const existing = serviceWorker([
    {
      url: "https://evil.test/",
      focus() {
        throw new Error("wrong origin");
      },
    },
    {
      url: "https://spool.test/desktop.html",
      focus() {
        throw new Error("wrong app");
      },
    },
    {
      url: "https://spool.test/",
      postMessage(data, ports) {
        calls.push(data.chatID);
        ports[0].postMessage({ handled: true });
      },
      async focus() {
        calls.push("focus");
      },
    },
  ]);
  const notification = {
    close() {
      calls.push("close");
    },
    data: { url: "https://spool.test/?chat=12345678" },
  };
  await existing.dispatch("notificationclick", { notification });
  assert.deepEqual(calls, ["close", "focus", "12345678"]);
  assert.deepEqual(existing.opened, []);
  const fresh = serviceWorker();
  await fresh.dispatch("notificationclick", { notification });
  assert.deepEqual(fresh.opened, ["https://spool.test/?chat=12345678"]);
  await fresh.dispatch("notificationclick", {
    notification: { close() {}, data: { url: "javascript:alert(1)" } },
  });
  assert.equal(fresh.opened[1], "https://spool.test/");
});

test("notification click navigates an old or offline app that cannot acknowledge the chat message", async () => {
  const navigated = [];
  const worker = serviceWorker([
    {
      url: "https://spool.test/",
      async focus() {},
      postMessage() {},
      async navigate(url) {
        navigated.push(url);
      },
    },
  ]);
  await worker.dispatch("notificationclick", {
    notification: {
      close() {},
      data: { url: "https://spool.test/?chat=12345678" },
    },
  });
  assert.deepEqual(navigated, ["https://spool.test/?chat=12345678"]);
});

test("private and group notification clicks open the inbox, preserving the intended recipient", async () => {
  for (const peer of ["sender-123456", "group-12345678", "online-room"]) {
    const calls = [];
    const worker = serviceWorker([
      {
        url: "https://spool.test/",
        async focus() {},
        postMessage(message, ports) {
          calls.push([message.type, message.peerID, message.userID]);
          ports[0].postMessage({ handled: true });
        },
      },
    ]);
    const url = `https://spool.test/?inbox=${peer}&recipient=user-123456`;
    await worker.dispatch("notificationclick", {
      notification: { close() {}, data: { url } },
    });
    assert.deepEqual(calls, [["OPEN_INBOX", peer, "user-123456"]]);
    assert.deepEqual(worker.opened, []);
    const fresh = serviceWorker();
    await fresh.dispatch("notificationclick", {
      notification: { close() {}, data: { url } },
    });
    assert.deepEqual(fresh.opened, [url]);
  }
});
