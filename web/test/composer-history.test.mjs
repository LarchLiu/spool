import assert from "node:assert/strict";
import test from "node:test";
import { composerHistoryMessages } from "../.test-build/composer-history.js";

const user = (text, extra = {}) => ({
  role: "user",
  kind: "text",
  text,
  ...extra,
});
const answer = { role: "assistant", kind: "text", text: "Response" };
const chat = (parts, turns = [{ part_start: 0, part_end: parts.length }]) => ({
  transcript: { parts },
  turns,
});

test("history preserves user input, whitespace and steering order without recalling answers or attachment contents", () => {
  const parts = [
    user("  First\nmessage  "),
    { role: "user", kind: "context", text: "Private attachment contents" },
    answer,
    user("Follow up", { id: "steer-1" }),
    user("Additional block", { id: "steer-1" }),
    user("Next steering message", { id: "steer-2" }),
    answer,
    { role: "user", kind: "image", text: "[image]" },
    answer,
    user(" \n "),
  ];
  assert.deepEqual(composerHistoryMessages(chat(parts)), [
    "  First\nmessage  ",
    "Follow up\nAdditional block",
    "Next steering message",
  ]);
});

test("consecutive turns stay separate even when there is no assistant response", () => {
  assert.deepEqual(
    composerHistoryMessages(
      chat(
        [user("First"), user("Second")],
        [
          { part_start: 0, part_end: 1 },
          { part_start: 1, part_end: 2 },
        ],
      ),
    ),
    ["First", "Second"],
  );
});

test("repeated prompts remain separate history entries", () => {
  assert.deepEqual(
    composerHistoryMessages(chat([user("Again"), answer, user("Again")])),
    ["Again", "Again"],
  );
});

test("new chats and chats without text input have no recall entries", () => {
  assert.deepEqual(composerHistoryMessages(null), []);
  assert.deepEqual(composerHistoryMessages(chat([])), []);
  assert.deepEqual(composerHistoryMessages(chat([answer])), []);
});
