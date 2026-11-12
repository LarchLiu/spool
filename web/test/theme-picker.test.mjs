import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ThemePicker } from "../.test-build/components/web/src/theme-picker.js";

const t = (key) => key;

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
