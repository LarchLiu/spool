import {
  resolveLanguage,
  type Locale,
  type UIPreferences,
} from "./preferences.js";

// A presentation-only hint injected by the desktop's page preload. It must
// never be used for authentication or to grant API/native privileges.
export function isDesktopChat(
  surface: unknown = globalThis,
): surface is { spoolDesktop: true } {
  return (
    typeof surface === "object" &&
    surface !== null &&
    "spoolDesktop" in surface &&
    surface.spoolDesktop === true
  );
}

export const desktopSettingsPath = "/__desktop/settings";
export const desktopServersPath = "/__desktop/servers";
export const desktopServerConnectPath = "/__desktop/server-connect";
export const desktopServerReadyPath = "/__desktop/server-ready";
export const desktopSidebarWidthPath = "/__desktop/sidebar-width";

export type DesktopServerStatus =
  "connected" | "connecting" | "login" | "error" | "disconnected";
export type DesktopServerConnection = {
  state: DesktopServerStatus;
  authenticated: boolean;
};
export type DesktopServerTabsSnapshot = {
  sidebar_width?: number;
  selection?: string;
  transition?: { from: string; started_at: number; scroll_left?: number };
  entries: {
    id: string;
    name: string;
    local: boolean;
    status: DesktopServerStatus;
    // 仅用于合并标签；实际登录由原生端核验目标和线路。
    server_id?: string;
    client_url?: string;
    // The session came from a federated sign-in, not a password.
    federated?: boolean;
  }[];
  selected: string;
};

export type DesktopFederationProgress = {
  target: string;
  state: "pending" | "done" | "failed";
  reason?: string;
};

// Names an authorized server only. Native code selects a trusted route and
// performs the whole sign-in; no ticket or session ever reaches this page.
export function requestDesktopFederationLogin(
  target: string,
  surface: unknown = globalThis,
): void {
  if (!/^[a-z2-7]{52}$/.test(target)) return;
  desktopNavigationHint("/__desktop/federation-login?target=" + target, surface);
}

export function subscribeDesktopFederation(
  listener: (progress: DesktopFederationProgress) => void,
  surface: unknown = globalThis,
): () => void {
  return subscribeDesktopEvent(
    "spool:federation",
    (event) => {
      const value: unknown = (event as CustomEvent).detail;
      if (
        typeof value !== "object" ||
        value === null ||
        !("target" in value) ||
        typeof value.target !== "string" ||
        !("state" in value) ||
        (value.state !== "pending" &&
          value.state !== "done" &&
          value.state !== "failed")
      )
        return;
      listener({
        target: value.target,
        state: value.state,
        reason:
          "reason" in value && typeof value.reason === "string"
            ? value.reason
            : undefined,
      });
    },
    surface,
  );
}
const emptyDesktopServers: DesktopServerTabsSnapshot = {
  entries: [],
  selected: "local",
};

export function desktopServers(
  surface: unknown = globalThis,
): DesktopServerTabsSnapshot {
  if (!isDesktopChat(surface) || !("spoolServers" in surface))
    return emptyDesktopServers;
  const value = surface.spoolServers as DesktopServerTabsSnapshot | undefined;
  return value &&
    Array.isArray(value.entries) &&
    value.entries.every(
      (server) =>
        server &&
        typeof server.id === "string" &&
        typeof server.name === "string" &&
        typeof server.local === "boolean" &&
        ["connected", "connecting", "login", "error", "disconnected"].includes(
          server.status,
        ),
    ) &&
    typeof value.selected === "string"
    ? value
    : emptyDesktopServers;
}

export function subscribeDesktopServers(
  listener: () => void,
  surface: unknown = globalThis,
): () => void {
  return subscribeDesktopEvent("spool:servers-changed", listener, surface);
}

export function selectDesktopServer(
  id: string,
  surface: unknown = globalThis,
  scrollLeft = 0,
): void {
  if (!/^[a-zA-Z0-9_-]{1,128}$/.test(id)) return;
  if (!Number.isFinite(scrollLeft) || scrollLeft < 0 || scrollLeft > 1e7) return;
  desktopNavigationHint(
    desktopServersPath +
      "?" +
      new URLSearchParams({ id, scroll_left: String(scrollLeft) }),
    surface,
  );
}

export function subscribeDesktopServerSelection(
  listener: () => void,
  surface: unknown = globalThis,
): () => void {
  return subscribeDesktopEvent("spool:server-selected", listener, surface);
}

export function notifyDesktopServerState(
  state: DesktopServerStatus,
  surface: unknown = globalThis,
  authenticated = state === "connected",
): void {
  desktopNavigationHint(
    "/__desktop/server-state?" + new URLSearchParams({ state, authenticated: String(authenticated) }),
    surface,
  );
}

export function reportDesktopServerState(
  state: DesktopServerStatus,
  surface: unknown = globalThis,
  authenticated = state === "connected",
): () => void {
  if (!isDesktopChat(surface)) return () => {};
  let retry: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  const unsubscribe = subscribeDesktopEvent(
    "spool:server-state-ack",
    (event) => {
      const value = (event as CustomEvent<DesktopServerConnection>).detail;
      if (value?.state === state && value.authenticated === authenticated) stop();
    },
    surface,
  );
  const stop = () => {
    stopped = true;
    clearTimeout(retry);
    unsubscribe();
  };
  const notify = () => {
    if (stopped) return;
    notifyDesktopServerState(state, surface, authenticated);
    if (!stopped) retry = setTimeout(notify, 100);
  };
  // Cached native pages can suspend animation frames. Report health without
  // waiting for paint, and retry navigation hints until native receives one.
  notify();
  return stop;
}

export function connectDesktopServer(surface: unknown = globalThis): void {
  desktopNavigationHint(desktopServerConnectPath, surface);
}

export function notifyDesktopServerReady(
  state: DesktopServerStatus,
  selection: string,
  surface: unknown = globalThis,
  authenticated = state === "connected",
): void {
  desktopNavigationHint(
    desktopServerReadyPath + "?" + new URLSearchParams({ state, selection, authenticated: String(authenticated) }),
    surface,
  );
}

export function subscribeDesktopServerReadyAck(
  listener: (paint: DesktopServerConnection & { selection: string }) => void,
  surface: unknown = globalThis,
): () => void {
  return subscribeDesktopEvent(
    "spool:server-ready-ack",
    (event) => {
      const paint: unknown = (event as CustomEvent).detail;
      if (
        !paint ||
        typeof paint !== "object" ||
        !("state" in paint) ||
        !("selection" in paint) ||
        typeof paint.selection !== "string" ||
        !("authenticated" in paint) ||
        typeof paint.authenticated !== "boolean"
      )
        return;
      const state = paint.state;
      if (
        state === "connected" ||
        state === "connecting" ||
        state === "login" ||
        state === "error" ||
        state === "disconnected"
      )
        listener({ state, selection: paint.selection, authenticated: paint.authenticated });
    },
    surface,
  );
}

export function notifyDesktopSidebarWidth(
  width: number,
  surface: unknown = globalThis,
): void {
  if (!Number.isFinite(width) || width < 220 || width > 480) return;
  desktopNavigationHint(
    desktopSidebarWidthPath +
      "?" +
      new URLSearchParams({ width: String(Math.round(width)) }),
    surface,
  );
}

export function desktopSidebarWidth(
  snapshot: DesktopServerTabsSnapshot,
): number {
  const width = snapshot.sidebar_width;
  return typeof width === "number" &&
    Number.isFinite(width) &&
    width >= 220 &&
    width <= 480
    ? width
    : 298;
}

export function notifyDesktopServerName(
  name: string,
  surface: unknown = globalThis,
): void {
  desktopNavigationHint(
    "/__desktop/server-name?" + new URLSearchParams({ name }),
    surface,
  );
}

// This carries no identity. Native code rereads and validates its HTTPOnly
// cookie after the chat has completed a login, logout or initial session load.
export async function notifyDesktopSessionChanged(
  surface: unknown = globalThis,
): Promise<void> {
  desktopNavigationHint("/__desktop/session-changed", surface);
}

export async function notifyDesktopViewChanged(
  chat: string,
  sessionChanged: boolean,
  surface: unknown = globalThis,
): Promise<void> {
  if (chat && !/^[a-zA-Z0-9_][a-zA-Z0-9_-]{7,127}$/.test(chat)) return;
  const params = new URLSearchParams({ chat });
  if (sessionChanged) params.set("session", "changed");
  desktopNavigationHint("/__desktop/view?" + params, surface);
}

function desktopNavigationHint(path: string, surface: unknown): void {
  if (!isDesktopChat(surface) || !("location" in surface)) return;
  const location = surface.location;
  if (
    typeof location !== "object" ||
    location === null ||
    !("assign" in location) ||
    typeof location.assign !== "function"
  )
    return;
  location.assign(path);
}

export function isMacDesktop(surface: unknown = globalThis): boolean {
  return (
    isDesktopChat(surface) &&
    "mygo" in surface &&
    typeof surface.mygo === "object" &&
    surface.mygo !== null &&
    "platform" in surface.mygo &&
    surface.mygo.platform === "darwin"
  );
}

export function desktopFileAccessURL(
  chatID: string | undefined,
  path: string,
  surface: unknown = globalThis,
): string | undefined {
  if (!isMacDesktop(surface) || !chatID || !path.startsWith("/")) return;
  return `/__desktop/file-access?${new URLSearchParams({ chat: chatID, path })}`;
}

export type DesktopFileAccessResult = {
  chat_id: string;
  path: string;
  error?: string;
};

export function subscribeDesktopFileAccess(
  listener: (result: DesktopFileAccessResult) => void,
  surface: unknown = globalThis,
): () => void {
  return subscribeDesktopEvent(
    "spool:file-access-result",
    (event) => {
      const result: unknown = (event as CustomEvent).detail;
      if (
        typeof result === "object" &&
        result !== null &&
        "chat_id" in result &&
        typeof result.chat_id === "string" &&
        "path" in result &&
        typeof result.path === "string" &&
        (!("error" in result) || typeof result.error === "string")
      )
        listener(result as DesktopFileAccessResult);
    },
    surface,
  );
}

type DesktopRuntime = {
  readonly locale: string;
  on: (event: string, listener: () => void) => () => void;
};

function desktopRuntime(surface: unknown): DesktopRuntime | undefined {
  if (!isDesktopChat(surface) || !("mygo" in surface)) return;
  const runtime = surface.mygo;
  if (
    typeof runtime === "object" &&
    runtime !== null &&
    "locale" in runtime &&
    typeof runtime.locale === "string" &&
    "on" in runtime &&
    typeof runtime.on === "function"
  ) {
    return runtime as DesktopRuntime;
  }
}

export function desktopLocale(
  surface: unknown = globalThis,
): Locale | undefined {
  const runtime = desktopRuntime(surface);
  return runtime ? resolveLanguage("system", [runtime.locale]) : undefined;
}

export function desktopTheme(
  surface: unknown = globalThis,
): "light" | "dark" | undefined {
  if (!isDesktopChat(surface) || !("spoolDesktopTheme" in surface)) return;
  const theme = surface.spoolDesktopTheme;
  return theme === "light" || theme === "dark" ? theme : undefined;
}

export function subscribeDesktopTheme(
  listener: () => void,
  surface: unknown = globalThis,
): () => void {
  return subscribeDesktopEvent("spool:appearance-changed", listener, surface);
}

export function desktopUpdateAvailable(surface: unknown = globalThis): boolean {
  return (
    isDesktopChat(surface) &&
    "spoolUpdateAvailable" in surface &&
    surface.spoolUpdateAvailable === true
  );
}

export function subscribeDesktopUpdates(
  listener: () => void,
  surface: unknown = globalThis,
): () => void {
  return subscribeDesktopEvent("spool:update-changed", listener, surface);
}

export type DesktopNotificationTarget = { url: string; user: string };

export type SessionNotificationKind = "completed" | "failed" | "approval";
export type SessionNotification = {
  chat: string;
  user: string;
  kind: SessionNotificationKind;
};
export type DesktopSessionNotification = SessionNotification;

export function isSessionNotification(
  value: unknown,
): value is SessionNotification {
  const validID = /^[a-zA-Z0-9_][a-zA-Z0-9_-]{7,127}$/;
  return (
    typeof value === "object" &&
    value !== null &&
    "chat" in value &&
    typeof value.chat === "string" &&
    validID.test(value.chat) &&
    "user" in value &&
    typeof value.user === "string" &&
    validID.test(value.user) &&
    "kind" in value &&
    (value.kind === "completed" ||
      value.kind === "failed" ||
      value.kind === "approval")
  );
}

export function takeDesktopSessionNotifications(
  surface: unknown = globalThis,
): DesktopSessionNotification[] {
  if (!isDesktopChat(surface) || !("spoolSessionNotifications" in surface))
    return [];
  const pending = surface.spoolSessionNotifications;
  delete (surface as { spoolSessionNotifications?: unknown })
    .spoolSessionNotifications;
  if (!Array.isArray(pending)) return [];
  return Array.from(pending).filter(isSessionNotification);
}

export function subscribeDesktopSessionNotifications(
  listener: () => void,
  surface: unknown = globalThis,
): () => void {
  return subscribeDesktopEvent(
    "spool:session-notification-changed",
    listener,
    surface,
  );
}

export function desktopViewedChat(surface: unknown = globalThis): string {
  if (!isDesktopChat(surface) || !("spoolViewedChat" in surface)) return "";
  const chat = surface.spoolViewedChat;
  return typeof chat === "string" &&
    /^[a-zA-Z0-9_][a-zA-Z0-9_-]{7,127}$/.test(chat)
    ? chat
    : "";
}

export function subscribeDesktopSessionView(
  listener: () => void,
  surface: unknown = globalThis,
): () => void {
  return subscribeDesktopEvent("spool:session-view-changed", listener, surface);
}

export function takeDesktopNotification(
  surface: unknown = globalThis,
): DesktopNotificationTarget | null {
  if (!isDesktopChat(surface) || !("spoolNotificationTarget" in surface))
    return null;
  const target = surface.spoolNotificationTarget;
  delete (surface as { spoolNotificationTarget?: unknown })
    .spoolNotificationTarget;
  if (
    typeof target !== "object" ||
    target === null ||
    !("url" in target) ||
    typeof target.url !== "string" ||
    !("user" in target) ||
    typeof target.user !== "string" ||
    !target.url.startsWith("/?") ||
    target.url.length > 512 ||
    !/^[a-zA-Z0-9_][a-zA-Z0-9_-]{7,127}$/.test(target.user)
  )
    return null;
  return { url: target.url, user: target.user };
}

export function subscribeDesktopNotifications(
  listener: () => void,
  surface: unknown = globalThis,
): () => void {
  return subscribeDesktopEvent("spool:notification-changed", listener, surface);
}

function subscribeDesktopEvent(
  event: string,
  listener: (event: Event) => void,
  surface: unknown,
): () => void {
  if (
    !isDesktopChat(surface) ||
    !("addEventListener" in surface) ||
    typeof surface.addEventListener !== "function" ||
    !("removeEventListener" in surface) ||
    typeof surface.removeEventListener !== "function"
  ) {
    return () => {};
  }
  const removeEventListener = surface.removeEventListener.bind(surface);
  surface.addEventListener(event, listener);
  return () => removeEventListener(event, listener);
}

export function subscribeDesktopLocale(
  listener: () => void,
  surface: unknown = globalThis,
): () => void {
  return desktopRuntime(surface)?.on("mygo:locale", listener) ?? (() => {});
}

export function chatPreferences(
  preferences: UIPreferences,
  locale: Locale | undefined,
  surface: unknown = globalThis,
): UIPreferences {
  if (!isDesktopChat(surface)) return preferences;
  // MyGo makes prefers-color-scheme reflect the native theme override, not
  // just the OS setting. Account preferences must not override desktop UI.
  return { theme: "system", language: locale ?? "system" };
}
