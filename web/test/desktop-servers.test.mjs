import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  desktopServers,
  subscribeDesktopServers,
  requestDesktopFederationLogin,
  notifyDesktopFederationTargets,
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
  DesktopServerTabBar,
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
    /aria-current="page"[^>]*title="自定义 &amp; &lt;Home&gt; · Connected · Sign-in required"/,
  );
  assert.match(html, /aria-label="本机的名称, Connected · Signed in"/);
  assert.match(html, /href="\/__desktop\/servers"[^>]*aria-label="Add server"/);
  assert.doesNotMatch(html, /Local server/);
  delete globalThis.spoolDesktop;
  assert.equal(
    renderToStaticMarkup(createElement(DesktopServerTabs, { t: (key) => key })),
    "",
  );
});

test("trusted tabs preserve ordinary logins and isolate each source", () => {
  const office = {
    id: "a".repeat(52),
    name: "Office",
    clientURL: "https://office.example/",
  };
  const lab = { id: "b".repeat(52), name: "Lab" };
  const paired = { id: "c".repeat(52), name: "Paired" };
  const entry = (id, name, status = "connected", extra = {}) => ({
    id,
    name,
    local: id === "local",
    status,
    ...extra,
  });
  const servers = {
    selected: "home",
    entries: [
      entry("local", "Local"),
      entry("home", "Home", "connected", {
        federation_targets: [{ ...office, client_url: office.clientURL }, lab, paired],
      }),
      entry("ordinary", "Ordinary"),
      entry("office", "Work", "login", {
        client_url: "https://OFFICE.example:443",
      }),
      entry("paired", "Paired", "connected", { server_id: paired.id }),
      entry("studio", "Studio", "connected", { federation_targets: [{ ...office, client_url: office.clientURL }] }),
      entry("home-office", "Home Office", "connected", { federation_source: "home", federation_target: office.id }),
      entry("studio-office", "Studio Office", "login", { federation_source: "studio", federation_target: office.id }),
    ],
  };
  const t = (key, values) => key.replace("{server}", values?.server ?? "");
  const labels = (snapshot, targets) => {
    const html = renderToStaticMarkup(
      createElement(DesktopServerTabBar, { t, servers: snapshot, targets }),
    );
    return [...html.matchAll(/<(?:button|a)\b[^>]*aria-label="([^"]+)"/g)].map(
      (match) => match[1],
    );
  };
  const order = [
    "Local, Connected · Signed in",
    "Home, Connected · Signed in",
    "Home → Sign in to Home Office with this account, Connected · Signed in",
    "Home → Sign in to Lab with this account, Disconnected",
    "Home → Sign in to Paired with this account, Disconnected",
    "Ordinary, Connected · Signed in",
    "Work, Connected · Sign-in required",
    "Paired, Connected · Signed in",
    "Studio, Connected · Signed in",
    "Studio → Sign in to Studio Office with this account, Connected · Sign-in required",
    "Add server",
  ];
  assert.deepEqual(labels(servers), order);
  assert.deepEqual(labels({ ...servers, selected: "ordinary" }), order);
  assert.deepEqual(labels(servers, []), order.filter((label) => !label.startsWith("Home →")));
  const selected = renderToStaticMarkup(createElement(DesktopServerTabBar, {
    t, servers: { ...servers, selected: "studio-office", selected_tab: `federation:studio:${office.id}` },
  }));
  assert.deepEqual([...selected.matchAll(/aria-current="page"[^>]*aria-label="([^"]+)"/g)].map((match) => match[1]),
    ["Studio → Sign in to Studio Office with this account, Connected · Sign-in required"]);
  const ordinary = renderToStaticMarkup(createElement(DesktopServerTabBar, {
    t, servers: { ...servers, selected: "office" },
  }));
  assert.deepEqual([...ordinary.matchAll(/aria-current="page"[^>]*aria-label="([^"]+)"/g)].map((match) => match[1]),
    ["Work, Connected · Sign-in required"]);
});

test("live trusted aliases only use native status for the same source and route", () => {
  const id = "b".repeat(52);
  const clientURL = "https://lab.example/chat/";
  const native = {
    id,
    name: "Previously named",
    client_url: "https://LAB.example:443/chat",
    client_mode: "tailscale",
    status: "login",
  };
  const selected = { id: "local", name: "Home", local: true, status: "connected" };
  const other = {
    id: "studio", name: "Studio", local: false, status: "connected",
    federation_targets: [{ ...native, status: "error" }],
  };
  const live = { id, name: "Shared Lab", clientURL, clientMode: "tailscale", status: "connected" };
  const t = (key, values) => key.replace("{server}", values?.server ?? "");
  const render = (targets, target, saved) => {
    const html = renderToStaticMarkup(createElement(DesktopServerTabBar, {
      t, targets: [target], servers: {
        selected: selected.id,
        entries: [{ ...selected, federation_targets: targets }, other, ...(saved ? [saved] : [])],
      },
    }));
    return html.match(/<button\b[^>]*aria-label="Home → [^<]+?[\s\S]*?<\/button>/)?.[0];
  };
  const assertStatus = (tab, state, label) => {
    assert.ok(tab.includes(`, ${label}"`));
    assert.match(tab, new RegExp(`lucide-key-round[^>]+data-state="${state}"`));
    assert.doesNotMatch(tab, /server-status-dot/);
  };
  assertStatus(render([native], live), "login", "Connected · Sign-in required");
  assertStatus(render([{ ...native, client_mode: "" }], { ...live, clientMode: undefined }),
    "login", "Connected · Sign-in required");
  for (const [snapshotTargets, target] of [
    [[], live],
    [[native], { ...live, id: "c".repeat(52) }],
    [[native], { ...live, clientURL: "https://lab.example/new/" }],
    [[native], { ...live, clientMode: undefined }],
    [[{ ...native, client_url: undefined }], { ...live, clientURL: undefined }],
    [[{ ...native, status: "connected" }], live],
  ]) {
    assertStatus(render(snapshotTargets, target), "disconnected", "Disconnected");
  }
  assertStatus(render([native], live, {
    id: "stored", name: "Saved Lab", local: false, status: "connected", server_id: id,
  }), "login", "Connected · Sign-in required");
  assertStatus(render([native], live, {
    id: "stored", name: "Trusted Lab", local: false, status: "connected", server_id: id,
    federation_source: selected.id, federation_target: id,
  }), "connected", "Connected · Signed in");
});

test("federation navigation sends the source and target without credentials or addresses", () => {
  const calls = [];
  const target = "a".repeat(52);
  const surface = {
    spoolDesktop: true,
    location: { assign: (url) => calls.push(url) },
  };
  requestDesktopFederationLogin("home", target, surface, 18);
  requestDesktopFederationLogin("studio", target, surface);
  requestDesktopFederationLogin("home", `${target}&client_url=https://unlisted.example`, surface);
  requestDesktopFederationLogin("home&account=someone", target, surface);
  requestDesktopFederationLogin("home", target, { location: surface.location });
  notifyDesktopFederationTargets(surface);
  assert.deepEqual(calls, [
    `/__desktop/federation-login?source=home&target=${target}&scroll_left=18`,
    `/__desktop/federation-login?source=studio&target=${target}&scroll_left=0`,
    "/__desktop/federation-targets",
  ]);
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
    "/__desktop/server-state?state=connected&authenticated=true",
    "/__desktop/server-name?name=%E5%AE%B6%E9%87%8C%E7%9A%84+%26+work",
    "/__desktop/server-connect",
    "/__desktop/server-ready?state=login&selection=local%3A0&authenticated=false",
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
          detail: { state: calls.length === 1 ? "connected" : "error", authenticated: false },
        }),
      );
    },
  };
  const stop = reportDesktopServerState("error", surface);
  t.after(stop);
  assert.deepEqual(calls, ["/__desktop/server-state?state=error&authenticated=false"]);
  await new Promise((resolve) => setTimeout(resolve, 250));
  assert.deepEqual(calls, [
    "/__desktop/server-state?state=error&authenticated=false",
    "/__desktop/server-state?state=error&authenticated=false",
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
  assert.deepEqual(calls, ["/__desktop/server-state?state=connecting&authenticated=false"]);
});

test("a restored session reports socket ownership before ready and ignores signed-out acknowledgements", async (t) => {
  const surface = new EventTarget();
  surface.spoolDesktop = true;
  const calls = [];
  surface.location = {
    assign(url) {
      calls.push(url);
      surface.dispatchEvent(new CustomEvent("spool:server-state-ack", {
        detail: { state: "error", authenticated: calls.length > 1 },
      }));
    },
  };
  const stop = reportDesktopServerState("error", surface, true);
  t.after(stop);
  await new Promise((resolve) => setTimeout(resolve, 250));
  assert.deepEqual(calls, [
    "/__desktop/server-state?state=error&authenticated=true",
    "/__desktop/server-state?state=error&authenticated=true",
  ]);
  notifyDesktopServerReady("connecting", "local:0", surface, true);
  assert.equal(calls.at(-1), "/__desktop/server-ready?state=connecting&selection=local%3A0&authenticated=true");
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
    assert.deepEqual(paint, { state: "login", selection: "local:42", authenticated: false });
    acknowledged++;
  }, surface);
  surface.dispatchEvent(new Event("spool:server-ready-ack"));
  surface.dispatchEvent(
    new CustomEvent("spool:server-ready-ack", { detail: "invalid" }),
  );
  assert.equal(acknowledged, 0);
  surface.dispatchEvent(
    new CustomEvent("spool:server-ready-ack", {
      detail: { state: "login", selection: "local:42", authenticated: false },
    }),
  );
  assert.equal(acknowledged, 1);
  off();
  surface.dispatchEvent(
    new CustomEvent("spool:server-ready-ack", {
      detail: { state: "login", selection: "local:42", authenticated: false },
    }),
  );
  assert.equal(acknowledged, 1);
  subscribeDesktopServerReadyAck(
    () => assert.fail("browser subscribed"),
    new EventTarget(),
  )();
});
