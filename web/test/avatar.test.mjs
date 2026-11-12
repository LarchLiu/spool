import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { Avatar } from "@dicebear/core";
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
const imageSVG = (person) => {
  const uri = avatarDataURI(person);
  assert.ok(uri.startsWith("data:image/svg+xml;charset=utf-8,"));
  return decodeURIComponent(uri.slice(uri.indexOf(",") + 1));
};

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

test("existing users get a stable local image from their display name", () => {
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

test("local avatar cache shares images across identities and display sizes", () => {
  const person = {
    ...user,
    avatar: { style: "clay", seed: "cache:shared-components" },
  };
  const uri = avatarDataURI(person);
  for (const size of [16, 28, 80]) {
    const html = renderToStaticMarkup(
      createElement(UserAvatar, {
        person: { ...person, id: `other-${size}`, nickname: "Renamed" },
        size,
      }),
    );
    assert.ok(html.includes(uri.replaceAll("&", "&amp;")));
    assert.match(html, new RegExp(`width="${size}" height="${size}"`));
  }
});

test("avatar cache keys use resolved style and seed and respect configuration changes", () => {
  const person = { ...user, nickname: "cache:automatic-name" };
  const automatic = avatarDataURI(person);
  assert.equal(
    avatarDataURI({ ...person, nickname: ` ${person.nickname} ` }),
    automatic,
  );
  assert.equal(
    avatarDataURI({ ...person, nickname: "", username: person.nickname }),
    automatic,
  );
  assert.equal(
    avatarDataURI({ ...person, avatar: { style: "initials", seed: "" } }),
    automatic,
  );
  const renamed = avatarDataURI({ ...person, nickname: "cache:renamed" });
  const picked = avatarDataURI({
    ...person,
    avatar: { style: "clay", seed: "cache:picked" },
  });
  const restyled = avatarDataURI({
    ...person,
    avatar: { style: "thumbs", seed: "cache:picked" },
  });
  assert.equal(new Set([automatic, renamed, picked, restyled]).size, 4);
  assert.equal(avatarDataURI(person), automatic);
});

test("local JS images remain byte-identical to all 24 Go and Android golden vectors", () => {
  const cases = JSON.parse(readFileSync(new URL("../../internal/avatar/testdata/web-fixtures.json", import.meta.url), "utf8"));
  assert.equal(cases.length, 24);
  for (const item of cases) {
    const svg = imageSVG({ ...user, avatar: { style: item.name, seed: item.seed } });
    assert.equal(svg, item.svg);
    assert.equal(createHash("sha256").update(svg).digest("hex"), item.sha256);
  }
});

test("local generation is reused and its LRU retains at most 256 image entries", () => {
  const original = Avatar.prototype.toDataUri;
  let calls = 0;
  Avatar.prototype.toDataUri = function () { calls++; return original.call(this); };
  const person = (i) => ({ ...user, avatar: { style: "clay", seed: `lru-local:${i}` } });
  try {
    for (let i = 0; i < 256; i++) avatarDataURI(person(i));
    assert.equal(calls, 256);
    avatarDataURI(person(0));
    assert.equal(calls, 256);
    avatarDataURI(person(256));
    assert.equal(calls, 257);
    avatarDataURI(person(0));
    assert.equal(calls, 257);
    avatarDataURI(person(1));
    assert.equal(calls, 258);
  } finally { Avatar.prototype.toDataUri = original; }
});

test("the local URI cache also evicts by its eight-MiB content budget", () => {
  const original = Avatar.prototype.toDataUri;
  let calls = 0;
  Avatar.prototype.toDataUri = () => { calls++; return "x".repeat(3 * 1024 * 1024); };
  const person = (i) => ({ ...user, avatar: { style: "clay", seed: `byte-budget:${i}` } });
  try {
    for (let i = 0; i < 3; i++) avatarDataURI(person(i));
    assert.equal(calls, 3);
    avatarDataURI(person(2)); assert.equal(calls, 3);
    avatarDataURI(person(0)); assert.equal(calls, 4);
  } finally { Avatar.prototype.toDataUri = original; }
});

test("Spool Bot reuses its static logo without requesting an avatar", () => {
  for (const size of [16, 28]) {
    const html = renderToStaticMarkup(
      createElement(UserAvatar, { person: { ...user, bot: true }, size }),
    );
    assert.match(html, /src="\/logo\.png"/);
  }
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
    assert.match(imageSVG(picked), /^<svg /);
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

test("Unicode and markup-like names remain safe local generation data", () => {
  for (const nickname of [
    "小明",
    "あきら",
    "🐱 Alex",
    '<script onload="alert(1)">',
  ]) {
    for (const [style] of avatarStyles) {
      const person = { ...user, nickname, avatar: { style, seed: "" } };
      const svg = imageSVG(person);
      assert.match(svg, /^<svg /);
      assert.doesNotMatch(svg, /<(?:script|image|foreignObject)\b|\sonload\s*=/i);
      assert.doesNotMatch(avatarDataURI(person), /<|>|[\x00-\x1f]/);
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

test("local styles retain their matching attribution in the picker", () => {
  for (const [style] of avatarStyles) {
    const meta = avatarAttribution(style);
    assert.ok(meta.creator.name);
    assert.ok(meta.source.name);
    assert.ok(meta.license.name);
    assert.match(meta.license.url, /^https:\/\//);
  }
  assert.equal(avatarAttribution("croodles").creator.name, "vijay verma");
  assert.equal(avatarAttribution("toon-head").creator.name, "Johan Melin");
  assert.equal(avatarAttribution("croodles").license.name, "CC BY 4.0");
  assert.equal(avatarAttribution("toon-head").license.name, "CC BY 4.0");
});

test("avatar images use a local data URI and decorative alt text", () => {
  const html = renderToStaticMarkup(
    createElement(UserAvatar, { person: user }),
  );
  assert.match(html, /src="data:image\/svg\+xml;charset=utf-8,/);
  assert.match(html, /alt=""/);
  assert.doesNotMatch(html, /src="https?:/);
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
