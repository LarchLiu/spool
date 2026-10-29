import { test } from "node:test";
import assert from "node:assert/strict";
import {
  mergeNavigationChats,
  subscribeWorkspaceEvents,
} from "../.test-build/workspace-stream.js";

test("authorized navigation removes revoked chats without losing newer SSE revisions", () => {
  const previous = [
    { id: "revoked", updated_at: 99 },
    { id: "newer", updated_at: 3, title: "new" },
    { id: "avatars", updated_at: 4, participants: [{ id: "alice" }] },
  ];
  const incoming = [
    { id: "newer", updated_at: 2, title: "stale" },
    {
      id: "avatars",
      updated_at: 4,
      participants: [{ id: "alice" }, { id: "bob" }],
    },
    { id: "new-chat", updated_at: 5 },
  ];
  const result = mergeNavigationChats(previous, incoming);
  assert.deepEqual(
    result.map((v) => v.id),
    ["newer", "avatars", "new-chat"],
  );
  assert.equal(result[0].title, "new");
  assert.equal(result[1].participants.length, 2);
  assert.equal(previous[2].participants.length, 1);
  assert.deepEqual(mergeNavigationChats(previous, []), []);
});

test("older activity metadata still updates current session control permissions", () => {
  const result = mergeNavigationChats(
    [{ id: "chat", updated_at: 5, title: "Current", can_control: true }],
    [{ id: "chat", updated_at: 4, title: "Older", can_control: false }],
  );
  assert.equal(result[0].title, "Current");
  assert.equal(result[0].can_control, false);
});

function fixture(t) {
  const sockets = [];
  const received = [];
  const auth = [];
  let visible = true;
  let failOpen = false;
  const stream = subscribeWorkspaceEvents(
    Object.fromEntries(
      [
        "navigation",
        "agents",
        "presence",
        "profiles",
        "connected",
        "expired",
      ].map((key) => [key, (value) => received.push([key, value])]),
    ),
    {
      open: () => {
        if (failOpen) throw new Error("blocked");
        const socket = {
          readyState: 0,
          sent: [],
          close() {
            this.closed = true;
          },
          send(value) {
            this.sent.push(JSON.parse(value));
          },
          opened() {
            this.readyState = 1;
            this.onopen();
          },
          emit(type, data) {
            this.onmessage({ data: JSON.stringify({ type, data }) });
          },
        };
        sockets.push(socket);
        return socket;
      },
      csrf: () => "csrf-secret",
      visible: () => visible,
      checkAuth: async (signal) => {
        auth.push(signal);
      },
    },
  );
  t.after(() => stream.close());
  return {
    stream,
    sockets,
    received,
    auth,
    visible: (value) => {
      visible = value;
    },
    block: (value) => {
      failOpen = value;
    },
  };
}

test("workspace socket authenticates and synchronizes navigation/agents/presence", (t) => {
  const f = fixture(t);
  const socket = f.sockets[0];
  socket.opened();
  assert.deepEqual(socket.sent, [
    { type: "hello", csrf: "csrf-secret", visible: true },
  ]);
  const navigation = {
    projects: [{ id: "p" }],
    chats: [{ id: "chat", participants: [{ id: "new-actor" }] }],
  };
  socket.emit("navigation", navigation);
  socket.emit("agents", [{ backend: "codex" }]);
  socket.emit("profiles", {
    revision: 1,
    people: [{ id: "alice", nickname: "Alice" }],
  });
  socket.emit("presence", [{ id: "alice", devices: 2 }]);
  assert.equal(
    f.received.some(([name]) => name === "connected"),
    false,
  );
  socket.emit("ready", true);
  assert.deepEqual(f.received, [
    ["navigation", navigation],
    ["agents", [{ backend: "codex" }]],
    ["profiles", { revision: 1, people: [{ id: "alice", nickname: "Alice" }] }],
    ["presence", [{ id: "alice", devices: 2 }]],
    ["connected", true],
  ]);
  assert.equal(f.auth.length, 0);
});

test("heartbeat responds with current visibility, without sending CSRF again", (t) => {
  const f = fixture(t);
  const socket = f.sockets[0];
  socket.opened();
  f.visible(false);
  f.stream.visibility();
  socket.emit("ping", true);
  assert.deepEqual(socket.sent.slice(1), [
    { type: "visibility", visible: false },
    { type: "pong", visible: false },
  ]);
});

test("disconnect retries with backoff and rejects frames from stale sockets", (t) => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] });
  const f = fixture(t);
  const old = f.sockets[0];
  old.opened();
  old.emit("ready", true);
  old.onerror();
  assert.equal(old.closed, true);
  assert.equal(f.auth.length, 1);
  const count = f.received.length;
  old.emit("navigation", { chats: ["stale"] });
  old.onclose({ code: 1006 });
  assert.equal(f.received.length, count);
  t.mock.timers.tick(610);
  assert.equal(f.sockets.length, 2);
  f.sockets[1].opened();
  f.sockets[1].emit("navigation", { chats: ["missed-while-offline"] });
  f.sockets[1].emit("ready", true);
  assert.deepEqual(f.received.slice(-2), [
    ["navigation", { chats: ["missed-while-offline"] }],
    ["connected", true],
  ]);
});

test("silent half-open socket times out and resume reconnects immediately", (t) => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] });
  const f = fixture(t);
  const old = f.sockets[0];
  old.opened();
  old.emit("ready", true);
  t.mock.timers.tick(45001);
  assert.equal(old.closed, true);
  assert.deepEqual(f.received.at(-1), ["connected", false]);
  f.stream.resume();
  assert.equal(f.sockets.length, 2);
  f.sockets[1].opened();
  f.sockets[1].emit("ready", true);
  f.stream.resume();
  assert.deepEqual(f.sockets[1].sent.at(-1), {
    type: "refresh",
    visible: true,
  });
});

test("revoked session stops reconnecting and expires authentication", (t) => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] });
  const f = fixture(t);
  f.sockets[0].onclose({ code: 1008 });
  assert.deepEqual(f.received.at(-1), ["expired", undefined]);
  t.mock.timers.tick(120000);
  f.stream.resume();
  assert.equal(f.sockets.length, 1);
});

test("cleanup cancels auth checks, retries, timers and stale callbacks", (t) => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] });
  const f = fixture(t);
  const socket = f.sockets[0];
  socket.onerror();
  f.stream.close();
  assert.equal(f.auth[0].aborted, true);
  const count = f.received.length;
  socket.opened();
  socket.emit("ready", true);
  socket.onerror();
  socket.onclose({ code: 1008 });
  t.mock.timers.tick(120000);
  assert.equal(f.received.length, count);
  assert.equal(f.sockets.length, 1);
});

test("blocked connection or invalid JSON retries rather than losing synchronization", (t) => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] });
  const f = fixture(t);
  f.sockets[0].onmessage({ data: "not-json" });
  f.block(true);
  t.mock.timers.tick(610);
  assert.equal(f.sockets.length, 1);
  f.block(false);
  t.mock.timers.tick(1210);
  assert.equal(f.sockets.length, 2);
});
