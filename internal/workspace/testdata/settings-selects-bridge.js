// Isolated native-bridge fixture: no real settings, providers or scheduled jobs.
const model = (id, levels) => ({
  model: id, displayName: id, defaultReasoningEffort: "",
  supportedReasoningEfforts: levels.map((reasoningEffort) => ({ reasoningEffort, description: "" })),
});
const snapshot = {
  section: "Appearance", message: "", version: "test", database: "", application_log: "", crash_log: "", update_log: "",
  github_path: "", github_state: "", github_message: "", github_login: "",
  settings: { host: "127.0.0.1", port: 8787, public_url: "", idle_minutes: 10, history_cache_mib: 256, auto_start: false },
  preferences: { theme: "system", language: "system" },
  update_preferences: { automatic_checks: false, automatic_installs: false, check_interval_minutes: 60 },
  status: { Running: false, RestartRequired: false, Address: "", Error: "", Online: [] },
  updates: { Enabled: false, Checking: false, Installing: false, Installed: false, Version: "", Notes: "", Error: "", ErrorPhase: "", LastCheck: "", Downloaded: 0, Total: 0 },
  users: [],
  projects: [{
    id: "select-fixture", name: "Select fixture", path: "/fixture", members: [], member_access: {}, member_own_access: {}, archived: false, shared_chats: false, shared_local_chats: false,
    issues: { enabled: true, interval_minutes: 60, labels: [], repository: "fixture/test", user_id: "", assignee_ids: [], backend: "codex", model: "codex-a", effort: "high", permission: "ask" },
  }],
  agents: [
    { backend: "codex", enabled: true, installed: true, executable: "", resolved_path: "", version: "test", checked_at: 0, catalog: { defaultModel: "codex-a", defaultEffort: "high", models: [model("codex-a", ["low", "high"]), model("codex-fixed", [])] } },
    { backend: "claude", enabled: true, installed: true, executable: "", resolved_path: "", version: "test", checked_at: 0, catalog: { defaultModel: "claude-a", defaultEffort: "", models: [model("claude-a", ["medium", "max"]), model("claude-fixed", [])] } },
  ],
};
window.smokeSettings = { snapshot, commands: [] };
window.mygo = {
  on() { return () => {}; },
  async call(method, command) {
    if (method === "DesktopSettings.Snapshot") return structuredClone(snapshot);
    if (method !== "DesktopSettings.Execute") throw new Error("Unexpected bridge call: " + method);
    window.smokeSettings.commands.push(structuredClone(command));
    if (command.kind === "save-appearance") snapshot.preferences = command.preferences;
    if (command.kind === "navigate") snapshot.section = command.section;
    if (command.kind === "save-project") snapshot.projects = [command.project];
    await new Promise((resolve) => setTimeout(resolve, 40));
    return structuredClone(snapshot);
  },
};
