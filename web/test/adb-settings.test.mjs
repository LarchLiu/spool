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
const { ADBShareSection } = await server.ssrLoadModule(
  "/src/adb-share-settings.tsx",
);
const { ADBHostSettings } = await server.ssrLoadModule(
  "/src/adb-host-settings.tsx",
);
const { PeoplePage, ServicePage } = await server.ssrLoadModule(
  "/src/settings-pages.tsx",
);
const { translator } = await server.ssrLoadModule("/src/i18n.ts");

const t = translator("en");

function share(properties) {
  return renderToStaticMarkup(
    createElement(ADBShareSection, {
      availability: { available: true },
      support: "supported",
      state: { phase: "idle" },
      t,
      onStart() {},
      onStop() {},
      ...properties,
    }),
  );
}

test("sharing a phone is offered only to a user the host allows", () => {
  // Without the grant, and while the server has not answered, nothing shows:
  // the page does not reveal how the host is configured.
  for (const availability of [
    null,
    { available: false, reason: "not_authorized" },
  ])
    for (const support of ["supported", "insecure", "unsupported"])
      assert.equal(share({ availability, support }), "");

  const html = share({});
  assert.match(html, /<button type="button">Share a phone over USB<\/button>/);
  // Sharing exposes the phone to everything on the server, and says so.
  assert.match(html, /every agent and program on it can control the phone/);
  assert.doesNotMatch(html, /Stop sharing/);

  for (const [properties, reason] of [
    [
      { availability: { available: false, reason: "disabled" } },
      /The ADB bridge is turned off on the server\./,
    ],
    [
      { availability: { available: false, reason: "adb_missing" } },
      /adb is not installed on the server\./,
    ],
    [{ support: "insecure" }, /Sharing a phone requires HTTPS or localhost\./],
    [{ support: "unsupported" }, /Use Chrome or Edge on a computer\./],
  ]) {
    const refused = share(properties);
    assert.match(refused, reason);
    assert.doesNotMatch(refused, /<button/);
  }
});

test("a share reports where it stands and can always be stopped", () => {
  const stop = /<button type="button">Stop sharing<\/button>/;
  let html = share({ state: { phase: "choosing" } });
  assert.match(html, /Choose the phone in the browser&#x27;s device list/);
  assert.doesNotMatch(html, /<button/);

  html = share({ state: { phase: "connecting", device: "Pixel" } });
  assert.match(html, /role="status">Connecting…</);
  assert.match(html, stop);

  html = share({
    state: { phase: "unauthorized", serial: "127.0.0.1:41000" },
  });
  assert.match(html, /role="status">Allow USB debugging on the phone/);
  assert.match(html, stop);

  html = share({ state: { phase: "online", serial: "127.0.0.1:41000" } });
  assert.match(html, /role="status">The phone is shared as 127\.0\.0\.1:41000\./);
  assert.match(html, /adb -s 127\.0\.0\.1:41000/);
  assert.match(html, /sharing ends when it closes/);
  assert.match(html, stop);
  assert.doesNotMatch(html, /Share a phone over USB/);

  html = share({ state: { phase: "waiting", code: "device_gone" } });
  assert.match(html, /Plug it in again to keep sharing/);
  assert.match(html, stop);
  html = share({ state: { phase: "waiting", code: "network" } });
  assert.match(html, /Reconnecting…/);
  assert.match(html, stop);

  // A share that is under way stays visible and stoppable even when the
  // server stops answering or has withdrawn access.
  for (const availability of [
    null,
    { available: false, reason: "not_authorized" },
  ])
    assert.match(
      share({
        availability,
        state: { phase: "online", serial: "127.0.0.1:41000" },
      }),
      stop,
    );
});

test("a share that ended says why and what to do about it", () => {
  let html = share({
    state: {
      phase: "failed",
      code: "usb_busy",
      detail: "Unable to claim interface.",
    },
  });
  assert.match(html, /role="alert">The browser could not open the phone/);
  assert.match(html, /adb kill-server/);
  assert.match(html, /<p>Unable to claim interface\.<\/p>/);
  assert.match(html, /<button type="button">Share a phone over USB<\/button>/);

  // adb's own words explain a refused connection.
  html = share({
    state: {
      phase: "failed",
      code: "connect_failed",
      detail: "failed to connect to 127.0.0.1:41000",
    },
  });
  assert.match(html, /role="alert">The server&#x27;s adb could not connect/);
  assert.match(html, /<p>failed to connect to 127\.0\.0\.1:41000<\/p>/);

  html = share({ state: { phase: "failed", code: "reselect_device" } });
  assert.match(html, /role="alert">The browser could not identify the same phone\. Choose it again/);
  assert.match(html, /<button type="button">Share a phone over USB<\/button>/);

  // A code this client does not know still shows the server's diagnostic.
  html = share({
    state: { phase: "failed", code: "later", detail: "Something new." },
  });
  assert.match(html, /role="alert">Sharing the phone stopped unexpectedly\./);
  assert.match(html, /<p>Something new\.<\/p>/);

  // Withdrawn access is explained once, in the user's language, and offers
  // nothing to press.
  html = share({
    availability: { available: false, reason: "not_authorized" },
    state: { phase: "failed", code: "revoked", detail: "ADB bridge access ended." },
  });
  assert.match(html, /role="alert">Your access to the ADB bridge ended\./);
  assert.doesNotMatch(html, /ADB bridge access ended\.|<button/);
  html = share({
    availability: { available: false, reason: "disabled" },
    state: { phase: "failed", code: "disabled" },
  });
  assert.equal(html.match(/The ADB bridge is turned off on the server\./g).length, 1);
  assert.doesNotMatch(html, /<button/);
});

function host(adb, settings = {}) {
  return renderToStaticMarkup(
    createElement(ADBHostSettings, {
      data: {
        settings,
        status: { ADB: adb },
        users: [
          { id: "u1", username: "alice", nickname: "Alice", source_server_id: "source-id", source_server_name: "Office" },
          { id: "u2", username: "bob", nickname: "" },
        ],
      },
      run: async () => undefined,
      t,
    }),
  );
}

test("the host decides whether devices can be shared and sees which ones are", () => {
  let html = host({ Path: "", Devices: [] });
  assert.match(html, /aria-label="Enable ADB bridge" aria-checked="false"/);
  assert.match(html, /role="status">Not installed</);
  assert.match(html, /Install Android SDK Platform-Tools/);
  // What turning it on means for the devices people share.
  assert.match(html, /Every agent and program running as this computer&#x27;s user/);
  assert.match(html, /Project access does not include it/);
  assert.doesNotMatch(html, /Shared devices|data-adb-device/);

  html = host(
    { Path: "/opt/android/platform-tools/adb", Devices: [] },
    { adb_bridge_enabled: true },
  );
  assert.match(html, /aria-label="Enable ADB bridge" aria-checked="true"/);
  assert.match(html, /role="status">Installed</);
  assert.match(html, /\/opt\/android\/platform-tools\/adb/);
  assert.match(html, /No devices are shared right now\./);
  assert.doesNotMatch(html, /data-adb-device/);

  html = host(
    {
      Path: "/opt/android/platform-tools/adb",
      Devices: [
        { Owner: "u1", Transport: "usb", Serial: "127.0.0.1:41000" },
        { Owner: "u2", Transport: "wireless", Serial: "127.0.0.1:41001" },
        { Owner: "gone", Transport: "usb", Serial: "127.0.0.1:41002" },
      ],
    },
    { adb_bridge_enabled: true },
  );
  assert.equal(html.match(/data-adb-device/g).length, 3);
  assert.match(html, /adb -s followed by its address/);
  // Each device with how it is attached and whose it is.
  assert.match(html, /127\.0\.0\.1:41000[\s\S]*?USB · Alice · Office/);
  assert.match(html, /127\.0\.0\.1:41001[\s\S]*?Wireless debugging · bob/);
  assert.match(html, /127\.0\.0\.1:41002[\s\S]*?USB · gone/);

  // A service that does not report the bridge shows nothing about it.
  assert.equal(host(undefined), "");
});

test("the service page offers the ADB bridge beside remote desktop", () => {
  const html = renderToStaticMarkup(
    createElement(ServicePage, {
      data: {
        settings: {
          host: "127.0.0.1",
          port: 8787,
          public_url: "",
          idle_minutes: 15,
          history_cache_mib: 64,
          auto_start: true,
        },
        status: {
          Running: true,
          Error: "",
          ADB: { Path: "/opt/android/platform-tools/adb", Devices: [] },
        },
        users: [],
        database: "fixture.sqlite",
      },
      run: async () => undefined,
      t,
    }),
  );
  assert.match(html, /role="tab"[^>]*>(?:(?!<\/button>)[\s\S])*ADB bridge/);
  assert.match(html, /aria-label="Enable ADB bridge" aria-checked="false"/);
});

test("people management shows who may share devices, separately from remote desktop", () => {
  const html = renderToStaticMarkup(
    createElement(PeoplePage, {
      data: {
        users: [
          { id: "alice", username: "alice", nickname: "Alice", adb_bridge: true },
          {
            id: "bob",
            username: "bob",
            nickname: "Bob",
            remote_desktop: true,
            adb_bridge: true,
          },
          { id: "carol", username: "carol", nickname: "Carol", remote_desktop: true },
          { id: "dave", username: "dave", nickname: "Dave" },
        ],
        status: { RemoteDesktopConnections: {} },
      },
      run() {},
      t,
    }),
  );
  const [alice, bob, carol, dave] = html.match(/<li>.*?<\/li>/g);
  assert.match(alice, /aria-label="ADB bridge allowed"/);
  assert.doesNotMatch(alice, /Remote desktop allowed|settings-remote-count/);
  assert.match(bob, /aria-label="ADB bridge allowed"/);
  assert.match(bob, /aria-label="Remote desktop allowed"/);
  assert.doesNotMatch(carol, /ADB bridge allowed/);
  assert.match(carol, /aria-label="Remote desktop allowed"/);
  assert.doesNotMatch(dave, /ADB bridge allowed|Remote desktop allowed/);
});
