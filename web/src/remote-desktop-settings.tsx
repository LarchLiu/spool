import { appURL } from "./app-path";
import { useEffect, useRef, useState } from "react";
import { MonitorUp, X } from "lucide-react";
import { api } from "./api";
import type { Translator } from "./i18n";
import { configureVoxlFrame } from "./voxl-frame";

export function RemoteDesktopConnection({
  available,
  secure,
  t,
  onConnect,
}: {
  available: boolean;
  secure: boolean;
  t: Translator;
  onConnect: () => void;
}) {
  if (!available) return null;
  return (
    <section
      className="notification-settings"
      aria-labelledby="remote-desktop-title"
    >
      <h3 id="remote-desktop-title">
        <MonitorUp size={15} aria-hidden="true" />
        {t("Remote connection")}
      </h3>
      {secure ? (
        <div className="notification-actions">
          <button type="button" onClick={onConnect}>
            {t("Connect to desktop")}
          </button>
        </div>
      ) : (
        <p>{t("Remote desktop requires HTTPS or localhost.")}</p>
      )}
    </section>
  );
}

export function RemoteDesktopDialog({
  t,
  onClose,
}: {
  t: Translator;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const disposeViewer = useRef<(() => void) | undefined>(undefined);
  useEffect(() => () => disposeViewer.current?.(), []);
  const [token, setToken] = useState<string>();
  const available = useRemoteDesktopAvailable(token);
  useEffect(() => {
    let disposed = false;
    let connection: string | undefined;
    const disconnect = (value: string) => {
      void api(
        `/desktop/disconnect?token=${encodeURIComponent(value)}`,
        {},
      ).catch(() => {});
    };
    void api<{ token: string }>("/desktop/connect", {})
      .then((value) => {
        connection = value.token;
        if (disposed) disconnect(value.token);
        else setToken(value.token);
      })
      .catch(() => {
        if (!disposed) onClose();
      });
    return () => {
      disposed = true;
      if (connection) disconnect(connection);
    };
  }, []);
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  useEffect(() => {
    if (available === false) onClose();
  }, [available, onClose]);
  return (
    <dialog
      className="remote-desktop-dialog"
      ref={dialog}
      aria-labelledby="remote-desktop-dialog-title"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <div className="dialog-heading">
        <h2 id="remote-desktop-dialog-title">
          <MonitorUp size={18} aria-hidden="true" />
          {t("Remote desktop")}
        </h2>
        <button type="button" autoFocus onClick={onClose}>
          {t("Disconnect desktop")}
          <X size={17} aria-hidden="true" />
        </button>
      </div>
      {token ? (
        <iframe
          className="remote-desktop-frame"
          title={t("Remote desktop")}
          src={appURL(`/api/desktop/view/?token=${encodeURIComponent(token)}`)}
          referrerPolicy="no-referrer"
          allow="fullscreen; clipboard-read; clipboard-write"
          allowFullScreen
          onLoad={(event) => {
            disposeViewer.current?.();
            disposeViewer.current = configureVoxlFrame(event.currentTarget, t);
          }}
        />
      ) : (
        <p role="status">{t("Connecting…")}</p>
      )}
    </dialog>
  );
}

function useRemoteDesktopAvailable(token?: string) {
  const [available, setAvailable] = useState<boolean | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    let loading = false;
    const refresh = () => {
      if (loading || document.visibilityState === "hidden") return;
      loading = true;
      const path = token
        ? `/desktop?token=${encodeURIComponent(token)}`
        : "/desktop";
      void api<{ available: boolean }>(path, undefined, controller.signal)
        .then((value) => {
          if (!controller.signal.aborted) setAvailable(value.available);
        })
        .catch(() => {
          if (!controller.signal.aborted) setAvailable(false);
        })
        .finally(() => {
          loading = false;
        });
    };
    refresh();
    const timer = setInterval(refresh, 5000);
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", refresh);
    return () => {
      controller.abort();
      clearInterval(timer);
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, [token]);
  return available;
}

export function RemoteDesktopSettings({
  t,
  onConnect,
}: {
  t: Translator;
  onConnect: () => void;
}) {
  const available = useRemoteDesktopAvailable();
  return (
    <RemoteDesktopConnection
      available={available === true}
      secure={window.isSecureContext}
      t={t}
      onConnect={onConnect}
    />
  );
}
