import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
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

test("settings keeps a minimal sidebar with one back icon and right-aligned status, not a version", async () => {
  const source = await readFile(
    new URL("../src/settings-app.tsx", import.meta.url),
    "utf8",
  );
  assert.doesNotMatch(source, /settings-(?:brand|breadcrumb|search)/);
  assert.match(
    source,
    /className="settings-sidebar"[\s\S]*?<h1>\{t\("Settings"\)\}<\/h1>\s*<nav/,
  );
  assert.match(
    source,
    /settings-sidebar-bottom[\s\S]*?className="icon"[\s\S]*?aria-label=\{t\("Back to Chat"\)\}[\s\S]*?<ArrowLeft[^>]*\/>\s*<\/button>\s*<span className="settings-sidebar-status" role="status">[\s\S]*?settings-status-dot/,
  );
  assert.doesNotMatch(
    source,
    /settings-version|\{t\("Back to Chat"\)\}\s*<\/button>/,
  );
  assert.equal((source.match(/kind: "open-chat"/g) ?? []).length, 1);
  assert.doesNotMatch(source, /data\?\.version \|\| "Spool"/);
});

test("settings and chat use the same shared scrollbar rule", async () => {
  const css = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  assert.match(
    css,
    /\.sidebar-scroll,\s*\.history,\s*\.settings-sidebar-content,\s*\.settings-main\s*\{\s*scrollbar-width: thin;\s*scrollbar-color: var\(--border-strong\) transparent;/,
  );
});

test("chat and settings footers share the same height and icon buttons stay circular", async () => {
  const shared = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  const settings = await readFile(
    new URL("../src/settings.css", import.meta.url),
    "utf8",
  );
  assert.match(shared, /--sidebar-footer-height: 64px/);
  assert.match(
    shared,
    /\.side-foot,\s*\.settings-sidebar-bottom \{[^}]*height: var\(--sidebar-footer-height\)/,
  );
  assert.match(
    shared,
    /\.icon \{[^}]*width: 32px;[^}]*height: 32px;[^}]*border-radius: 50%/,
  );
  assert.match(
    shared,
    /\.icon:hover:not\(:disabled\) \{[^}]*background: var\(--control-hover\)/,
  );
  assert.match(
    shared,
    /\.icon:active:not\(:disabled\) \{[^}]*background: var\(--control-active\)/,
  );
  assert.match(settings, /\.settings-sidebar-status \{[^}]*margin-left: auto/);
  assert.match(
    settings,
    /\.settings-sidebar-content \{[^}]*min-height: 0;[^}]*overflow: auto/,
  );
  assert.match(
    settings,
    /\.desktop-settings-shell button:where\(:not\(\.icon\)\) \{/,
  );
  assert.doesNotMatch(settings, /\.settings-sidebar-bottom[^}]*\{[^}]*height:/);
  assert.doesNotMatch(settings, /\.settings-sidebar \{[^}]*padding/);
});

test("chat sends and settings controls share the application primary palette", async () => {
  const shared = await readFile(
    new URL("../src/style.css", import.meta.url),
    "utf8",
  );
  const settings = await readFile(
    new URL("../src/settings.css", import.meta.url),
    "utf8",
  );
  assert.match(shared, /--color-primary: #b74e2d/);
  assert.match(shared, /--color-primary: #ca7657/);
  assert.match(shared, /\.send \{[^}]*background: var\(--color-primary\)/);
  assert.match(
    shared,
    /\.send:hover:not\(:disabled\) \{\s*background: var\(--color-primary-hover\)/,
  );
  assert.match(
    settings,
    /\.settings-primary \{[^}]*background: var\(--color-primary\)/,
  );
  assert.match(
    settings,
    /\.settings-toggle\[aria-checked="true"\] \{\s*background: var\(--color-primary\)/,
  );
  assert.match(settings, /accent-color: var\(--color-primary\)/);
  assert.match(
    settings,
    /\.desktop-settings-shell button:where\(:not\(\.icon\)\) \{[^}]*color: var\(--text\)/,
  );
  assert.match(
    settings,
    /\.settings-primary \{\s*color: var\(--color-on-primary\)/,
  );
  assert.doesNotMatch(settings, /(?:^|[;{])\s*color: var\(--color-primary\)/);
  assert.doesNotMatch(settings, /--settings-accent|#3278cf|#2468bb/);
});

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

test("removing members also removes automation controllers and chooses a valid owner", () => {
  const project = newProject();
  project.members = ["a", "b"];
  project.issues.assignee_ids = ["a", "b"];
  project.issues.user_id = "a";
  const next = projectMembers(project, ["b"]);
  assert.deepEqual(next.issues.assignee_ids, ["b"]);
  assert.equal(next.issues.user_id, "b");
  const empty = projectMembers(next, []);
  assert.deepEqual(empty.issues.assignee_ids, []);
  assert.equal(empty.issues.user_id, "");
  assert.deepEqual(project.members, ["a", "b"]);
});
