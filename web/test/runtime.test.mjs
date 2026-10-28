import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  isDesktopChat,
  desktopSettingsPath,
  desktopLocale,
  desktopTheme,
  subscribeDesktopLocale,
  subscribeDesktopTheme,
  chatPreferences,
} from "../.test-build/runtime.js";
import { resolveLanguage, resolveTheme } from "../.test-build/preferences.js";
import { DesktopSettingsLink, ChatSettingsControl } from "../.test-build/components/web/src/desktop-settings.js";

test("desktop presentation requires the explicit preload flag, not a query or generic MyGo runtime", () => {
  assert.equal(isDesktopChat({ spoolDesktop: true }), true);
  for (const surface of [null, {}, { spoolDesktop: "true" }, { spoolDesktop: false }, { mygo: {} }, { location: { search: "?desktop=true" } }]) {
    assert.equal(isDesktopChat(surface), false);
  }
});

test("desktop skips PWA registration and install UI without changing browser behavior", async () => {
  const source = await readFile(new URL("../src/main.tsx", import.meta.url), "utf8");
  assert.match(source, /if \(isDesktopChat\(\)\) return;\s*const handler =/);
  assert.match(source, /setInstall\(e as BeforeInstallPromptEvent\)/);
  assert.match(source, /\.register\("\/sw\.js"\)/);
});

test("native settings link is desktop-only, localized, and stays in the same page", (t) => {
  const render = () => renderToStaticMarkup(createElement(DesktopSettingsLink, {
    t: (key) => key === "Settings" ? "设置" : key,
    className: "test-settings",
  }));
  assert.equal(render(), "");
  globalThis.spoolDesktop = true;
  t.after(() => { delete globalThis.spoolDesktop; });
  const html = render();
  assert.match(html, /class="icon desktop-settings test-settings desktop-settings-labeled"/);
  assert.ok(html.includes(`href="${desktopSettingsPath}"`));
  assert.match(html, /aria-label="设置"/);
  assert.match(html, /<span>设置<\/span>/);
  assert.doesNotMatch(html, /target=|onclick=|window\.open/);
});

test("native settings destination matches the desktop navigation guard", async () => {
  const source = await readFile(new URL("../../cmd/desktop/chat.go", import.meta.url), "utf8");
  assert.ok(source.includes(`const desktopSettingsPath = "${desktopSettingsPath}"`));
});

test("settings remains reachable from loading, sign-in and authenticated chat", async () => {
  const main = await readFile(new URL("../src/main.tsx", import.meta.url), "utf8");
  const sidebar = await readFile(new URL("../src/sidebar.tsx", import.meta.url), "utf8");
  assert.match(main, /className="loading"[\s\S]*?<DesktopSettingsLink[^>]*className="desktop-settings-corner"/);
  assert.match(main, /className=\{isDesktopChat\(\) \? "login desktop-login" : "login"\}[\s\S]*?<DesktopSettingsLink[^>]*className="desktop-settings-corner"/);
  assert.match(sidebar, /<ChatSettingsControl t=\{t\} onAppearance=\{props\.onSettings\} \/>\s*<button[\s\S]*?onClick=\{props\.onLogout\}/);
  assert.doesNotMatch(sidebar, /sidebar-settings|Palette|DesktopSettingsLink/);
  assert.match(main, /const appearance = !isDesktopChat\(\) && appearanceOpen/);
  assert.match(main, /if \(isDesktopChat\(\) \|\| savingPreferences\) return/);
  assert.match(main, /!isDesktopChat\(\) && \(\s*<button\s*className="icon login-preferences"/);
});

test("footer settings replaces appearance in desktop but preserves the browser control", (t) => {
  const render = () => renderToStaticMarkup(createElement(ChatSettingsControl, {
    t: (key) => key,
    onAppearance() { assert.fail("render must not open a dialog"); },
  }));
  const browser = render();
  assert.match(browser, /^<button[^>]*aria-label="Appearance and language"/);
  assert.doesNotMatch(browser, /href=/);
  globalThis.spoolDesktop = true;
  t.after(() => { delete globalThis.spoolDesktop; });
  const desktop = render();
  assert.ok(desktop.includes(`href="${desktopSettingsPath}"`));
  assert.match(desktop, /aria-label="Settings"/);
  assert.doesNotMatch(desktop, /<button|<span>|Appearance and language/);
  assert.doesNotMatch(desktop, /desktop-settings-labeled/);
});

test("desktop locale starts with the native setting and updates through MyGo events", () => {
  const listeners = new Set();
  const runtime = {
    locale: "ja-JP",
    on(event, listener) {
      assert.equal(event, "mygo:locale");
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
  const surface = { spoolDesktop: true, mygo: runtime };
  assert.equal(desktopLocale(surface), "ja");
  const seen = [];
  const off = subscribeDesktopLocale(() => seen.push(desktopLocale(surface)), surface);
  runtime.locale = "zh-Hans";
  for (const listener of listeners) listener();
  assert.deepEqual(seen, ["zh-CN"]);
  off();
  assert.equal(listeners.size, 0);
  assert.equal(desktopLocale({ mygo: runtime }), undefined);
  subscribeDesktopLocale(() => assert.fail("browser subscribed to native events"), { mygo: runtime })();
  assert.equal(listeners.size, 0);
  for (const mygo of [null, {}, { locale: "en" }, { locale: 7, on() {} }]) {
    const invalid = { spoolDesktop: true, mygo };
    assert.equal(desktopLocale(invalid), undefined);
    assert.doesNotThrow(() => subscribeDesktopLocale(() => {}, invalid)());
  }
});

test("embedded Windows appearance overrides stale media queries without affecting browsers", () => {
  const listeners = new Map();
  const surface = {
    spoolDesktop: true,
    addEventListener(name, listener) { listeners.set(name, listener); },
    removeEventListener(name, listener) {
      if (listeners.get(name) === listener) listeners.delete(name);
    },
  };
  assert.equal(desktopTheme(surface), undefined);
  const seen = [];
  const off = subscribeDesktopTheme(() => seen.push(desktopTheme(surface)), surface);
  surface.spoolDesktopTheme = "light";
  listeners.get("spool:appearance-changed")();
  surface.spoolDesktopTheme = "dark";
  listeners.get("spool:appearance-changed")();
  assert.deepEqual(seen, ["light", "dark"]);
  off();
  assert.equal(listeners.size, 0);
  surface.spoolDesktopTheme = "invalid";
  assert.equal(desktopTheme(surface), undefined);
  assert.equal(desktopTheme({ spoolDesktopTheme: "dark" }), undefined);
  subscribeDesktopTheme(() => assert.fail("browser subscribed to native theme"), { spoolDesktopTheme: "dark" })();
});

test("desktop ignores account and logged-out preferences while browser settings remain independent", () => {
  for (const theme of ["system", "dark", "light"]) {
    for (const language of ["system", "en", "zh-CN", "ja"]) {
      const account = { theme, language };
      const desktop = chatPreferences(account, "ja", { spoolDesktop: true });
      assert.deepEqual(desktop, { theme: "system", language: "ja" });
      assert.equal(resolveTheme(desktop.theme, true), "dark");
      assert.equal(resolveTheme(desktop.theme, false), "light");
      assert.equal(resolveLanguage(desktop.language, ["en-US"]), "ja");
      assert.equal(chatPreferences(account, "ja", {}), account);
      assert.deepEqual(account, { theme, language });
    }
  }
  assert.deepEqual(chatPreferences({ theme: "dark", language: "ja" }, undefined, { spoolDesktop: true }), {
    theme: "system", language: "system",
  });
});

test("presentation subscribes to native locale without waiting for login or reloading chat", async () => {
  const source = await readFile(new URL("../src/use-presentation.ts", import.meta.url), "utf8");
  assert.match(source, /useSyncExternalStore\(\s*subscribeDesktopLocale,\s*desktopLocale,/);
  assert.match(source, /useSyncExternalStore\(\s*subscribeDesktopTheme,\s*desktopTheme,/);
  assert.match(source, /chatPreferences\(preferences, nativeLocale\)/);
  assert.match(source, /resolveLanguage\(effective\.language, systemLanguages\)/);
  assert.match(source, /resolveTheme\(effective\.theme, systemDark\)/);
  assert.match(source, /nativeTheme \?\? resolveTheme/);
  assert.match(source, /setAPILanguage\(locale\)/);
});
