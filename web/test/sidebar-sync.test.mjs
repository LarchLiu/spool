import assert from "node:assert/strict";
import test from "node:test";
import { createSidebarSync } from "../.test-build/sidebar-sync.js";

const snapshot = (revision = 0, pinned = [], collapsed = {}) => ({
  revision, pinned, collapsed: { pinned: false, projects: false, recents: false, ...collapsed },
});
const tick = () => new Promise(resolve => setImmediate(resolve));
function fixture(t, initial = snapshot()) {
  const requests = [], errors = [];
  const sync = createSidebarSync(initial, (change, signal) => new Promise((resolve, reject) => {
    requests.push({ change, signal, resolve, reject });
  }), error => errors.push(error));
  sync.start(); t.after(() => sync.stop());
  return { sync, requests, errors };
}

test("sidebar sync waits for the server and rebases queued changes over other devices", async t => {
  const { sync, requests } = fixture(t);
  sync.togglePin("local");
  assert.deepEqual(sync.snapshot().pinned, ["local"]);
  assert.equal(requests.length, 0);
  sync.accept(snapshot(1, ["remote"]));
  assert.deepEqual(requests[0].change, { chat_id: "local", pinned: true });
  sync.togglePin("next");
  sync.toggleSection("projects");
  assert.equal(requests.length, 1, "serialize writes from this device");
  sync.accept(snapshot(3, ["other", "local", "remote"], { recents: true }));
  requests[0].resolve(snapshot(2, ["local", "remote"]));
  await tick();
  assert.deepEqual(sync.snapshot().pinned, ["next", "other", "local", "remote"]);
  assert.equal(sync.snapshot().collapsed.projects, true);
  assert.equal(sync.snapshot().collapsed.recents, false, "remote sections do not affect local state");
  assert.deepEqual(requests[1].change, { chat_id: "next", pinned: true });
  requests[1].resolve(snapshot(4, ["next", "other", "local", "remote"]));
  await tick();
  sync.accept(snapshot(1));
  assert.equal(sync.snapshot().revision, 4, "ignore old fallback snapshots");
});

test("sidebar sync imports old local settings only into an uninitialized account", async t => {
  const local = snapshot(0, ["local"], { projects: true });
  const { sync, requests } = fixture(t, local);
  sync.toggleSection("recents");
  sync.accept(snapshot());
  assert.deepEqual(requests[0].change, { initial: { pinned: ["local"] } });
  assert.deepEqual(sync.snapshot().pinned, ["local"]);
  assert.equal(sync.snapshot().collapsed.recents, true);
  // Another device won the migration race before our import was processed.
  requests[0].resolve(snapshot(1, ["remote"]));
  await tick();
  assert.deepEqual(sync.snapshot().pinned, ["remote"]);
  assert.equal(requests.length, 1, "section toggles never write to the server");
  assert.equal(sync.snapshot().collapsed.projects, true);
  const established = fixture(t, local);
  established.sync.accept(snapshot(8, ["server"]));
  assert.equal(established.requests.length, 0);
  assert.deepEqual(established.sync.snapshot().pinned, ["server"]);
  assert.equal(established.sync.snapshot().collapsed.projects, true);
  const localSections = fixture(t, snapshot(0, [], { projects: true }));
  localSections.sync.accept(snapshot()); localSections.sync.toggleSection("recents");
  assert.equal(localSections.requests.length, 0, "local sections do not initialize a server record");
});

test("failed operations roll back without discarding newer device changes or queued edits", async t => {
  const { sync, requests, errors } = fixture(t);
  sync.accept(snapshot(1));
  sync.togglePin("rejected");
  sync.toggleSection("recents");
  sync.accept(snapshot(2, ["remote"], { projects: true }));
  const failure = new Error("Could not save preferences");
  requests[0].reject(failure);
  await tick();
  assert.deepEqual(errors, [failure]);
  assert.deepEqual(sync.snapshot().pinned, ["remote"]);
  assert.deepEqual(sync.snapshot().collapsed, { pinned: false, projects: false, recents: true });
  assert.equal(requests.length, 1);
});

test("account teardown fences in-flight replies and empty local state does not initialize the server", async t => {
  const { sync, requests } = fixture(t);
  sync.accept(snapshot());
  assert.equal(requests.length, 0);
  sync.togglePin("alice");
  sync.stop();
  assert.equal(requests[0].signal.aborted, true);
  requests[0].resolve(snapshot(1, ["alice"]));
  await tick();
  assert.equal(sync.snapshot().revision, 0);
  const other = fixture(t);
  other.sync.accept(snapshot(5, ["bob"]));
  assert.deepEqual(other.sync.snapshot().pinned, ["bob"]);
  sync.start();
  assert.equal(requests.length, 2, "effect restart retries an explicit operation safely");
  requests[1].resolve(snapshot(1, ["alice"]));
  await tick();
  assert.equal(sync.snapshot().revision, 1);
  assert.deepEqual(other.sync.snapshot().pinned, ["bob"]);
});
