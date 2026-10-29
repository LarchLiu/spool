import assert from "node:assert/strict";
import test from "node:test";
import { Children, createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  INITIAL_CHAT_LIMIT,
  SidebarChatList,
} from "../.test-build/components/web/src/sidebar-chat-list.js";

function fixture(count, options = {}) {
  const chats = Array.from({ length: count }, (_, index) => ({
    id: `chat-${index}`,
    title: `Chat ${index}`,
    updated_at: count - index,
  }));
  let limit = INITIAL_CHAT_LIMIT;
  return {
    chats,
    get limit() {
      return limit;
    },
    view() {
      const tree = SidebarChatList({
        chats,
        limit,
        t: (key) => key,
        renderChat: (chat) =>
          createElement(
            "div",
            { key: chat.id, "data-chat-id": chat.id },
            chat.title,
          ),
        onLimit: (next) => {
          limit = next;
        },
        ...options,
      });
      const button = Children.toArray(tree.props.children).find(
        (child) => child.type === "button",
      );
      const html = renderToStaticMarkup(tree);
      return {
        button,
        html,
        ids: [...html.matchAll(/data-chat-id="([^"]+)"/g)].map(
          (match) => match[1],
        ),
      };
    },
  };
}

test("chat lists initially show the first five records without reordering", () => {
  const list = fixture(30);
  assert.deepEqual(list.view().ids, list.chats.slice(0, 5).map((chat) => chat.id));
  assert.equal(list.view().button.props.children, "Show more");
});

test("empty and short lists never show a pagination or collapse button", () => {
  for (const count of [0, 1, 4, 5]) {
    const view = fixture(count).view();
    assert.equal(view.ids.length, count);
    assert.equal(view.button, undefined);
    assert.equal(view.html.includes("No conversations yet."), count === 0);
  }
});

test("each click appends at most ten records, then collapse restores five", () => {
  for (const total of [6, 14, 15, 16, 25, 26, 38]) {
    const list = fixture(total);
    let visible = 5;
    while (visible < total) {
      const view = list.view();
      assert.equal(view.button.props.children, "Show more");
      assert.equal(view.button.props.type, "button");
      view.button.props.onClick();
      visible = Math.min(visible + 10, total);
      assert.equal(list.limit, visible);
      assert.deepEqual(
        list.view().ids,
        list.chats.slice(0, visible).map((chat) => chat.id),
      );
    }
    assert.equal(list.view().button.props.children, "Show less");
    list.view().button.props.onClick();
    assert.equal(list.limit, 5);
    assert.equal(list.view().ids.length, 5);
    assert.equal(list.view().button.props.children, "Show more");
    list.view().button.props.onClick();
    assert.equal(list.view().ids.length, Math.min(15, total));
  }
});

test("recent and project chat lists expand and collapse independently", () => {
  const recent = fixture(26);
  const project = fixture(26, { nested: true });
  project.view().button.props.onClick();
  assert.equal(project.view().ids.length, 15);
  assert.equal(recent.view().ids.length, 5);
  recent.view().button.props.onClick();
  recent.view().button.props.onClick();
  recent.view().button.props.onClick();
  recent.view().button.props.onClick();
  assert.equal(recent.view().ids.length, 5);
  assert.equal(project.view().ids.length, 15);
  assert.equal(project.view().button.props.className, "show-more nested");
});

test("the toggle follows refreshed list lengths and still collapses to five", () => {
  const list = fixture(16);
  list.view().button.props.onClick();
  list.view().button.props.onClick();
  list.chats.push({ id: "older-chat", title: "Older chat", updated_at: 0 });
  assert.equal(list.view().ids.length, 16);
  assert.equal(list.view().button.props.children, "Show more");
  list.view().button.props.onClick();
  assert.equal(list.view().ids.length, 17);
  assert.equal(list.view().button.props.children, "Show less");
  list.chats.splice(8);
  assert.equal(list.view().button.props.children, "Show less");
  list.view().button.props.onClick();
  assert.equal(list.view().ids.length, 5);
  list.chats.splice(5);
  assert.equal(list.view().button, undefined);
});

test("chat-list controls and empty states use the existing translations", () => {
  const t = (key) => `translated:${key}`;
  const list = fixture(6, { t });
  assert.match(list.view().html, /translated:Show more/);
  list.view().button.props.onClick();
  assert.match(list.view().html, /translated:Show less/);
  assert.match(
    fixture(0, { t, nested: true }).view().html,
    /class="nav-empty nested">translated:No conversations yet\./,
  );
});
