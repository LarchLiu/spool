import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ProjectChatStatus } from "../.test-build/components/web/src/project-chat-status.js";

const t = (key) => `translated:${key}`;
const render = (chats, notifications = {}, collapsed = true) =>
  renderToStaticMarkup(
    createElement(ProjectChatStatus, { collapsed, chats, notifications, t }),
  );
const statuses = (html) =>
  [...html.matchAll(/data-status="([^"]+)"/g)].map((match) => match[1]);

test("empty and quiet projects do not show conversation status icons", () => {
  assert.equal(render([]), "");
  assert.equal(
    render([
      { id: "idle", status: "idle" },
      { id: "read", status: "completed" },
    ]),
    "",
  );
  assert.equal(render([{ id: "read", status: "failed" }]), "");
});

test("expanded projects hide every summary icon and restore them on collapse", () => {
  const chats = [
    { id: "running", status: "running" },
    { id: "completed", status: "idle" },
    { id: "failed", status: "idle" },
    { id: "approval", status: "idle" },
  ];
  const notifications = {
    completed: "completed",
    failed: "failed",
    approval: "approval",
  };
  const collapsed = render(chats, notifications);
  assert.deepEqual(statuses(collapsed), ["approval", "failed", "completed"]);
  assert.equal(render(chats, notifications, false), "");
  assert.equal(render(chats, notifications), collapsed);
});

test("multiple running conversations share one loading indicator with a translated label", () => {
  const html = render([
    { id: "first", status: "running" },
    { id: "second", status: "running" },
  ]);
  assert.equal((html.match(/role="status"/g) || []).length, 1);
  assert.match(html, /aria-label="translated:Working"/);
  assert.match(html, /title="translated:Working"/);
  assert.deepEqual(statuses(html), []);
});

test("loading and every unread notification kind can appear together without duplicates", () => {
  const chats = [
    "running",
    "completed",
    "failed",
    "approval",
    "another-completed",
    "another-approval",
  ].map((id) => ({
    id,
    status: id === "running" ? "running" : "idle",
  }));
  const notifications = {
    completed: "completed",
    failed: "failed",
    approval: "approval",
    "another-completed": "completed",
    "another-approval": "approval",
  };
  const html = render(chats, notifications);
  assert.equal((html.match(/role="status"/g) || []).length, 1);
  assert.deepEqual(statuses(html), ["approval", "failed", "completed"]);
  for (const label of ["Completed", "Failed", "Approval required"])
    assert.ok(html.includes(`aria-label="translated:${label}"`));
});

test("a project includes conversations past the initial sidebar page and excludes other projects", () => {
  const chats = Array.from({ length: 12 }, (_, index) => ({
    id: `chat-${index}`,
    status: "idle",
  }));
  assert.deepEqual(
    statuses(render(chats, { "chat-11": "approval", elsewhere: "failed" })),
    ["approval"],
  );
  assert.equal(render([], { elsewhere: "completed" }), "");
});

test("reading notifications, stopping work and removing conversations update the summary", () => {
  const chats = [
    { id: "first", status: "running" },
    { id: "second", status: "idle" },
  ];
  assert.deepEqual(
    statuses(render(chats, { first: "failed", second: "failed" })),
    ["failed"],
  );
  assert.deepEqual(statuses(render(chats, { second: "failed" })), ["failed"]);
  chats[0].status = "completed";
  assert.equal(render(chats), "");
  assert.equal(render(chats.slice(0, 1), { second: "failed" }), "");
});
