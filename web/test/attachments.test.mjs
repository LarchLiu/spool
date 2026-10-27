import { test } from "node:test";
import assert from "node:assert/strict";
import {
  attachmentURL,
  validateAttachments,
} from "../.test-build/attachment-selection.js";
import { optimisticChat, adoptCreatedChat } from "../.test-build/chat-state.js";

test("attachment limits match the server before reading file contents", () => {
  assert.equal(validateAttachments([{ size: 4 * 1024 * 1024 }]), null);
  assert.ok(validateAttachments([{ size: 0 }]));
  assert.ok(validateAttachments([{ size: 4 * 1024 * 1024 + 1 }]));
  assert.ok(
    validateAttachments(Array.from({ length: 7 }, () => ({ size: 1 }))),
  );
  assert.ok(
    validateAttachments(
      Array.from({ length: 5 }, () => ({ size: 4 * 1024 * 1024 })),
    ),
  );
});

test("draft images use local previews; history uses authenticated chat-scoped URLs", () => {
  assert.equal(
    attachmentURL("chat-id", { id: "file-id", preview_url: "blob:test" }),
    "blob:test",
  );
  assert.equal(
    attachmentURL("chat/id", { id: "file?id" }),
    "/api/chats/chat%2Fid/attachments?id=file%3Fid",
  );
});

test("first-send adoption keeps attachments and the chosen permission mode on screen", () => {
  const turn = {
    id: "pending-1",
    prompt: "",
    backend: "claude",
    permission_mode: "manual",
    attachments: [
      {
        id: "file-id",
        name: "image.png",
        media_type: "image/png",
        size: 10,
        preview_url: "blob:test",
      },
    ],
    status: "running",
    started_at: 1,
  };
  const optimistic = optimisticChat(null, "project", turn);
  const chat = adoptCreatedChat(optimistic, {
    id: "real-chat",
    project_id: "project",
    title: "New chat",
    backend: "claude",
    status: "ready",
    transcript: { parts: [] },
    turns: [],
    updated_at: 1,
  });
  assert.equal(chat.permission_mode, "manual");
  assert.equal(chat.turns[0].attachments[0].preview_url, "blob:test");
  assert.equal(chat.turns[0].prompt, "");
});
