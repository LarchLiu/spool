import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { createRoot } from "react-dom/client";
import { Info, KeyRound, ScanQrCode, X } from "lucide-react";
import { defaultPreferences } from "./preferences";
import { usePresentation } from "./use-presentation";
import { Hint, Row, SettingsError, Toggle } from "./settings-ui";
import { ServerDialogAutosave } from "./server-dialog-autosave";
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
import "./server-connection-tabs.css";
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
const emptyProfile: DesktopServerProfile = {
  id: "",
  name: "",
  server_name: "",
  address: "",
  direct: "",
  tailscale: "",
  relay: "",
  enabled: false,
  automatic: true,
  mode: "direct",
};

export function ServerDialog() {
  const { t } = usePresentation(defaultPreferences);
  const initialSnapshot = useRef(serverDialogInitialSnapshot());
  const [snapshot, setSnapshot] = useState(initialSnapshot.current);
  const latestSnapshot = useRef(initialSnapshot.current);
  const acceptSnapshot = (next: ServerDialogSnapshot) => {
    latestSnapshot.current = next;
    setSnapshot(next);
  };
  const [draft, setDraft] = useState<DesktopServerProfile | undefined>(
    initialSnapshot.current?.server,
  );
  const [visible, setVisible] = useState<ConnectionMode>(
    initialSnapshot.current?.server.mode || "direct",
  );
  const [busy, setBusy] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [error, setError] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [pairing, setPairing] = useState("");
  const [fingerprint, setFingerprint] = useState("");
  const operation = useRef(false);
  const active = useRef(true);
  const viewEpoch = useRef(0);
  const form = useRef<HTMLFormElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const pairingImage = useRef<HTMLInputElement>(null);
  const connectionActive = !!snapshot?.active || pairing === "pending";
  const currentMode = pairing === "pending" ? "relay" : snapshot?.server.mode;
  const displayName =
    draft?.name.trim() || snapshot?.server.server_name || "Home";
  const saveProfile = useRef(
    async (
      _value: DesktopServerProfile,
    ): Promise<DesktopServerProfile | undefined> => undefined,
  );
  saveProfile.current = async (value) => {
    const epoch = viewEpoch.current;
    operation.current = true;
    setSaving(true);
    setSaved(false);
    setError("");
    try {
      const next = await serverDialogCall<ServerDialogSnapshot>(
        "Save",
        value,
        false,
      );
      if (!active.current || epoch !== viewEpoch.current) return;
      acceptSnapshot(next);
      setSaved(true);
      return next.server;
    } catch (e) {
      if (active.current && epoch === viewEpoch.current)
        setError(String(e instanceof Error ? e.message : e));
    } finally {
      if (epoch === viewEpoch.current) {
        operation.current = false;
        if (active.current) setSaving(false);
      }
    }
  };
  const [autosave] = useState(
    () =>
      new ServerDialogAutosave(
        initialSnapshot.current?.server ?? emptyProfile,
        (profile) => saveProfile.current(profile),
        setDraft,
      ),
  );
  const flush = () =>
    autosave.flush(
      (key) =>
        form.current
          ?.querySelector<HTMLInputElement>(`[data-server-field="${key}"]`)
          ?.checkValidity() ?? true,
    );
  const commitInput = (
    event: KeyboardEvent<HTMLInputElement>,
    key: "name" | ConnectionMode,
  ) => {
    if (event.key !== "Enter" || event.nativeEvent.isComposing) return;
    event.preventDefault();
    if (event.currentTarget.reportValidity()) autosave.commit(key);
  };
  const close = useRef<() => void>(() => {});
  close.current = () => {
    const epoch = viewEpoch.current;
    void flush().then((saved) => {
      if (saved && epoch === viewEpoch.current) void serverDialogCall("Close");
      else form.current?.reportValidity();
    });
  };
  useEffect(() => {
    if (!saved) return;
    const timer = setTimeout(() => setSaved(false), 2500);
    return () => clearTimeout(timer);
  }, [saved]);
  useEffect(() => {
    active.current = true;
    let initial = !initialSnapshot.current;
    const receive = (event: Event) => {
      const next = (event as CustomEvent<ServerDialogSnapshot>).detail;
      viewEpoch.current++;
      operation.current = false;
      initial = false;
      acceptSnapshot(next);
      autosave.reset(next.server);
      setVisible(next.server.mode || "direct");
      setError("");
      setBusy(false);
      setSaving(false);
      setSaved(false);
      setStopping(false);
      setDeleting(false);
      setPairing("");
      setFingerprint("");
      document.querySelector(".server-dialog-shell")?.scrollTo(0, 0);
    };
    window.addEventListener("spool:server-dialog", receive);
    const escape = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape" && !event.isComposing) {
        event.preventDefault();
        close.current();
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
        acceptSnapshot(next);
        if (initial) {
          autosave.reset(next.server);
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
    if (!ready || !content.current) return;
    const epoch = viewEpoch.current;
    const element = content.current;
    let frame = 0;
    let lastHeight = 0;
    // Notify native only after React commits the form (or an initialization
    // error) and usePresentation applies the theme to the document. Measure
    // intrinsic content, so the modal can shrink as well as grow independently
    // of its scroll viewport.
    const measure = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        frame = requestAnimationFrame(() => {
          const height = Math.ceil(element.getBoundingClientRect().height);
          if (height <= 0 || height === lastHeight) return;
          lastHeight = height;
          void serverDialogCall("Ready", draft?.id || "", height).catch((e) => {
            if (active.current && epoch === viewEpoch.current)
              setError(String(e instanceof Error ? e.message : e));
          });
        });
      });
    };
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    measure();
    return () => {
      cancelAnimationFrame(frame);
      observer.disconnect();
    };
  }, [ready, snapshot]);
  const run = async (fn: () => Promise<void>) => {
    const epoch = viewEpoch.current;
    await autosave.settled();
    if (!active.current || epoch !== viewEpoch.current || operation.current)
      return;
    operation.current = true;
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
      { ...next, id: latestSnapshot.current?.server.id || next.id },
      connect,
    );
    if (active.current && epoch === viewEpoch.current) {
      acceptSnapshot(result);
      autosave.reset(result.server);
      setSaved(true);
    }
    return result;
  };
  const pair = async () => {
    if (!draft || !form.current?.reportValidity()) return;
    const epoch = viewEpoch.current;
    await flush();
    if (!active.current || epoch !== viewEpoch.current) return;
    await run(async () => {
      await save({ ...autosave.draft, mode: "relay", enabled: true });
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
        acceptSnapshot(result);
        autosave.reset(result.server);
      }
    });
  };
  const connect = async () => {
    if (!draft || !form.current?.reportValidity()) return;
    const epoch = viewEpoch.current;
    await flush();
    if (!active.current || epoch !== viewEpoch.current) return;
    if (visible === "relay" && autosave.draft.relay) {
      try {
        const data = JSON.parse(
          atob(
            autosave.draft.relay
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
      await save({ ...autosave.draft, mode: visible, enabled: true }, true);
    });
  };
  const disconnect = async () => {
    if (stopping || !draft?.id) return;
    const previousEpoch = viewEpoch.current;
    await flush();
    if (!active.current || previousEpoch !== viewEpoch.current) return;
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
        acceptSnapshot(next);
        autosave.sync(next.server);
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
    let scanned = false;
    void run(async () => {
      const code = await readPairingQRCode(file);
      if (!active.current || epoch !== viewEpoch.current) return;
      autosave.edit("relay", code);
      scanned = true;
      setPairing("");
      setFingerprint("");
    }).then(() => {
      if (scanned && epoch === viewEpoch.current) autosave.commit("relay");
    });
  };
  return (
    <main className="desktop-settings-shell server-dialog-shell">
      <div ref={content} className="server-dialog-content">
        <header className="server-dialog-header">
          <h1>
            {draft?.id
              ? `${displayName} · ${t("Settings")}`
              : t("Add server")}
          </h1>
          <span className="settings-sr-only" role="status">
            {t(
              saving || busy
                ? "Working…"
                : saved
                  ? "Saved"
                  : "Server connection",
            )}
          </span>
          <button
            type="button"
            className="icon"
            aria-label={t("Close")}
            onClick={() => close.current()}
          >
            <X size={19} />
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
              void flush();
            }}
          >
            <fieldset className="server-editor">
              <section className="server-dialog-identity">
                <div className="server-name-field">
                  <Row title={t("Display name (optional)")}>
                    <input
                      aria-label={t("Display name (optional)")}
                      aria-describedby="server-name-help"
                      data-server-field="name"
                      value={draft.name}
                      placeholder={snapshot?.server.server_name || "Home"}
                      onChange={(event) =>
                        autosave.edit("name", event.target.value)
                      }
                      onBlur={(event) => {
                        if (event.currentTarget.checkValidity())
                          autosave.commit("name");
                      }}
                      onKeyDown={(event) => commitInput(event, "name")}
                      maxLength={80}
                      autoComplete="off"
                      spellCheck={false}
                      disabled={busy}
                    />
                  </Row>
                  <p id="server-name-help">
                    {t(
                      "Leave blank to use the name from the server's settings.",
                    )}
                  </p>
                </div>
                <Row title={t("Connect to this server")}>
                  <Toggle
                    label={t("Connect to this server")}
                    value={draft.enabled}
                    disabled={busy}
                    onChange={(enabled) => {
                      autosave.edit("enabled", enabled);
                      autosave.commit("enabled");
                    }}
                  />
                </Row>
              </section>
              <section className="server-dialog-connections">
                <h2>{t("Server connection")}</h2>
                <Row title={t("Allow automatic connection switching")}>
                  <Toggle
                    label={t("Allow automatic connection switching")}
                    value={draft.automatic}
                    disabled={busy}
                    onChange={(automatic) => {
                      autosave.edit("automatic", automatic);
                      autosave.commit("automatic");
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
                        data-server-field={mode}
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
                          autosave.edit(mode, event.target.value);
                          setPairing("");
                        }}
                        onBlur={(event) => {
                          if (event.currentTarget.checkValidity())
                            autosave.commit(mode);
                        }}
                        onKeyDown={(event) => commitInput(event, mode)}
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
                                await save({
                                  ...autosave.draft,
                                  mode: "tailscale",
                                });
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
              {draft.id && (
                <footer className="server-dialog-footer">
                  <button
                    type="button"
                    className="danger"
                    disabled={busy}
                    onClick={() => setDeleting(true)}
                  >
                    {t("Delete server")}
                  </button>
                </footer>
              )}
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
            <button type="submit" hidden />
          </form>
        )}
      </div>
    </main>
  );
}

createRoot(document.getElementById("root")!).render(<ServerDialog />);
