import { test } from "node:test";
import assert from "node:assert/strict";
import { runningTurn, stopReply } from "../.test-build/turn-control.js";
import { reconcile } from "../.test-build/chat-state.js";

const turn = {
  id: "turn-1",
  prompt: "Hello",
  status: "running",
  started_at: 10,
};
const chat = {
  id: "chat-1",
  project_id: "project-1",
  title: "Hello",
  backend: "codex",
  status: "running",
  transcript: { parts: [] },
  turns: [turn],
  updated_at: 10,
};
const stopped = {
  ...chat,
  status: "stopped",
  updated_at: 20,
  turns: [
    { ...turn, status: "stopped", reply: "Partial reply", finished_at: 20 },
  ],
};

test("stop targets the exact active server turn and preserves the partial reply", async () => {
  assert.equal(runningTurn(chat), turn);
  assert.equal(runningTurn(stopped), undefined);
  assert.equal(runningTurn(null), undefined);
  const calls = [];
  assert.equal(
    await stopReply(chat, undefined, async (...args) => {
      calls.push(args);
      return stopped;
    }),
    stopped,
  );
  assert.deepEqual(calls, [["chat-1", "turn-1"]]);
  assert.equal(reconcile(stopped, chat).status, "stopped");
  assert.equal(
    reconcile(stopped, { ...chat, updated_at: 20 }).status,
    "stopped",
  );
});

test("an immediate stop waits for first-chat creation and message acknowledgement", async () => {
  let acknowledge;
  const accepted = new Promise((resolve) => {
    acknowledge = resolve;
  });
  const optimistic = {
    ...chat,
    id: "pending-1",
    turns: [{ ...turn, id: "pending-1" }],
  };
  const calls = [];
  const stopping = stopReply(optimistic, accepted, async (...args) => {
    calls.push(args);
    return stopped;
  });
  await Promise.resolve();
  assert.deepEqual(calls, []);
  acknowledge(chat);
  assert.equal(await stopping, stopped);
  assert.deepEqual(calls, [["chat-1", "turn-1"]]);
});

test("failed submissions and already finished replies do not issue stale stop requests", async () => {
  let calls = 0;
  const stop = async () => {
    calls++;
    return stopped;
  };
  assert.equal(await stopReply(chat, Promise.resolve(null), stop), null);
  assert.equal(await stopReply(chat, Promise.resolve(stopped), stop), stopped);
  assert.equal(
    await stopReply(
      { ...chat, turns: [{ ...turn, id: "pending-1" }] },
      undefined,
      stop,
    ),
    null,
  );
  assert.equal(calls, 0);
});
