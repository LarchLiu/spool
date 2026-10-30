import type { UIPreferences } from "./preferences";
import type { Online, User } from "./types";

export const settingsSections = [
  "Service",
  "People",
  "Projects",
  "Agents",
  "Online",
  "Network",
  "Appearance",
  "About",
] as const;
export type SettingsSection = (typeof settingsSections)[number];
export type Backend = "codex" | "claude";
export type ServerSettings = {
  host: string;
  port: number;
  public_url: string;
  idle_minutes: number;
  history_cache_mib: number;
  auto_start: boolean;
  tsnet_enabled: boolean;
  tsnet_hostname: string;
  tsnet_port: number;
  use_system_proxy: boolean;
};
export type Person = User & {
  disabled: boolean;
};
export type PersonDraft = Person & { password: string };
export type Automation = {
  enabled: boolean;
  interval_minutes: number;
  labels: string[];
  repository: string;
  user_id: string;
  assignee_ids?: string[];
  backend: Backend;
  model?: string;
  effort?: string;
  permission?: string;
};
export type ManagedProject = {
  id: string;
  name: string;
  path: string;
  members: string[];
  member_access: Record<string, "read-only" | "control">;
  member_own_access: Record<string, "read-only" | "create">;
  archived: boolean;
  shared_chats: boolean;
  shared_local_chats?: boolean;
  issues: Automation;
  next_run?: number;
  last_error?: string;
  last_chat?: string;
};
export type ManagedAgent = {
  backend: Backend;
  enabled: boolean;
  installed: boolean;
  executable: string;
  resolved_path: string;
  version: string;
  error?: string;
  checked_at: number;
  catalog: {
    defaultModel?: string;
    defaultEffort?: string;
    models: {
      model: string;
      displayName: string;
      supportedReasoningEfforts: { reasoningEffort: string }[];
    }[];
  };
};
export type UpdatePreferences = {
  automatic_checks: boolean;
  automatic_installs: boolean;
  check_interval_minutes: 30 | 60 | 360 | 720 | 1440;
};
export type ReleaseNotes = {
  version: string;
  name: string;
  body: string;
  url: string;
  published_at: string;
  prerelease: boolean;
};
export type ReleaseHistory = {
  current_version: string;
  target_version: string;
  releases: ReleaseNotes[];
};
export type SettingsSnapshot = {
  section: string;
  message: string;
  settings: ServerSettings;
  preferences: UIPreferences;
  notifications_enabled?: boolean;
  notifications_supported?: boolean;
  update_preferences: UpdatePreferences;
  status: {
    Running: boolean;
    RestartRequired: boolean;
    Address: string;
    Error: string;
    Online: (Online & Pick<User, "nickname" | "avatar">)[];
    TSNet?: { State: string; AuthURL: string; URL: string; Error: string };
    TSNetAuthKeySet?: boolean;
  };
  users: Person[];
  projects: ManagedProject[];
  agents: ManagedAgent[];
  updates: {
    Enabled: boolean;
    Checking: boolean;
    Installing: boolean;
    Installed: boolean;
    Version: string;
    Notes: string;
    Error: string;
    ErrorPhase: string;
    LastCheck: string;
    Downloaded: number;
    Total: number;
  };
  version: string;
  database: string;
  application_log: string;
  crash_log: string;
  update_log: string;
  github_path: string;
  github_state: string;
  github_message: string;
  github_login: string;
  saved_id?: string;
  repository?: string;
};
export type SettingsCommand =
  | { kind: "navigate"; section: SettingsSection }
  | { kind: "save-settings"; settings: ServerSettings }
  | { kind: "save-tsnet-auth-key"; auth_key: string }
  | { kind: "save-appearance"; preferences: UIPreferences }
  | { kind: "save-notifications"; enabled: boolean }
  | { kind: "test-notification" }
  | { kind: "save-updates"; update_preferences: UpdatePreferences }
  | { kind: "save-person"; person: PersonDraft }
  | { kind: "save-project"; project: ManagedProject }
  | { kind: "save-agent"; backend: Backend; enabled: boolean; path: string }
  | { kind: "save-github" | "check-repository"; path: string }
  | {
      kind: "show-file";
      target: "database" | "application" | "crash" | "update";
    }
  | {
      kind:
        | "open-chat"
        | "open-browser"
        | "tsnet-login"
        | "copy-tsnet-url"
        | "start-service"
        | "stop-service"
        | "restart-service"
        | "refresh-agents"
        | "check-github"
        | "check-updates"
        | "install-update"
        | "restart-update";
    };

type NativeRuntime = {
  call: <T>(method: string, ...args: unknown[]) => Promise<T>;
  on: (event: string, listener: (value: string) => void) => () => void;
};
function runtime(surface: unknown = globalThis): NativeRuntime | undefined {
  if (typeof surface !== "object" || !surface || !("mygo" in surface)) return;
  const value = surface.mygo;
  if (
    typeof value === "object" &&
    value &&
    "call" in value &&
    typeof value.call === "function" &&
    "on" in value &&
    typeof value.on === "function"
  )
    return value as NativeRuntime;
}
export function hasSettingsBridge(surface: unknown = globalThis) {
  return !!runtime(surface);
}
export function settingsCall<T>(
  method:
    | "Action"
    | "Snapshot"
    | "Execute"
    | "ChoosePath"
    | "ReleaseNotes"
    | "TSNetLogin"
    | "TSNetRegister",
  ...args: unknown[]
): Promise<T> {
  const bridge = runtime();
  if (!bridge)
    return Promise.reject(
      new Error(
        "Desktop settings are only available in the Spool desktop app.",
      ),
    );
  return bridge.call<T>(`DesktopSettings.${method}`, ...args);
}
export function subscribeSettingsSection(
  listener: (section: SettingsSection) => void,
) {
  return (
    runtime()?.on("spool:settings-section", (value) => {
      if (settingsSections.includes(value as SettingsSection))
        listener(value as SettingsSection);
    }) ?? (() => {})
  );
}
export function newPerson(): PersonDraft {
  return {
    id: "",
    username: "",
    nickname: "",
    password: "",
    disabled: false,
  };
}
export function newProject(): ManagedProject {
  return {
    id: "",
    name: "",
    path: "",
    members: [],
    member_access: {},
    member_own_access: {},
    archived: false,
    shared_chats: false,
    shared_local_chats: false,
    issues: {
      enabled: false,
      interval_minutes: 60,
      labels: [],
      repository: "",
      user_id: "",
      assignee_ids: [],
      backend: "codex",
    },
  };
}
export function projectDraft(project: ManagedProject): ManagedProject {
  const copy = structuredClone(project);
  copy.members ??= [];
  copy.member_access ??= {};
  copy.member_own_access ??= {};
  copy.issues.labels ??= [];
  copy.issues.assignee_ids ??= copy.issues.user_id ? [copy.issues.user_id] : [];
  return copy;
}
export function projectMembers(
  project: ManagedProject,
  members: string[],
): ManagedProject {
  const ids = (project.issues.assignee_ids ?? []).filter((id) =>
    members.includes(id),
  );
  return {
    ...project,
    members,
    member_access: Object.fromEntries(
      members.map((id) => [id, project.member_access?.[id] ?? "control"]),
    ),
    member_own_access: Object.fromEntries(
      members.map((id) => [id, project.member_own_access?.[id] ?? "create"]),
    ),
    issues: {
      ...project.issues,
      assignee_ids: ids,
      user_id: ids.includes(project.issues.user_id)
        ? project.issues.user_id
        : (ids[0] ?? ""),
    },
  };
}
