import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { ApplicationBoundary } from "./application-boundary";
import { api, setCSRF } from "./api";
import { appBasePath } from "./app-path";
import { parsePreferences, preferencesStorageKey } from "./preferences";
import { usePresentation } from "./use-presentation";
import { RemoteDesktopViewer } from "./remote-desktop-settings";
import {
  closeRemoteDesktopWindow,
  notifyRemoteDesktopConnected,
} from "./remote-desktop-window";
import "./style.css";

function RemoteDesktopPage() {
  const [preferences] = useState(() => {
    try {
      return parsePreferences(
        localStorage.getItem(
          (appBasePath() === "/" ? "" : appBasePath()) + preferencesStorageKey,
        ),
      );
    } catch {
      return parsePreferences(null);
    }
  });
  const { t } = usePresentation(preferences);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState("");
  const [closed, setClosed] = useState(false);
  const [serverName, setServerName] = useState(
    () => new URLSearchParams(location.search).get("server") || "Home",
  );
  useEffect(() => {
    document.title = `Spool · ${serverName} · ${t("Remote desktop")}`;
  }, [t, serverName]);
  useEffect(() => {
    const update = (event: Event) => {
      const name = (event as CustomEvent).detail;
      if (typeof name === "string") setServerName(name);
    };
    window.addEventListener("spool:remote-desktop-server", update);
    return () =>
      window.removeEventListener("spool:remote-desktop-server", update);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    void api<{ csrf: string }>("/me", undefined, controller.signal)
      .then((value) => {
        if (controller.signal.aborted) return;
        setCSRF(value.csrf);
        setReady(true);
      })
      .catch((error: Error) => {
        if (!controller.signal.aborted) setError(error.message);
      });
    const expire = () => {
      setClosed(true);
      closeRemoteDesktopWindow();
    };
    window.addEventListener("auth-expired", expire);
    return () => {
      controller.abort();
      window.removeEventListener("auth-expired", expire);
    };
  }, []);
  if (error)
    return (
      <main className="remote-desktop-window">
        <p role="alert">{error}</p>
      </main>
    );
  if (closed)
    return (
      <main className="remote-desktop-window">
        <p role="status">{t("Disconnected")}</p>
      </main>
    );
  if (!ready)
    return (
      <main className="remote-desktop-window">
        <p role="status">{t("Connecting…")}</p>
      </main>
    );
  return (
    <RemoteDesktopViewer
      t={t}
      serverName={serverName}
      onConnected={notifyRemoteDesktopConnected}
      onClose={() => {
        setClosed(true);
        closeRemoteDesktopWindow();
      }}
    />
  );
}

createRoot(document.getElementById("root")!).render(
  <ApplicationBoundary>
    <RemoteDesktopPage />
  </ApplicationBoundary>,
);
