import { useSyncExternalStore } from "react";
import { LoaderCircle, Plug, Plus, RefreshCw, Server } from "lucide-react";
import type { MessageKey, Translator } from "./i18n";
import { SegmentedTabs } from "./segmented-tabs";
import {
  desktopServers,
  desktopServersPath,
  isDesktopChat,
  selectDesktopServer,
  subscribeDesktopServers,
  type DesktopServerStatus,
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

export function DesktopServerTabs({
  t,
  className = "",
}: {
  t: Translator;
  className?: string;
}) {
  const servers = useSyncExternalStore(
    subscribeDesktopServers,
    desktopServers,
    desktopServers,
  );
  if (!isDesktopChat()) return null;
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
      options={servers.entries.map((server) => ({
        value: server.id,
        label: server.name,
        title: `${server.name} · ${t(labels[server.status])}`,
        accessibleLabel: `${server.name}, ${t(labels[server.status])}`,
        icon: (
          <span
            className="server-status-dot"
            data-state={server.status}
            aria-hidden="true"
          />
        ),
      }))}
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
