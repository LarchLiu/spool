import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { UpdateCheckInterval, UpdateIndicator } from "../.test-build/components/web/src/update-controls.js";
import { AppearanceDialog } from "../.test-build/components/web/src/appearance-dialog.js";

const t = (key, values = {}) => key.replace(/\{([^}]+)\}/g, (_, name) => String(values[name] ?? `{${name}}`));
const intervals = [30, 60, 360, 720, 1440];

test("the update interval has exactly five choices and saves only a changed selection", () => {
  for (const value of intervals) {
    const changes = [];
    const props = { value, disabled: false, onChange: (next) => changes.push(next), t };
    const html = renderToStaticMarkup(createElement(UpdateCheckInterval, props));
    assert.match(html, /role="group" aria-label="Check interval"/);
    assert.equal((html.match(/<button/g) ?? []).length, 5);
    assert.equal((html.match(/aria-pressed="true"/g) ?? []).length, 1);
    for (const button of UpdateCheckInterval(props).props.children) button.props.onClick();
    assert.deepEqual(changes, intervals.filter((interval) => interval !== value));
  }
});

test("automatic checks can be disabled without losing the chosen interval", () => {
  const html = renderToStaticMarkup(createElement(UpdateCheckInterval, {
    value: 1440, disabled: true, onChange() {}, t,
  }));
  assert.equal((html.match(/disabled=""/g) ?? []).length, 5);
  assert.match(html, /aria-pressed="true"[^>]*>24 hours<\/button>/);
});

test("update indicators expose a localized meaning", () => {
  const html = renderToStaticMarkup(createElement(UpdateIndicator, { t: (key) => `translated:${key}` }));
  assert.match(html, /role="img" aria-label="translated:Update available"/);
});

test("browser settings show the running version rather than the SQLite hint, even while saving", () => {
  for (const saving of [true, false]) {
    const html = renderToStaticMarkup(createElement(AppearanceDialog, {
      preferences: { theme: "system", language: "en" },
      saving, version: "9.8.7", onChange() {}, onClose() {}, t,
    }));
    assert.match(html, /<p>Current version: 9\.8\.7<\/p>/);
    assert.doesNotMatch(html, /SQLite|Changes apply immediately/);
  }
});
