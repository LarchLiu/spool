import assert from "node:assert/strict";
import test from "node:test";
import {
  shareSettingsSnapshot,
  settingsPageDataEqual,
  settingsCommandScope,
  isSettingsAction,
} from "../.test-build/settings-snapshot.js";
import { settingsSections } from "../.test-build/settings-client.js";

function snapshot() {
  return {
    settings: { use_system_proxy: false },
    status: {
      Running: true,
      RestartRequired: false,
      TSNet: { State: "Running", URL: "https://spool.test.ts.net" },
      Online: [],
    },
    users: [{ id: "person", nickname: "Person" }],
    projects: [{ id: "project", members: ["person"] }],
    agents: [{ backend: "codex", catalog: { models: [{ model: "test" }] } }],
    preferences: { theme: "system", language: "en" },
    updates: { Downloaded: 0 },
    update_preferences: { automatic_checks: true },
    notifications_enabled: true,
    notifications_supported: true,
  };
}

test("identical polling DTOs keep the entire snapshot, arrays and drafts stable", () => {
  const previous = snapshot();
  const next = shareSettingsSnapshot(previous, structuredClone(previous));
  assert.equal(next, previous);
  for (const section of settingsSections)
    assert.equal(settingsPageDataEqual(section, previous, next), true, section);
});

test("changed status shares unrelated nested references without mutating either input", () => {
  const previous = snapshot();
  const incoming = structuredClone(previous);
  incoming.updates.Downloaded = 100;
  const next = shareSettingsSnapshot(previous, incoming);
  assert.notEqual(next, previous);
  assert.notEqual(next.updates, previous.updates);
  assert.equal(previous.updates.Downloaded, 0);
  assert.equal(next.updates.Downloaded, 100);
  for (const key of [
    "settings",
    "status",
    "users",
    "projects",
    "agents",
    "preferences",
  ])
    assert.equal(next[key], previous[key]);
  assert.deepEqual(incoming, { ...previous, updates: { Downloaded: 100 } });
  for (const section of settingsSections)
    assert.equal(
      settingsPageDataEqual(section, previous, next),
      section !== "About",
      section,
    );
});

test("page dependencies distinguish presence, membership, settings and appearance changes", () => {
  const cases = [
    [(next) => next.status.Online.push({ id: "person" }), ["Online"]],
    [(next) => (next.users[0].nickname = "Updated"), ["People", "Projects"]],
    [(next) => (next.status.TSNet.State = "NeedsLogin"), ["Service"]],
    [(next) => (next.settings.use_system_proxy = true), ["Service", "Network"]],
    [(next) => (next.preferences.theme = "dark"), ["Appearance"]],
    [(next) => (next.notifications_enabled = false), ["Appearance"]],
    [(next) => (next.status.RestartRequired = true), ["Service", "Network"]],
    [
      (next) => next.agents[0].catalog.models.push({ model: "new" }),
      ["Agents", "Projects"],
    ],
  ];
  for (const [edit, changed] of cases) {
    const previous = snapshot();
    const incoming = structuredClone(previous);
    edit(incoming);
    const next = shareSettingsSnapshot(previous, incoming);
    for (const section of settingsSections)
      assert.equal(
        settingsPageDataEqual(section, previous, next),
        !changed.includes(section),
        section,
      );
  }
});

test("structural sharing retains item identity and handles insertions, removals and nulls", () => {
  const first = { id: "a", name: "A" };
  const second = { id: "b", name: "B" };
  const previous = [first, second];
  const next = shareSettingsSnapshot(previous, [
    { ...first },
    { ...second, name: "New" },
  ]);
  assert.equal(next[0], first);
  assert.notEqual(next[1], second);
  assert.deepEqual(shareSettingsSnapshot(previous, [{ ...first }]), [first]);
  assert.deepEqual(shareSettingsSnapshot(previous, []), []);
  assert.equal(shareSettingsSnapshot({}, null), null);
  assert.deepEqual(shareSettingsSnapshot(null, {}), {});
  assert.deepEqual(shareSettingsSnapshot({ removed: true }, {}), {});
});

test("clipboard, navigation and reveal actions bypass mutation scope while saves remain ordered", () => {
  for (const kind of [
    "navigate",
    "copy-tsnet-url",
    "show-file",
    "open-browser",
    "open-chat",
  ]) {
    assert.equal(isSettingsAction({ kind }), true);
    assert.equal(settingsCommandScope({ kind }), undefined);
  }
  for (const [kind, scope] of [
    ["save-settings", undefined],
    ["save-appearance", undefined],
    ["save-updates", undefined],
    ["restart-service", "Service"],
    ["save-person", "People"],
    ["save-project", "Projects"],
    ["refresh-agents", "Agents"],
    ["test-notification", "Appearance"],
    ["restart-update", "About"],
  ]) {
    assert.equal(isSettingsAction({ kind }), false);
    assert.equal(settingsCommandScope({ kind }), scope);
  }
});
