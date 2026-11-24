import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createTestViteServer } from "./vite-fixture.mjs";

// The SSR fixture imports browser code; Bun's navigator has no languages list.
const navigatorDescriptor = Object.getOwnPropertyDescriptor(
  globalThis,
  "navigator",
);
Object.defineProperty(globalThis, "navigator", {
  configurable: true,
  value: { languages: ["en"] },
});
test.after(() => {
  if (navigatorDescriptor)
    Object.defineProperty(globalThis, "navigator", navigatorDescriptor);
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
const { RemoteDesktopConnection, RemoteDesktopViewer } =
  await server.ssrLoadModule("/src/remote-desktop-settings.tsx");
const {
  createRemoteDesktopWindow,
  remoteDesktopWindowPath,
  remoteDesktopClosePath,
} = await server.ssrLoadModule("/src/remote-desktop-window.ts");
const { VoxlSettings } = await server.ssrLoadModule("/src/voxl-settings.tsx");
const { configureVoxlFrame } = await server.ssrLoadModule("/src/voxl-frame.ts");
const { setCSRF } = await server.ssrLoadModule("/src/api.ts");
const { PeoplePage, OnlinePage } = await server.ssrLoadModule("/src/settings-pages.tsx");

const t = (key, values = {}) =>
  key.replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? `{${name}}`));

test("people and online settings distinguish external people by their source server", () => {
  const visitor = {
    id: "visitor", username: "external:visitor", nickname: "Alice",
    source_server_id: "source-id", source_server_name: "Office",
    state: "online", devices: 1,
  };
  const data = { users: [visitor, { id: "local", username: "alice", nickname: "Alice" }], status: { Online: [visitor] } };
  const people = renderToStaticMarkup(createElement(PeoplePage, { data, run() {}, t }));
  assert.match(people, /<strong>Alice · Office<\/strong>/);
  assert.match(people, /<strong>Alice<\/strong>/);
  assert.match(people, />Federated sign-in<\/span>/);
  const online = renderToStaticMarkup(createElement(OnlinePage, { data, t }));
  assert.match(online, /Alice · Office/);
});

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

test("remote desktop renders a standalone viewer without a modal over chat", () => {
  const html = renderToStaticMarkup(
    createElement(RemoteDesktopViewer, { t, onClose() {} }),
  );
  assert.match(html, /^<main class="remote-desktop-window"/);
  assert.match(html, /Disconnect desktop/);
  assert.match(html, /Connecting…/);
  assert.doesNotMatch(html, /<dialog|role="dialog"|target=|token=|6901/);
});

function windowFixture(native = false) {
  const surface = new EventTarget();
  const navigations = [],
    opened = [],
    statuses = [];
  let blocked = 0;
  surface.location = {
    origin: "https://spool.example",
    assign: (path) => navigations.push(path),
  };
  if (native) surface.spoolDesktop = true;
  surface.open = (...args) => {
    const viewer = {
      closed: false,
      focused: 0,
      focus() {
        this.focused++;
      },
      close() {
        this.closed = true;
      },
    };
    opened.push({ args, viewer });
    return viewer;
  };
  const controller = createRemoteDesktopWindow(
    surface,
    (value) => statuses.push(value),
    () => blocked++,
    () => "Build server",
  );
  const message = (source, origin = surface.location.origin) => {
    const event = new Event("message");
    Object.assign(event, {
      source,
      origin,
      data: { type: "spool:remote-desktop-connected", token: "fixture-view" },
    });
    surface.dispatchEvent(event);
  };
  return {
    surface,
    controller,
    navigations,
    opened,
    statuses,
    message,
    blocked: () => blocked,
  };
}

test("browser remote windows reuse the live viewer and accept status only from that viewer", () => {
  const f = windowFixture();
  try {
    f.controller.open();
    assert.equal(f.opened.length, 1);
    assert.equal(
      f.opened[0].args[0],
      "/remote-desktop.html?server=Build+server",
    );
    assert.match(f.opened[0].args[2], /popup/);
    const viewer = f.opened[0].viewer;
    f.message({}, "https://spool.example");
    f.message(viewer, "https://evil.example");
    assert.equal(f.statuses.at(-1), false);
    f.message(viewer);
    assert.equal(f.statuses.at(-1), true);
    f.controller.open();
    assert.equal(f.opened.length, 1);
    assert.equal(viewer.focused, 1);
    viewer.close();
    f.controller.open();
    assert.equal(f.opened.length, 2);
    assert.equal(f.statuses.at(-1), false);
    f.message(viewer);
    assert.equal(f.statuses.at(-1), false);
  } finally {
    f.controller.dispose();
  }
  assert.equal(f.opened[1].viewer.closed, true);
  f.message(f.opened[1].viewer);
  assert.equal(f.statuses.at(-1), false);
});

test("closing a browser viewer clears its connection indicator", async () => {
  const f = windowFixture();
  try {
    f.controller.open();
    f.message(f.opened[0].viewer);
    f.opened[0].viewer.close();
    await new Promise((resolve) => setTimeout(resolve, 600));
    assert.equal(f.statuses.at(-1), false);
  } finally {
    f.controller.dispose();
  }
});

test("leaving chat closes its viewer and revokes the connection before the page unloads", () => {
  const fetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = (url, options) => {
    requests.push({ url, options });
    return Promise.resolve(new Response("{}"));
  };
  setCSRF("fixture-csrf");
  const f = windowFixture();
  try {
    f.controller.open();
    f.message(f.opened[0].viewer);
    f.surface.dispatchEvent(new Event("pagehide"));
    assert.equal(f.opened[0].viewer.closed, true);
    assert.equal(f.statuses.at(-1), false);
    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, "/api/desktop/disconnect?token=fixture-view");
    assert.equal(requests[0].options.method, "POST");
    assert.equal(requests[0].options.headers["X-CSRF-Token"], "fixture-csrf");
    assert.equal(requests[0].options.keepalive, true);
    f.message(f.opened[0].viewer);
    assert.equal(f.statuses.at(-1), false);
    // A restored page can connect again through the same controller.
    f.controller.open();
    f.message(f.opened[1].viewer);
    assert.equal(f.statuses.at(-1), true);
    f.controller.dispose();
    assert.equal(f.opened[1].viewer.closed, true);
    assert.equal(requests.length, 2);
    const statuses = f.statuses.length;
    f.surface.dispatchEvent(new Event("pagehide"));
    assert.equal(f.statuses.length, statuses);
    assert.equal(requests.length, 2);
  } finally {
    f.controller.dispose();
    setCSRF("");
    globalThis.fetch = fetch;
  }
});

test("blocked browser popups report an error without claiming a connection", () => {
  const f = windowFixture();
  f.surface.open = () => null;
  try {
    f.controller.open();
    assert.equal(f.blocked(), 1);
    assert.equal(f.statuses.at(-1), false);
  } finally {
    f.controller.dispose();
  }
});

test("desktop remote windows use native presentation and follow native connection events", () => {
  const f = windowFixture(true);
  f.controller.open();
  assert.deepEqual(f.navigations, [remoteDesktopWindowPath]);
  assert.equal(f.opened.length, 0);
  const event = new Event("spool:remote-desktop");
  event.detail = { connected: true, token: "native-view" };
  f.surface.dispatchEvent(event);
  assert.equal(f.statuses.at(-1), true);
  f.controller.dispose();
  assert.deepEqual(f.navigations, [
    remoteDesktopWindowPath,
    remoteDesktopClosePath,
  ]);
  assert.equal(f.statuses.at(-1), false);
});

test("people management shows live per-user remote connection counts and a separate disconnect action", () => {
  const users = [
    { id: "alice", username: "alice", nickname: "Alice", remote_desktop: true },
    { id: "bob", username: "bob", nickname: "Bob", remote_desktop: true },
    {
      id: "charlie",
      username: "external:charlie",
      nickname: "Charlie",
      source_server_id: "source-server-id",
      disabled: true,
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
  assert.match(charlieRow, />Federated sign-in · Disabled<\/span>/);
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
  assert.match(html, /role="status">Not installed</);
  assert.doesNotMatch(html, />Upgrade voxl</);
  assert.doesNotMatch(html, />Update voxl</);
  assert.match(html, /aria-label="Enable remote desktop"[^>]*disabled/);
  assert.match(html, /\/isolated\/\.spool-dev\/voxl/);
  data.status.Voxl.upgrade_available = true;
  data.settings.remote_desktop_enabled = true;
  html = render();
  assert.match(html, /role="status">Upgrade required</);
  assert.match(html, /<button type="button">Upgrade voxl<\/button>/);
  assert.doesNotMatch(
    html,
    />Not installed<|>Install voxl<|>Waiting for connection</,
  );
  assert.match(html, /aria-label="Enable remote desktop"[^>]*disabled/);
  data.settings.remote_desktop_enabled = false;
  data.status.Voxl.installed = true;
  html = render();
  assert.match(html, /role="status">Installed</);
  assert.match(html, /<button type="button">Upgrade voxl<\/button>/);
  assert.doesNotMatch(html, />Not installed<|>Install voxl</);
  assert.match(html, /aria-label="Enable remote desktop" aria-checked="false"/);
  assert.doesNotMatch(html, /aria-label="Enable remote desktop"[^>]*disabled/);
  data.settings.remote_desktop_enabled = true;
  html = render();
  assert.match(html, /role="status">Waiting for connection</);
  assert.match(html, /<button type="button">Upgrade voxl<\/button>/);
  data.status.Voxl.running = true;
  html = render();
  assert.match(html, /role="status">Running</);
  assert.match(
    html,
    /<button type="button" disabled="">Upgrade voxl<\/button>/,
  );
  assert.doesNotMatch(html, /aria-label="Enable remote desktop"[^>]*disabled/);
  data.status.Voxl.running = false;
  data.status.Voxl.installing = true;
  html = render();
  assert.match(html, /role="status">Upgrading voxl…</);
  assert.match(
    html,
    /<button type="button" disabled="">Upgrade voxl<\/button>/,
  );
  data.status.Voxl.installing = false;
  data.status.Voxl.upgrade_available = false;
  data.settings.remote_desktop_enabled = false;
  html = render();
  assert.doesNotMatch(html, />Install voxl<|>Upgrade voxl</);
  assert.doesNotMatch(html, />Update voxl</);
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
  assert.doesNotMatch(html, />Update voxl<|>Upgrade voxl</);
  data.status.Voxl.running = false;
  data.status.Voxl.upgrade_available = true;
  data.status.Voxl.installing = true;
  data.status.Voxl.install_step = "Checking for a newer voxl release…";
  html = render();
  assert.match(html, /role="status">Checking for a newer voxl release…/);
  assert.match(
    html,
    /<button type="button" disabled="">Upgrade voxl<\/button>/,
  );
  assert.doesNotMatch(html, />Update voxl<|>Install voxl</);
  assert.match(html, /aria-label="Enable remote desktop"[^>]*disabled/);
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
  const render = () =>
    renderToStaticMarkup(
      createElement(VoxlSettings, { data, run() {}, t: translated }),
    );
  let html = render();
  assert.match(
    html,
    /role="status">translated: Preparing Windows dependencies with vcpkg…/,
  );
  assert.match(html, /disabled=""[^>]*>translated: Install voxl/);
  assert.match(html, /Homebrew/);
  assert.match(html, /Linux checks system build libraries/);
  data.status.Voxl.installing = false;
  data.status.Voxl.error =
    "voxl build dependencies are still unavailable after Homebrew installation.\nx264, opus";
  html = render();
  assert.match(
    html,
    /translated: voxl build dependencies are still unavailable after Homebrew installation\./,
  );
  assert.match(html, /x264, opus/);
  assert.doesNotMatch(html, /translated: x264/);
  assert.ok(
    html.indexOf('role="alert"') < html.indexOf("Enable remote desktop"),
  );
  assert.ok(html.indexOf('role="alert"') < html.indexOf("Component licenses"));
  assert.match(html, /run install\.sh for WebKitGTK/);
  assert.match(
    html,
    /temporary toolchain and removes it after the build without changing your toolchain/,
  );
});
