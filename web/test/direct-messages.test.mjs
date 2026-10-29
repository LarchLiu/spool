import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  createDirectInbox,
  inboxMessageLimit,
  onlineChatRoom,
  messagePeer,
} from "../.test-build/direct-messages.js";
import { DirectMessagePanel } from "../.test-build/components/web/src/direct-message-panel.js";
import { PeopleGroupPicker } from "../.test-build/components/web/src/people-group-picker.js";

const people = [
  { id: "bob", nickname: "Bob", avatar: { style: "thumbs", seed: "bob" } },
];
const message = (id, from = "bob", to = "alice") => ({
  id,
  request_id: id,
  from,
  to,
  text: "hello",
  sent_at: 123,
});

test("inbox separates people, deduplicates echoes and tracks unread messages", () => {
  const inbox = createDirectInbox();
  let updates = 0;
  const unsubscribe = inbox.subscribe(() => updates++);
  inbox.receive(message("first"), "alice", true, people);
  assert.equal(inbox.snapshot().unread.bob, 1);
  assert.equal(inbox.snapshot().active, "");
  assert.deepEqual(inbox.snapshot().people, people);
  inbox.receive(message("first"), "alice", true, people);
  inbox.receive(
    message("unrelated", "charlie", "david"),
    "alice",
    true,
    people,
  );
  assert.equal(inbox.snapshot().messages.length, 1);
  inbox.open("bob", people);
  assert.equal(inbox.snapshot().unread.bob, 0);
  inbox.receive(message("visible"), "alice", true, people);
  assert.equal(inbox.snapshot().unread.bob, 0);
  inbox.receive(message("background"), "alice", false, people);
  assert.equal(inbox.snapshot().unread.bob, 1);
  inbox.read();
  assert.equal(inbox.snapshot().unread.bob, 0);
  inbox.receive(message("own-echo", "alice", "bob"), "alice", false, people);
  assert.equal(inbox.snapshot().unread.bob, 0);
  inbox.receive(message("other-person", "charlie"), "alice", true, people);
  assert.equal(inbox.snapshot().unread.charlie, 1);
  assert.equal(inbox.snapshot().unread.bob, 0);
  inbox.close();
  assert.equal(inbox.snapshot().active, "");
  unsubscribe();
  const before = updates;
  inbox.reset();
  assert.equal(updates, before);
  assert.deepEqual(inbox.snapshot(), {
    active: "",
    messages: [],
    unread: {},
    people: [],
    groups: [],
  });
});

test("inbox is bounded, keeps last known profiles while offline and starts empty", () => {
  const inbox = createDirectInbox();
  for (let i = 0; i < inboxMessageLimit + 20; i++)
    inbox.receive(message(String(i)), "alice", true, people);
  assert.equal(inbox.snapshot().messages.length, inboxMessageLimit);
  assert.equal(inbox.snapshot().messages[0].id, "20");
  assert.equal(inbox.snapshot().unread.bob, 99);
  inbox.profiles([]);
  assert.equal(inbox.snapshot().people[0].nickname, "Bob");
  inbox.profiles([{ ...people[0], nickname: "Robert" }]);
  assert.equal(inbox.snapshot().people[0].nickname, "Robert");
  assert.equal(createDirectInbox().snapshot().messages.length, 0);
  inbox.reset();
  assert.equal(inbox.snapshot().people.length, 0);
});

test("private panel escapes messages, explains lifetime and disables offline sending", () => {
  const view = (status, connected = true) =>
    renderToStaticMarkup(
      createElement(DirectMessagePanel, {
        user: "alice",
        peer: people[0],
        people,
        unread: {},
        messages: [
          { ...message("html"), text: "<img src=x onerror=alert(1)>" },
          message("own", "alice", "bob"),
        ],
        connected,
        status,
        locale: "en",
        t: (key) => key,
        onSend: async () => message("sent"),
        onPeer() {},
        onClose() {},
      }),
    );
  const html = view("online");
  assert.match(html, /role="log"/);
  assert.match(html, /aria-labelledby="direct-message-title"/);
  assert.match(html, /Messages are not saved/);
  assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
  assert.doesNotMatch(html, /<img src="x"/);
  assert.match(html, /class="direct-message own"/);
  assert.match(html, /maxLength="2000"/i);
  assert.match(view(undefined), /This person is offline/);
  assert.match(view("online", false), /Wait for reconnection/);
});

test("group messages have their own conversation and unread count, never a private thread", () => {
  const inbox = createDirectInbox();
  const group = message("group", "bob", onlineChatRoom);
  assert.equal(messagePeer(group, "alice"), onlineChatRoom);
  assert.equal(messagePeer(group, "bob"), onlineChatRoom);
  inbox.open("bob", people);
  inbox.receive(group, "alice", true, people);
  assert.equal(inbox.snapshot().unread[onlineChatRoom], 1);
  assert.equal(inbox.snapshot().unread.bob, 0);
  const privateToBob = inbox
    .snapshot()
    .messages.filter((item) => messagePeer(item, "alice") === "bob");
  assert.equal(privateToBob.length, 0);
  inbox.open(onlineChatRoom, people);
  assert.equal(inbox.snapshot().unread[onlineChatRoom], 0);
  inbox.receive(
    message("own-group", "alice", onlineChatRoom),
    "alice",
    false,
    people,
  );
  assert.equal(inbox.snapshot().unread[onlineChatRoom], 0);
  inbox.receive(message("private", "bob"), "alice", true, people);
  assert.equal(inbox.snapshot().unread.bob, 1);
});

test("group panel identifies senders and is usable without a private recipient online", () => {
  const room = { id: onlineChatRoom, nickname: "Online group chat" };
  const html = renderToStaticMarkup(
    createElement(DirectMessagePanel, {
      user: "alice",
      peer: room,
      people: [room, ...people],
      unread: {},
      messages: [message("group", "bob", onlineChatRoom)],
      connected: true,
      onlineCount: 3,
      locale: "en",
      t: (key) => key,
      onSend: async () => message("sent"),
      onPeer() {},
      onClose() {},
    }),
  );
  assert.match(html, /Online group chat/);
  assert.match(html, /group-message-author">Bob/);
  assert.match(html, /aria-label="Group message"/);
  assert.match(html, /aria-label="Send group message"/);
  assert.doesNotMatch(html, /This person is offline/);
});

test("selected groups remain separate from private/public chats and have invite controls", () => {
  const group = {
    id: "group-selected",
    name: "A small group",
    members: ["alice", "bob"],
    created_by: "alice",
  };
  const inbox = createDirectInbox();
  inbox.groups([group]);
  const selectedMessage = {
    ...message("selected", "bob", group.id),
    group: true,
  };
  inbox.receive(selectedMessage, "alice", true, people);
  assert.equal(messagePeer(selectedMessage, "alice"), group.id);
  assert.equal(inbox.snapshot().unread[group.id], 1);
  assert.equal(inbox.snapshot().unread.bob, undefined);
  assert.equal(inbox.snapshot().unread[onlineChatRoom], undefined);
  const html = renderToStaticMarkup(
    createElement(DirectMessagePanel, {
      user: "alice",
      peer: { id: group.id, nickname: group.name },
      people,
      group,
      unread: {},
      messages: [selectedMessage],
      connected: true,
      locale: "en",
      t: (key) => key,
      onSend: async () => selectedMessage,
      onPeer() {},
      onClose() {},
      onInvite() {},
    }),
  );
  assert.match(html, /A small group/);
  assert.match(html, /aria-label="Invite people"/);
  assert.match(html, /group-message-author">Bob/);
  inbox.groups([]);
  assert.equal(inbox.snapshot().groups.length, 0);
  inbox.reset();
  assert.equal(inbox.snapshot().messages.length, 0);
});

test("member picker explains scope and lets users choose specific people", () => {
  const html = renderToStaticMarkup(
    createElement(PeopleGroupPicker, {
      people,
      t: (key) => key,
      onSubmit: async () => ({}),
      onClose() {},
      onDone() {},
    }),
  );
  assert.match(html, /New group chat/);
  assert.match(html, /type="checkbox" value="bob"/);
  assert.match(html, /Only selected members/);
  assert.match(html, /Group name/);
  assert.match(html, /Create group/);
});
