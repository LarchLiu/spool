import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { readFile } from "node:fs/promises";
import { createTestViteServer } from "./vite-fixture.mjs";

const server = await createTestViteServer(test, {
  root: new URL("..", import.meta.url).pathname,
  configFile: false,
  logLevel: "silent",
  appType: "custom",
  server: { middlewareMode: true, watch: null, hmr: false },
  esbuild: { jsx: "automatic" },
});
const { ServicePage, NetworkPage } = await server.ssrLoadModule(
  "/src/settings-pages.tsx",
);

function renderService(state = {}, settings = {}) {
  return renderToStaticMarkup(
    createElement(ServicePage, {
      data: {
        settings: {
          host: "127.0.0.1",
          port: 8787,
          public_url: "",
          idle_minutes: 15,
          history_cache_mib: 64,
          auto_start: true,
          tsnet_enabled: true,
          tsnet_hostname: "spool-server",
          tsnet_port: 443,
          ...settings,
        },
        status: { Running: true, Error: "", ...state },
        database: "fixture.sqlite",
      },
      run: async () => undefined,
      t: (key) => key,
    }),
  );
}

test("service offers optional masked enrollment without storing a key in the settings draft", async () => {
  const html = renderService({
    TSNet: {
      State: "NeedsLogin",
      AuthURL: "https://login.tailscale.com/a/test",
    },
  });
  assert.match(
    html,
    /aria-label="Enable embedded Tailscale" aria-checked="true"/,
  );
  assert.match(
    html,
    /aria-label="Tailscale Auth Key \(optional\)" type="password"/,
  );
  assert.match(html, /Sign in to Tailscale/);
  assert.match(html, /Waiting for Tailscale login/);
  assert.doesNotMatch(html, /Clear pending key/);
  assert.doesNotMatch(html, /aria-label="Public URL[^>]+disabled=""/);
  assert.match(
    html,
    /Optional HTTPS address of your reverse proxy for public internet access\. Configure the proxy separately\./,
  );
  const autosave = await readFile(
    new URL("../src/settings-autosave.ts", import.meta.url),
    "utf8",
  );
  assert.doesNotMatch(autosave, /auth_key|authKey/);
  const pages = await readFile(
    new URL("../src/settings-pages.tsx", import.meta.url),
    "utf8",
  );
  assert.match(
    pages,
    /await settingsCall<boolean>\("TSNetRegister", authKey\)/,
  );
  assert.match(pages, /setAuthRegistered\(true\)/);
  assert.match(
    pages,
    /Click Use Auth Key to register now without stopping Spool/,
  );
});

test("tailnet shows enrollment errors and the explicit restart action without staged key controls", () => {
  const html = renderService({
    RestartRequired: true,
    TSNetAuthKeySet: true,
    TSNet: { State: "Error", Error: "Key rejected [redacted]" },
  });
  assert.doesNotMatch(html, /Clear pending key/);
  assert.match(html, /Tailscale unavailable/);
  assert.match(html, /Key rejected \[redacted\]/);
  assert.doesNotMatch(html, /data-restart-service/);
  assert.doesNotMatch(html, /Sign in to Tailscale/);
});

test("registered identity shows its status without repeated apply instructions or a login link", () => {
  const html = renderService({
    RestartRequired: true,
    TSNet: { State: "Registered", URL: "" },
  });
  assert.match(html, />Registered</);
  assert.doesNotMatch(html, /bottom bar|pending.*changes/);
  assert.doesNotMatch(html, /data-restart-service/);
  assert.doesNotMatch(
    html,
    /Sign in to Tailscale|Clear pending key|Copy address/,
  );
});

test("waiting for login always exposes a sign-in action even before an auth URL arrives", () => {
  for (const State of ["NeedsLogin", "NoState"]) {
    const html = renderService({ TSNet: { State, AuthURL: "" } });
    assert.match(html, />Sign in to Tailscale</);
    assert.match(html, /Click Sign in to request its login link/);
    assert.doesNotMatch(html, /https:\/\/login\.tailscale\.com/);
  }
});

test("login requests bypass the global settings queue and only report local errors", async () => {
  const pages = await readFile(
    new URL("../src/settings-pages.tsx", import.meta.url),
    "utf8",
  );
  assert.match(pages, /await settingsCall<boolean>\("TSNetLogin"\)/);
  assert.doesNotMatch(pages, /run\(\{ kind: "tsnet-login"/);
  assert.match(pages, /setLoginError\(\s*error instanceof Error/);
  assert.match(pages, /disabled=\{requestingLogin\}/);
});

function renderNetwork(settings = {}) {
  return renderToStaticMarkup(
    createElement(NetworkPage, {
      data: {
        settings: {
          proxy_enabled: true,
          use_system_proxy: true,
          proxy_url: "http://127.0.0.1:8889",
          no_proxy: "internal.example,192.168.0.0/16",
          ...settings,
        },
        status: { RestartRequired: true },
      },
      run: async () => undefined,
      t: (key) => key,
    }),
  );
}

test("system proxy is an application Network setting, not a Tailscale option", async () => {
  const html = renderNetwork();
  assert.match(html, /aria-label="Enable proxy" aria-checked="true"/);
  assert.match(html, /aria-label="Use system proxy" aria-checked="true"/);
  assert.match(html, /including updates and Tailscale/);
  assert.match(
    html,
    /aria-label="Proxy URL"[^>]+value="http:\/\/127\.0\.0\.1:8889"/,
  );
  assert.match(
    html,
    /aria-label="No proxy"[^>]+value="internal\.example,192\.168\.0\.0\/16"/,
  );
  assert.match(html, /Leave blank to inherit environment proxies/);
  assert.match(html, /Leave blank to inherit NO_PROXY/);
  assert.doesNotMatch(html, /Restart service/);
  assert.doesNotMatch(renderService(), /Use system proxy/);
  const app = await readFile(
    new URL("../src/settings-app.tsx", import.meta.url),
    "utf8",
  );
  assert.match(app, /Network: NetworkPage/);
  assert.match(app, /data-page=\{key\} hidden=\{section !== key\}/);
  const client = await readFile(
    new URL("../src/settings-client.ts", import.meta.url),
    "utf8",
  );
  assert.match(client, /"Online",\s*"Appearance",\s*"Network"/);
  assert.doesNotMatch(client, /tsnet_use_system_proxy/);
});

test("proxy controls follow master, system, custom order and retain inactive values", () => {
  for (const proxy_enabled of [false, true]) {
    for (const use_system_proxy of [false, true]) {
      const html = renderNetwork({ proxy_enabled, use_system_proxy });
      const master = html.match(
        /<button[^>]*aria-label="Enable proxy"[^>]*>/,
      )?.[0];
      const system = html.match(
        /<button[^>]*aria-label="Use system proxy"[^>]*>/,
      )?.[0];
      const custom = html.match(/<input[^>]*aria-label="Proxy URL"[^>]*>/)?.[0];
      const bypass = html.match(/<input[^>]*aria-label="No proxy"[^>]*>/)?.[0];
      assert.ok(master && system && custom && bypass);
      assert.ok(html.indexOf(master) < html.indexOf(system));
      assert.ok(html.indexOf(system) < html.indexOf(custom));
      assert.ok(html.indexOf(custom) < html.indexOf(bypass));
      assert.doesNotMatch(master, /disabled/);
      assert.equal(system.includes('disabled=""'), !proxy_enabled);
      assert.equal(
        custom.includes('disabled=""'),
        !proxy_enabled || use_system_proxy,
      );
      assert.equal(
        bypass.includes('disabled=""'),
        !proxy_enabled || use_system_proxy,
      );
      assert.match(custom, /value="http:\/\/127\.0\.0\.1:8889"/);
      assert.match(bypass, /value="internal\.example,192\.168\.0\.0\/16"/);
    }
  }
});

test("connected tailnet displays the assigned HTTPS address and leaves Spool login required", () => {
  const html = renderService(
    { TSNet: { State: "Running", URL: "https://spool.example.ts.net" } },
    { public_url: "https://chat.example.com" },
  );
  assert.match(html, /Connected to tailnet/);
  assert.match(html, /<code>https:\/\/spool.example.ts.net<\/code>/);
  assert.match(html, /Copy address/);
  assert.match(html, /class="icon settings-address-qr"[^>]*aria-label="Show address QR code"/);
  assert.match(html, /Spool login is still required/);
  assert.match(
    html,
    /aria-label="Public URL[^>]+value="https:\/\/chat\.example\.com"/,
  );
  assert.doesNotMatch(html, /aria-label="Public URL[^>]+disabled/);
  assert.doesNotMatch(html, /Sign in to Tailscale|Clear pending key/);
});

test("disabled tailnet does not lock a custom reverse-proxy origin", () => {
  const html = renderService(
    { TSNet: { State: "Disabled" } },
    { tsnet_enabled: false },
  );
  assert.match(
    html,
    /aria-label="Enable embedded Tailscale" aria-checked="false"/,
  );
  assert.doesNotMatch(html, /aria-label="Public URL[^>]+disabled/);
});

test("local Web access uses enablement and LAN switches instead of a listen IP input", () => {
  const html = renderService({ Address: "127.0.0.1:8787" });
  assert.match(html, /aria-label="Enable Web access" aria-checked="true"/);
  assert.match(html, /aria-label="Allow LAN access" aria-checked="false"/);
  assert.doesNotMatch(html, /Listen IP|value="127\.0\.0\.1"/);
  assert.match(html, /aria-label="Public URL"/);
  assert.doesNotMatch(html, /independent of Tailscale/);
  const lan = renderService({}, { host: "0.0.0.0" });
  assert.match(lan, /aria-label="Allow LAN access" aria-checked="true"/);
  const disabled = renderService({ Address: "" }, { web_enabled: false, host: "0.0.0.0" });
  assert.match(disabled, /aria-label="Enable Web access" aria-checked="false"/);
  assert.match(disabled, /aria-label="Allow LAN access" aria-checked="true"[^>]*disabled/);
  assert.match(disabled, /aria-label="HTTP port"[^>]*disabled/);
  assert.match(disabled, /aria-label="Public URL"[^>]*disabled/);
  assert.doesNotMatch(disabled, /Active listen address/);
  assert.match(disabled, /aria-label="Enable embedded Tailscale" aria-checked="true"/);
});

test("address QR buttons use the public input and appear beside relay and Tailnet addresses", () => {
  const html = renderService({
    TSNet: { State: "Running", URL: "https://node.example.ts.net" },
    Relay: { State: "connected", WebURL: "https://relay.example.test/web/server/" },
  }, {
    public_url: "https://spool.example.test",
    relay_enabled: true,
    relay_web_enabled: true,
  });
  assert.equal((html.match(/class="icon settings-address-qr"/g) ?? []).length, 3);
  assert.match(html, /aria-label="Public internet access"/);
  assert.doesNotMatch(html, /without Tailscale|bottom bar/);
  const invalid = renderService({}, { public_url: "javascript:alert(1)" });
  assert.match(invalid, /class="icon settings-address-qr"[^>]*disabled/);
});

test("Web address links open the correct browser URLs without the old global chat buttons", () => {
  const html = renderService({ Address: "0.0.0.0:8787" }, { public_url: "https://spool.example.test" });
  assert.doesNotMatch(html, /Open Chat|Open in Browser/);
  assert.match(html, /class="icon settings-address-link" href="http:\/\/127\.0\.0\.1:8787" target="_blank" rel="noopener noreferrer"/);
  assert.match(html, /class="icon settings-address-link" href="https:\/\/spool\.example\.test"/);
  const ipv6 = renderService({ Address: "[::]:8787" });
  assert.match(ipv6, /href="http:\/\/\[::1\]:8787"/);
  const invalid = renderService({}, { public_url: "javascript:alert(1)" });
  assert.doesNotMatch(invalid, /href="javascript:/);
});
