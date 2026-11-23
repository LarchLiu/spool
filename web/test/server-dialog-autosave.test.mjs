import assert from "node:assert/strict";
import test from "node:test";
import { ServerDialogAutosave } from "../.test-build/server-dialog-autosave.js";

const profile = (overrides = {}) => ({
  id: "server", name: "Original", server_name: "Home", address: "https://home.example/",
  direct: "https://home.example/", tailscale: "", relay: "", mode: "direct",
  enabled: true, automatic: true, ...overrides,
});
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
};

test("field autosaves leave incomplete addresses editable and do not submit them with switches", async () => {
  const calls = [];
  const editor = new ServerDialogAutosave(profile(), async (next) => {
    calls.push(next);
    return next;
  }, () => {});
  editor.edit("direct", "https://");
  editor.edit("name", "Renamed");
  editor.commit("name");
  editor.edit("automatic", false);
  editor.commit("automatic");
  await editor.flush((key) => key !== "direct");
  assert.equal(calls.length, 2);
  assert.equal(calls[0].direct, "https://home.example/");
  assert.equal(calls[1].name, "Renamed");
  assert.equal(calls[1].automatic, false);
  assert.equal(editor.draft.direct, "https://");
});

test("rapid saves merge in order, reuse newly created identity, and preserve a newer edit", async () => {
  const held = deferred();
  const calls = [];
  const editor = new ServerDialogAutosave(profile({ id: "", name: "", enabled: false }), async (next) => {
    calls.push(next);
    if (calls.length === 1) await held.promise;
    return { ...next, id: "created", name: next.name.trim() };
  }, () => {});
  editor.edit("name", " First ");
  editor.commit("name");
  await Promise.resolve();
  editor.edit("name", "Latest");
  editor.commit("name");
  editor.edit("tailscale", "https://tail.example");
  editor.commit("tailscale");
  held.resolve();
  assert.equal(await editor.flush(), true);
  assert.equal(calls.length, 3);
  assert.equal(calls[0].id, "");
  assert.equal(calls[1].id, "created");
  assert.equal(calls[2].id, "created");
  assert.equal(calls[2].name, "Latest");
  assert.equal(editor.draft.name, "Latest");
  assert.equal(editor.draft.tailscale, "https://tail.example");
});

test("toggling back while a save is pending persists the newer switch value", async () => {
  const held = deferred();
  const calls = [];
  const editor = new ServerDialogAutosave(profile(), async (next) => {
    calls.push(next);
    if (calls.length === 1) await held.promise;
    return next;
  }, () => {});
  editor.edit("automatic", false);
  editor.commit("automatic");
  await Promise.resolve();
  editor.edit("automatic", true);
  editor.commit("automatic");
  held.resolve();
  await editor.flush();
  assert.deepEqual(calls.map((value) => value.automatic), [false, true]);
  assert.equal(editor.draft.automatic, true);
});

test("failed saves retain input, retry, and do not poison the next field", async () => {
  let failed = true;
  const calls = [];
  const editor = new ServerDialogAutosave(profile(), async (next) => {
    calls.push(next);
    if (failed) throw new Error("save failed");
    return next;
  }, () => {});
  editor.edit("name", "Retry");
  assert.equal(await editor.flush(), false);
  assert.equal(editor.draft.name, "Retry");
  failed = false;
  editor.edit("automatic", false);
  editor.commit("automatic");
  assert.equal(await editor.flush(), true);
  assert.equal(calls.at(-1).name, "Retry");
  assert.equal(calls.at(-1).automatic, false);
});

test("opening another server discards queued work and ignores the previous acknowledgement", async () => {
  const held = deferred();
  const calls = [];
  const editor = new ServerDialogAutosave(profile(), async (next) => {
    calls.push(next);
    await held.promise;
    return next;
  }, () => {});
  editor.edit("name", "Old save");
  editor.commit("name");
  editor.edit("automatic", false);
  editor.commit("automatic");
  await Promise.resolve();
  editor.reset(profile({ id: "another", name: "Another" }));
  held.resolve();
  await held.promise;
  await Promise.resolve();
  await editor.flush();
  assert.equal(calls.length, 1);
  assert.equal(editor.draft.id, "another");
  assert.equal(editor.draft.name, "Another");
});
