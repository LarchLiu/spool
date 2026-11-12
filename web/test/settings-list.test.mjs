import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { UserAvatar } from "../.test-build/user-avatar.js";
import { avatarDataURI } from "../.test-build/avatar.js";
import {
  SettingsBack,
  SettingsList,
} from "../.test-build/components/web/src/settings-list.js";

test("management lists render full-row buttons in a semantic list, not inline editor tabs", () => {
  const html = renderToStaticMarkup(
    createElement(SettingsList, {
      label: "People",
      summary: "2 people",
      empty: "No people yet.",
      onSelect() {},
      items: [
        {
          id: "one",
          title: "First Person",
          description: "first-user",
          status: "Enabled",
          icon: createElement("svg"),
        },
        {
          id: "two",
          title: "Second Person",
          description: "second-user",
          status: "Disabled",
          icon: createElement("svg"),
        },
      ],
      action: createElement("button", { type: "button" }, "New person"),
    }),
  );
  assert.match(html, /section[^>]*aria-label="People"/);
  assert.match(html, /<ul class="settings-items">/);
  assert.equal((html.match(/<li>/g) ?? []).length, 2);
  assert.equal((html.match(/class="settings-list-item"/g) ?? []).length, 2);
  assert.match(html, /type="button"[^>]*data-entity-id="one"/);
  for (const value of [
    "First Person",
    "first-user",
    "Enabled",
    "Second Person",
    "Disabled",
    "New person",
  ])
    assert.ok(html.includes(value));
  assert.doesNotMatch(html, /<form|<input|settings-entity-list/);
});

test("empty management lists keep the create action visible and have no editor", () => {
  const html = renderToStaticMarkup(
    createElement(SettingsList, {
      label: "Projects",
      summary: "0 projects",
      empty: "No projects yet.",
      items: [],
      onSelect() {},
      action: createElement("button", { type: "button" }, "New project"),
    }),
  );
  assert.match(html, /No projects yet\./);
  assert.match(html, /New project/);
  assert.doesNotMatch(html, /<ul|<form|<input/);
});

test("people list rows render the user's selected chat avatar before their name", () => {
  const person = {
    id: "person",
    username: "alice",
    nickname: "Alice",
    avatar: { style: "thumbs", seed: "alice-custom-avatar" },
  };
  const html = renderToStaticMarkup(
    createElement(SettingsList, {
      label: "People",
      summary: "1 person",
      empty: "Empty",
      onSelect() {},
      items: [
        {
          id: person.id,
          title: person.nickname,
          description: person.username,
          status: "Enabled",
          icon: createElement(UserAvatar, { person, size: 34 }),
        },
      ],
    }),
  );
  assert.ok(html.includes(`src="${avatarDataURI(person).replaceAll("&", "&amp;")}"`));
  assert.match(
    html,
    /settings-list-icon[^>]*aria-hidden="true"><img class="user-avatar"/,
  );
  assert.match(html, /width="34" height="34" alt=""/);
  assert.ok(
    html.indexOf('class="user-avatar"') < html.indexOf("<strong>Alice"),
  );
});

test("list rows escape local names and paths, and show long metadata without editable fields", () => {
  const path = "/Users/test/projects/very-long-project-directory";
  const html = renderToStaticMarkup(
    createElement(SettingsList, {
      label: "Projects",
      summary: "1 project",
      empty: "Empty",
      onSelect() {},
      items: [
        {
          id: "project",
          title: "<script>not code</script>",
          description: path,
          status: "Archived",
          icon: null,
        },
      ],
    }),
  );
  assert.ok(html.includes(`title="${path}"`));
  assert.match(html, /&lt;script&gt;not code&lt;\/script&gt;/);
  assert.doesNotMatch(html, /<script>|<input/);
});

test("editors provide a non-submit back action", () => {
  const html = renderToStaticMarkup(
    createElement(SettingsBack, { label: "Back to list", onClick() {} }),
  );
  assert.match(html, /type="button" class="settings-back"/);
  assert.match(html, /Back to list/);
});
