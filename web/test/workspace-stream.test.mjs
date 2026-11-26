import { test } from "node:test";
import assert from "node:assert/strict";
import {
  mergeNavigationChats,
  subscribeWorkspaceEvents,
} from "../.test-build/workspace-stream.js";
import { onlineChatRoom } from "../.test-build/direct-messages.js";

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

function fixture(t, view) {
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
        "server",
        "federationTargets",
        "connected",
        "expired",
        "directMessage",
        "groups",
        "sessionNotification",
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
      view,
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

test("browser view follows selection and focus on hello, navigation, visibility and heartbeat", (t) => {
  let view = { focused: true, chat: "viewed-chat123" };
  const f = fixture(t, () => view);
  const socket = f.sockets[0];
  socket.opened();
  assert.deepEqual(socket.sent[0], {
    type: "hello", csrf: "csrf-secret", visible: true, ...view,
  });
  view = { focused: true, chat: "another-chat123" };
  f.stream.view();
  assert.deepEqual(socket.sent.at(-1), { type: "view", visible: true, ...view });
  view = { ...view, focused: false };
  f.stream.view();
  assert.equal(socket.sent.at(-1).focused, false);
  f.visible(false);
  f.stream.visibility();
  socket.emit("ping", true);
  assert.deepEqual(socket.sent.at(-1), { type: "pong", visible: false, ...view });
});

test("session status events are delivered independently of push and reject malformed payloads", (t) => {
  const f = fixture(t);
  const socket = f.sockets[0];
  socket.opened();
  socket.emit("ready", true);
  const note = { chat: "chat-12345678", user: "user-12345678", kind: "completed" };
  for (const kind of ["completed", "failed", "approval"]) socket.emit("session_notification", { ...note, kind });
  for (const value of [null, {}, { ...note, kind: "test" }, { ...note, chat: "../api/me" }, { ...note, user: "short" }])
    socket.emit("session_notification", value);
  assert.deepEqual(f.received.filter(([kind]) => kind === "sessionNotification"),
    ["completed", "failed", "approval"].map((kind) => ["sessionNotification", { ...note, kind }]));
});

test("workspace socket delivers the server's name on initial sync and rename", (t) => {
  const f = fixture(t);
  const socket = f.sockets[0];
  socket.opened();
  socket.emit("server", { server_name: "Home" });
  socket.emit("ready", true);
  socket.emit("server", { server_name: "办公室 · Mac" });
  assert.deepEqual(f.received, [
    ["server", { server_name: "Home" }],
    ["connected", true],
    ["server", { server_name: "办公室 · Mac" }],
  ]);
});

test("workspace snapshots grant and revoke targets, with a fresh list after reconnect", (t) => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] });
  const f = fixture(t);
  const id = "b".repeat(52);
  const lab = {
    id, name: "Lab", client_url: "https://lab.example", client_mode: "tailscale",
    address: "private", status: "connected",
  };
  const parsed = [{ id, name: "Lab", clientURL: "https://lab.example/", clientMode: "tailscale" }];
  const socket = f.sockets[0];
  socket.opened();
  socket.emit("federation_targets", []);
  socket.emit("ready", true);
  socket.emit("federation_targets", [lab]);
  socket.emit("federation_targets", {});
  socket.emit("federation_targets", []);
  socket.onerror();
  socket.emit("federation_targets", [lab]);
  t.mock.timers.tick(610);
  f.sockets[1].opened();
  f.sockets[1].emit("federation_targets", [lab]);
  f.sockets[1].emit("ready", true);
  assert.deepEqual(
    f.received.filter(([kind]) => kind === "federationTargets").map(([, targets]) => targets),
    [[], parsed, [], parsed],
  );
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

test("relay disconnect during hello, view or heartbeat send reconnects without escaping into React", (t) => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] });
  for (const action of ["hello", "view", "visibility", "heartbeat", "refresh"]) {
    const f = fixture(t);
    const socket = f.sockets[0];
    if (action !== "hello") {
      socket.opened();
      socket.emit("ready", true);
    }
    socket.send = () => { throw new DOMException("Relay connection lost", "InvalidStateError"); };
    assert.doesNotThrow(() => {
      if (action === "hello") socket.opened();
      else if (action === "view") f.stream.view();
      else if (action === "visibility") f.stream.visibility();
      else if (action === "heartbeat") socket.emit("ping", true);
      else f.stream.resume();
    }, action);
    assert.equal(socket.closed, true, `${action} closes the broken socket`);
    assert.deepEqual(f.received.at(-1), ["connected", false]);
    t.mock.timers.tick(610);
    assert.equal(f.sockets.length, 2, `${action} reconnects`);
    f.sockets[1].opened();
    f.sockets[1].emit("ready", true);
    assert.deepEqual(f.received.at(-1), ["connected", true]);
    f.stream.close();
  }
});

test("private messages require a ready connection and wait for the server echo", async (t) => {
  const f = fixture(t);
  await assert.rejects(f.stream.sendMessage("bob", "hello"), {
    code: "unavailable",
  });
  const socket = f.sockets[0];
  socket.opened();
  await assert.rejects(f.stream.sendMessage("bob", "hello"), {
    code: "unavailable",
  });
  socket.emit("ready", true);
  await assert.rejects(f.stream.sendMessage("bob", " \n "), {
    code: "invalid_message",
  });
  await assert.rejects(f.stream.sendMessage("bob", "x".repeat(2001)), {
    code: "invalid_message",
  });
  const sent = f.stream.sendMessage("bob", "hello");
  const request = socket.sent.at(-1);
  assert.equal(request.type, "direct_message");
  assert.equal(request.to, "bob");
  assert.equal(request.text, "hello");
  assert.equal(request.visible, true);
  assert.ok(request.request_id.length > 8);
  assert.equal(
    f.received.some(([kind]) => kind === "directMessage"),
    false,
  );
  const message = {
    id: "server-id",
    request_id: request.request_id,
    from: "alice",
    to: "bob",
    text: "hello",
    sent_at: 123,
  };
  socket.emit("direct_message", message);
  assert.deepEqual(await sent, message);
  assert.deepEqual(f.received.at(-1), ["directMessage", message]);
  socket.emit("direct_message", {
    ...message,
    id: "reply",
    request_id: "other-device",
  });
  assert.equal(f.received.at(-1)[1].id, "reply");
});

test("transport accepts 2000 Unicode code points and rejects the next one", async (t) => {
  const f = fixture(t);
  const socket = f.sockets[0];
  socket.opened();
  socket.emit("ready", true);
  for (const text of [
    "🙂".repeat(2000),
    "你🙂".repeat(1000),
    "e\u0301".repeat(1000),
  ]) {
    const pending = f.stream.sendMessage("bob", text);
    const request = socket.sent.at(-1);
    assert.equal(request.text, text);
    socket.emit("direct_message", {
      id: request.request_id,
      request_id: request.request_id,
      from: "alice",
      to: "bob",
      text,
      sent_at: 123,
    });
    assert.equal((await pending).text, text);
    await assert.rejects(f.stream.sendMessage("bob", text + "x"), {
      code: "invalid_message",
    });
  }
});

test("offline errors retain their request and disconnected messages are never replayed", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout", "Date"] });
  const f = fixture(t);
  const socket = f.sockets[0];
  socket.opened();
  socket.emit("ready", true);
  const first = f.stream.sendMessage("bob", "offline");
  const rejected = assert.rejects(first, { code: "offline" });
  socket.emit("direct_message_error", {
    request_id: socket.sent.at(-1).request_id,
    code: "offline",
  });
  await rejected;
  const second = assert.rejects(f.stream.sendMessage("bob", "maybe arrived"), {
    code: "unknown",
  });
  socket.onerror();
  await second;
  t.mock.timers.tick(610);
  const next = f.sockets[1];
  next.opened();
  next.emit("ready", true);
  assert.equal(
    next.sent.some((message) => message.type === "direct_message"),
    false,
  );
  const timeout = assert.rejects(f.stream.sendMessage("bob", "no ack"), {
    code: "unknown",
  });
  t.mock.timers.tick(10001);
  await timeout;
  const cleanup = assert.rejects(f.stream.sendMessage("bob", "closing"), {
    code: "unknown",
  });
  f.stream.close();
  await cleanup;
});

test("group messages share the socket but use a distinct broadcast event", async (t) => {
  const f = fixture(t);
  const socket = f.sockets[0];
  socket.opened();
  socket.emit("ready", true);
  const sent = f.stream.sendMessage(onlineChatRoom, "hello everyone");
  const request = socket.sent.at(-1);
  assert.equal(request.type, "group_message");
  assert.equal(request.to, onlineChatRoom);
  const group = {
    id: "group-id",
    request_id: request.request_id,
    from: "alice",
    to: onlineChatRoom,
    text: "hello everyone",
    sent_at: 123,
  };
  socket.emit("group_message", group);
  assert.deepEqual(await sent, group);
  assert.deepEqual(f.received.at(-1), ["directMessage", group]);
});

test("selected-group creation and invitations wait for authenticated server results", async (t) => {
  const f = fixture(t);
  const socket = f.sockets[0];
  socket.opened();
  socket.emit("ready", true);
  const created = f.stream.createGroup("Team", ["bob"]);
  const request = socket.sent.at(-1);
  assert.equal(request.type, "group_create");
  assert.deepEqual(request.members, ["bob"]);
  const group = {
    id: "group-team",
    name: "Team",
    members: ["alice", "bob"],
    created_by: "alice",
  };
  socket.emit("chat_groups", [group]);
  socket.emit("group_result", { request_id: request.request_id, group });
  assert.deepEqual(await created, group);
  assert.deepEqual(f.received.at(-1), ["groups", [group]]);
  const invited = f.stream.inviteGroup(group.id, ["charlie"]);
  const invite = socket.sent.at(-1);
  assert.equal(invite.type, "group_invite");
  assert.equal(invite.to, group.id);
  const updated = { ...group, members: [...group.members, "charlie"] };
  socket.emit("group_result", {
    request_id: invite.request_id,
    group: updated,
  });
  assert.deepEqual(await invited, updated);
  const sent = f.stream.sendMessage(group.id, "only selected people");
  const messageRequest = socket.sent.at(-1);
  assert.equal(messageRequest.type, "group_message");
  socket.emit("group_message", {
    id: "selected-message",
    request_id: messageRequest.request_id,
    from: "alice",
    to: group.id,
    text: "only selected people",
    group: true,
    sent_at: 123,
  });
  assert.equal((await sent).group, true);
});
