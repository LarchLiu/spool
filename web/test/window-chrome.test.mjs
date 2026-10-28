import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { isMacDesktop } from "../.test-build/runtime.js";
import { DesktopWindowChrome } from "../.test-build/components/web/src/desktop-window-chrome.js";

test("custom Mac chrome requires both the desktop preload and the native macOS platform", () => {
  assert.equal(
    isMacDesktop({ spoolDesktop: true, mygo: { platform: "darwin" } }),
    true,
  );
  for (const surface of [
    null,
    {},
    { mygo: { platform: "darwin" } },
    { spoolDesktop: true },
    { spoolDesktop: true, mygo: null },
    { spoolDesktop: true, mygo: { platform: "win32" } },
    { spoolDesktop: true, mygo: { platform: "linux" } },
  ]) {
    assert.equal(isMacDesktop(surface), false);
  }
});

test("preload retains native insets before DOM creation and updates them for fullscreen", async () => {
  const source = await readFile(
    new URL("../../cmd/desktop/window_chrome.go", import.meta.url),
    "utf8",
  );
  const script = source.match(/const desktopWindowPreload = `([\s\S]*?)`/)[1];
  const listeners = new Map();
  const properties = new Map();
  const document = {
    documentElement: null,
    addEventListener(event, listener) {
      listeners.set(event, listener);
    },
  };
  const window = {
    mygo: {
      platform: "darwin",
      on(event, listener) {
        listeners.set(event, listener);
      },
    },
  };
  vm.runInNewContext(script, { window, document });
  assert.equal(window.spoolDesktop, true);
  assert.equal(
    Object.getOwnPropertyDescriptor(window, "spoolDesktop").writable,
    false,
  );
  const update = listeners.get("spool:titlebar");
  update({ left: 82 });
  const root = {
    dataset: {},
    style: {
      setProperty(name, value) {
        properties.set(name, value);
      },
    },
  };
  document.documentElement = root;
  listeners.get("DOMContentLoaded")();
  assert.equal(root.dataset.desktopPlatform, "darwin");
  assert.equal(properties.get("--desktop-controls-left"), "82px");
  update({ left: 0 });
  assert.equal(properties.get("--desktop-controls-left"), "0px");
  for (const value of [
    null,
    {},
    { left: "82" },
    { left: -1 },
    { left: Infinity },
  ])
    update(value);
  assert.equal(properties.get("--desktop-controls-left"), "0px");
  update({ left: 82 });
  assert.equal(properties.get("--desktop-controls-left"), "82px");
});

test("Windows preload forwards native appearance changes to embedded pages", async () => {
  const source = await readFile(
    new URL("../../cmd/desktop/window_chrome.go", import.meta.url),
    "utf8",
  );
  const script = source.match(/const desktopWindowPreload = `([\s\S]*?)`/)[1];
  const listeners = new Map();
  const notifications = [];
  const window = {
    mygo: {
      platform: "win32",
      on(event, listener) { listeners.set(event, listener); },
    },
    dispatchEvent(event) { notifications.push(event.type); },
  };
  vm.runInNewContext(script, { window, Event: class { constructor(type) { this.type = type; } } });
  assert.equal(window.spoolDesktop, true);
  assert.equal(window.spoolDesktopTheme, undefined);
  const update = listeners.get("spool:appearance");
  assert.equal(typeof update, "function");
  update("light");
  assert.equal(window.spoolDesktopTheme, "light");
  update("dark");
  assert.equal(window.spoolDesktopTheme, "dark");
  update("system");
  assert.equal(window.spoolDesktopTheme, "dark");
  assert.deepEqual(notifications, ["spool:appearance-changed", "spool:appearance-changed"]);
});

test("only Mac desktop renders a logo-only window brand with a native drag strip", (t) => {
  const render = () => renderToStaticMarkup(createElement(DesktopWindowChrome));
  assert.equal(render(), "");
  globalThis.spoolDesktop = true;
  globalThis.mygo = { platform: "win32" };
  t.after(() => {
    delete globalThis.spoolDesktop;
    delete globalThis.mygo;
  });
  assert.equal(render(), "");
  globalThis.mygo.platform = "darwin";
  const html = render();
  assert.match(html, /class="desktop-window-drag-strip" aria-hidden="true"/);
  assert.match(html, /class="desktop-window-brand" aria-hidden="true"/);
  assert.match(html, /<img src="\/logo.png" alt="" draggable="false"/);
  assert.doesNotMatch(html, /Spool|button|input/);
});

test("Mac chrome shares native control insets and does not intercept header buttons", async () => {
  const css = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  const chrome = await readFile(
    new URL("../../cmd/desktop/window_chrome.go", import.meta.url),
    "utf8",
  );
  const sidebar = await readFile(
    new URL("../src/sidebar.tsx", import.meta.url),
    "utf8",
  );
  const settings = await readFile(
    new URL("../src/settings.css", import.meta.url),
    "utf8",
  );
  assert.match(chrome, /window\.mygo\.on\("spool:titlebar"/);
  assert.match(chrome, /setProperty\("--desktop-controls-left"/);
  assert.match(
    css,
    /left: calc\(var\(--desktop-controls-left, 80px\) \+ 12px\)/,
  );
  assert.match(css, /\.desktop-window-brand \{[^}]*--app-region: drag/);
  assert.match(
    css,
    /:is\([^}]*button,[^}]*\[role="separator"\][^}]*\{\s*--app-region: no-drag/,
  );
  assert.match(sidebar, /!isMacDesktop\(\) && \([\s\S]*?Spool/);
  assert.match(
    settings,
    /:root\[data-desktop-platform="darwin"\] \.settings-sidebar-content \{\s*padding-top: 82px/,
  );
});
