import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  desktopServers,
  subscribeDesktopServers,
  selectDesktopServer,
  notifyDesktopServerState,
  reportDesktopServerState,
  notifyDesktopServerName,
  connectDesktopServer,
  subscribeDesktopServerSelection,
  subscribeDesktopServerReadyAck,
  notifyDesktopServerReady,
  notifyDesktopSidebarWidth,
  desktopSidebarWidth,
} from "../.test-build/runtime.js";
import { serverDialogCall } from "../.test-build/server-dialog-client.js";
import {
  DesktopServerAction,
  DesktopServerTabs,
} from "../.test-build/components/web/src/desktop-servers.js";

test("footer actions match Android connection labels and accessible icon controls", () => {
  for (const [state, label, icon] of [
    ["disconnected", "Connect", "plug"],
    ["login", "Sign in", "plug"],
    ["error", "Retry", "refresh-cw"],
    ["connecting", "Connecting…", "loader-circle"],
  ]) {
    const props = { t: (key) => key, state, onConnect() {} };
    const text = renderToStaticMarkup(
      createElement(DesktopServerAction, props),
    );
    assert.ok(text.includes(`aria-label="${label}"`));
    assert.ok(text.includes(`>${label}</button>`));
    assert.equal(text.includes('disabled=""'), state === "connecting");
    const button = renderToStaticMarkup(
      createElement(DesktopServerAction, { ...props, iconOnly: true }),
    );
    assert.ok(button.includes(`title="${label}"`));
    assert.ok(button.includes(`lucide-${icon}`));
  }
  assert.equal(
    renderToStaticMarkup(
      createElement(DesktopServerAction, {
        t: (key) => key,
        state: "connected",
        onConnect() {},
      }),
    ),
    "",
  );
});

test("server tabs receive native display names and preserve the selected server and status", (t) => {
  const snapshot = {
    entries: [
      { id: "local", name: "本机的名称", local: true, status: "connected" },
      {
        id: "remote-1234",
        name: "自定义 & <Home>",
        local: false,
        status: "login",
      },
    ],
    selected: "remote-1234",
  };
  assert.equal(
    desktopServers({ spoolDesktop: true, spoolServers: snapshot }),
    snapshot,
  );
  globalThis.spoolDesktop = true;
  globalThis.spoolServers = snapshot;
  t.after(() => {
    delete globalThis.spoolDesktop;
    delete globalThis.spoolServers;
  });
  const html = renderToStaticMarkup(
    createElement(DesktopServerTabs, { t: (key) => key }),
  );
  assert.match(html, /本机的名称/);
  assert.match(html, /自定义 &amp; &lt;Home&gt;/);
  assert.match(
    html,
    /aria-current="page"[^>]*title="自定义 &amp; &lt;Home&gt; · Sign-in required"/,
  );
  assert.match(html, /href="\/__desktop\/servers"[^>]*aria-label="Add server"/);
  assert.doesNotMatch(html, /Local server/);
  delete globalThis.spoolDesktop;
  assert.equal(
    renderToStaticMarkup(createElement(DesktopServerTabs, { t: (key) => key })),
    "",
  );
});

test("server snapshots and subscriptions reject malformed/browser surfaces", () => {
  for (const value of [
    null,
    {},
    { selected: "local", entries: [null] },
    {
      selected: "local",
      entries: [{ id: "local", name: {}, local: true, status: "connected" }],
    },
  ]) {
    assert.deepEqual(
      desktopServers({ spoolDesktop: true, spoolServers: value }).entries,
      [],
    );
  }
  const events = new Map();
  const surface = {
    spoolDesktop: true,
    addEventListener: (event, listener) => events.set(event, listener),
    removeEventListener: (event) => events.delete(event),
  };
  let received = 0;
  const off = subscribeDesktopServers(() => received++, surface);
  events.get("spool:servers-changed")();
  assert.equal(received, 1);
  off();
  assert.equal(events.size, 0);
});

test("selection and metadata use display-only navigation hints", () => {
  const calls = [];
  const surface = {
    spoolDesktop: true,
    location: { assign: (url) => calls.push(url) },
  };
  selectDesktopServer("remote-1234", surface);
  notifyDesktopServerState("connected", surface);
  notifyDesktopServerName("家里的 & work", surface);
  connectDesktopServer(surface);
  notifyDesktopServerReady("login", "local:0", surface);
  notifyDesktopSidebarWidth(328, surface);
  assert.deepEqual(calls, [
    "/__desktop/servers?id=remote-1234&scroll_left=0",
    "/__desktop/server-state?state=connected",
    "/__desktop/server-name?name=%E5%AE%B6%E9%87%8C%E7%9A%84+%26+work",
    "/__desktop/server-connect",
    "/__desktop/server-ready?state=login&selection=local%3A0",
    "/__desktop/sidebar-width?width=328",
  ]);
  selectDesktopServer("remote&id=local", surface);
  selectDesktopServer("local", { location: surface.location });
  notifyDesktopServerName("browser", { location: surface.location });
  connectDesktopServer({ location: surface.location });
  notifyDesktopServerReady("login", "local:0", { location: surface.location });
  for (const width of [0, 219, 481, NaN, Infinity])
    notifyDesktopSidebarWidth(width, surface);
  assert.equal(calls.length, 6);
});

test("desktop sidebar width uses one validated native preference", () => {
  for (const width of [220, 308, 480])
    assert.equal(desktopSidebarWidth({ sidebar_width: width }), width);
  for (const width of [undefined, null, 0, 219, 481, "328", NaN, Infinity])
    assert.equal(desktopSidebarWidth({ sidebar_width: width }), 298);
});

test("connection health retries without animation frames until native acknowledges the current state", async (t) => {
  const surface = new EventTarget();
  surface.spoolDesktop = true;
  const calls = [];
  surface.location = {
    assign(url) {
      calls.push(url);
      surface.dispatchEvent(
        new CustomEvent("spool:server-state-ack", {
          detail: calls.length === 1 ? "connected" : "error",
        }),
      );
    },
  };
  const stop = reportDesktopServerState("error", surface);
  t.after(stop);
  assert.deepEqual(calls, ["/__desktop/server-state?state=error"]);
  await new Promise((resolve) => setTimeout(resolve, 250));
  assert.deepEqual(calls, [
    "/__desktop/server-state?state=error",
    "/__desktop/server-state?state=error",
  ]);
});

test("connection health stops retrying when its page or state is retired", async () => {
  const surface = new EventTarget();
  surface.spoolDesktop = true;
  const calls = [];
  surface.location = { assign: (url) => calls.push(url) };
  const stop = reportDesktopServerState("connecting", surface);
  stop();
  reportDesktopServerState("login", new EventTarget())();
  await new Promise((resolve) => setTimeout(resolve, 150));
  assert.deepEqual(calls, ["/__desktop/server-state?state=connecting"]);
});

test("native tab selection carries its current scroll position with the selected server", () => {
  const calls = [];
  const surface = {
    spoolDesktop: true,
    location: {
      assign: (url) => calls.push(url),
    },
  };
  selectDesktopServer("remote-1234", surface, 156.5);
  for (const scroll of [-1, NaN, Infinity, 1e8])
    selectDesktopServer("remote-1234", surface, scroll);
  assert.deepEqual(calls, ["/__desktop/servers?id=remote-1234&scroll_left=156.5"]);
});

test("returning to a cached server requests its sidebar without discarding the page", () => {
  const surface = new EventTarget();
  surface.spoolDesktop = true;
  let revealed = 0;
  const off = subscribeDesktopServerSelection(() => revealed++, surface);
  surface.dispatchEvent(new Event("spool:server-selected"));
  assert.equal(revealed, 1);
  off();
  surface.dispatchEvent(new Event("spool:server-selected"));
  assert.equal(revealed, 1);
  subscribeDesktopServerSelection(
    () => assert.fail("browser subscribed"),
    new EventTarget(),
  )();
});

test("remote server editor uses its dedicated native dialog bridge", async (t) => {
  const calls = [];
  globalThis.mygo = {
    call: async (...args) => {
      calls.push(args);
      return { saved: true };
    },
  };
  t.after(() => {
    delete globalThis.mygo;
  });
  const profile = { name: "", direct: "https://home.example", enabled: true };
  assert.deepEqual(await serverDialogCall("Save", profile, true), {
    saved: true,
  });
  assert.deepEqual(calls, [["DesktopServers.Save", profile, true]]);
  delete globalThis.mygo;
  await assert.rejects(
    serverDialogCall("Snapshot"),
    /only available in the Spool desktop app/,
  );
});

test("native paint acknowledgements are scoped to desktop and unsubscribe", () => {
  const surface = new EventTarget();
  surface.spoolDesktop = true;
  let acknowledged = 0;
  const off = subscribeDesktopServerReadyAck((paint) => {
    assert.deepEqual(paint, { state: "login", selection: "local:42" });
    acknowledged++;
  }, surface);
  surface.dispatchEvent(new Event("spool:server-ready-ack"));
  surface.dispatchEvent(
    new CustomEvent("spool:server-ready-ack", { detail: "invalid" }),
  );
  assert.equal(acknowledged, 0);
  surface.dispatchEvent(
    new CustomEvent("spool:server-ready-ack", {
      detail: { state: "login", selection: "local:42" },
    }),
  );
  assert.equal(acknowledged, 1);
  off();
  surface.dispatchEvent(
    new CustomEvent("spool:server-ready-ack", {
      detail: { state: "login", selection: "local:42" },
    }),
  );
  assert.equal(acknowledged, 1);
  subscribeDesktopServerReadyAck(
    () => assert.fail("browser subscribed"),
    new EventTarget(),
  )();
});
