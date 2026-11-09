import { useEffect, useId, useRef, useState } from "react";
import { ChevronRight, Info } from "lucide-react";
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

function DeviceApprovalDialog({
  device,
  t,
  busy,
  error,
  onDecide,
}: {
  device: DeviceRequest;
  t: Translator;
  busy: boolean;
  error: string;
  onDecide: (approve: boolean) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const descriptionID = useId();
  useEffect(() => {
    const element = dialog.current;
    element?.showModal();
    return () => element?.close();
  }, []);
  return (
    <dialog
      ref={dialog}
      className="settings-relay-approval-dialog"
      aria-label={t("Device approval")}
      aria-describedby={descriptionID}
      aria-busy={busy}
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onDecide(false);
      }}
    >
      <h2>{t("Device approval")}</h2>
      <strong className="settings-relay-approval-name">{device.name}</strong>
      <p id={descriptionID}>
        {t("Compare this fingerprint with the phone before allowing access.")}
      </p>
      <code className="settings-relay-fingerprint">
        {device.id.match(/.{1,8}/g)?.join(" ")}
      </code>
      {error && (
        <div className="settings-error" role="alert">
          {knownMessage(error, t)}
        </div>
      )}
      <div className="settings-relay-approval-actions">
        <button
          type="button"
          autoFocus
          disabled={busy}
          onClick={() => onDecide(false)}
        >
          {t("Reject device")}
        </button>
        <button
          type="button"
          className="settings-primary"
          disabled={busy}
          onClick={() => onDecide(true)}
        >
          {t("Allow device")}
        </button>
      </div>
    </dialog>
  );
}

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
  const [decision, setDecision] = useState<"approved" | "rejected">();
  const [expanded, setExpanded] = useState(false);
  const [devicesExpanded, setDevicesExpanded] = useState(false);
  const pairingID = useId();
  const pairingStatusID = useId();
  const devicesID = useId();
  const devicesHelpID = useId();
  const devicesHelp = t(
    "Revoking a device disconnects its LAN and relay sessions. Pair again to restore access.",
  );
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
    const code = currentCode.current;
    try {
      await settingsCall(method, ...args);
      revision.current++;
      if (method === "DecideRelayDevice") {
        if (code && code === currentCode.current)
          setDecision(args[1] ? "approved" : "rejected");
        setDevices((value) => ({
          ...value,
          pending: value.pending.filter(
            (device) => device.request_id !== args[0],
          ),
        }));
      }
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
        revision.current++;
        setPairing(value);
        setDecision(undefined);
        setDevices((devices) => ({ ...devices, pending: [] }));
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
      setDecision(undefined);
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
      const request = revision.current;
      try {
        const value = await settingsCall<Devices>("RelayDevices");
        if (!disposed && request === revision.current) {
          setDevices(value);
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
    setDecision(undefined);
    setExpanded(false);
    setDevices((value) => ({ ...value, pending: [] }));
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
  useEffect(() => {
    if (!active || !expanded || !pairing || decision) return;
    const tick = () => setNow(Date.now());
    tick();
    const timer = setInterval(tick, 1000);
    document.addEventListener("visibilitychange", tick);
    return () => {
      clearInterval(timer);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [active, expanded, pairing, decision]);
  const remaining = pairing
    ? Math.max(0, Math.ceil((pairing.expires_at * 1000 - now) / 1000))
    : 0;
  const pairingInactive = !!decision || remaining === 0;
  const countdown = `${Math.floor(remaining / 60)
    .toString()
    .padStart(2, "0")}:${(remaining % 60).toString().padStart(2, "0")}`;
  const request =
    active && expanded && running && !pending && !pairingInactive
      ? devices.pending.find((device) => device.expires_at * 1000 > now)
      : undefined;

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
              {pairing && (
                <div className="settings-relay-pairing-content">
                  <div
                    className="settings-relay-pairing-qr"
                    data-inactive={pairingInactive}
                  >
                    <img
                      src={pairing.qr}
                      width={320}
                      height={320}
                      alt={t("Spool server pairing QR code")}
                      aria-describedby={pairingStatusID}
                    />
                  </div>
                  <p>{pairing.relay_url}</p>
                  <p
                    id={pairingStatusID}
                    className="settings-relay-pairing-status"
                    role={pairingInactive ? "status" : "timer"}
                  >
                    {decision === "approved"
                      ? t(
                          "Pairing complete. Generate a new code to pair another device.",
                        )
                      : decision === "rejected"
                        ? t("Pairing code canceled. Generate a new code.")
                        : remaining === 0
                          ? t("Pairing code expired. Generate a new code.")
                          : t("Pairing code expires in {time}", {
                              time: countdown,
                            })}
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
                        disabled={pairingInactive}
                        iconOnly
                      />
                    </summary>
                    <textarea
                      readOnly
                      aria-label={t("Pairing code")}
                      disabled={pairingInactive}
                      value={pairing.code}
                    />
                  </details>
                </div>
              )}
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
          aria-describedby={devicesHelpID}
          onClick={() => setDevicesExpanded(!devicesExpanded)}
        >
          <span className="settings-relay-disclosure-copy">
            <strong className="settings-relay-devices-title">
              {t("Paired devices")}
              <span
                id={devicesHelpID}
                className="settings-relay-devices-help"
                title={devicesHelp}
                role="img"
                aria-label={devicesHelp}
              >
                <Info size={14} aria-hidden="true" />
              </span>
            </strong>
          </span>
          <span className="settings-relay-device-count">
            {devices.devices.length}
          </span>
          <ChevronRight size={16} aria-hidden="true" />
        </button>
        <div id={devicesID} hidden={!devicesExpanded}>
          {devicesExpanded && (
            <>
              {devices.devices.length === 0 && (
                <p style={{ padding: "0 20px" }}>{t("No paired devices")}</p>
              )}
              {devices.devices.map((device) => (
                <div key={device.id} className="settings-relay-device">
                  <div className="settings-relay-device-info">
                    <strong>{device.name}</strong>
                    <code className="settings-relay-fingerprint">
                      {device.id.match(/.{1,8}/g)?.join(" ")}
                    </code>
                  </div>
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() =>
                      void deviceAction("RevokeRelayDevice", device.id)
                    }
                  >
                    {t("Revoke device")}
                  </button>
                </div>
              ))}
            </>
          )}
        </div>
      </div>
      {request && (
        <DeviceApprovalDialog
          key={request.request_id}
          device={request}
          t={t}
          busy={busy}
          error={error}
          onDecide={(approve) =>
            void deviceAction("DecideRelayDevice", request.request_id, approve)
          }
        />
      )}
    </>
  );
}
