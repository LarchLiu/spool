import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const smoke = await readFile(new URL("../cmd/desktop/smoke.go", import.meta.url), "utf8");
const helper = smoke.match(/const smokeSettingsReadyScript = `([^`]+)`/)?.[1];
assert.ok(helper, "native smoke must expose its actual settings readiness predicate");

function fixture() {
  const outer = { disabled: false };
  const pages = new Map();
  for (const section of ["People", "Projects", "Agents"]) {
    const controls = { disabled: false };
    const back = { disabled: false };
    controls.matches = (selector) => {
      assert.equal(selector, ":disabled");
      return controls.disabled || outer.disabled;
    };
    back.matches = (selector) => {
      assert.equal(selector, ":disabled");
      return back.disabled || controls.matches(selector);
    };
    pages.set(section, {
      hidden: false,
      controls,
      back,
      querySelector(selector) {
        if (selector === ".settings-page-controls") return this.controls;
        if (selector === ".settings-back") return this.back;
        return null;
      },
    });
  }
  const window = {};
  vm.runInNewContext(helper, {
    window,
    document: {
      querySelector(selector) {
        const section = selector.match(/^\[data-page="([^"]+)"\]$/)?.[1];
        return pages.get(section) ?? null;
      },
    },
  });
  return { outer, pages, ready: window.smokeSettingsReady };
}

test("persisted settings do not make Back ready before the page save acknowledgement", () => {
  for (const section of ["People", "Projects", "Agents"]) {
    const { outer, pages, ready } = fixture();
    const page = pages.get(section);
    assert.equal(ready(section, ".settings-back"), true);
    page.controls.disabled = true;
    // Saving has reached native storage, but React still awaits the bridge reply.
    assert.equal(outer.disabled, false);
    assert.equal(page.back.disabled, false);
    assert.equal(ready(section), false);
    assert.equal(ready(section, ".settings-back"), false);
    page.controls.disabled = false;
    assert.equal(ready(section, ".settings-back"), true);
  }
});

test("readiness checks missing, hidden and effectively disabled controls without blocking other pages", () => {
  const { outer, pages, ready } = fixture();
  const people = pages.get("People");
  assert.equal(ready("Missing"), false);
  assert.equal(ready("People", ".missing-button"), false);
  people.hidden = true;
  assert.equal(ready("People"), false);
  people.hidden = false;
  people.back.disabled = true;
  assert.equal(ready("People"), true);
  assert.equal(ready("People", ".settings-back"), false);
  people.back.disabled = false;
  people.controls.disabled = true;
  assert.equal(ready("People"), false);
  assert.equal(ready("Projects", ".settings-back"), true);
  people.controls = null;
  assert.equal(ready("People"), false);
  outer.disabled = true;
  assert.equal(ready("Projects", ".settings-back"), false);
});

test("native smoke never treats the outer fieldset as a page save acknowledgement", async () => {
  const notifications = await readFile(new URL("../cmd/desktop/notifications_smoke.go", import.meta.url), "utf8");
  assert.doesNotMatch(smoke + notifications, /querySelector\(['"]\.settings-content['"]\)\??\.disabled/);
  assert.match(smoke, /smokeSettingsReady\(%q, '\.settings-back'\)/);
  assert.match(smoke, /const result = await call\.call\(this, method, \.\.\.args\);\s+await new Promise/);
});
