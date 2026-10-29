import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import {
  createSettingsCommandQueue,
  ServiceSettingsAutosave,
} from "../.test-build/settings-autosave.js";

const initial = () => ({
  host: "127.0.0.1",
  port: 8787,
  public_url: "",
  idle_minutes: 15,
  history_cache_mib: 64,
  auto_start: true,
});
function fixture(execute) {
  const commands = [];
  const run = createSettingsCommandQueue(async (command) => {
    commands.push(command);
    return execute ? execute(command) : { settings: command.settings };
  });
  let changes = 0;
  const autosave = new ServiceSettingsAutosave(
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

test("every connection/runtime setting saves without an explicit save action", async () => {
  const { autosave, commands } = fixture();
  for (const [key, value] of [
    ["host", "localhost"],
    ["port", "9000"],
    ["public_url", "https://chat.example.test"],
    ["idle_minutes", "30"],
    ["history_cache_mib", "128"],
    ["auto_start", false],
  ]) {
    autosave.edit(key, value);
    await autosave.commit(key);
  }
  assert.equal(commands.length, 6);
  assert.deepEqual(commands.at(-1).settings, {
    host: "localhost",
    port: 9000,
    public_url: "https://chat.example.test",
    idle_minutes: 30,
    history_cache_mib: 128,
    auto_start: false,
  });
  await autosave.commit("auto_start");
  assert.equal(commands.length, 6, "unchanged values must not be resaved");
  assert.ok(commands.every((command) => command.kind === "save-settings"));
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

test("service UI commits on blur/Enter, removes save controls and restarts only on click", async () => {
  const pages = await readFile(
    new URL("../src/settings-pages.tsx", import.meta.url),
    "utf8",
  );
  const service = pages.slice(
    pages.indexOf("export function ServicePage"),
    pages.indexOf("export function PeoplePage"),
  );
  assert.doesNotMatch(service, /<form|<Save|Save settings|submit\(/);
  assert.equal((service.match(/onCommit=/g) ?? []).length, 5);
  assert.match(
    service,
    /autosave\.edit\("auto_start", v\);\s*void autosave\.commit\("auto_start"\)/,
  );
  assert.match(service, /data\.status\.RestartRequired &&/);
  assert.match(
    service,
    /onClick=\{\(\) => void run\(\{ kind: "restart-service" \}\)\}/,
  );
  const fields = await readFile(
    new URL("../src/settings-ui.tsx", import.meta.url),
    "utf8",
  );
  assert.match(fields, /onBlur=.*[\s\S]*reportValidity\(\)[\s\S]*onCommit\(\)/);
  assert.match(fields, /e\.key === "Enter" && !e\.nativeEvent\.isComposing/);
  const app = await readFile(
    new URL("../src/settings-app.tsx", import.meta.url),
    "utf8",
  );
  assert.match(app, /createSettingsCommandQueue/);
  assert.match(app, /setBusy\(command\.kind !== "save-settings"\)/);
});

test("native smoke requires real page focus and blur before accepting autosave", async () => {
  const source = await readFile(
    new URL("../../cmd/desktop/smoke.go", import.meta.url),
    "utf8",
  );
  const probe = source.slice(
    source.indexOf("func (p *panel) probeSettings("),
    source.indexOf("func (p *panel) probeAboutSettings("),
  );
  assert.match(probe, /p\.showMainWindow\(\)\s+view\.Focus\(\)/);
  const focus = probe.indexOf("return document.hasFocus();");
  assert.ok(
    focus >= 0 && focus < probe.indexOf("smokeSetInput(input, 'localhost')"),
  );
  assert.match(probe, /input\.addEventListener\('blur'/);
  assert.match(probe, /blur: window\.smokeServiceBlurred === true/);
  assert.match(probe, /saved: snapshot\.settings\.host === 'localhost'/);
  assert.doesNotMatch(
    probe,
    /dispatchEvent\(new (?:FocusEvent|Event)\('(?:blur|focusout)'/,
  );
  assert.match(probe, /nativeSection: snapshot\.section === %q/);
  assert.match(probe, /compact settings %s: %w/);
});
