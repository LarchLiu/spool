import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { LoginStory } from "../.test-build/components/web/src/login-story.js";
import { ThemePicker } from "../.test-build/components/web/src/theme-picker.js";

const t = (key) => key;

test("login keeps the full web brand and groups all three lines of story copy", () => {
  const html = renderToStaticMarkup(createElement(LoginStory, { t }));
  assert.match(html, /class="brand"><img class="brand-mark"[^>]+\/>Spool/);
  assert.match(html, /class="tag">Local agent workspace<\/span>/);
  assert.match(
    html,
    /class="login-story-copy"><span class="eyebrow">Your projects\. Your agents\.<\/span><h1>Ideas, brought to life\.<\/h1><p>Return to your projects from any device\. Let your local agent keep going\.<\/p>/,
  );
  assert.match(html, /class="login-foot"/);
});

test("only desktop vertically centers the left-aligned copy and moves its brand below traffic lights", async () => {
  const css = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  const webStory = css.match(/^\.login-story \{([^}]+)\}/m)[1];
  assert.match(webStory, /display: flex;/);
  assert.match(webStory, /justify-content: space-between;/);
  assert.match(
    css,
    /\.desktop-login \.login-story \{[^}]*grid-template-rows: auto 1fr auto;/,
  );
  assert.match(
    css,
    /\.desktop-login \.login-story-copy \{\s*align-self: center;\s*\}/,
  );
  assert.match(
    css,
    /:root:has\(\.desktop-login\) \.desktop-window-brand \{\s*display: none;/,
  );
  assert.match(
    css,
    /:root\[data-desktop-platform="darwin"\] \.desktop-login \.login-story \{\s*padding-top: 82px;/,
  );
  assert.doesNotMatch(css, /\.login-story-copy\s*\{[^}]*text-align: center/);
});

test("desktop login footer matches its responsive horizontal inset without moving the main copy", async () => {
  const css = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  assert.match(
    css,
    /\.desktop-login \.login-story \{[^}]*--login-story-inset: 48px;[^}]*padding-bottom: 80px;[^}]*padding-inline: var\(--login-story-inset\)/,
  );
  assert.match(
    css,
    /\.desktop-login \.login-story > \.login-foot \{\s*top: calc\(80px - var\(--login-story-inset\)\);/,
  );
  for (const inset of [30, 28]) {
    assert.ok(css.includes(`--login-story-inset: ${inset}px;`));
  }
});

test("desktop login only moves its existing native settings link to the top-right", async () => {
  const css = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  const rule = css.match(
    /\.desktop-login > \.desktop-settings-corner \{([^}]+)\}/,
  )[1];
  for (const declaration of [
    "left: auto",
    "bottom: auto",
    "right: 20px",
    "top: 20px",
    "z-index: 13",
  ]) {
    assert.ok(rule.includes(declaration));
  }
});

test("theme icons expose translated labels and exactly one pressed selection", () => {
  for (const value of ["light", "dark", "system"]) {
    const html = renderToStaticMarkup(
      createElement(ThemePicker, {
        value,
        t: (key) => `translated:${key}`,
        onChange() {},
      }),
    );
    assert.match(html, /role="group" aria-label="translated:Theme"/);
    assert.equal((html.match(/<button/g) ?? []).length, 3);
    assert.equal((html.match(/aria-pressed="true"/g) ?? []).length, 1);
    assert.match(
      html,
      new RegExp(`data-theme-choice="${value}"[^>]*aria-pressed="true"`),
    );
    for (const label of ["Light", "Dark", "Follow system"]) {
      assert.ok(html.includes(`aria-label="translated:${label}"`));
      assert.ok(html.includes(`title="translated:${label}"`));
    }
    assert.equal((html.match(/aria-hidden="true"/g) ?? []).length, 3);
    assert.doesNotMatch(html, /<select|<option|<span/);
  }
});

test("each theme icon saves its choice, while clicking the selected icon does not save again", () => {
  for (const value of ["light", "dark", "system"]) {
    const changes = [];
    const picker = ThemePicker({
      value,
      t,
      onChange: (theme) => changes.push(theme),
    });
    for (const button of picker.props.children) button.props.onClick();
    assert.deepEqual(
      changes,
      ["light", "dark", "system"].filter((theme) => theme !== value),
    );
    assert.ok(
      picker.props.children.every((button) => button.props.type === "button"),
    );
  }
});
