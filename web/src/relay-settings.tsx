import { useEffect, useId, useRef, useState } from "react";
import { ChevronRight } from "lucide-react";
import { settingsCall, type SettingsSnapshot } from "./settings-client";
import { CopyAddressButton, knownMessage, TextField } from "./settings-ui";
import type { Translator } from "./i18n";

type Pairing = {
  code: string;
  qr: string;
  relay_url: string;
  server_id: string;
  fingerprint: string;
  expires_at: number;
};

type Device = { id: string; name: string; created_at: number };
type DeviceRequest = Device & { request_id: string; expires_at: number };
type Devices = { devices: Device[]; pending: DeviceRequest[] };

export function RelayRegistration({
  data,
  t,
  disabled = false,
  onRegistered,
  onBusyChange,
}: {
  data: SettingsSnapshot;
  t: Translator;
  disabled?: boolean;
  onRegistered: () => void;
  onBusyChange: (busy: boolean) => void;
}) {
  const [token, setToken] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setToken("");
    setName("");
    setError("");
  }, [data.settings.relay_url]);
  const registering = busy || data.status.Relay?.Registering;
  return (
    <div
      className="settings-relay-section"
      role="group"
      aria-label={t("Relay registration token")}
    >
      <TextField
        title={t("Relay server name (optional)")}
        value={name}
        onChange={setName}
        disabled={registering}
        placeholder={t("For example, Home Mac")}
      />
      <TextField
        title={t("Relay registration token")}
        type="password"
        value={token}
        onChange={setToken}
        disabled={registering}
        placeholder={t("Registration token (optional)")}
        help={t(
          "Enter a token, or leave it empty for automatic registration. The token is saved after registration succeeds.",
        )}
      />
      <div className="settings-card-actions">
        <button
          type="button"
          disabled={
            registering ||
            disabled ||
            !(data.settings.relay_url ?? "").trim() ||
            (token.trim().length > 0 && token.trim().length < 32)
          }
          onClick={async () => {
            setBusy(true);
            onBusyChange(true);
            setError("");
            try {
              const status = await settingsCall<
                NonNullable<SettingsSnapshot["status"]["Relay"]>
              >("RegisterRelay", data.settings.relay_url, token, name);
              if (!status.Registered)
                throw new Error(
                  "Relay registration was not confirmed. Try again.",
                );
              setToken("");
              onRegistered();
            } catch (e) {
              setError(e instanceof Error ? e.message : String(e));
            } finally {
              setBusy(false);
              onBusyChange(false);
            }
          }}
        >
          {t(registering ? "Registering…" : "Register")}
        </button>
      </div>
      {error && (
        <div className="settings-error" role="alert">
          {knownMessage(error, t)}
        </div>
      )}
    </div>
  );
}

export function RelayPairing({
  data,
  t,
  active = true,
}: {
  data: SettingsSnapshot;
  t: Translator;
  active?: boolean;
}) {
  const [pairing, setPairing] = useState<Pairing>();
  const [expanded, setExpanded] = useState(false);
  const [devicesExpanded, setDevicesExpanded] = useState(false);
  const pairingID = useId();
  const devicesID = useId();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [devices, setDevices] = useState<Devices>({ devices: [], pending: [] });
  const [now, setNow] = useState(Date.now());
  const revision = useRef(0);
  const currentCode = useRef("");
  const pending = data.status.PendingChanges?.includes("relay") ?? false;
  const running =
    data.status.Running &&
    ["connecting", "connected", "reconnecting"].includes(
      data.status.Relay?.State ?? "",
    );
  async function refreshDevices() {
    setDevices(await settingsCall<Devices>("RelayDevices"));
  }
  async function deviceAction(
    method: "DecideRelayDevice" | "RevokeRelayDevice",
    ...args: unknown[]
  ) {
    setBusy(true);
    setError("");
    try {
      await settingsCall(method, ...args);
      await refreshDevices();
      if (method === "DecideRelayDevice" && args[1]) setDevicesExpanded(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  async function generatePairing() {
    const request = ++revision.current;
    setBusy(true);
    setError("");
    try {
      const value = await settingsCall<Pairing>("RelayPairing");
      if (request === revision.current) {
        setPairing(value);
        currentCode.current = value.code;
        setNow(Date.now());
      } else {
        void settingsCall("CancelRelayPairing", value.code).catch(() => {});
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  async function togglePairing() {
    if (!expanded) {
      setExpanded(true);
      await generatePairing();
      return;
    }
    setBusy(true);
    setError("");
    try {
      if (currentCode.current)
        await settingsCall("CancelRelayPairing", currentCode.current);
      revision.current++;
      currentCode.current = "";
      setPairing(undefined);
      setExpanded(false);
      setDevices((value) => ({ ...value, pending: [] }));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    if (!active) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const value = await settingsCall<Devices>("RelayDevices");
        if (!disposed) {
          setDevices(value);
          setNow(Date.now());
        }
      } catch (e) {
        if (!disposed) setError(String(e));
      }
      if (!disposed) timer = setTimeout(poll, 1500);
    };
    void poll();
    return () => {
      disposed = true;
      clearTimeout(timer);
    };
  }, [active]);
  useEffect(() => {
    revision.current++;
    setPairing(undefined);
    setExpanded(false);
    if (currentCode.current) {
      void settingsCall("CancelRelayPairing", currentCode.current).catch(
        () => {},
      );
      currentCode.current = "";
    }
  }, [
    active,
    running,
    pending,
    data.settings.relay_url,
    data.settings.relay_enabled,
    data.settings.relay_lan_port,
    data.settings.relay_web_enabled,
  ]);
  useEffect(
    () => () => {
      revision.current++;
      if (currentCode.current)
        void settingsCall("CancelRelayPairing", currentCode.current).catch(
          () => {},
        );
    },
    [],
  );
  return (
    <>
      <div
        className="settings-relay-section"
        role="group"
        aria-label={t("Pair Android phone")}
      >
        <button
          type="button"
          className="settings-relay-disclosure"
          aria-label={t("Pair Android phone")}
          aria-expanded={expanded}
          aria-controls={pairingID}
          aria-busy={busy}
          disabled={
            busy ||
            !active ||
            !running ||
            pending ||
            !data.settings.relay_enabled
          }
          onClick={() => void togglePairing()}
        >
          <span className="settings-relay-disclosure-copy">
            <strong>{t("Pair Android phone")}</strong>
            {(pending || !running) && (
              <small>
                {t(
                  pending
                    ? "Apply relay changes before pairing a phone."
                    : "Start Spool Relay before pairing a phone.",
                )}
              </small>
            )}
          </span>
          <ChevronRight size={16} aria-hidden="true" />
        </button>
        {error && (
          <div className="settings-error" role="alert">
            {knownMessage(error, t)}
          </div>
        )}
        <div id={pairingID} hidden={!expanded}>
          {expanded && (
            <>
              <p className="settings-relay-help">
                {t(
                  "Scan with the Spool app, compare the device fingerprint here, then approve the phone. Each code is valid for five minutes and one device.",
                )}
              </p>
              {pairing && pairing.expires_at * 1000 <= now && (
                <p role="status">
                  {t("Pairing code expired. Generate a new code.")}
                </p>
              )}
              {pairing && pairing.expires_at * 1000 > now && (
                <div style={{ padding: 20 }}>
                  <img
                    src={pairing.qr}
                    width={320}
                    height={320}
                    style={{
                      maxWidth: "100%",
                      height: "auto",
                      imageRendering: "pixelated",
                    }}
                    alt={t("Spool server pairing QR code")}
                  />
                  <p>{pairing.relay_url}</p>
                  <p role="status">
                    {t("Pairing code expires at")}{" "}
                    {new Date(pairing.expires_at * 1000).toLocaleTimeString()}
                  </p>
                  <p>{t("Server identity (SHA-256)")}</p>
                  <code
                    style={{
                      display: "block",
                      overflowWrap: "anywhere",
                      userSelect: "text",
                    }}
                  >
                    {pairing.fingerprint}
                  </code>
                  <details className="settings-pairing-code">
                    <summary>
                      {t("Manual pairing code")}
                      <CopyAddressButton
                        key={pairing.code}
                        t={t}
                        kind="copy-relay-pairing-code"
                        text={pairing.code}
                        iconOnly
                      />
                    </summary>
                    <textarea
                      readOnly
                      aria-label={t("Pairing code")}
                      value={pairing.code}
                    />
                  </details>
                </div>
              )}
              {devices.pending.map((device) => (
                <div
                  key={device.request_id}
                  style={{ padding: 20 }}
                  role="group"
                  aria-label={t("Device approval")}
                >
                  <strong>
                    {t("Device approval")}: {device.name}
                  </strong>
                  <p>
                    {t(
                      "Compare this fingerprint with the phone before allowing access.",
                    )}
                  </p>
                  <code
                    style={{
                      display: "block",
                      overflowWrap: "anywhere",
                      userSelect: "text",
                    }}
                  >
                    {device.id.match(/.{1,8}/g)?.join(" ")}
                  </code>
                  <div className="settings-card-actions">
                    <button
                      disabled={busy}
                      onClick={() =>
                        void deviceAction(
                          "DecideRelayDevice",
                          device.request_id,
                          false,
                        )
                      }
                    >
                      {t("Reject device")}
                    </button>
                    <button
                      disabled={busy}
                      onClick={() =>
                        void deviceAction(
                          "DecideRelayDevice",
                          device.request_id,
                          true,
                        )
                      }
                    >
                      {t("Allow device")}
                    </button>
                  </div>
                </div>
              ))}

              <div className="settings-card-actions">
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => void generatePairing()}
                >
                  {t("Refresh pairing code")}
                </button>
              </div>
            </>
          )}
        </div>
      </div>
      <div
        className="settings-relay-section"
        role="group"
        aria-label={t("Paired devices")}
      >
        <button
          type="button"
          className="settings-relay-disclosure"
          aria-label={t("Paired devices")}
          aria-expanded={devicesExpanded}
          aria-controls={devicesID}
          onClick={() => setDevicesExpanded(!devicesExpanded)}
        >
          <span className="settings-relay-disclosure-copy">
            <strong>{t("Paired devices")}</strong>
          </span>
          <span className="settings-relay-device-count">
            {devices.devices.length}
          </span>
          <ChevronRight size={16} aria-hidden="true" />
        </button>
        <div id={devicesID} hidden={!devicesExpanded}>
          {devicesExpanded && (
            <>
              <p className="settings-relay-help">
                {t(
                  "Revoking a device disconnects its LAN and relay sessions. Pair again to restore access.",
                )}
              </p>
              {devices.devices.length === 0 && (
                <p style={{ padding: "0 20px" }}>{t("No paired devices")}</p>
              )}
              {devices.devices.map((device) => (
                <div key={device.id} style={{ padding: "12px 20px" }}>
                  <strong>{device.name}</strong>
                  <code
                    style={{
                      display: "block",
                      overflowWrap: "anywhere",
                      userSelect: "text",
                    }}
                  >
                    {device.id.match(/.{1,8}/g)?.join(" ")}
                  </code>
                  <div className="settings-card-actions">
                    <button
                      disabled={busy}
                      onClick={() =>
                        void deviceAction("RevokeRelayDevice", device.id)
                      }
                    >
                      {t("Revoke device")}
                    </button>
                  </div>
                </div>
              ))}
            </>
          )}
        </div>
      </div>
    </>
  );
}
