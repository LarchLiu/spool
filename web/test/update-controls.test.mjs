import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { UpdateCheckInterval, UpdateIndicator } from "../.test-build/components/web/src/update-controls.js";
import { AppearanceDialog } from "../.test-build/components/web/src/appearance-dialog.js";
import { settingsSections } from "../.test-build/settings-client.js";

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

test("project permissions and update intervals share segmented buttons with theme-colored selection", async () => {
  const css = await readFile(new URL("../src/settings.css", import.meta.url), "utf8");
  assert.match(css, /\.settings-check-interval,\s*\.settings-member-access \{[^}]*display: inline-flex;[^}]*background: var\(--soft\)/);
  assert.match(css, /\.settings-check-interval button,\s*\.settings-member-access button \{[^}]*background: transparent;[^}]*color: var\(--muted\)/);
  assert.match(css, /\.settings-check-interval button\[aria-pressed="true"\],\s*\.settings-member-access button\[aria-pressed="true"\] \{[^}]*background: var\(--elevated\);[^}]*color: var\(--color-primary\)/);
});

test("update indicators use a shared primary-color dot and expose a localized meaning", async () => {
  const html = renderToStaticMarkup(createElement(UpdateIndicator, { t: (key) => `translated:${key}` }));
  assert.match(html, /role="img" aria-label="translated:Update available"/);
  const css = await readFile(new URL("../src/style.css", import.meta.url), "utf8");
  assert.match(css, /\.update-indicator \{[^}]*background: var\(--color-primary\)/);
  assert.match(css, /\.desktop-settings > \.update-indicator \{[^}]*position: absolute;[^}]*top: 3px;[^}]*right: 3px/);
});

test("About replaces Updates, marks pending updates and ends with a safe repository icon link", async () => {
  assert.equal(settingsSections.at(-1), "About");
  assert.equal(settingsSections.includes("Updates"), false);
  const app = await readFile(new URL("../src/settings-app.tsx", import.meta.url), "utf8");
  assert.match(app, /key === "About" &&\s*data\?\.updates\.Enabled &&\s*data\.updates\.Version &&\s*<UpdateIndicator/);
  assert.match(app, /About: AboutPage/);
  assert.match(app, /data-page=\{key\} hidden=\{section !== key\}/);
  const pages = await readFile(new URL("../src/settings-pages.tsx", import.meta.url), "utf8");
  assert.match(pages, /export function AboutPage/);
  assert.match(pages, /<Row title=\{t\("Check interval"\)\}>\s*<UpdateCheckInterval/);
  assert.doesNotMatch(pages, /<Row title=\{t\("Check interval"\)\} wide>/);
  assert.match(pages, /\.\.\.\(latest \?\? data\)\.update_preferences/);
  assert.match(pages, /<Row title=\{t\("GitHub repository"\)\} help="LarchLiu\/spool">[\s\S]*?href="https:\/\/github.com\/LarchLiu\/spool"[\s\S]*?target="_blank"\s*rel="noopener noreferrer"[\s\S]*?<Github[^>]+\/>[\s\S]*?<\/Card>\s*<\/>\s*\);\s*\}/);
});

test("browser settings show the running version rather than the SQLite hint, even while saving", async () => {
  for (const saving of [true, false]) {
    const html = renderToStaticMarkup(createElement(AppearanceDialog, {
      preferences: { theme: "system", language: "en" },
      saving, version: "9.8.7", onChange() {}, onClose() {}, t,
    }));
    assert.match(html, /<p>Current version: 9\.8\.7<\/p>/);
    assert.doesNotMatch(html, /SQLite|Changes apply immediately/);
  }
  const main = await readFile(new URL("../src/main.tsx", import.meta.url), "utf8");
  assert.match(main, /api<\{ version: string \}>\("\/version"\)/);
  assert.match(main, /<AppearanceDialog[\s\S]*?version=\{version\}/);
});
