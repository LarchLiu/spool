import { test } from "node:test";
import assert from "node:assert/strict";
import { subscribeChatEvents } from "../.test-build/chat-stream.js";

function fixture() {
  const listeners = new Map();
  const received = [];
  const requests = [];
  const auth = [];
  const source = {
    addEventListener: (name, callback) => listeners.set(name, callback),
    close: () => (source.closed = true),
    emit: (name, value) => listeners.get(name)({ data: JSON.stringify(value) }),
  };
  const close = subscribeChatEvents(
    "chat-1",
    Object.fromEntries(
      ["snapshot", "metadata", "turn", "connected", "error"].map((name) => [
        name,
        (value) => received.push([name, value]),
      ]),
    ),
    {
      open: (path) => {
        assert.equal(path, "/api/chats/chat-1/events");
        return source;
      },
      snapshot: (signal) =>
        new Promise((resolve, reject) => {
          requests.push({ signal, resolve, reject });
        }),
      checkAuth: async (signal) => auth.push(signal),
    },
  );
  return { source, received, requests, auth, close };
}
const settle = () => new Promise((resolve) => setImmediate(resolve));

test("SSE initializes and reconnects without a redundant history GET", () => {
  const f = fixture();
  assert.equal(f.requests.length, 0);
  const snapshot = { id: "chat-1", turns: [{ id: "turn-1" }] };
  f.source.emit("snapshot", snapshot);
  f.source.emit("metadata", { id: "chat-1", updated_at: 2 });
  f.source.emit("turn", { turn: { id: "turn-2" } });
  f.source.onerror();
  assert.equal(f.auth.length, 1);
  f.source.emit("snapshot", { ...snapshot, updated_at: 3 });
  assert.equal(f.requests.length, 0);
  assert.equal(f.received.filter(([name]) => name === "snapshot").length, 2);
  assert.equal(f.received.filter(([name]) => name === "turn").length, 1);
  assert.equal(f.received.filter(([name]) => name === "metadata").length, 1);
  assert.deepEqual(f.received.at(-2), ["connected", true]);
  f.close();
  assert.equal(f.source.closed, true);
});

test("failed initialization fetches once to surface HTTP/auth errors and can retry", async () => {
  const f = fixture();
  f.source.onerror();
  f.source.onerror();
  assert.equal(f.requests.length, 1);
  const error = new Error("Chat not found");
  f.requests[0].reject(error);
  await settle();
  assert.deepEqual(f.received.at(-1), ["error", error]);
  f.source.onerror();
  assert.equal(f.requests.length, 2);
  f.requests[1].resolve({ id: "chat-1" });
  await settle();
  assert.deepEqual(f.received.at(-1), ["snapshot", { id: "chat-1" }]);
  f.source.onerror();
  assert.equal(f.auth.length, 1);
  assert.equal(f.requests.length, 2);
  f.close();
});

test("an SSE snapshot cancels an outstanding fallback and cannot be overwritten", async () => {
  for (const fail of [false, true]) {
    const f = fixture();
    f.source.onerror();
    f.source.emit("snapshot", { id: "chat-1", updated_at: 2 });
    assert.equal(f.requests[0].signal.aborted, true);
    if (fail) f.requests[0].reject(new Error("stale request"));
    else f.requests[0].resolve({ id: "chat-1", updated_at: 1 });
    await settle();
    assert.deepEqual(f.received.at(-1), [
      "snapshot",
      { id: "chat-1", updated_at: 2 },
    ]);
    f.close();
  }
});

test("navigation cancels pending fallback, auth checks and stale stream events", async () => {
  for (const fail of [false, true]) {
    const f = fixture();
    f.source.onerror();
    f.close();
    assert.equal(f.requests[0].signal.aborted, true);
    const count = f.received.length;
    if (fail) f.requests[0].reject(new Error("old chat"));
    else f.requests[0].resolve({ id: "chat-1" });
    f.source.emit("snapshot", { id: "chat-1" });
    f.source.emit("metadata", { id: "chat-1" });
    f.source.emit("turn", { turn: { id: "old-turn" } });
    f.source.onerror();
    await settle();
    assert.equal(f.received.length, count);
  }
  const f = fixture();
  f.source.emit("snapshot", { id: "chat-1" });
  f.source.onerror();
  f.close();
  assert.equal(f.auth[0].aborted, true);
});
