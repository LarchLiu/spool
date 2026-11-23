import { useEffect, useState, useSyncExternalStore } from "react";
import {
  KeyRound,
  LoaderCircle,
  Plug,
  Plus,
  RefreshCw,
  Server,
} from "lucide-react";
import type { MessageKey, Translator } from "./i18n";
import { SegmentedTabs, type SegmentedTab } from "./segmented-tabs";
import {
  federationClientURL,
  parseFederationTargets,
  type FederationTarget,
} from "./desktop-federation";
import {
  desktopServers,
  desktopServersPath,
  isDesktopChat,
  requestDesktopFederationLogin,
  selectDesktopServer,
  subscribeDesktopFederation,
  subscribeDesktopServers,
  type DesktopFederationProgress,
  type DesktopServerStatus,
  type DesktopServerTabsSnapshot,
} from "./runtime";

const labels: Record<DesktopServerStatus, MessageKey> = {
  connected: "Connected · Signed in",
  connecting: "Connecting…",
  login: "Connected · Sign-in required",
  error: "Connection failed",
  disconnected: "Disconnected",
};

export function DesktopServerAction({
  t,
  state,
  onConnect,
  iconOnly = false,
}: {
  t: Translator;
  state: DesktopServerStatus;
  onConnect: () => void;
  iconOnly?: boolean;
}) {
  if (state === "connected") return null;
  const pending = state === "connecting";
  const action = t(
    pending
      ? "Connecting…"
      : state === "login"
        ? "Sign in"
        : state === "error"
          ? "Retry"
          : "Connect",
  );
  const Icon = pending ? LoaderCircle : state === "error" ? RefreshCw : Plug;
  return (
    <button
      type="button"
      className={
        iconOnly ? "icon desktop-server-action" : "desktop-server-action"
      }
      onClick={onConnect}
      disabled={pending}
      aria-label={action}
      title={iconOnly ? action : undefined}
    >
      {iconOnly ? (
        <Icon
          size={16}
          className={pending ? "server-action-spinner" : undefined}
        />
      ) : (
        action
      )}
    </button>
  );
}

type FederationProgress = Record<string, DesktopFederationProgress>;

type DesktopServerEntry = DesktopServerTabsSnapshot["entries"][number];
const federationTabKey = (source: string, target: string) =>
  `federation:${source}:${target}`;

function ServerStatusIcon({
  status,
  trusted,
}: {
  status: DesktopServerStatus;
  trusted?: boolean;
}) {
  return trusted ? (
    <KeyRound
      size={13}
      className="server-status-key"
      data-state={status}
      aria-hidden="true"
    />
  ) : (
    <span
      className="server-status-dot"
      data-state={status}
      aria-hidden="true"
    />
  );
}

function serverOption(
  t: Translator,
  server: DesktopServerEntry,
): SegmentedTab<string> {
  const status =
    t(labels[server.status]) +
    (server.federated ? ` · ${t("Federated sign-in")}` : "");
  return {
    value: server.id,
    label: server.name,
    title: `${server.name} · ${status}`,
    accessibleLabel: `${server.name}, ${status}`,
    icon: (
      <ServerStatusIcon status={server.status} trusted={server.federated} />
    ),
  };
}

function federationTargetStatus(
  source: DesktopServerEntry,
  target: FederationTarget,
): DesktopServerStatus {
  if (!target.clientURL || !Array.isArray(source.federation_targets))
    return "disconnected";
  // Only the matching native route has a health result. An anonymous probe
  // can report reachability, but cannot establish a signed-in session.
  const status = source.federation_targets.find(
    (entry) =>
      entry?.id === target.id &&
      federationClientURL(entry.client_url) === target.clientURL &&
      (entry.client_mode ?? "") === (target.clientMode ?? ""),
  )?.status;
  return status === "login" || status === "error" || status === "connecting"
    ? status
    : "disconnected";
}

function federationOption(
  t: Translator,
  source: DesktopServerEntry,
  target: FederationTarget,
  current?: DesktopFederationProgress,
  server?: DesktopServerEntry,
): SegmentedTab<string> {
  const name = server?.name ?? target.name;
  const status = server?.status ?? federationTargetStatus(source, target);
  const pending = current?.state === "pending";
  const failed = current?.state === "failed";
  const label = t(
    pending
      ? "Signing in to {server}…"
      : failed && current.reason === "unconfigured"
        ? "No trusted address is known for this server."
        : failed
          ? "Could not sign in to {server}. Try again."
          : "Sign in to {server} with this account",
    { server: name },
  );
  const action = `${source.name} → ${label}`;
  const statusLabel = t(labels[status]);
  return {
    value: federationTabKey(source.id, target.id),
    label: name,
    title: pending ? action : `${action} · ${statusLabel}`,
    accessibleLabel: pending ? action : `${action}, ${statusLabel}`,
    className: "desktop-federation-target",
    activateCurrent: true,
    state: current?.state,
    disabled: pending,
    icon: pending ? (
      <LoaderCircle
        size={13}
        className="server-action-spinner"
        aria-hidden="true"
      />
    ) : (
      <ServerStatusIcon status={status} trusted />
    ),
  };
}

// 只调整标签显示顺序，不更改已保存的服务器配置。
function serverOptions(
  t: Translator,
  servers: DesktopServerTabsSnapshot,
  targets: FederationTarget[] | undefined,
  progress: FederationProgress,
) {
  const groups = servers.entries
    .filter((server) => !server.federation_target)
    .map((source) => ({
      source,
      targets:
        (source.id === servers.selected ? targets : undefined) ??
        parseFederationTargets(source.federation_targets) ??
        [],
    }));
  const aliases = new Map<string, { source: string; target: string }>();
  const savedAliases = new Map<string, string>();
  const options = groups.map(({ source, targets }) =>
    targets.flatMap((target) => {
      const saved = servers.entries.find(
        (server) =>
          server.federation_source === source.id &&
          server.federation_target === target.id,
      );
      const key = federationTabKey(source.id, target.id);
      aliases.set(key, { source: source.id, target: target.id });
      if (saved) savedAliases.set(saved.id, key);
      return [federationOption(t, source, target, progress[key], saved)];
    }),
  );
  const selection = (id: string, _alias?: string) => savedAliases.get(id) ?? id;
  return {
    aliases,
    selection,
    options: groups.flatMap(({ source }, i) => [
      serverOption(t, source),
      ...options[i],
    ]),
  };
}

function useFederationProgress(account: string | undefined) {
  const [progress, setProgress] = useState<FederationProgress>({});
  useEffect(() => {
    setProgress({});
    if (!isDesktopChat()) return;
    return subscribeDesktopFederation((value) =>
      setProgress((old) => {
        const key = federationTabKey(
          value.source ?? desktopServers().selected,
          value.target,
        );
        const next = { ...old };
        if (value.state === "done" || value.reason === "cancelled")
          delete next[key];
        else next[key] = value;
        return next;
      }),
    );
  }, [account]);
  return progress;
}

export function DesktopServerTabs({
  t,
  account,
  targets,
  className = "",
}: {
  t: Translator;
  // 用于在当前页面换账号时清除旧的登录进度。
  account?: string;
  targets?: FederationTarget[];
  className?: string;
}) {
  const servers = useSyncExternalStore(
    subscribeDesktopServers,
    desktopServers,
    desktopServers,
  );
  const progress = useFederationProgress(account);
  if (!isDesktopChat()) return null;
  return (
    <DesktopServerTabBar
      t={t}
      servers={servers}
      targets={account ? targets : undefined}
      progress={progress}
      className={className}
    />
  );
}

export function DesktopServerTabBar({
  t,
  servers,
  targets,
  progress = {},
  className = "",
}: {
  t: Translator;
  servers: DesktopServerTabsSnapshot;
  targets?: FederationTarget[];
  progress?: FederationProgress;
  className?: string;
}) {
  const { aliases, selection, options } = serverOptions(
    t,
    servers,
    targets,
    progress,
  );
  return (
    <SegmentedTabs
      className={`desktop-server-tabs ${className}`}
      trackClassName="desktop-server-track"
      label={t("Servers")}
      navigation
      value={selection(servers.selected, servers.selected_tab)}
      onChange={(id, scrollLeft) => {
        const alias = aliases.get(id);
        if (alias)
          requestDesktopFederationLogin(
            alias.source,
            alias.target,
            globalThis,
            scrollLeft,
          );
        else selectDesktopServer(id, globalThis, scrollLeft);
      }}
      transition={
        servers.transition && {
          from: selection(servers.transition.from, servers.transition.from_tab),
          startedAt: servers.transition.started_at,
          scrollLeft: servers.transition.scroll_left,
        }
      }
      options={options}
      trailing={
        <a
          className="icon desktop-server-add"
          href={desktopServersPath}
          aria-label={t("Add server")}
          title={t("Add server")}
          draggable={false}
        >
          <Plus size={18} />
        </a>
      }
    />
  );
}

export function DesktopServerWelcome({
  t,
  name,
  state,
  onConnect,
}: {
  t: Translator;
  name: string;
  state: DesktopServerStatus;
  onConnect: () => void;
}) {
  const pending = state === "connecting";
  const action = pending
    ? "Connecting…"
    : state === "login"
      ? "Sign in"
      : state === "error"
        ? "Retry"
        : "Connect";
  const hint =
    state === "login"
      ? "Sign in to view this server’s workspace."
      : "Connect to view this server’s workspace.";
  return (
    <section className="desktop-server-welcome">
      <Server size={32} aria-hidden="true" />
      <h1>{name}</h1>
      <p role="status">{t(labels[state])}</p>
      <p>{t(hint)}</p>
      <button className="primary" onClick={onConnect} disabled={pending}>
        {t(action)}
      </button>
    </section>
  );
}
