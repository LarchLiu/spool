import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  turnOperator,
  reconcile,
  reconcileMetadata,
  prependHistory,
} from "../.test-build/chat-state.js";
import {
  ChatOperatorAvatars,
  ChatOperatorList,
  TurnOperatorAvatar,
  TurnActions,
} from "../.test-build/components/web/src/turn-identity.js";
import { MessageFooter } from "../.test-build/components/web/src/message-footer.js";
import { TurnResponse } from "../.test-build/components/web/src/tool-activity.js";

const alice = {
  id: "alice",
  nickname: "Alice",
  avatar: { style: "clay", seed: "alice-picked" },
};
const bob = {
  id: "bob",
  nickname: "Bob",
  avatar: { style: "thumbs", seed: "bob-picked" },
};
const bot = { id: "spool-bot", nickname: "Spool Bot", bot: true };
const turn = {
  id: "turn",
  user_id: bob.id,
  status: "completed",
  started_at: 10,
  part_start: 0,
  input_end: 1,
  part_end: 2,
};
const chat = {
  id: "chat",
  project_id: "project",
  user_id: alice.id,
  participants: [alice, bob, bot],
  updated_at: 20,
  status: "completed",
  turns: [turn],
  transcript: {
    parts: [
      { role: "user", kind: "text", text: "hello" },
      { role: "assistant", kind: "text", text: "done" },
    ],
  },
};
const t = (key, values = {}) =>
  key.replace(/\{(\w+)\}/g, (_, key) => values[key]);
const render = (Component, props) =>
  renderToStaticMarkup(createElement(Component, props));

test("turn identity uses actual sender and falls back to the creator for legacy turns", () => {
  assert.deepEqual(turnOperator(chat, turn), bob);
  assert.deepEqual(turnOperator(chat, { ...turn, user_id: undefined }), alice);
  assert.deepEqual(turnOperator({ ...chat, participants: [] }, turn, bob), bob);
  assert.deepEqual(
    turnOperator({ ...chat, participants: [] }, { ...turn, user_id: bot.id }),
    bot,
  );
});

test("Spool Bot renders the logo, while human operators use their selected avatars", () => {
  const logo = render(TurnOperatorAvatar, { person: bot });
  assert.match(logo, /aria-label="Spool Bot"/);
  assert.match(logo, /src="\/logo.png"/);
  assert.doesNotMatch(logo, /data:image\/svg/);
  const human = render(TurnOperatorAvatar, { person: bob });
  assert.match(human, /aria-label="Bob"/);
  assert.match(human, /data:image\/svg/);
  assert.equal(render(TurnOperatorAvatar, {}), "");
});

test("chat-list operator stacks are independent of presence or authorized collaborators", () => {
  const html = render(ChatOperatorAvatars, {
    chat: { ...chat, collaborator_ids: ["not-an-operator"] },
  });
  assert.match(html, /aria-label="Alice, Bob, Spool Bot"/);
  assert.match(html, /<button[^>]*type="button"[^>]*class="chat-operators"/);
  assert.match(html, /aria-expanded="false"/);
  assert.doesNotMatch(html, /title=|chat-operator-popover/);
  assert.equal((html.match(/<img /g) || []).length, 3);
  assert.match(html, /src="\/logo.png"/);
  assert.doesNotMatch(html, /not-an-operator/);
  const many = render(ChatOperatorAvatars, {
    chat: {
      ...chat,
      participants: [...chat.participants, { id: "four", nickname: "Fourth" }],
    },
  });
  assert.match(many, /avatar-stack-more">\+1/);
  assert.match(many, /aria-label="Alice, Bob, Spool Bot, Fourth"/);
  assert.equal(
    render(ChatOperatorAvatars, { chat: { ...chat, participants: undefined } }),
    "",
  );
  assert.equal(
    render(ChatOperatorAvatars, { chat: { ...chat, participants: [] } }),
    "",
  );
});

test("the member popover lists every operator with their avatar and escaped full name", () => {
  const people = [
    alice,
    bob,
    bot,
    { id: "four", nickname: '<img src=x onerror="bad()"> Fourth' },
  ];
  const html = render(ChatOperatorList, { people });
  assert.equal((html.match(/<li>/g) || []).length, 4);
  assert.equal((html.match(/<img /g) || []).length, 4);
  assert.match(html, /src="\/logo.png"/);
  assert.match(html, /<span>Alice<\/span>/);
  assert.match(html, /<span>Bob<\/span>/);
  assert.match(html, /<span>Spool Bot<\/span>/);
  assert.match(html, /&lt;img src=x onerror=&quot;bad\(\)&quot;&gt; Fourth/);
  assert.doesNotMatch(html, /<img src=x|avatar-stack-more/);
});

test("audit controls display the operator rather than the turn's sender", () => {
  const html = render(TurnActions, {
    chat,
    turn: {
      ...turn,
      actions: [
        { id: "stop", user_id: alice.id, kind: "stop", created_at: 1000 },
        { id: "approve", user_id: bob.id, kind: "approve", created_at: 1001 },
        { id: "deny", user_id: alice.id, kind: "deny", created_at: 1002 },
      ],
    },
    locale: "en",
    t,
  });
  assert.match(html, /Stopped by Alice/);
  assert.match(html, /Approved by Bob/);
  assert.match(html, /Denied by Alice/);
  assert.equal((html.match(/<time /g) || []).length, 3);
  assert.equal(render(TurnActions, { chat, turn, locale: "en", t }), "");
  assert.match(
    render(MessageFooter, { role: "user", text: "hello", author: "Bob", t }),
    /class="message-author">Bob/,
  );
});

test("turn actions appear after the reply and before its copy icon and time", () => {
  for (const kind of ["approve", "deny", "stop"]) {
    const actions = createElement(TurnActions, {
      chat,
      turn: {
        ...turn,
        actions: [{ id: kind, user_id: alice.id, kind, created_at: 1000 }],
      },
      locale: "en",
      t,
    });
    const html = render(TurnResponse, {
      parts: chat.transcript.parts,
      running: false,
      timestamp: 2000,
      summary: "Worked",
      locale: "en",
      actions,
      t,
    });
    assert.match(html, /class="assistant-text"/);
    assert.match(html, /class="turn-actions"/);
    assert.match(html, /message-footer-assistant/);
    assert.ok(html.indexOf("assistant-text") < html.indexOf("turn-actions"));
    assert.ok(
      html.indexOf("turn-actions") < html.indexOf("message-footer-assistant"),
    );
    assert.equal((html.match(/class="turn-actions"/g) || []).length, 1);
    for (const running of [true, false]) {
      const noAnswer = render(TurnResponse, {
        parts: [],
        running,
        summary: "Working",
        actions,
        t,
      });
      assert.match(noAnswer, /class="turn-actions"/);
      assert.doesNotMatch(noAnswer, /message-footer|Copy message/);
    }
  }
});

test("single chat avatars and agent icons share a 16px slot while stacks stay compact", async () => {
  const css = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  assert.match(
    css,
    /^\.avatar-stack-item \+ \.avatar-stack-item \{\s*margin-left: -9px;/m,
  );
  assert.match(
    css,
    /\.nav-chat-agent \{[^}]*place-items: center;[^}]*width: 16px;\s*height: 16px;/,
  );
  assert.match(
    css,
    /\.nav-chat \.avatar-stack-item \{\s*width: 16px;\s*height: 16px;\s*border-width: 1px;/,
  );
  assert.match(
    css,
    /\.nav-chat \.avatar-stack \.user-avatar \{\s*width: 14px;\s*height: 14px;/,
  );
  assert.match(
    css,
    /\.nav-chat \.avatar-stack-item:only-child \{\s*border: 0;/,
  );
  assert.match(
    css,
    /\.nav-chat \.avatar-stack-item:only-child \.user-avatar \{\s*width: 16px;\s*height: 16px;/,
  );
  assert.match(
    css,
    /\.nav-chat \.avatar-stack-more \{\s*width: auto;\s*min-width: 20px;/,
  );
  assert.match(
    css,
    /\.nav-chat \.avatar-stack-item \+ \.avatar-stack-item \{\s*margin-left: -10px;/,
  );
  assert.match(
    css,
    /\.nav-chat \.avatar-stack-item \+ \.avatar-stack-more \{\s*margin-left: -3px;/,
  );
});

test("SSE metadata, native IDs and backward pagination retain actor identities", () => {
  const withMetadata = reconcileMetadata(chat, {
    id: chat.id,
    updated_at: 21,
    participants: [bot, bob],
  });
  assert.deepEqual(withMetadata.participants, [bot, bob]);
  const live = { ...chat, turns: [{ ...turn, native_turn_id: "native" }] };
  const native = {
    ...chat,
    updated_at: 22,
    turns: [{ ...turn, id: "history-native", native_turn_id: "native" }],
  };
  const merged = reconcile(live, native);
  assert.equal(merged.turns.length, 1);
  assert.equal(merged.turns[0].user_id, bob.id);
  const older = {
    ...chat,
    turns: [{ ...turn, id: "older", user_id: alice.id }],
  };
  const history = prependHistory(merged, older);
  assert.deepEqual(
    history.turns.map((item) => item.user_id),
    [alice.id, bob.id],
  );
});

test("both sidebar lists replace the leading agent icon only when operators exist", async () => {
  const sidebar = await readFile(
    new URL("../src/sidebar.tsx", import.meta.url),
    "utf8",
  );
  assert.match(
    sidebar,
    /!!chat\.participants\?\.length && (?:\(\s*)?<ChatOperatorAvatars chat=\{chat\} \/>/,
  );
  assert.match(
    sidebar,
    /!chat\.participants\?\.length && \(\s*<span className="nav-chat-agent">\s*<AgentIcon/,
  );
  assert.match(
    sidebar,
    /<ChatOperatorAvatars chat=\{chat\} \/>\s*(?:\))?\}\s*<button\s+type="button"\s+className="nav-chat-open"/,
  );
  assert.equal((sidebar.match(/<ChatOperatorAvatars /g) || []).length, 1);
  assert.match(sidebar, /chatButton\(chat, true\)/);
  assert.match(sidebar, /chatButton\(chat, false\)/);
});

test("message avatars sit to the right", async () => {
  const main = await readFile(
    new URL("../src/main.tsx", import.meta.url),
    "utf8",
  );
  assert.match(
    main,
    /<\/div>\s*<TurnOperatorAvatar person=\{operator\} \/>\s*<\/div>\s*<TurnResponse/,
  );
  assert.match(main, /actions=\{\s*<TurnActions[\s\S]*?\/>\s*\}/);
  const css = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  assert.match(css, /\.user-row\s*\{[^}]*display: flex;[^}]*gap: 10px;/);
});
