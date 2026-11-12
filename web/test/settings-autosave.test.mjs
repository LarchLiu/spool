import assert from "node:assert/strict";
import test from "node:test";
import {
  createSettingsCommandQueue,
  NetworkSettingsAutosave,
  ServiceSettingsAutosave,
} from "../.test-build/settings-autosave.js";

const initial = () => ({
  host: "127.0.0.1",
  port: 59001,
  public_url: "",
  web_trusted_proxies: "127.0.0.0/8,::1/128",
  idle_minutes: 15,
  history_cache_mib: 64,
  auto_start: true,
  tsnet_enabled: false,
  tsnet_hostname: "spool-server",
  tsnet_port: 443,
  proxy_enabled: true,
  use_system_proxy: false,
  proxy_url: "",
  no_proxy: "",
});
function fixture(execute, Autosave = ServiceSettingsAutosave) {
  const commands = [];
  const run = createSettingsCommandQueue(async (command) => {
    commands.push(command);
    return execute ? execute(command) : { settings: command.settings };
  });
  let changes = 0;
  const autosave = new Autosave(
    initial(),
    async (settings) =>
      (
        await run((latest) => ({
          kind: "save-settings",
          settings: settings(latest?.settings),
        }))
      )?.settings,
    () => changes++,
  );
  return { autosave, commands, run, changes: () => changes };
}

test("service inputs keep raw numeric drafts and save only the committed field", async () => {
  const { autosave, commands } = fixture();
  autosave.edit("port", "");
  autosave.edit("host", "localhost");
  assert.equal(commands.length, 0);
  await autosave.commit("host");
  assert.deepEqual(commands[0].settings, { ...initial(), host: "localhost" });
  assert.equal(autosave.draft.port, "");
  for (const invalid of ["", "0", "1.5", "65536", "NaN", "Infinity"]) {
    autosave.edit("port", invalid);
    await autosave.commit("port");
    assert.equal(commands.length, 1, invalid);
  }
  autosave.edit("port", "9000");
  await autosave.commit("port");
  assert.deepEqual(commands[1].settings, {
    ...initial(),
    host: "localhost",
    port: 9000,
  });
});

test("unchanged polling and unrelated proxy changes do not redraw service drafts", () => {
  let redraws = 0;
  const autosave = new ServiceSettingsAutosave(
    initial(),
    async () => undefined,
    () => redraws++,
  );
  const draft = autosave.draft;
  autosave.sync({ ...initial() });
  autosave.sync({ ...initial(), use_system_proxy: true });
  autosave.sync({
    ...initial(),
    proxy_enabled: false,
    proxy_url: "http://proxy:80",
    no_proxy: "internal.example",
  });
  autosave.edit("host", initial().host);
  assert.equal(redraws, 0);
  assert.equal(autosave.draft, draft);
  autosave.edit("host", "localhost");
  const edited = autosave.draft;
  autosave.sync({ ...initial() });
  assert.equal(redraws, 1);
  assert.equal(autosave.draft, edited);
  autosave.sync({ ...initial(), port: 9000 });
  assert.equal(redraws, 2);
  assert.equal(autosave.draft.host, "localhost");
  assert.equal(autosave.draft.port, "9000");
});

test("every connection/runtime setting saves without an explicit save action", async () => {
  const { autosave, commands } = fixture();
  for (const [key, value] of [
    ["host", "localhost"],
    ["port", "9000"],
    ["public_url", "https://chat.example.test"],
    ["web_trusted_proxies", "192.0.2.10/32"],
    ["idle_minutes", "30"],
    ["history_cache_mib", "128"],
    ["auto_start", false],
    ["tsnet_hostname", "spool-office"],
    ["tsnet_port", "8443"],
    ["tsnet_enabled", true],
  ]) {
    autosave.edit(key, value);
    await autosave.commit(key);
  }
  assert.equal(commands.length, 10);
  assert.deepEqual(commands.at(-1).settings, {
    host: "localhost",
    port: 9000,
    public_url: "https://chat.example.test",
    web_trusted_proxies: "192.0.2.10/32",
    idle_minutes: 30,
    history_cache_mib: 128,
    auto_start: false,
    tsnet_enabled: true,
    tsnet_hostname: "spool-office",
    tsnet_port: 8443,
    proxy_enabled: true,
    use_system_proxy: false,
    proxy_url: "",
    no_proxy: "",
  });
  await autosave.commit("auto_start");
  assert.equal(commands.length, 10, "unchanged values must not be resaved");
  assert.ok(commands.every((command) => command.kind === "save-settings"));
});

test("local Web switches autosave and cancel restores enablement and access scope", async () => {
  const { autosave, commands } = fixture();
  assert.equal(
    autosave.draft.web_enabled,
    true,
    "legacy snapshots keep Web enabled",
  );
  autosave.edit("host", "0.0.0.0");
  await autosave.commit("host");
  autosave.edit("web_enabled", false);
  await autosave.commit("web_enabled");
  assert.equal(commands.at(-1).settings.web_enabled, false);
  assert.equal(commands.at(-1).settings.host, "0.0.0.0");
  assert.equal(commands.at(-1).settings.port, initial().port);
  autosave.prepareCancel("web")({ ...initial(), web_enabled: true });
  assert.equal(autosave.draft.web_enabled, true);
  assert.equal(autosave.draft.host, "127.0.0.1");
});

test("rapid field commits merge in order before a queued manual restart", async () => {
  let release;
  const waiting = new Promise((resolve) => (release = resolve));
  let first = true;
  const { autosave, commands, run } = fixture(async (command) => {
    if (first) {
      first = false;
      await waiting;
    }
    return { settings: command.settings };
  });
  autosave.edit("port", "9000");
  const port = autosave.commit("port");
  autosave.edit("idle_minutes", "30");
  const idle = autosave.commit("idle_minutes");
  const restart = run({ kind: "restart-service" });
  await Promise.resolve();
  assert.equal(commands.length, 1);
  release();
  await Promise.all([port, idle, restart]);
  assert.deepEqual(
    commands.map((command) => command.kind),
    ["save-settings", "save-settings", "restart-service"],
  );
  assert.equal(commands[1].settings.port, 9000);
  assert.equal(commands[1].settings.idle_minutes, 30);
});

test("custom proxy fields autosave independently and keep drafts across polling", async () => {
  const { autosave, commands } = fixture(undefined, NetworkSettingsAutosave);
  autosave.edit("proxy_url", "http://127.0.0.1:8889");
  autosave.edit("no_proxy", "internal.example,192.168.0.0/16");
  autosave.sync({ ...initial(), public_url: "https://chat.example.com" });
  assert.equal(autosave.draft.proxy_url, "http://127.0.0.1:8889");
  assert.equal(autosave.draft.no_proxy, "internal.example,192.168.0.0/16");
  await Promise.all([
    autosave.commit("proxy_url"),
    autosave.commit("no_proxy"),
  ]);
  assert.equal(commands.at(-1).settings.proxy_url, "http://127.0.0.1:8889");
  assert.equal(
    commands.at(-1).settings.no_proxy,
    "internal.example,192.168.0.0/16",
  );
  assert.equal(commands.at(-1).settings.public_url, "https://chat.example.com");
  autosave.edit("use_system_proxy", true);
  await autosave.commit("use_system_proxy");
  assert.equal(commands.at(-1).settings.proxy_url, "http://127.0.0.1:8889");
  autosave.edit("proxy_enabled", false);
  await autosave.commit("proxy_enabled");
  assert.equal(commands.at(-1).settings.proxy_enabled, false);
  assert.equal(commands.at(-1).settings.use_system_proxy, true);
  assert.equal(commands.at(-1).settings.proxy_url, "http://127.0.0.1:8889");
  assert.equal(
    commands.at(-1).settings.no_proxy,
    "internal.example,192.168.0.0/16",
  );
  autosave.edit("proxy_url", "");
  await autosave.commit("proxy_url");
  assert.equal(commands.at(-1).settings.proxy_url, "");
  assert.equal(commands.at(-1).settings.use_system_proxy, true);
});

test("rapid proxy mode toggles persist in order without discarding custom values", async () => {
  const { autosave, commands } = fixture(undefined, NetworkSettingsAutosave);
  autosave.edit("proxy_url", "socks5://127.0.0.1:1080");
  const custom = autosave.commit("proxy_url");
  autosave.edit("use_system_proxy", true);
  const system = autosave.commit("use_system_proxy");
  autosave.edit("proxy_enabled", false);
  const off = autosave.commit("proxy_enabled");
  autosave.edit("proxy_enabled", true);
  const on = autosave.commit("proxy_enabled");
  await Promise.all([custom, system, off, on]);
  assert.deepEqual(
    commands.map((command) => command.settings.proxy_enabled),
    [true, true, false, true],
  );
  assert.equal(autosave.draft.proxy_enabled, true);
  assert.equal(autosave.draft.use_system_proxy, true);
  assert.equal(commands.at(-1).settings.proxy_url, "socks5://127.0.0.1:1080");
});

test("a switch toggled back during an in-flight save is not silently dropped", async () => {
  const { autosave, commands } = fixture();
  autosave.edit("auto_start", false);
  const off = autosave.commit("auto_start");
  autosave.edit("auto_start", true);
  const on = autosave.commit("auto_start");
  await Promise.all([off, on]);
  assert.deepEqual(
    commands.map((command) => command.settings.auto_start),
    [false, true],
  );
  assert.equal(autosave.draft.auto_start, true);
});

test("an older acknowledgement cannot replace a newer reverted edit, even when its save fails", async () => {
  let saves = 0;
  const { autosave, commands } = fixture(async (command) => {
    if (++saves === 2) return undefined;
    return { settings: command.settings };
  });
  autosave.edit("auto_start", false);
  const off = autosave.commit("auto_start");
  autosave.edit("auto_start", true);
  const on = autosave.commit("auto_start");
  await off;
  assert.equal(autosave.draft.auto_start, true);
  await on;
  autosave.sync({ ...initial(), auto_start: false });
  assert.equal(autosave.draft.auto_start, true);
  await autosave.commit("auto_start");
  assert.deepEqual(
    commands.map((command) => command.settings.auto_start),
    [false, true, true],
  );
});

test("a save failure does not overwrite another field and the queue merges later polling updates", async () => {
  let snapshot = { settings: initial() };
  const commands = [];
  const run = createSettingsCommandQueue(
    async (command) => {
      commands.push(command);
      if (command.settings.host === "invalid-host") return undefined;
      return (snapshot = { settings: command.settings });
    },
    () => snapshot,
  );
  const autosave = new ServiceSettingsAutosave(
    initial(),
    async (settings) =>
      (
        await run((latest) => ({
          kind: "save-settings",
          settings: settings(latest?.settings),
        }))
      )?.settings,
    () => {},
  );
  autosave.edit("host", "invalid-host");
  const host = autosave.commit("host");
  autosave.edit("port", "9000");
  const port = autosave.commit("port");
  await Promise.all([host, port]);
  assert.equal(snapshot.settings.host, initial().host);
  assert.equal(snapshot.settings.port, 9000);
  assert.equal(autosave.draft.host, "invalid-host");
  snapshot = { settings: { ...snapshot.settings, history_cache_mib: 128 } };
  autosave.edit("idle_minutes", "30");
  await autosave.commit("idle_minutes");
  assert.equal(commands.at(-1).settings.history_cache_mib, 128);
  assert.equal(commands.at(-1).settings.port, 9000);
});

test("polls and older acknowledgements preserve newer edits and normalize committed values", async () => {
  let release;
  const waiting = new Promise((resolve) => (release = resolve));
  const { autosave } = fixture(async (command) => {
    await waiting;
    return {
      settings: {
        ...command.settings,
        public_url: command.settings.public_url.replace(/\/$/, ""),
      },
    };
  });
  autosave.edit("host", "localhost");
  autosave.sync({ ...initial(), history_cache_mib: 128 });
  assert.equal(autosave.draft.host, "localhost");
  assert.equal(autosave.draft.history_cache_mib, "128");
  const saved = autosave.commit("host");
  autosave.edit("host", "0.0.0.0");
  release();
  await saved;
  assert.equal(autosave.draft.host, "0.0.0.0");
  autosave.edit("public_url", "https://chat.example.test/");
  await autosave.commit("public_url");
  assert.equal(autosave.draft.public_url, "https://chat.example.test");
});

test("failed saves leave the draft retryable and do not poison the command queue", async () => {
  let fail = true;
  const { autosave, commands } = fixture(async (command) => {
    if (fail) return undefined;
    return { settings: command.settings };
  });
  autosave.edit("host", "localhost");
  await autosave.commit("host");
  autosave.sync(initial());
  assert.equal(autosave.draft.host, "localhost");
  fail = false;
  await autosave.commit("host");
  assert.equal(commands.length, 2);
  await autosave.commit("host");
  assert.equal(commands.length, 2);
  const run = createSettingsCommandQueue(async (command) => {
    if (command.kind === "stop-service") throw new Error("fixture failure");
    return { kind: command.kind };
  });
  await assert.rejects(run({ kind: "stop-service" }), /fixture failure/);
  assert.deepEqual(await run({ kind: "start-service" }), {
    kind: "start-service",
  });
});

test("cancelling Web discards incomplete and failed drafts while keeping other edits", async () => {
  const { autosave, commands } = fixture(async () => undefined);
  autosave.sync({ ...initial(), port: 9000 });
  autosave.edit("port", "");
  await autosave.commit("port");
  autosave.edit("host", "invalid-host");
  await autosave.commit("host");
  autosave.edit("public_url", "https://unfinished.example.test");
  autosave.edit("web_trusted_proxies", "192.0.2.10/32");
  autosave.edit("tsnet_port", "");
  autosave.edit("relay_url", "https://relay.example.test");
  autosave.edit("idle_minutes", "30");
  autosave.edit("history_cache_mib", "128");
  autosave.prepareCancel("web")(initial());
  assert.equal(autosave.draft.port, "59001");
  assert.equal(autosave.draft.host, initial().host);
  assert.equal(autosave.draft.public_url, initial().public_url);
  assert.equal(
    autosave.draft.web_trusted_proxies,
    initial().web_trusted_proxies,
  );
  assert.equal(autosave.draft.tsnet_port, "");
  assert.equal(autosave.draft.relay_url, "https://relay.example.test");
  assert.equal(autosave.draft.idle_minutes, "30");
  assert.equal(autosave.draft.history_cache_mib, "128");
  assert.equal(
    commands.length,
    1,
    "cancelling must not save the discarded drafts",
  );
});

test("Relay cancellation preserves runtime and unrelated drafts", () => {
  const { autosave } = fixture();
  autosave.edit("relay_enabled", true);
  autosave.edit("relay_web_enabled", true);
  autosave.edit("relay_url", "https://relay.example.test");
  autosave.edit("relay_lan_port", "");
  autosave.edit("idle_minutes", "");
  autosave.edit("port", "");
  autosave.edit("auto_start", false);
  autosave.prepareCancel("relay")(initial());
  assert.equal(autosave.draft.relay_enabled, false);
  assert.equal(autosave.draft.relay_web_enabled, false);
  assert.equal(autosave.draft.relay_url, "");
  assert.equal(autosave.draft.relay_lan_port, "10095");
  assert.equal(autosave.draft.idle_minutes, "");
  assert.equal(autosave.draft.port, "");
  assert.equal(autosave.draft.auto_start, false);
});

test("Tailscale cancellation resets both service and network drafts", () => {
  const { autosave: service } = fixture();
  const { autosave: network } = fixture(undefined, NetworkSettingsAutosave);
  service.edit("tsnet_enabled", true);
  service.edit("tsnet_hostname", "unfinished-node");
  service.edit("tsnet_port", "");
  service.edit("port", "");
  network.edit("proxy_enabled", false);
  network.edit("use_system_proxy", true);
  network.edit("proxy_url", "http://unfinished-proxy");
  network.edit("no_proxy", "internal.example");
  network.prepareCancel("web")(initial());
  assert.equal(network.draft.proxy_url, "http://unfinished-proxy");
  assert.equal(network.draft.no_proxy, "internal.example");
  service.prepareCancel("tsnet")(initial());
  network.prepareCancel("tsnet")(initial());
  assert.equal(service.draft.tsnet_enabled, false);
  assert.equal(service.draft.tsnet_hostname, "spool-server");
  assert.equal(service.draft.tsnet_port, "443");
  assert.equal(service.draft.port, "");
  assert.deepEqual(network.draft, {
    proxy_enabled: true,
    use_system_proxy: false,
    proxy_url: "",
    no_proxy: "",
  });
});

test("cancellation preserves edits made while the request is pending", async () => {
  const { autosave } = fixture();
  autosave.edit("port", "");
  autosave.edit("host", "localhost");
  const reset = autosave.prepareCancel("web");
  autosave.edit("port", "9100");
  reset(initial());
  assert.equal(autosave.draft.port, "9100");
  assert.equal(autosave.draft.host, initial().host);
  autosave.sync(initial());
  assert.equal(autosave.draft.port, "9100");
  await autosave.commit("port");
  assert.equal(autosave.draft.port, "9100");
});
