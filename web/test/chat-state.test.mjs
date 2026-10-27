import { test } from "node:test";
import assert from "node:assert/strict";
import {
  adoptCreatedChat,
  canControlChat,
  optimisticChat,
  reconcile,
  reconcileMetadata,
} from "../.test-build/chat-state.js";

test("all task assignees can control chat, while regular shared chats stay owner-only", () => {
  const task = {
    automation_chat: true,
    user_id: "alice",
    collaborator_ids: ["alice", "bob"],
  };
  assert.equal(canControlChat(task, "bob"), true);
  assert.equal(canControlChat(task, "charlie"), false);
  assert.equal(
    canControlChat({ ...task, collaborator_ids: ["bob"] }, "alice"),
    false,
  );
  assert.equal(canControlChat({ user_id: "alice" }, "bob"), false);
  assert.equal(canControlChat({ user_id: "alice" }, "alice"), true);
});

const userPart = { role: "user", kind: "text", text: "Hello" };
const answerPart = { role: "assistant", kind: "text", text: "Hi" };
const helloTranscript = { parts: [userPart, answerPart] };
const running = {
  id: "turn-1",
  part_start: 0,
  input_end: 1,
  part_end: 2,
  status: "running",
  started_at: 10,
};

function chat(turns, updated_at = 10, transcript = { parts: [] }) {
  return {
    id: "chat-1",
    project_id: "project-1",
    title: "Hello",
    backend: "codex",
    status: turns.at(-1)?.status || "ready",
    transcript,
    turns,
    updated_at,
  };
}

test("live metadata updates PRs and task controls without replacing streamed content", () => {
  const previous = {
    ...chat([running], 10, helloTranscript),
    automation_chat: true,
    collaborator_ids: ["alice"],
  };
  const artifacts = [
    { type: "pull_request", url: "https://github.com/o/r/pull/81" },
  ];
  const next = reconcileMetadata(previous, {
    id: previous.id,
    updated_at: 11,
    automation_chat: true,
    collaborator_ids: ["bob"],
    artifacts,
  });
  assert.deepEqual(next.artifacts, artifacts);
  assert.equal(canControlChat(next, "bob"), true);
  assert.equal(canControlChat(next, "alice"), false);
  assert.equal(next.status, "running");
  assert.equal(next.turns, previous.turns);
  assert.equal(next.transcript, previous.transcript);
  const cleared = reconcileMetadata(next, {
    id: next.id,
    updated_at: 12,
    automation_chat: true,
  });
  assert.deepEqual(cleared.collaborator_ids, []);
  assert.deepEqual(cleared.artifacts, []);
  assert.equal(canControlChat(cleared, "bob"), false);
});

test("metadata ignores old revisions and other chats, and never creates a chat", () => {
  const previous = chat([running]);
  assert.equal(
    reconcileMetadata(previous, { id: previous.id, updated_at: 9 }),
    previous,
  );
  assert.equal(
    reconcileMetadata(previous, { id: "other-chat", updated_at: 20 }),
    previous,
  );
  assert.equal(reconcileMetadata(null, { id: previous.id, updated_at: 20 }), null);
});

test("a turn replay at the metadata revision still delivers a pending approval", () => {
  const previous = chat([running], 10, helloTranscript);
  const metadata = reconcileMetadata(previous, { id: previous.id, updated_at: 20 });
  const turn = {
    ...running,
    pending_approval: { id: "approval-1", title: "Allow command?" },
  };
  assert.equal(
    reconcile(metadata, { ...metadata, updated_at: 10, turns: [turn] }),
    metadata,
  );
  const replay = reconcile(metadata, { ...metadata, turns: [turn] });
  assert.equal(replay.turns[0].pending_approval.id, "approval-1");
  assert.deepEqual(replay.transcript, metadata.transcript);
});

test("late REST acknowledgement cannot undo a completed SSE turn", () => {
  const completed = { ...running, status: "completed", finished_at: 20 };
  const previous = chat([completed], 20, {
    parts: [userPart, { ...answerPart, text: "Hi there" }],
  });
  assert.deepEqual(reconcile(previous, chat([running], 10, helloTranscript)), previous);
  assert.equal(reconcile(previous, chat([running], 20, helloTranscript)).status, "completed");
});

test("a stale snapshot cannot erase an optimistic user message", () => {
  const pending = {
    ...running,
    id: "pending-1",
    part_start: 2,
    input_end: 3,
    part_end: 3,
  };
  const completed = { ...running, status: "completed" };
  const previous = chat([completed, pending], 10, {
    parts: [...helloTranscript.parts, { ...userPart, text: "Next" }],
  });
  const result = reconcile(previous, chat([completed], 10, helloTranscript));
  assert.equal(result.turns.at(-1).id, "pending-1");
  assert.equal(result.status, "running");
  assert.equal(result.transcript.parts.at(-1).text, "Next");
});

test("server acknowledgement replaces rather than duplicates the pending turn", () => {
  const pending = { ...running, id: "pending-1" };
  const result = reconcile(chat([pending], 9, helloTranscript), chat([running], 10, helloTranscript));
  assert.deepEqual(result.turns, [running]);
});

test("shorter running snapshots do not remove streamed transcript text", () => {
  const previous = chat([running], 10, {
    parts: [userPart, { ...answerPart, text: "Hi there" }],
  });
  const result = reconcile(previous, chat([running], 10, helloTranscript));
  assert.equal(result.transcript.parts[1].text, "Hi there");
});

test("metadata-only completion stops the spinner without erasing live content", () => {
  const previous = chat([running], 10, {
    parts: [userPart, { ...answerPart, text: "streamed answer" }],
  });
  const persisted = {
    ...chat([{ ...running, status: "completed", finished_at: 30, part_end: 0, input_end: 0 }], 30),
    status: "completed",
  };
  const result = reconcile(previous, persisted);
  assert.equal(result.status, "completed");
  assert.equal(result.turns[0].status, "completed");
  assert.equal(result.turns[0].part_end, 2);
  assert.equal(result.transcript.parts[1].text, "streamed answer");
});

test("same-revision snapshots cannot remove a newly streamed turn", () => {
  const first = { ...running, status: "completed" };
  const second = { ...running, id: "turn-2", part_start: 2, input_end: 3, part_end: 4 };
  assert.equal(reconcile(chat([first, second]), chat([first])).turns.length, 2);
});

test("switching chats never merges another conversation", () => {
  const next = { ...chat([]), id: "chat-2" };
  assert.deepEqual(reconcile(chat([running], 10, helloTranscript), next), next);
});

test("first send can display a user message before a chat exists", () => {
  const turn = {
    ...running,
    id: "pending-first",
    backend: "codex",
    requested_model: "chosen-model",
    reasoning_effort: "high",
  };
  const pending = optimisticChat(null, "project-1", turn, [userPart]);
  assert.equal(pending.project_id, "project-1");
  assert.equal(pending.status, "running");
  assert.equal(pending.model, "chosen-model");
  assert.equal(pending.reasoning_effort, "high");
  assert.equal(pending.turns[0].part_end, 1);
  assert.deepEqual(pending.transcript.parts, [userPart]);
});

test("chat creation preserves the first message until REST or SSE acknowledges it", () => {
  const turn = {
    ...running,
    id: "pending-first",
    backend: "claude",
    requested_model: "chosen-claude",
    reasoning_effort: "",
  };
  const pending = optimisticChat(null, "project-1", turn, [userPart]);
  const created = {
    ...chat([], 20),
    backend: "claude",
    transcript: { parts: [userPart] },
  };
  const adopted = adoptCreatedChat(pending, created);
  assert.equal(adopted.id, created.id);
  assert.equal(adopted.backend, "claude");
  assert.equal(adopted.model, "chosen-claude");
  assert.deepEqual(adopted.turns, [pending.turns[0]]);
  assert.equal(adopted.status, "running");
  assert.deepEqual(reconcile(adopted, created).turns, [pending.turns[0]]);
  const accepted = { ...turn, id: "server-first", part_end: 1 };
  const acknowledged = reconcile(adopted, {
    ...created,
    turns: [accepted],
    transcript: { parts: [userPart] },
  });
  assert.deepEqual(acknowledged.turns, [accepted]);
});

test("later sends append to the existing chat without changing its identity or history", () => {
  const previous = chat([{ ...running, status: "completed" }], 10, helloTranscript);
  const nextPart = { ...userPart, text: "Next" };
  const turn = { ...running, id: "pending-next" };
  const pending = optimisticChat(previous, previous.project_id, turn, [nextPart]);
  assert.equal(pending.id, previous.id);
  assert.equal(pending.updated_at, previous.updated_at);
  assert.equal(pending.turns.at(-1).part_start, 2);
  assert.deepEqual(pending.transcript.parts, [...helloTranscript.parts, nextPart]);
  assert.equal(previous.turns.length, 1);
});

test("cross-agent continuation keeps the chat and earlier agent attribution", () => {
  const earlier = { ...running, status: "completed", backend: "codex" };
  const previous = { ...chat([earlier], 10, helloTranscript), backend: "codex" };
  const next = { ...running, id: "pending-switch", backend: "claude", requested_model: "test-claude", permission_mode: "acceptEdits" };
  const pending = optimisticChat(previous, previous.project_id, next, [userPart]);
  assert.equal(pending.id, previous.id);
  assert.equal(pending.backend, "claude");
  assert.equal(pending.permission_mode, "acceptEdits");
  assert.equal(pending.turns[0].backend, "codex");
  assert.deepEqual(pending.transcript.parts.slice(0, 2), helloTranscript.parts);
  const accepted = reconcile(pending, {
    ...pending,
    updated_at: 20,
    turns: [earlier, { ...pending.turns[1], id: "accepted-switch" }],
  });
  assert.equal(accepted.backend, "claude");
  assert.equal(accepted.turns.length, 2);
  assert.equal(accepted.turns[0].backend, "codex");
});
