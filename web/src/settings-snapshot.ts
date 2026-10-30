import type {
  SettingsCommand,
  SettingsSection,
  SettingsSnapshot,
} from "./settings-client";

// Bridge replies are JSON DTOs. Keep unchanged subtrees referentially stable so
// polling one live status does not invalidate every settings page and draft.
export function shareSettingsSnapshot<T>(previous: T, next: T): T {
  if (Object.is(previous, next)) return previous;
  if (
    previous === null ||
    next === null ||
    typeof previous !== "object" ||
    typeof next !== "object" ||
    Array.isArray(previous) !== Array.isArray(next)
  )
    return next;
  const before = previous as Record<string, unknown>;
  const after = next as Record<string, unknown>;
  const keys = Object.keys(after);
  let equal = keys.length === Object.keys(before).length;
  const result: Record<string, unknown> = Array.isArray(next)
    ? ([] as unknown as Record<string, unknown>)
    : {};
  for (const key of keys) {
    const value = shareSettingsSnapshot(before[key], after[key]);
    result[key] = value;
    if (!Object.hasOwn(before, key) || value !== before[key]) equal = false;
  }
  return equal ? previous : (result as T);
}

export function settingsPageDataEqual(
  section: SettingsSection,
  a: SettingsSnapshot,
  b: SettingsSnapshot,
) {
  switch (section) {
    case "Service":
      return (
        a.settings === b.settings &&
        a.database === b.database &&
        a.application_log === b.application_log &&
        a.crash_log === b.crash_log &&
        a.status.Running === b.status.Running &&
        a.status.RestartRequired === b.status.RestartRequired &&
        a.status.Address === b.status.Address &&
        a.status.Error === b.status.Error &&
        a.status.TSNet === b.status.TSNet &&
        a.status.TSNetAuthKeySet === b.status.TSNetAuthKeySet
      );
    case "Network":
      return (
        a.settings.use_system_proxy === b.settings.use_system_proxy &&
        a.status.RestartRequired === b.status.RestartRequired
      );
    case "People":
      return a.users === b.users;
    case "Projects":
      return (
        a.projects === b.projects &&
        a.users === b.users &&
        a.agents === b.agents &&
        a.github_path === b.github_path &&
        a.github_message === b.github_message
      );
    case "Agents":
      return a.agents === b.agents;
    case "Online":
      return a.status.Online === b.status.Online;
    case "Appearance":
      return (
        a.preferences === b.preferences &&
        a.notifications_enabled === b.notifications_enabled &&
        a.notifications_supported === b.notifications_supported
      );
    case "About":
      return (
        a.updates === b.updates &&
        a.update_preferences === b.update_preferences &&
        a.version === b.version &&
        a.update_log === b.update_log
      );
  }
}

export function isSettingsAction(command: SettingsCommand): boolean {
  return [
    "navigate",
    "open-chat",
    "open-browser",
    "show-file",
    "copy-tsnet-url",
  ].includes(command.kind);
}

// Autosaves never disable a page. Longer mutations disable only their own
// controls, leaving navigation and other pages usable without a global flash.
export function settingsCommandScope(
  command: SettingsCommand,
): SettingsSection | undefined {
  switch (command.kind) {
    case "start-service":
    case "stop-service":
    case "restart-service":
      return "Service";
    case "save-person":
      return "People";
    case "save-project":
    case "save-github":
    case "check-github":
    case "check-repository":
      return "Projects";
    case "save-agent":
    case "refresh-agents":
      return "Agents";
    case "test-notification":
      return "Appearance";
    case "restart-update":
      return "About";
  }
}
