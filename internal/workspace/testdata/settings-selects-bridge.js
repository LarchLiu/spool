// Isolated native-bridge fixture: no real settings, providers or scheduled jobs.
const model = (id, levels) => ({
  model: id, displayName: id, defaultReasoningEffort: "",
  supportedReasoningEfforts: levels.map((reasoningEffort) => ({ reasoningEffort, description: "" })),
});
const snapshot = {
  section: "Appearance", message: "", version: "test", database: "/fixture/spool.sqlite", application_log: "/fixture/application.log", crash_log: "/fixture/crash.log", update_log: "/fixture/update.log",
  github_path: "", github_state: "", github_message: "", github_login: "",
  settings: { web_enabled: true, host: "127.0.0.1", port: 8787, public_url: "https://chat.example.com", idle_minutes: 10, history_cache_mib: 256, auto_start: false, tsnet_enabled: false, tsnet_hostname: "spool-server", tsnet_port: 443, proxy_enabled: true, use_system_proxy: false, proxy_url: "", no_proxy: "" },
  preferences: { theme: "system", language: "system" },
  update_preferences: { automatic_checks: false, automatic_installs: false, check_interval_minutes: 60 },
  status: { Running: true, RestartRequired: false, PendingChanges: [], Address: "127.0.0.1:8787", Error: "", Online: [], TSNet: { State: "Disabled", AuthURL: "", URL: "", Error: "" }, TSNetAuthKeySet: false, Voxl: { installed: true, installing: false, running: false, directory: "/fixture/voxl" } },
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
let activeSettings = structuredClone(snapshot.settings);
const entryFields = {
  web: ["web_enabled", "host", "port", "public_url"],
  relay: ["relay_enabled", "relay_url", "relay_lan_port", "relay_web_enabled"],
  tsnet: ["tsnet_enabled", "tsnet_hostname", "tsnet_port", "proxy_enabled", "use_system_proxy", "proxy_url", "no_proxy"],
};
const refreshPending = () => {
  snapshot.status.PendingChanges = Object.keys(entryFields).filter(target =>
    (target === "web"
      ? snapshot.settings.web_enabled !== activeSettings.web_enabled || snapshot.settings.web_enabled && entryFields.web.some(field => snapshot.settings[field] !== activeSettings[field])
      : entryFields[target].some(field => snapshot.settings[field] !== activeSettings[field])) ||
    target === "tsnet" && snapshot.status.TSNet.State === "Registered");
  snapshot.status.RestartRequired = snapshot.status.PendingChanges.length > 0;
};
window.smokeSettings = { snapshot, commands: [], snapshots: 0, executes: 0, sectionListeners: [] };
window.mygo = {
  on(event, listener) {
    if (event === "spool:settings-section") window.smokeSettings.sectionListeners.push(listener);
    return () => {};
  },
  async call(method, command) {
    if (method === "DesktopSettings.AddressQRCode") {
      window.smokeSettings.commands.push({ kind: "address-qr", address: command });
      if (window.smokeSettings.failNextQR) {
        window.smokeSettings.failNextQR = false;
        throw new Error("Fixture QR failure");
      }
      if (window.smokeSettings.holdNextQR) {
        window.smokeSettings.holdNextQR = false;
        await new Promise(resolve => { window.smokeSettings.releaseQR = resolve; });
      }
      const response = await fetch("/address-qr", { method: "POST", body: JSON.stringify(command) });
      if (!response.ok) throw new Error("Fixture QR generation failed");
      return await response.json();
    }
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
      refreshPending();
      return true;
    }
    if (method === "DesktopSettings.TSNetLogin") {
      window.smokeSettings.commands.push({ kind: "tsnet-login" });
      if (!snapshot.settings.tsnet_enabled) snapshot.status.TSNet = { State: "NeedsLogin", AuthURL: "", URL: "", Error: "" };
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
    if (window.smokeSettings.holdNextCommand === command.kind) {
      window.smokeSettings.holdNextCommand = "";
      await new Promise(resolve => { window.smokeSettings.releaseCommand = resolve; });
    }
    if (command.kind === "save-appearance") snapshot.preferences = command.preferences;
    if (command.kind === "navigate") snapshot.section = command.section;
    if (window.smokeSettings.failNextCommand === command.kind) {
      window.smokeSettings.failNextCommand = "";
      throw new Error("Fixture save failed");
    }
    if (command.kind === "save-project") {
      const project = { ...command.project, id: command.project.id || "new-project" };
      snapshot.projects = [...snapshot.projects.filter(item => item.id !== project.id), project];
      snapshot.saved_id = project.id;
    }
    if (command.kind === "save-person") {
      const { password, ...person } = command.person;
      person.id ||= "new-person";
      snapshot.users = [...snapshot.users.filter(item => item.id !== person.id), person];
      snapshot.saved_id = person.id;
    }
    if (command.kind === "save-agent") {
      snapshot.agents = snapshot.agents.map(agent => agent.backend === command.backend
        ? { ...agent, enabled: command.enabled, executable: command.path } : agent);
    }
    if (command.kind === "save-github") snapshot.github_path = command.path;
    if (command.kind === "save-settings") {
      snapshot.settings = command.settings;
      refreshPending();
    }
    if (command.kind === "save-tsnet-auth-key") {
      snapshot.status.TSNetAuthKeySet = Boolean(command.auth_key);
      snapshot.status.RestartRequired = true;
    }
    if (command.kind === "apply-service-changes") {
      for (const field of entryFields[command.target]) activeSettings[field] = snapshot.settings[field];
      if (command.target === "web") snapshot.status.Address = activeSettings.web_enabled ? `${activeSettings.host}:${activeSettings.port}` : "";
      if (command.target === "relay") snapshot.status.Relay = {
        ...snapshot.status.Relay,
        State: activeSettings.relay_enabled ? "connected" : "disabled",
        WebURL: activeSettings.relay_enabled && activeSettings.relay_web_enabled ? "https://relay.example.test/web/browser-server/" : "",
      };
      if (command.target === "tsnet") {
        snapshot.status.TSNet = !activeSettings.tsnet_enabled
          ? { State: "Disabled", AuthURL: "", URL: "", Error: "" }
          : window.smokeSettings.registered || snapshot.status.TSNetAuthKeySet
          ? { State: "Running", AuthURL: "", URL: "https://spool.example.ts.net", Error: "" }
          : { State: "NeedsLogin", AuthURL: "", URL: "", Error: "" };
        snapshot.status.TSNetAuthKeySet = false;
      }
      refreshPending();
    }
    if (command.kind === "cancel-service-changes") {
      for (const field of entryFields[command.target]) snapshot.settings[field] = activeSettings[field];
      refreshPending();
    }
    await new Promise((resolve) => setTimeout(resolve, 40));
    return structuredClone(snapshot);
  },
};
