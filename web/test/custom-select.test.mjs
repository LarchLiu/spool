import assert from "node:assert/strict";
import test from "node:test";
import { readFile, readdir } from "node:fs/promises";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { CustomSelect } from "../.test-build/components/web/src/custom-select.js";
import { selectPopupLayout } from "../.test-build/components/web/src/custom-select-layout.js";
import { AppearanceDialog } from "../.test-build/components/web/src/appearance-dialog.js";

const options = [
  { value: "", label: "Default" },
  { value: "en", label: "English" },
  { value: "ja", label: "日本語" },
];
const render = (props = {}) =>
  renderToStaticMarkup(
    createElement(CustomSelect, {
      value: "en",
      options,
      label: "Language",
      onChange() {},
      ...props,
    }),
  );

test("custom selection exposes its label, selected value and listbox semantics", () => {
  const html = render();
  assert.match(html, /type="button"/);
  assert.match(html, /aria-label="Language"/);
  assert.match(html, /aria-haspopup="listbox"/);
  assert.match(html, /aria-expanded="false"/);
  assert.match(html, />English<\/span>/);
  assert.doesNotMatch(html, /<select|<option|<ul/);
  assert.match(render({ value: "" }), />Default<\/span>/);
  assert.match(render({ value: "unknown" }), />unknown<\/span>/);
});

test("custom selection preserves disabled/empty states and escapes option text", () => {
  assert.match(render({ disabled: true }), /disabled=""/);
  assert.match(render({ options: [] }), /disabled=""/);
  assert.doesNotMatch(render(), /disabled=""/);
  assert.match(
    render({ options: [{ value: "en", label: '<img src=x onerror="oops">' }] }),
    /&lt;img/,
  );
});

test("appearance language uses the shared control and preserves saving state", () => {
  const html = renderToStaticMarkup(
    createElement(AppearanceDialog, {
      preferences: { theme: "dark", language: "ja" },
      saving: true,
      version: "test",
      t: (key) => key,
      onChange() {},
      onClose() {},
    }),
  );
  assert.match(
    html,
    /class="custom-select-trigger"[^>]*aria-label="Language"[^>]*disabled=""/,
  );
  assert.match(html, />日本語<\/span>/);
  assert.doesNotMatch(html, /<select|<option/);
});

test("all frontend views forbid browser-native dropdowns", async () => {
  for (const name of await readdir(new URL("../src/", import.meta.url))) {
    if (!/\.[jt]sx?$/.test(name)) continue;
    const source = await readFile(
      new URL(`../src/${name}`, import.meta.url),
      "utf8",
    );
    assert.doesNotMatch(
      source,
      /<(?:select|option|optgroup|datalist)\b|createElement\(["']select["']/,
      name,
    );
  }
});

test("packaged desktop smoke follows the custom language control instead of a native select", async () => {
  const source = await readFile(
    new URL("../../cmd/desktop/smoke.go", import.meta.url),
    "utf8",
  );
  const appearance = source.slice(
    source.indexOf("func (p *panel) probeThemePicker("),
    source.indexOf("func (p *panel) probeManagementLists("),
  );
  assert.match(
    appearance,
    /\.custom-select-trigger\[aria-haspopup="listbox"\]/,
  );
  for (const [, selector] of appearance.matchAll(
    /querySelector(?:All)?\('([^']*)'\)/g,
  )) {
    assert.ok(selector !== "select" && !selector.endsWith(" select"), selector);
  }
  assert.match(appearance, /\[role="option"\]\[data-value=/);
  assert.match(appearance, /snapshot\.preferences\.language === %q/);
  assert.match(appearance, /document\.documentElement\.lang === %q/);
  assert.match(appearance, /appearance render: %w/);
});

const view = { x: 0, y: 0, width: 390, height: 700 };
test("popup aligns below the anchor and flips above near the viewport bottom", () => {
  const anchor = { left: 20, top: 100, bottom: 134, right: 188, width: 168 };
  assert.deepEqual(selectPopupLayout(anchor, view, 4), {
    left: 20,
    top: 138,
    width: 180,
    maxHeight: 154,
    side: "bottom",
  });
  assert.deepEqual(
    selectPopupLayout({ ...anchor, top: 620, bottom: 654 }, view, 4),
    {
      left: 20,
      top: 616,
      width: 180,
      maxHeight: 154,
      side: "top",
    },
  );
});

test("popup caps long menus and never overflows horizontally on narrow or offset viewports", () => {
  const anchor = { left: 350, top: 50, bottom: 84, right: 520, width: 170 };
  assert.deepEqual(selectPopupLayout(anchor, view, 100), {
    left: 202,
    top: 88,
    width: 180,
    maxHeight: 280,
    side: "bottom",
  });
  const tiny = { x: 30, y: 80, width: 160, height: 220 };
  const layout = selectPopupLayout(
    { ...anchor, top: 200, bottom: 234 },
    tiny,
    50,
  );
  assert.equal(layout.left, 38);
  assert.equal(layout.width, 144);
  assert.equal(layout.side, "top");
  assert.equal(layout.maxHeight, 108);
});
