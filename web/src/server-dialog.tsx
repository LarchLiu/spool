import { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { Info, KeyRound, Network, ScanQrCode, Server, X } from "lucide-react";
import { defaultPreferences } from "./preferences";
import { usePresentation } from "./use-presentation";
import { Card, Hint, Row, SettingsError, Toggle } from "./settings-ui";
import { SegmentedTabs } from "./segmented-tabs";
import { ConnectionControl } from "./connection-control";
import { readPairingQRCode } from "./pairing-qr";
import {
  serverDialogCall,
  serverDialogInitialSnapshot,
  type ConnectionMode,
  type DesktopServerProfile,
  type ServerDialogSnapshot,
} from "./server-dialog-client";
import type { MessageKey } from "./i18n";
import "./style.css";
import "./settings.css";
import "./server-dialog.css";

const names: Record<ConnectionMode, MessageKey> = {
  direct: "Public HTTPS",
  tailscale: "Tailscale",
  relay: "Spool Relay",
};
const fields: Record<ConnectionMode, MessageKey> = {
  direct: "Public HTTPS address",
  tailscale: "Tailscale HTTPS address",
  relay: "Pairing code",
};
const statuses: Record<string, MessageKey> = {
  connected: "Connected · Signed in",
  connecting: "Connecting…",
  login: "Connected · Sign-in required",
  error: "Connection failed",
  disconnected: "Disconnected",
};
const descriptions: Record<ConnectionMode, MessageKey> = {
  direct:
    "Connect using the same public HTTPS address as the PWA. Tailscale is not required.",
  tailscale:
    "Your private server, through this app's embedded node. No system VPN or shared auth key.",
  relay:
    "Prefer the paired server on your LAN; use its relay when away. Both paths verify the same server identity.",
};

export function ServerDialog() {
  const { t } = usePresentation(defaultPreferences);
  const initialSnapshot = useRef(serverDialogInitialSnapshot());
  const [snapshot, setSnapshot] = useState(initialSnapshot.current);
  const [draft, setDraft] = useState<DesktopServerProfile | undefined>(
    initialSnapshot.current?.server,
  );
  const [visible, setVisible] = useState<ConnectionMode>(
    initialSnapshot.current?.server.mode || "direct",
  );
  const [busy, setBusy] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [error, setError] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [pairing, setPairing] = useState("");
  const [fingerprint, setFingerprint] = useState("");
  const operation = useRef(false);
  const active = useRef(true);
  const viewEpoch = useRef(0);
  const form = useRef<HTMLFormElement>(null);
  const pairingImage = useRef<HTMLInputElement>(null);
  const connectionActive = !!snapshot?.active || pairing === "pending";
  const currentMode = pairing === "pending" ? "relay" : snapshot?.server.mode;
  const displayName =
    draft?.name.trim() || snapshot?.server.server_name || "Home";
  useEffect(() => {
    active.current = true;
    let initial = !initialSnapshot.current;
    const receive = (event: Event) => {
      const next = (event as CustomEvent<ServerDialogSnapshot>).detail;
      viewEpoch.current++;
      operation.current = false;
      initial = false;
      setSnapshot(next);
      setDraft(next.server);
      setVisible(next.server.mode || "direct");
      setError("");
      setBusy(false);
      setStopping(false);
      setDeleting(false);
      setPairing("");
      setFingerprint("");
      document.querySelector(".server-dialog-shell")?.scrollTo(0, 0);
    };
    window.addEventListener("spool:server-dialog", receive);
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.isComposing) {
        event.preventDefault();
        void serverDialogCall("Close");
      }
    };
    window.addEventListener("keydown", escape);
    const refresh = async () => {
      if (operation.current || document.hidden) return;
      const epoch = viewEpoch.current;
      try {
        const next = await serverDialogCall<ServerDialogSnapshot>("Snapshot");
        if (!active.current || operation.current || epoch !== viewEpoch.current)
          return;
        setSnapshot(next);
        if (initial) {
          setDraft(next.server);
          setVisible(next.server.mode || "direct");
          initial = false;
        }
      } catch (e) {
        if (active.current && epoch === viewEpoch.current)
          setError(String(e instanceof Error ? e.message : e));
      }
    };
    if (initial) void refresh();
    const timer = setInterval(() => void refresh(), 3000);
    return () => {
      active.current = false;
      clearInterval(timer);
      window.removeEventListener("spool:server-dialog", receive);
      window.removeEventListener("keydown", escape);
    };
  }, []);
  const ready = !!draft || !!error;
  useEffect(() => {
    if (!ready) return;
    const epoch = viewEpoch.current;
    // Notify native only after React commits the form (or an initialization
    // error) and usePresentation applies the theme to the document.
    let frame = requestAnimationFrame(() => {
      frame = requestAnimationFrame(() => {
        void serverDialogCall("Ready", draft?.id || "").catch((e) => {
          if (active.current && epoch === viewEpoch.current)
            setError(String(e instanceof Error ? e.message : e));
        });
      });
    });
    return () => cancelAnimationFrame(frame);
  }, [ready, snapshot]);
  const run = async (fn: () => Promise<void>) => {
    if (operation.current) return;
    operation.current = true;
    const epoch = viewEpoch.current;
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      if (active.current && epoch === viewEpoch.current) {
        setPairing("");
        setError(String(e instanceof Error ? e.message : e));
      }
    } finally {
      if (epoch === viewEpoch.current) {
        operation.current = false;
        if (active.current) setBusy(false);
      }
    }
  };
  const save = async (next: DesktopServerProfile, connect = false) => {
    const epoch = viewEpoch.current;
    const result = await serverDialogCall<ServerDialogSnapshot>(
      "Save",
      next,
      connect,
    );
    if (active.current && epoch === viewEpoch.current) {
      setSnapshot(result);
      setDraft(result.server);
    }
    return result;
  };
  const pair = async () => {
    if (!draft || !form.current?.reportValidity()) return;
    await run(async () => {
      const epoch = viewEpoch.current;
      await save({ ...draft, mode: "relay", enabled: true });
      if (!active.current || epoch !== viewEpoch.current) return;
      setPairing("pending");
      for (;;) {
        const result = await serverDialogCall<{
          state: string;
          fingerprint: string;
        }>("PairRelay");
        if (!active.current || epoch !== viewEpoch.current) return;
        setPairing(result.state);
        setFingerprint(result.fingerprint);
        if (result.state !== "pending") break;
        await new Promise<void>((resolve) => setTimeout(resolve, 1500));
        if (!active.current || epoch !== viewEpoch.current) return;
      }
      const result = await serverDialogCall<ServerDialogSnapshot>("Snapshot");
      if (active.current && epoch === viewEpoch.current) {
        setSnapshot(result);
        setDraft(result.server);
      }
    });
  };
  const connect = () => {
    if (!draft || !form.current?.reportValidity()) return;
    if (visible === "relay" && draft.relay) {
      try {
        const data = JSON.parse(
          atob(
            draft.relay
              .slice("spool-pair:".length)
              .replace(/-/g, "+")
              .replace(/_/g, "/"),
          ),
        );
        if (data.invite) {
          void pair();
          return;
        }
      } catch {
        /* The native validator reports malformed pairing codes. */
      }
    }
    void run(async () => {
      await save({ ...draft, mode: visible, enabled: true }, true);
    });
  };
  const disconnect = async () => {
    if (stopping || !draft?.id) return;
    const epoch = ++viewEpoch.current;
    operation.current = true;
    setBusy(true);
    setStopping(true);
    setError("");
    setPairing("");
    setFingerprint("");
    try {
      const next = await serverDialogCall<ServerDialogSnapshot>("Disconnect");
      if (active.current && epoch === viewEpoch.current) {
        setSnapshot(next);
        setDraft((old) => old && { ...old, enabled: false });
      }
    } catch (e) {
      if (active.current && epoch === viewEpoch.current)
        setError(String(e instanceof Error ? e.message : e));
    } finally {
      if (epoch === viewEpoch.current) {
        operation.current = false;
        if (active.current) {
          setBusy(false);
          setStopping(false);
        }
      }
    }
  };
  const scanPairing = (file: File) => {
    const epoch = viewEpoch.current;
    void run(async () => {
      const code = await readPairingQRCode(file);
      if (!active.current || epoch !== viewEpoch.current) return;
      setDraft((old) => old && { ...old, relay: code });
      setPairing("");
      setFingerprint("");
    });
  };
  return (
    <main className="desktop-settings-shell server-dialog-shell">
      <header className="server-dialog-header">
        <span className="server-dialog-symbol">
          <Server size={20} />
        </span>
        <div>
          <h1>
            {draft?.id ? `${displayName} · ${t("Settings")}` : t("Add server")}
          </h1>
          <p>{t("Server connection")}</p>
        </div>
        <button
          type="button"
          className="icon"
          aria-label={t("Close")}
          onClick={() => void serverDialogCall("Close")}
        >
          <X size={18} />
        </button>
      </header>
      <SettingsError message={error} t={t} />
      {!draft ? (
        <Hint>{t("Working…")}</Hint>
      ) : (
        <form
          ref={form}
          onSubmit={(event) => {
            event.preventDefault();
            void run(async () => {
              await save({
                ...draft,
                mode: draft[draft.mode]?.trim() ? draft.mode : visible,
              });
            });
          }}
        >
          <fieldset className="server-editor">
            <Card>
              <div className="server-name-field">
                <Row title={t("Display name (optional)")}>
                  <input
                    aria-label={t("Display name (optional)")}
                    aria-describedby="server-name-help"
                    value={draft.name}
                    placeholder={snapshot?.server.server_name || "Home"}
                    onChange={(event) =>
                      setDraft({ ...draft, name: event.target.value })
                    }
                    autoComplete="off"
                    spellCheck={false}
                    disabled={busy}
                  />
                </Row>
                <p id="server-name-help">
                  {t("Leave blank to use the name from the server's settings.")}
                </p>
              </div>
              <Row title={t("Connect to this server")}>
                <Toggle
                  label={t("Connect to this server")}
                  value={draft.enabled}
                  disabled={busy}
                  onChange={(enabled) => {
                    setDraft({ ...draft, enabled });
                    if (draft.id)
                      void run(async () => {
                        await save({ ...draft, enabled });
                      });
                  }}
                />
              </Row>
            </Card>
            <section className="server-dialog-connections">
              <h2>
                <Network size={16} />
                {t("Server connection")}
              </h2>
              <Row title={t("Allow automatic connection switching")}>
                <Toggle
                  label={t("Allow automatic connection switching")}
                  value={draft.automatic}
                  disabled={busy}
                  onChange={(automatic) => {
                    setDraft({ ...draft, automatic });
                    if (snapshot?.server.id)
                      void run(async () => {
                        const epoch = viewEpoch.current;
                        const next =
                          await serverDialogCall<ServerDialogSnapshot>(
                            "Save",
                            { ...snapshot.server, automatic },
                            false,
                          );
                        if (active.current && epoch === viewEpoch.current)
                          setSnapshot(next);
                      });
                  }}
                />
              </Row>
              <SegmentedTabs
                className="server-connection-tabs"
                label={t("Connection mode")}
                value={visible}
                onChange={setVisible}
                options={(["direct", "tailscale", "relay"] as const).map(
                  (mode) => ({
                    value: mode,
                    label: t(names[mode]),
                    id: `connection-${mode}`,
                    controls: `connection-panel-${mode}`,
                    title: `${t(names[mode])} · ${t(statuses[mode === snapshot?.server.mode ? snapshot.status : "disconnected"] || "Disconnected")}`,
                    icon: (
                      <span
                        className="server-status-dot"
                        data-state={
                          mode === snapshot?.server.mode
                            ? snapshot.status
                            : "disconnected"
                        }
                        aria-hidden="true"
                      />
                    ),
                  }),
                )}
              />
              {(["direct", "tailscale", "relay"] as const).map((mode) => (
                <section
                  key={mode}
                  role="tabpanel"
                  id={`connection-panel-${mode}`}
                  aria-labelledby={`connection-${mode}`}
                  hidden={mode !== visible}
                >
                  <label className="server-connection-field">
                    <input
                      type={mode === "relay" ? "text" : "url"}
                      aria-label={t(fields[mode])}
                      value={draft[mode]}
                      placeholder={
                        mode === "relay"
                          ? "spool-pair:…"
                          : mode === "tailscale"
                            ? "https://spool.example.ts.net"
                            : "https://spool.example.com"
                      }
                      onChange={(event) => {
                        setDraft({ ...draft, [mode]: event.target.value });
                        setPairing("");
                      }}
                      autoComplete="off"
                      spellCheck={false}
                      disabled={busy}
                    />
                  </label>
                  <div className="server-connection-actions">
                    <button
                      className="server-connection-use"
                      type="button"
                      disabled={
                        stopping ||
                        (!(connectionActive && currentMode === mode) &&
                          (busy || !draft[mode]?.trim()))
                      }
                      onClick={
                        connectionActive && currentMode === mode
                          ? () => void disconnect()
                          : connect
                      }
                    >
                      {t(
                        connectionActive && currentMode === mode
                          ? "Disconnect"
                          : "Connect",
                      )}
                    </button>
                    <div className="server-connection-tools">
                      {mode !== "direct" && (
                        <ConnectionControl
                          active={visible === mode}
                          label={`${t("Information")}: ${t(names[mode])}`}
                          icon={<Info size={14} aria-hidden="true" />}
                        >
                          {() => (
                            <div className="server-connection-information">
                              <h3>{t(names[mode])}</h3>
                              {mode === snapshot?.server.mode &&
                                snapshot.server.id && (
                                  <p role="status">
                                    {t(
                                      statuses[snapshot.status] ||
                                        "Disconnected",
                                    )}
                                  </p>
                                )}
                              <p>{t(descriptions[mode])}</p>
                            </div>
                          )}
                        </ConnectionControl>
                      )}
                      {mode === "tailscale" && (
                        <button
                          className="icon"
                          type="button"
                          aria-label={t("Sign in to Tailscale")}
                          title={t("Sign in to Tailscale")}
                          disabled={busy || !draft.tailscale?.trim()}
                          onClick={() =>
                            void run(async () => {
                              const epoch = viewEpoch.current;
                              await save({ ...draft, mode: "tailscale" });
                              if (
                                !active.current ||
                                epoch !== viewEpoch.current
                              )
                                return;
                              await serverDialogCall("TailscaleLogin");
                            })
                          }
                        >
                          <KeyRound size={17} aria-hidden="true" />
                        </button>
                      )}
                      {mode === "relay" && (
                        <button
                          className="icon"
                          type="button"
                          aria-label={t("Scan pairing QR code")}
                          title={t("Scan pairing QR code")}
                          disabled={busy}
                          onClick={() => pairingImage.current?.click()}
                        >
                          <ScanQrCode size={18} aria-hidden="true" />
                        </button>
                      )}
                    </div>
                  </div>
                </section>
              ))}
              <input
                ref={pairingImage}
                type="file"
                accept="image/*"
                aria-label={t("Scan pairing QR code")}
                hidden
                onChange={(event) => {
                  const file = event.currentTarget.files?.[0];
                  event.currentTarget.value = "";
                  if (file) scanPairing(file);
                }}
              />
              {pairing && (
                <div className="server-pairing-status" role="status">
                  <p>
                    {t(
                      pairing === "approved"
                        ? "Connected"
                        : "Approve this device on the server to finish pairing.",
                    )}
                  </p>
                  {fingerprint && <code>{fingerprint}</code>}
                </div>
              )}
            </section>
            <footer className="server-dialog-footer">
              <button type="submit" disabled={busy}>
                {t("Save settings")}
              </button>
              {draft.id && (
                <button
                  type="button"
                  className="danger"
                  disabled={busy}
                  onClick={() => setDeleting(true)}
                >
                  {t("Delete server")}
                </button>
              )}
            </footer>
            {deleting && (
              <div className="server-delete-confirm" role="alert">
                <p>{t("Delete server {name}?", { name: displayName })}</p>
                <button
                  type="button"
                  className="danger"
                  disabled={busy}
                  onClick={() =>
                    void run(async () => {
                      await serverDialogCall("Delete");
                    })
                  }
                >
                  {t("Delete server")}
                </button>
                <button type="button" onClick={() => setDeleting(false)}>
                  {t("Cancel")}
                </button>
              </div>
            )}
          </fieldset>
        </form>
      )}
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<ServerDialog />);
