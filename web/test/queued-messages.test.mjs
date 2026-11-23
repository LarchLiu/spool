import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  composerEnterAction,
  QueuedMessages,
} from "../.test-build/components/web/src/queued-messages.js";
import { deliveryReceipt } from "../.test-build/components/web/src/message-delivery.js";

const key = (event = {}) => ({
  key: "Enter", shiftKey: false, metaKey: false, ctrlKey: false, altKey: false,
  isComposing: false, keyCode: 13, ...event,
});

test("Enter sends, Shift+Enter breaks the line and the command key steers", () => {
  assert.equal(composerEnterAction(key(), false), "send");
  assert.equal(composerEnterAction(key({ shiftKey: true }), false), null);
  assert.equal(composerEnterAction(key({ metaKey: true }), false), "steer");
  assert.equal(composerEnterAction(key({ ctrlKey: true }), false), "steer");
  assert.equal(composerEnterAction(key({ altKey: true }), false), "send");
  assert.equal(composerEnterAction(key({ metaKey: true, shiftKey: true }), false), null);
  assert.equal(composerEnterAction(key({ key: "a" }), false), null);
});

test("a touch keyboard keeps Enter for line breaks", () => {
  assert.equal(composerEnterAction(key(), true), null);
  assert.equal(composerEnterAction(key({ altKey: true }), true), null);
  assert.equal(composerEnterAction(key({ shiftKey: true }), true), null);
  assert.equal(composerEnterAction(key({ metaKey: true }), true), "steer");
  assert.equal(composerEnterAction(key({ ctrlKey: true }), true), "steer");
});

test("Enter that confirms an input method choice does not send", () => {
  assert.equal(composerEnterAction(key({ isComposing: true }), false), null);
  // WebKit confirms after the composition ended, with only the key code left.
  assert.equal(composerEnterAction(key({ keyCode: 229 }), false), null);
  assert.equal(composerEnterAction(key({ keyCode: 229, metaKey: true }), false), null);
});

const render = (props) => renderToStaticMarkup(createElement(QueuedMessages, {
  messages: [], running: true, busy: false, t: (text) => text,
  onSteer: () => {}, onDelete: () => {}, onEdit: () => {}, onMove: () => {}, ...props,
}));

test("queued messages are listed in order with their actions", () => {
  assert.equal(render({}), "");
  const html = render({ messages: [
    { id: "a", message: "first", created_at: 1 },
    { id: "b", message: " ", attachments: ["notes.txt", "plan.png"], created_at: 2 },
  ] });
  assert.match(html, /aria-label="Queued messages"/);
  assert.ok(html.indexOf("first") < html.indexOf("notes.txt, plan.png"));
  assert.equal(html.match(/Steer/g).length, 2);
  assert.equal(html.match(/aria-label="Delete queued message"/g).length, 2);
  assert.equal(html.match(/aria-label="Queued message actions"/g).length, 2);
  assert.equal(html.match(/aria-label="Drag to reorder"/g).length, 2);
  assert.doesNotMatch(html, /disabled/);
});

test("a queued message is sent rather than steered once nothing is running", () => {
  const html = render({ running: false, busy: true, messages: [{ id: "a", message: "first", created_at: 1 }] });
  assert.match(html, /Send now/);
  assert.doesNotMatch(html, /Steer/);
  assert.equal(html.match(/disabled=""/g).length, 3);
  assert.doesNotMatch(html, /aria-label="Drag to reorder"/);
});

test("local queue entries expose sending or recovery actions before they get a server ID", () => {
  const sending = render({ messages: [{ id: "local", message: "Immediate", created_at: 1, delivery_status: "sending" }] });
  assert.match(sending, /role="status"/);
  assert.match(sending, /Sending…/);
  assert.doesNotMatch(sending, />Steer<|aria-label="Delete queued message"|aria-label="Queued message actions"/);
  const failed = render({ messages: [{ id: "local", message: "Immediate", created_at: 1, delivery_status: "failed" }] });
  assert.match(failed, /Send failed/);
  assert.doesNotMatch(failed, /Edit message/);
  assert.match(failed, /aria-label="Queued message actions"/);
  assert.match(failed, /aria-label="Delete queued message"/);
});

test("receipt identity distinguishes identical messages and provisional provider input", () => {
  const chat = { queued_messages: [{ message: "same", client_message_id: "other" }], transcript: { parts: [] } };
  assert.deepEqual(deliveryReceipt(chat, "local"), { present: false, accepted: false });
  chat.transcript.parts.push({ role: "user", text: "same", client_message_id: "local", pending: true });
  assert.deepEqual(deliveryReceipt(chat, "local"), { present: true, accepted: false });
  chat.transcript.parts[0].pending = false;
  assert.deepEqual(deliveryReceipt(chat, "local"), { present: true, accepted: true });
  chat.transcript.parts = [];
  chat.queued_messages[0].client_message_id = "local";
  assert.deepEqual(deliveryReceipt(chat, "local"), { present: true, accepted: true });
});
