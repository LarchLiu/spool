import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { readFile } from "node:fs/promises";
import { createTestViteServer } from "./vite-fixture.mjs";

// The SSR fixture imports browser code; Bun's navigator has no languages list.
const navigatorDescriptor = Object.getOwnPropertyDescriptor(globalThis, "navigator");
Object.defineProperty(globalThis, "navigator", {
  configurable: true,
  value: { languages: ["en"] },
});
test.after(() => {
  if (navigatorDescriptor) Object.defineProperty(globalThis, "navigator", navigatorDescriptor);
  else delete globalThis.navigator;
});

const server = await createTestViteServer(test, {
  root: new URL("..", import.meta.url).pathname,
  configFile: false,
  logLevel: "silent",
  appType: "custom",
  server: { middlewareMode: true, watch: null, hmr: false },
  esbuild: { jsx: "automatic" },
});
const { RemoteDesktopConnection, RemoteDesktopDialog } =
  await server.ssrLoadModule("/src/remote-desktop-settings.tsx");
const { VoxlSettings } = await server.ssrLoadModule("/src/voxl-settings.tsx");
const { configureVoxlFrame } = await server.ssrLoadModule("/src/voxl-frame.ts");
const { setCSRF } = await server.ssrLoadModule("/src/api.ts");
const { PeoplePage } = await server.ssrLoadModule("/src/settings-pages.tsx");

const t = (key, values = {}) =>
  key.replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? `{${name}}`));

test("client settings hide remote connection until the host authorizes availability", () => {
  for (const secure of [false, true]) {
    assert.equal(
      renderToStaticMarkup(
        createElement(RemoteDesktopConnection, { available: false, secure, t }),
      ),
      "",
    );
  }
  const html = renderToStaticMarkup(
    createElement(RemoteDesktopConnection, {
      available: true,
      secure: true,
      t,
    }),
  );
  assert.match(html, /Remote connection/);
  assert.match(html, /<button type="button">Connect to desktop<\/button>/);
  assert.match(html, /Connect to desktop/);
  assert.doesNotMatch(
    html,
    /iframe|href=|target=|Install|Enable remote desktop|token=|6901|\.cargo/,
  );
});

test("remote desktop opens its own dialog and creates a revocable view before embedding the native client", async () => {
  const html = renderToStaticMarkup(
    createElement(RemoteDesktopDialog, { t, onClose() {} }),
  );
  assert.match(html, /^<dialog class="remote-desktop-dialog"/);
  assert.match(html, /Disconnect desktop/);
  assert.match(html, /Connecting…/);
  assert.doesNotMatch(html, /appearance-dialog|target=|token=|6901/);
  const source = await readFile(
    new URL("../src/remote-desktop-settings.tsx", import.meta.url),
    "utf8",
  );
  assert.match(source, /\/desktop\/connect/);
  assert.match(source, /\/desktop\/disconnect\?token=/);
  assert.match(source, /src=\{`\/api\/desktop\/view\/\?token=/);
  assert.match(source, /allow="fullscreen"/);
  assert.doesNotMatch(
    source,
    /requestFullscreen|Exit fullscreen|Maximize|Minimize/,
  );
});

test("people management shows live per-user remote connection counts and a separate disconnect action", () => {
  const users = [
    { id: "alice", username: "alice", nickname: "Alice", remote_desktop: true },
    { id: "bob", username: "bob", nickname: "Bob", remote_desktop: true },
    {
      id: "charlie",
      username: "charlie",
      nickname: "Charlie",
      remote_desktop: false,
    },
  ];
  const html = renderToStaticMarkup(
    createElement(PeoplePage, {
      data: { users, status: { RemoteDesktopConnections: { alice: 2 } } },
      run() {},
      t,
    }),
  );
  const [aliceRow, bobRow, charlieRow] = html.match(/<li>.*?<\/li>/g);
  assert.match(aliceRow, /role="img" aria-label="Remote desktop allowed"/);
  assert.match(
    aliceRow,
    /class="settings-remote-count is-connected"[^>]*>2<\/span>/,
  );
  assert.match(
    aliceRow,
    /<button type="button" aria-label="Disconnect desktop · Alice">Disconnect desktop<\/button>/,
  );
  assert.doesNotMatch(
    aliceRow,
    /<button[^>]*>(?:(?!<\/button>)[\s\S])*<button/,
  );
  assert.match(bobRow, /class="settings-remote-count"[^>]*>0<\/span>/);
  assert.match(bobRow, /role="img" aria-label="Remote desktop allowed"/);
  assert.doesNotMatch(bobRow, /Disconnect desktop/);
  assert.doesNotMatch(
    charlieRow,
    /settings-remote-count|Remote desktop allowed|Disconnect desktop/,
  );
});

test("native voxl uploads carry the current Spool CSRF token only to the exact same-origin upload endpoint", () => {
  class XHR {
    headers = {};
    open() {
      this.headers = {};
    }
    send(body) {
      this.body = body;
    }
    setRequestHeader(key, value) {
      this.headers[key] = value;
    }
  }
  const styles = new Map();
  const frame = {
    contentDocument: {
      baseURI: "https://spool.example/api/desktop/view/",
      defaultView: {
        XMLHttpRequest: XHR,
        location: { origin: "https://spool.example" },
      },
      getElementById: (id) => styles.get(id),
      createElement: () => ({}),
      head: { append: (style) => styles.set(style.id, style) },
    },
  };
  configureVoxlFrame(frame);
  configureVoxlFrame(frame);
  assert.equal(styles.size, 1);
  const request = new XHR();
  setCSRF("fixture-token");
  request.open("PUT", "files/upload?name=hello.txt");
  request.send("fixture bytes");
  assert.equal(request.headers["X-CSRF-Token"], "fixture-token");
  assert.equal(request.body, "fixture bytes");
  setCSRF("refreshed-token");
  request.open("PUT", "/api/desktop/view/files/upload?name=hello.txt");
  request.send("new bytes");
  assert.equal(request.headers["X-CSRF-Token"], "refreshed-token");
  for (const [method, path] of [
    ["GET", "files/list"],
    ["PUT", "https://other.example/api/desktop/view/files/upload"],
    ["PUT", "/api/other/upload"],
  ]) {
    request.open(method, path);
    request.send("fixture");
    assert.equal(request.headers["X-CSRF-Token"], undefined);
  }
  setCSRF("");
});

test("insecure browser settings explain HTTPS without opening a broken stream", () => {
  const html = renderToStaticMarkup(
    createElement(RemoteDesktopConnection, {
      available: true,
      secure: false,
      t,
    }),
  );
  assert.match(html, /Remote desktop requires HTTPS or localhost/);
  assert.doesNotMatch(html, /href=/);
});

test("host settings expose local installation, preserve separate enablement and disable activation before installation", () => {
  const data = {
    settings: { remote_desktop_enabled: false },
    status: {
      Running: true,
      Voxl: {
        installed: false,
        installing: false,
        running: false,
        directory: "/isolated/.spool-dev/voxl",
      },
    },
  };
  const render = () =>
    renderToStaticMarkup(createElement(VoxlSettings, { data, run() {}, t }));
  let html = render();
  assert.match(html, /Install voxl/);
  assert.match(html, /aria-label="Enable remote desktop"[^>]*disabled/);
  assert.match(html, /\/isolated\/\.spool-dev\/voxl/);
  data.status.Voxl.installed = true;
  html = render();
  assert.doesNotMatch(html, />Install voxl</);
  assert.match(html, /aria-label="Enable remote desktop" aria-checked="false"/);
  data.settings.remote_desktop_enabled = true;
  html = render();
  assert.match(html, />Waiting for connection</);
  assert.match(html, /stops when the last connection closes/);
  assert.doesNotMatch(html, />Retry</);
  data.status.Voxl.running = true;
  html = render();
  assert.match(html, /aria-checked="true"/);
  assert.match(html, />Running</);
});

test("local installer shows dependency progress and translates errors with a missing dependency list", () => {
  const data = {
    settings: {},
    status: {
      Voxl: {
        installed: false,
        installing: true,
        running: false,
        directory: "/isolated/voxl",
        install_step: "Preparing Windows dependencies with vcpkg…",
      },
    },
  };
  const translated = (key) => `translated: ${key}`;
  const render = () => renderToStaticMarkup(createElement(VoxlSettings, { data, run() {}, t: translated }));
  let html = render();
  assert.match(html, /role="status">translated: Preparing Windows dependencies with vcpkg…/);
  assert.match(html, /disabled=""[^>]*>translated: Install voxl/);
  assert.match(html, /Homebrew/);
  assert.match(html, /Linux checks system build libraries/);
  data.status.Voxl.installing = false;
  data.status.Voxl.error = "voxl build dependencies are still unavailable after Homebrew installation.\nx264, opus";
  html = render();
  assert.match(html, /translated: voxl build dependencies are still unavailable after Homebrew installation\./);
  assert.match(html, /x264, opus/);
  assert.doesNotMatch(html, /translated: x264/);
});
