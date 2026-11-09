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
  failStatus = false,
  failRefresh = false,
  statusResponse = { subscribed: true },
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
    if (path === "/push/status" && failStatus) throw new Error("status failed");
    if (path === "/push/status") return statusResponse;
    if (path === "/push/refresh" && failRefresh)
      throw new Error("refresh failed");
    return { subscribed: true };
  };
  const module = { exports: {} };
  vm.runInNewContext(compiled, {
    module,
    exports: module.exports,
    require(name) {
      if (name === "./app-path") return { appURL: (path) => path };
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

test("refresh and status failures propagate so settings can distinguish an unknown state from disabled", async () => {
  const status = device({ existing: true, failStatus: true });
  await assert.rejects(status.push.pushEnabled(), /status failed/);
  const refresh = device({ existing: true, failRefresh: true });
  await assert.rejects(refresh.push.syncExistingPush("en"), /refresh failed/);
  for (const statusResponse of [{}, null, { subscribed: "false" }]) {
    const invalid = device({ existing: true, statusResponse });
    await assert.rejects(
      invalid.push.pushEnabled(),
      /Could not check notification status/,
    );
  }
});

test("closing settings can abort both subscription recovery and the status request", async () => {
  const d = device({ existing: true });
  const controller = new AbortController();
  await d.push.syncExistingPush("en", controller.signal);
  await d.push.pushEnabled(controller.signal);
  assert.equal(d.calls.length, 2);
  for (const call of d.calls) assert.equal(call[2], controller.signal);
  controller.abort();
  await d.push.syncExistingPush("en", controller.signal);
  assert.equal(d.calls.length, 2);
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

function serviceWorker(windows = [], base = "/", environment = {}) {
  const handlers = {};
  const shown = [],
    opened = [];
  const self = {
    location: { origin: "https://spool.test" },
    addEventListener(type, handler) {
      handlers[type] = handler;
    },
    registration: {
      scope: "https://spool.test" + base,
      async showNotification(title, options) {
        shown.push({ title, options });
      },
    },
    clients: {
      claim: async () => {},
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
      ...environment,
    },
  );
  async function dispatch(type, data) {
    let work;
    handlers[type]({
      ...data,
      waitUntil(promise) {
        work = promise;
      },
      respondWith(promise) {
        work = promise;
      },
    });
    return await work;
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

test("relay workers isolate cache lifecycle, offline responses and notification targets by server path", async () => {
  const first = `/web/${"a".repeat(52)}/`;
  const second = `/web/${"b".repeat(52)}/`;
  const entries = new Map();
  const fetched = [];
  const offline = { offline: true };
  const caches = {
    async open(key) {
      if (!entries.has(key)) entries.set(key, new Map());
      const values = entries.get(key);
      return {
        async addAll(urls) { for (const url of urls) values.set(url, offline); },
        async match(request) { return values.get(request.url ?? request); },
        async put(request, response) { values.set(request.url ?? request, response); },
      };
    },
    async keys() { return [...entries.keys()]; },
    async delete(key) { return entries.delete(key); },
  };
  const environment = {
    caches,
    async fetch(request) { fetched.push(request.url); return { status: 503 }; },
  };
  const otherWindow = { url: "https://spool.test" + second, focus() { throw new Error("focused another server"); } };
  const a = serviceWorker([otherWindow], first, environment);
  const b = serviceWorker([], second, environment);
  await a.dispatch("install");
  await b.dispatch("install");
  entries.set(`spool-chat-shell:${first}:v1`, new Map());
  await a.dispatch("activate");
  assert.deepEqual([...entries.keys()], [`spool-chat-shell:${first}:v3`, `spool-chat-shell:${second}:v3`]);
  for (const [key, values] of entries) {
    const base = key.includes(first) ? first : second;
    assert.ok([...values.keys()].every((path) => path.startsWith(base)));
  }
  const request = (path, mode = "cors") => ({ url: "https://spool.test" + path, method: "GET", mode });
  assert.equal(await a.dispatch("fetch", { request: request(first, "navigate") }), offline);
  for (const path of [first + "api/me", first + "api/chats/chat/events", first + "sw.js", second + "assets/a.js"]) {
    assert.equal(await a.dispatch("fetch", { request: request(path) }), undefined);
  }
  assert.equal(fetched.length, 1);
  await a.dispatch("push", { data: { json: () => ({ url: "/?chat=12345678" }) } });
  assert.equal(a.shown[0].options.data.url, "https://spool.test" + first + "?chat=12345678");
  assert.equal(a.shown[0].options.icon, first + "icon-192.png");
  await a.dispatch("notificationclick", { notification: { close() {}, data: a.shown[0].options.data } });
  assert.deepEqual(a.opened, ["https://spool.test" + first + "?chat=12345678"]);
  await a.dispatch("push", { data: { json: () => ({ url: second + "?chat=12345678" }) } });
  assert.equal(a.shown[1].options.data.url, "https://spool.test" + first);
});

test("service worker displays session, sender and group details with a bounded fallback", async () => {
  const worker = serviceWorker();
  for (const [title, body] of [
    ["Spool · 任务成功：修复通知标题", "任务已完成"],
    ["Spool · 需要审核：修复通知标题", "任务等待审批"],
    ["Spool · 任务失败：修复通知标题", "任务执行失败"],
    ["Spool · Alice 发来消息", "你收到一条新私聊消息"],
    ["Spool · 群聊：产品组", "Alice 在群内发送了消息"],
  ]) {
    await worker.dispatch("push", { data: { json: () => ({ title, body }) } });
    assert.equal(worker.shown.at(-1).title, title);
    assert.equal(worker.shown.at(-1).options.body, body);
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
