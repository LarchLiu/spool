import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  avatarChoices,
  avatarAttribution,
  avatarConfig,
  avatarDataURI,
  avatarName,
  avatarStyles,
  defaultAvatar,
} from "../.test-build/avatar.js";
import { AvatarStack, UserAvatar } from "../.test-build/user-avatar.js";

const user = { id: "user-alice", nickname: "Alice", username: "alice" };
const svg = (person) =>
  decodeURIComponent(avatarDataURI(person).split(",").slice(1).join(","));

test("the style picker contains exactly the requested eight styles in order", () => {
  assert.deepEqual(avatarStyles, [
    ["clay", "Clay"],
    ["pixel-art", "Pixel Art"],
    ["croodles", "Croodles"],
    ["lorelei", "Lorelei"],
    ["notionists", "Notionists"],
    ["toon-head", "Toon Head"],
    ["voxel-art", "Voxel Art"],
    ["thumbs", "Thumbs"],
  ]);
  assert.deepEqual(defaultAvatar, { style: "clay", seed: "" });
  const images = avatarStyles.map(([style]) =>
    avatarDataURI({ ...user, avatar: { style, seed: "same-seed" } }),
  );
  assert.equal(new Set(images).size, 8);
});

test("existing users get a stable, locally generated name-based avatar", () => {
  assert.deepEqual(avatarConfig(user), defaultAvatar);
  assert.equal(avatarName(user), "Alice");
  assert.equal(avatarName({ ...user, nickname: "  " }), "alice");
  assert.equal(avatarName({ id: user.id, nickname: "" }), user.id);
  assert.equal(avatarDataURI(user), avatarDataURI({ ...user }));
  assert.notEqual(
    avatarDataURI(user),
    avatarDataURI({ ...user, nickname: "Bob" }),
  );
  assert.equal(
    avatarDataURI(user),
    avatarDataURI({ ...user, id: "another-account" }),
  );
});

test("hand-picked avatars survive renames and resetting the seed restores auto mode", () => {
  for (const [style] of avatarStyles) {
    const picked = { ...user, avatar: { style, seed: "chosen:42" } };
    assert.equal(
      avatarDataURI(picked),
      avatarDataURI({ ...picked, nickname: "Changed name" }),
    );
    assert.notEqual(
      avatarDataURI({ ...picked, avatar: { style, seed: "" } }),
      avatarDataURI(picked),
    );
    assert.match(svg(picked), /^<svg /);
    assert.doesNotMatch(svg(picked), /<script|<image|<foreignObject|onload=/i);
  }
});

test("gallery choices are stable, paginated, unique seeds within API UTF-8 limits", () => {
  const first = avatarChoices("小明", 0);
  assert.equal(first.length, 12);
  assert.deepEqual(first, avatarChoices("小明", 0));
  const second = avatarChoices("小明", 1);
  assert.equal(new Set([...first, ...second]).size, 24);
  const images = first.map((seed) =>
    avatarDataURI({ ...user, avatar: { style: "clay", seed } }),
  );
  assert.equal(new Set(images).size, 12);
  for (const seed of avatarChoices("🐱".repeat(50) + "\n", 50)) {
    assert.ok(Buffer.byteLength(seed, "utf8") <= 128);
    assert.doesNotMatch(seed, /\p{Cc}/u);
    assert.doesNotThrow(() =>
      avatarDataURI({ ...user, avatar: { style: "pixel-art", seed } }),
    );
  }
});

test("Unicode names work and names cannot inject executable SVG", () => {
  for (const nickname of [
    "小明",
    "あきら",
    "🐱 Alex",
    '<script onload="alert(1)">',
  ]) {
    for (const [style] of avatarStyles) {
      const person = { ...user, nickname, avatar: { style, seed: "" } };
      assert.match(svg(person), /^<svg /);
      assert.doesNotMatch(svg(person), /<script|onload="alert|<foreignObject/i);
    }
  }
  assert.deepEqual(
    avatarConfig({ ...user, avatar: { style: "unknown", seed: "x" } }),
    defaultAvatar,
  );
});

test("retired styles use Clay while keeping picked or automatic seeds", () => {
  for (const style of ["bottts-neutral", "shapes", "initials"]) {
    for (const seed of ["", "chosen:42"]) {
      const person = { ...user, avatar: { style, seed } };
      assert.deepEqual(avatarConfig(person), { style: "clay", seed });
      assert.equal(
        avatarDataURI(person),
        avatarDataURI({ ...user, avatar: { style: "clay", seed } }),
      );
    }
  }
});

test("bundled styles retain their attribution and SVG license metadata", () => {
  for (const [style] of avatarStyles) {
    const meta = avatarAttribution(style);
    assert.ok(meta.creator.name);
    assert.ok(meta.source.name);
    assert.ok(meta.license.name);
    assert.match(meta.license.url, /^https:\/\//);
    assert.match(svg({ ...user, avatar: { style, seed: "" } }), /<metadata\b/);
  }
  assert.equal(avatarAttribution("croodles").creator.name, "vijay verma");
  assert.equal(avatarAttribution("toon-head").creator.name, "Johan Melin");
  assert.equal(avatarAttribution("croodles").license.name, "CC BY 4.0");
  assert.equal(avatarAttribution("toon-head").license.name, "CC BY 4.0");
});

test("avatar image uses a data URI and the picker exposes accessible selection controls", () => {
  const html = renderToStaticMarkup(
    createElement(UserAvatar, { person: user }),
  );
  assert.match(html, /src="data:image\/svg\+xml/);
  assert.match(html, /alt=""/);
  assert.doesNotMatch(html, /src="https?:/);
  const dialog = readFileSync(
    new URL("../src/avatar-dialog.tsx", import.meta.url),
    "utf8",
  );
  assert.match(dialog, /aria-labelledby="avatar-title"/);
  assert.match(dialog, /aria-label=\{t\("Close avatar picker"\)\}/);
  assert.match(dialog, /aria-label=\{t\("Avatar \{number\}"/);
  assert.match(dialog, /aria-pressed=\{draft.seed === seed\}/);
  assert.match(dialog, /Use my name/);
  assert.match(dialog, /disabled=\{saving \|\| !changed\}/);
});

test("online avatar stack shows up to three people and summarizes the rest", () => {
  const people = Array.from({ length: 6 }, (_, index) => ({
    id: `person-${index}`,
    nickname: `Person ${index}`,
    avatar: { style: "thumbs", seed: `picked-${index}` },
  }));
  const render = (people) =>
    renderToStaticMarkup(createElement(AvatarStack, { people }));
  assert.equal(render([]), "");
  const single = render(people.slice(0, 1));
  assert.equal((single.match(/<img /g) || []).length, 1);
  assert.doesNotMatch(single, /avatar-stack-more/);
  const three = render(people.slice(0, 3));
  assert.equal((three.match(/<img /g) || []).length, 3);
  assert.doesNotMatch(three, /avatar-stack-more/);
  const many = render(people);
  assert.equal((many.match(/<img /g) || []).length, 3);
  assert.match(many, /avatar-stack-more">\+3<\/span>/);
  assert.match(many, /class="avatar-stack" aria-hidden="true"/);
  assert.ok(
    many.indexOf('title="Person 0"') < many.indexOf('title="Person 1"'),
  );
  assert.ok(many.includes(avatarDataURI(people[0]).replaceAll("&", "&amp;")));
});
