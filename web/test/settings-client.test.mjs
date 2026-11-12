import assert from "node:assert/strict";
import test from "node:test";
import {
  hasSettingsBridge,
  settingsCall,
  subscribeSettingsSection,
  newPerson,
  newProject,
  projectDraft,
  projectMembers,
  settingsSections,
} from "../.test-build/settings-client.js";

function mockRuntime(t, value) {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, "mygo");
  Object.defineProperty(globalThis, "mygo", {
    value,
    configurable: true,
    writable: true,
  });
  t.after(() => {
    if (descriptor) Object.defineProperty(globalThis, "mygo", descriptor);
    else delete globalThis.mygo;
  });
}

test("management has no HTTP fallback or presentation-flag authorization", async () => {
  for (const surface of [
    null,
    {},
    { spoolDesktop: true },
    { mygo: {} },
    { mygo: { call: true, on() {} } },
  ])
    assert.equal(hasSettingsBridge(surface), false);
  await assert.rejects(settingsCall("Snapshot"), /only available/);
});

test("management calls use the native methods and propagate native failures", async (t) => {
  const calls = [];
  mockRuntime(t, {
    call: async (...args) => {
      calls.push(args);
      return { section: "Service" };
    },
    on() {},
  });
  const command = { kind: "stop-service" };
  assert.deepEqual(await settingsCall("Execute", command), {
    section: "Service",
  });
  await settingsCall("ChoosePath", "directory", "/tmp/project");
  await settingsCall("ReleaseNotes");
  assert.deepEqual(calls, [
    ["DesktopSettings.Execute", command],
    ["DesktopSettings.ChoosePath", "directory", "/tmp/project"],
    ["DesktopSettings.ReleaseNotes"],
  ]);
  globalThis.mygo.call = async () => {
    throw new Error("only the desktop settings page");
  };
  await assert.rejects(
    settingsCall("Snapshot"),
    /only the desktop settings page/,
  );
});

test("native navigation accepts only known settings sections", (t) => {
  let handler,
    removed = false;
  mockRuntime(t, {
    call() {},
    on(event, listener) {
      assert.equal(event, "spool:settings-section");
      handler = listener;
      return () => {
        removed = true;
      };
    },
  });
  const selected = [];
  const off = subscribeSettingsSection((section) => selected.push(section));
  for (const value of [...settingsSections, "Chat", "anything"]) handler(value);
  assert.deepEqual(selected, settingsSections);
  off();
  assert.equal(removed, true);
});

test("new entities do not share mutable state or passwords", () => {
  const first = newProject(),
    second = newProject();
  first.members.push("person");
  first.issues.labels.push("bug");
  assert.deepEqual(second.members, []);
  assert.deepEqual(second.issues.labels, []);
  assert.equal(newPerson().password, "");
  assert.equal("read_only" in newPerson(), false);
  assert.deepEqual(second.member_access, {});
  assert.deepEqual(second.member_own_access, {});
  assert.equal(second.shared_local_chats, false);
  assert.equal(second.allow_external_files, true);
});

test("project edit drafts are independent from polling snapshots and support legacy assignees", () => {
  const project = newProject();
  project.issues.user_id = "person";
  delete project.issues.assignee_ids;
  const draft = projectDraft(project);
  assert.deepEqual(draft.issues.assignee_ids, ["person"]);
  draft.issues.labels.push("bug");
  draft.name = "unsaved";
  assert.deepEqual(project.issues.labels, []);
  assert.equal(project.name, "");
});

test("external session files default on for legacy drafts and retain explicit opt-outs", () => {
  const legacy = newProject();
  delete legacy.allow_external_files;
  assert.equal(projectDraft(legacy).allow_external_files, true);
  const disabled = newProject();
  disabled.allow_external_files = false;
  assert.equal(projectDraft(disabled).allow_external_files, false);
});

test("removing members also removes automation controllers and chooses a valid owner", () => {
  const project = newProject();
  project.members = ["a", "b"];
  project.issues.assignee_ids = ["a", "b"];
  project.issues.user_id = "a";
  project.member_access = { a: "read-only", b: "control" };
  project.member_own_access = { a: "read-only", b: "create" };
  const next = projectMembers(project, ["b"]);
  assert.deepEqual(next.issues.assignee_ids, ["b"]);
  assert.deepEqual(next.member_access, { b: "control" });
  assert.deepEqual(next.member_own_access, { b: "create" });
  assert.deepEqual(projectMembers(next, ["a", "b"]).member_own_access, { a: "create", b: "create" });
  assert.deepEqual(projectMembers(next, ["a", "b"]).member_access, { a: "control", b: "control" });
  assert.equal(next.issues.user_id, "b");
  const empty = projectMembers(next, []);
  assert.deepEqual(empty.issues.assignee_ids, []);
  assert.equal(empty.issues.user_id, "");
  assert.deepEqual(project.members, ["a", "b"]);
});
