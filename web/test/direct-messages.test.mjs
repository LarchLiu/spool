import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  createDirectInbox,
  inboxMessageLimit,
  onlineChatRoom,
  messagePeer,
  peopleConversationIDs,
} from "../.test-build/direct-messages.js";
import { DirectMessagePanel } from "../.test-build/components/web/src/direct-message-panel.js";
import { PeopleGroupPicker } from "../.test-build/components/web/src/people-group-picker.js";
import { PeopleConversationPicker } from "../.test-build/components/web/src/people-conversation-picker.js";

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
    groupNames: {},
    drafts: {},
    sending: {},
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
        draft: "",
        sending: false,
        onDraftChange() {},
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
  assert.match(html, /aria-modal="false"/);
  assert.match(html, /aria-label="Move chat window \(arrow keys\)"/);
  assert.match(html, /aria-label="Resize chat window \(arrow keys\)"/);
  assert.match(html, /Messages are not saved/);
  assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
  assert.doesNotMatch(html, /<img src="x"/);
  assert.match(html, /class="direct-message own"/);
  assert.doesNotMatch(html, /maxLength=/i);
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
      draft: "",
      sending: false,
      onDraftChange() {},
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
      draft: "",
      sending: false,
      onDraftChange() {},
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

test("conversation popup uses an anchored listbox trigger rather than a mobile native select", () => {
  const html = renderToStaticMarkup(
    createElement(PeopleConversationPicker, {
      value: "bob",
      people,
      unread: { bob: 2 },
      height: 360,
      t: (key) => key,
      onSelect() {},
    }),
  );
  assert.match(html, /aria-label="Choose conversation"/);
  assert.match(html, /aria-haspopup="listbox"/);
  assert.match(html, /aria-expanded="false"/);
  assert.match(html, /<span>Bob<\/span>/);
  assert.match(html, /class="direct-unread">2/);
  assert.doesNotMatch(html, /<select|role="listbox"/);
});

test("drafts survive switching, closing and profile eviction, but not reset", () => {
  const inbox = createDirectInbox();
  inbox.open("bob", people);
  inbox.draft("bob", "Unsent draft for Bob");
  inbox.open("charlie", []);
  inbox.draft("charlie", "A separate draft");
  inbox.close();
  for (let i = 0; i <= inboxMessageLimit; i++)
    inbox.receive(message(String(i), "david"), "alice", true, []);
  inbox.profiles([]);
  assert.equal(inbox.snapshot().people[0].nickname, "Bob");
  assert.ok(
    peopleConversationIDs(inbox.snapshot(), "alice", []).includes("bob"),
  );
  inbox.open("bob", []);
  assert.equal(inbox.snapshot().drafts.bob.text, "Unsent draft for Bob");
  assert.equal(inbox.snapshot().drafts.charlie.text, "A separate draft");
  inbox.draft("charlie", "");
  assert.equal(inbox.snapshot().drafts.charlie, undefined);
  inbox.reset();
  assert.deepEqual(inbox.snapshot().drafts, {});
});

test("only an acknowledged unchanged draft clears, even after the panel closes", async () => {
  const inbox = createDirectInbox();
  inbox.open("bob", people);
  inbox.draft("bob", "first");
  let resolve;
  const sent = inbox.send(
    "bob",
    "first",
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  assert.equal(inbox.snapshot().sending.bob, true);
  await assert.rejects(
    inbox.send("bob", "first", async () => message("duplicate")),
    { code: "rate_limited" },
  );
  inbox.close();
  assert.equal(inbox.snapshot().drafts.bob.text, "first");
  resolve(message("sent"));
  await sent;
  assert.equal(inbox.snapshot().drafts.bob, undefined);
  assert.equal(inbox.snapshot().sending.bob, undefined);

  inbox.draft("bob", "second");
  const changed = inbox.send(
    "bob",
    "second",
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  inbox.draft("bob", "newer edit");
  inbox.draft("bob", "second");
  resolve(message("old-ack"));
  await changed;
  assert.equal(inbox.snapshot().drafts.bob.text, "second");
  await assert.rejects(
    inbox.send("bob", "second", async () => {
      throw new Error("offline");
    }),
  );
  assert.equal(inbox.snapshot().drafts.bob.text, "second");
  assert.deepEqual(inbox.snapshot().sending, {});

  const oldSession = inbox.send(
    "bob",
    "second",
    () =>
      new Promise((done) => {
        resolve = done;
      }),
  );
  inbox.reset();
  inbox.draft("bob", "second");
  let nextResolve;
  const newSession = inbox.send(
    "bob",
    "second",
    () =>
      new Promise((done) => {
        nextResolve = done;
      }),
  );
  resolve(message("old-session-ack"));
  await oldSession;
  assert.equal(inbox.snapshot().drafts.bob.text, "second");
  assert.equal(inbox.snapshot().sending.bob, true);
  nextResolve(message("new-session-ack"));
  await newSession;
  assert.equal(inbox.snapshot().drafts.bob, undefined);
});

test("closed groups retain their label and read conversation, never live membership", () => {
  const inbox = createDirectInbox();
  const group = {
    id: "group-pair",
    name: "Pair group",
    members: ["alice", "bob"],
    created_by: "alice",
  };
  inbox.groups([group]);
  inbox.open(group.id, people);
  inbox.receive(
    { ...message("group", "bob", group.id), group: true },
    "alice",
    true,
    people,
  );
  inbox.open("bob", people);
  inbox.groups([]);
  inbox.receive(message("later-private"), "alice", true, people);
  assert.equal(inbox.snapshot().unread[group.id], 0);
  assert.ok(
    peopleConversationIDs(inbox.snapshot(), "alice", ["bob"]).includes(
      group.id,
    ),
  );
  assert.equal(inbox.snapshot().groupNames[group.id], "Pair group");
  assert.equal(inbox.snapshot().groups.length, 0);
  const html = renderToStaticMarkup(
    createElement(DirectMessagePanel, {
      user: "alice",
      peer: { id: group.id, nickname: inbox.snapshot().groupNames[group.id] },
      people,
      unread: {},
      messages: inbox.snapshot().messages,
      connected: true,
      draft: "retained",
      sending: false,
      onDraftChange() {},
      locale: "en",
      t: (key) => key,
      onSend: async () => message("sent"),
      onPeer() {},
      onClose() {},
    }),
  );
  assert.match(html, /This group is no longer available to you/);
  assert.match(html, /type="submit" disabled=""/);
  assert.doesNotMatch(html, /aria-label="Invite people"/);

  for (let i = 0; i < inboxMessageLimit; i++)
    inbox.receive(message(String(i)), "alice", true, people);
  assert.equal(inbox.snapshot().groupNames[group.id], undefined);
  assert.ok(
    !peopleConversationIDs(inbox.snapshot(), "alice", []).includes(group.id),
  );
  inbox.groups([group]);
  inbox.open(group.id, people);
  inbox.draft(group.id, "draft only");
  inbox.groups([]);
  inbox.close();
  assert.equal(inbox.snapshot().groupNames[group.id], "Pair group");
  assert.ok(
    peopleConversationIDs(inbox.snapshot(), "alice", []).includes(group.id),
  );
  inbox.draft(group.id, "");
  assert.equal(inbox.snapshot().groupNames[group.id], undefined);
});

test("composer validates code points rather than UTF-16 units without truncation", () => {
  const view = (draft) =>
    renderToStaticMarkup(
      createElement(DirectMessagePanel, {
        user: "alice",
        peer: people[0],
        people,
        unread: {},
        messages: [],
        connected: true,
        status: "online",
        draft,
        sending: false,
        onDraftChange() {},
        locale: "en",
        t: (key) => key,
        onSend: async () => message("sent"),
        onPeer() {},
        onClose() {},
      }),
    );
  for (const draft of [
    "🙂".repeat(2000),
    "你🙂".repeat(1000),
    "a".repeat(2000),
  ]) {
    const html = view(draft);
    assert.doesNotMatch(
      html,
      /type="submit" disabled|aria-invalid="true"|maxLength=/i,
    );
    assert.ok(html.includes(draft));
  }
  const html = view("你🙂".repeat(1000) + "x");
  assert.match(html, /aria-invalid="true"/);
  assert.match(html, /type="submit" disabled=""/);
  assert.match(html, /Enter a message of up to 2000 characters/);
});
