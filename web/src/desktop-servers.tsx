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
  loadFederationTargets,
  type FederationTarget,
} from "./desktop-federation";
import {
  desktopServers,
  desktopServersPath,
  isDesktopChat,
  requestDesktopFederationLogin,
  selectDesktopServer,
  subscribeDesktopFederation,
  subscribeDesktopServerSelection,
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
const federationTabPrefix = "federation:";

function serverOption(
  t: Translator,
  server: DesktopServerEntry,
  trusted = server.federated,
): SegmentedTab<string> {
  const status =
    t(labels[server.status]) + (trusted ? ` · ${t("Federated sign-in")}` : "");
  return {
    value: server.id,
    label: server.name,
    title: `${server.name} · ${status}`,
    accessibleLabel: `${server.name}, ${status}`,
    icon: (
      <>
        <span
          className="server-status-dot"
          data-state={server.status}
          aria-hidden="true"
        />
        {trusted && (
          <KeyRound
            size={12}
            className="server-federated-mark"
            aria-hidden="true"
          />
        )}
      </>
    ),
  };
}

function federationOption(
  t: Translator,
  target: FederationTarget,
  current?: DesktopFederationProgress,
  server?: DesktopServerEntry,
): SegmentedTab<string> {
  const name = server?.name ?? target.name;
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
  return {
    value: server?.id ?? federationTabPrefix + target.id,
    label: name,
    title: label,
    accessibleLabel: label,
    action: () => requestDesktopFederationLogin(target.id),
    className: "desktop-federation-target",
    state: current?.state,
    disabled: pending,
    icon: pending ? (
      <LoaderCircle
        size={13}
        className="server-action-spinner"
        aria-hidden="true"
      />
    ) : (
      <KeyRound
        size={13}
        className="server-federated-mark"
        aria-hidden="true"
      />
    ),
  };
}

// 只调整标签显示顺序，不更改已保存的服务器配置。
function serverOptions(
  t: Translator,
  servers: DesktopServerTabsSnapshot,
  targets: FederationTarget[],
  progress: FederationProgress,
) {
  const matched = new Set<string>();
  const trusted: SegmentedTab<string>[] = [];
  if (servers.entries.some((server) => server.id === servers.selected)) {
    for (const target of targets) {
      const matches = (server: DesktopServerEntry) =>
        server.server_id
          ? server.server_id === target.id
          : !!target.clientURL &&
            federationClientURL(server.client_url) === target.clientURL;
      if (
        servers.entries.some(
          (server) => server.id === servers.selected && matches(server),
        )
      )
        continue;
      const candidates = servers.entries.filter(
        (server) => !matched.has(server.id) && matches(server),
      );
      const saved =
        candidates.find((server) => server.status === "connected") ??
        candidates[0];
      if (saved) matched.add(saved.id);
      trusted.push(
        saved?.status === "connected"
          ? serverOption(t, saved, true)
          : federationOption(t, target, progress[target.id], saved),
      );
    }
  }
  return servers.entries.flatMap((server) => {
    if (matched.has(server.id)) return [];
    const option = serverOption(t, server);
    return server.id === servers.selected ? [option, ...trusted] : [option];
  });
}

function useFederationTargets(account: string | undefined) {
  const [targets, setTargets] = useState<FederationTarget[]>([]);
  const [progress, setProgress] = useState<FederationProgress>({});
  useEffect(() => {
    setTargets([]);
    setProgress({});
    if (!account || !isDesktopChat()) return;
    const controller = new AbortController();
    const load = () =>
      void loadFederationTargets(controller.signal).then(
        (value) => {
          if (!controller.signal.aborted) setTargets(value);
        },
        () => {},
      );
    load();
    // Authorization may have changed while another server was presented.
    const offSelection = subscribeDesktopServerSelection(load);
    const offProgress = subscribeDesktopFederation((value) =>
      setProgress((old) => {
        const next = { ...old };
        if (value.state === "done" || value.reason === "cancelled")
          delete next[value.target];
        else next[value.target] = value;
        return next;
      }),
    );
    return () => {
      controller.abort();
      offSelection();
      offProgress();
    };
  }, [account]);
  return { targets, progress };
}

export function DesktopServerTabs({
  t,
  account,
  className = "",
}: {
  t: Translator;
  // The signed-in account of this page; federated sign-in starts only here.
  account?: string;
  className?: string;
}) {
  const servers = useSyncExternalStore(
    subscribeDesktopServers,
    desktopServers,
    desktopServers,
  );
  const federation = useFederationTargets(account);
  if (!isDesktopChat()) return null;
  return (
    <DesktopServerTabBar
      t={t}
      servers={servers}
      {...federation}
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
  targets: FederationTarget[];
  progress?: FederationProgress;
  className?: string;
}) {
  return (
    <SegmentedTabs
      className={`desktop-server-tabs ${className}`}
      trackClassName="desktop-server-track"
      label={t("Servers")}
      navigation
      value={servers.selected}
      onChange={(id, scrollLeft) =>
        selectDesktopServer(id, globalThis, scrollLeft)
      }
      transition={
        servers.transition && {
          from: servers.transition.from,
          startedAt: servers.transition.started_at,
          scrollLeft: servers.transition.scroll_left,
        }
      }
      options={serverOptions(t, servers, targets, progress)}
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
