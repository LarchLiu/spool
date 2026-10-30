// Isolated native-bridge fixture: no real settings, providers or scheduled jobs.
const model = (id, levels) => ({
  model: id, displayName: id, defaultReasoningEffort: "",
  supportedReasoningEfforts: levels.map((reasoningEffort) => ({ reasoningEffort, description: "" })),
});
const snapshot = {
  section: "Appearance", message: "", version: "test", database: "", application_log: "", crash_log: "", update_log: "",
  github_path: "", github_state: "", github_message: "", github_login: "",
  settings: { host: "127.0.0.1", port: 8787, public_url: "", idle_minutes: 10, history_cache_mib: 256, auto_start: false, tsnet_enabled: false, tsnet_hostname: "spool-server", tsnet_port: 443, use_system_proxy: false },
  preferences: { theme: "system", language: "system" },
  update_preferences: { automatic_checks: false, automatic_installs: false, check_interval_minutes: 60 },
  status: { Running: false, RestartRequired: false, Address: "", Error: "", Online: [], TSNet: { State: "Disabled", AuthURL: "", URL: "", Error: "" }, TSNetAuthKeySet: false },
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
window.smokeSettings = { snapshot, commands: [], snapshots: 0, executes: 0, sectionListeners: [] };
window.mygo = {
  on(event, listener) {
    if (event === "spool:settings-section") window.smokeSettings.sectionListeners.push(listener);
    return () => {};
  },
  async call(method, command) {
    if (method === "DesktopSettings.Snapshot") {
      window.smokeSettings.snapshots++;
      return structuredClone(snapshot);
    }
    if (method === "DesktopSettings.Action") {
      window.smokeSettings.commands.push(structuredClone(command));
      if (command.kind === "navigate") snapshot.section = command.section;
      if (command.kind === "copy-tsnet-url") {
        if (window.smokeSettings.holdNextCopy) {
          window.smokeSettings.holdNextCopy = false;
          await new Promise((resolve) => { window.smokeSettings.releaseCopy = resolve; });
        }
        if (window.smokeSettings.failNextCopy) {
          window.smokeSettings.failNextCopy = false;
          throw new Error("Clipboard is temporarily unavailable");
        }
      }
      return true;
    }
    if (method === "DesktopSettings.TSNetRegister") {
      window.smokeSettings.commands.push({ kind: "tsnet-register" });
      snapshot.status.TSNet = { State: "Registering", AuthURL: "", URL: "", Error: "" };
      const node = snapshot.status.TSNet;
      if (window.smokeSettings.holdNextRegistration) {
        window.smokeSettings.holdNextRegistration = false;
        await new Promise((resolve) => { window.smokeSettings.releaseRegistration = resolve; });
      }
      if (snapshot.status.TSNet !== node) throw new Error("Tailscale registration was canceled. Try again when the service is ready.");
      if (window.smokeSettings.failNextRegistration) {
        window.smokeSettings.failNextRegistration = false;
        snapshot.status.TSNet.State = "Error";
        throw new Error("Auth Key rejected [redacted]");
      }
      window.smokeSettings.registered = true;
      snapshot.status.TSNet = { State: "Registered", AuthURL: "", URL: "", Error: "" };
      snapshot.status.RestartRequired = true;
      return true;
    }
    if (method === "DesktopSettings.TSNetLogin") {
      window.smokeSettings.commands.push({ kind: "tsnet-login" });
      const node = snapshot.status.TSNet;
      if (window.smokeSettings.holdNextLogin) {
        window.smokeSettings.holdNextLogin = false;
        await new Promise((resolve) => { window.smokeSettings.releaseLogin = resolve; });
      } else {
        await new Promise((resolve) => setTimeout(resolve, 150));
      }
      if (snapshot.status.TSNet !== node) throw new Error("Tailscale service was restarted or stopped. Try signing in again.");
      if (window.smokeSettings.failNextLogin) {
        window.smokeSettings.failNextLogin = false;
        throw new Error("Tailscale did not provide a login link. Check network access to Tailscale and try again, or use an Auth Key.");
      }
      snapshot.status.TSNet.AuthURL = "https://login.tailscale.com/a/test-only";
      return true;
    }
    if (method !== "DesktopSettings.Execute") throw new Error("Unexpected bridge call: " + method);
    window.smokeSettings.executes++;
    window.smokeSettings.commands.push(structuredClone(command));
    if (command.kind === "save-appearance") snapshot.preferences = command.preferences;
    if (command.kind === "navigate") snapshot.section = command.section;
    if (command.kind === "save-project") snapshot.projects = [command.project];
    if (command.kind === "save-settings") {
      snapshot.settings = command.settings;
      snapshot.status.RestartRequired = true;
    }
    if (command.kind === "save-tsnet-auth-key") {
      snapshot.status.TSNetAuthKeySet = Boolean(command.auth_key);
      snapshot.status.RestartRequired = true;
    }
    if (command.kind === "restart-service") {
      snapshot.status.Running = true;
      snapshot.status.RestartRequired = false;
      snapshot.status.TSNet = window.smokeSettings.registered || snapshot.status.TSNetAuthKeySet
        ? { State: "Running", AuthURL: "", URL: "https://spool.example.ts.net", Error: "" }
        : { State: "NeedsLogin", AuthURL: "", URL: "", Error: "" };
      snapshot.status.TSNetAuthKeySet = false;
      snapshot.settings.public_url = snapshot.status.TSNet.URL;
    }
    await new Promise((resolve) => setTimeout(resolve, 40));
    return structuredClone(snapshot);
  },
};
