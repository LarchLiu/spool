import { useEffect, useRef, useState } from "react";
import { settingsCall, type SettingsSnapshot } from "./settings-client";
import { CopyAddressButton, knownMessage, Row, TextField } from "./settings-ui";
import type { Translator } from "./i18n";

type Pairing = {
  code: string;
  qr: string;
  relay_url: string;
  server_id: string;
  fingerprint: string;
};

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
}: {
  data: SettingsSnapshot;
  t: Translator;
}) {
  const [pairing, setPairing] = useState<Pairing>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const revision = useRef(0);
  useEffect(() => {
    revision.current++;
    setPairing(undefined);
  }, [
    data.settings.relay_url,
    data.settings.relay_enabled,
    data.settings.relay_lan_port,
  ]);
  useEffect(
    () => () => {
      revision.current++;
    },
    [],
  );
  return (
    <div
      className="settings-relay-section"
      role="group"
      aria-label={t("Pair Android phone")}
    >
      <Row
        title={t("Pair Android phone")}
        help={t(
          "Scan with the Spool Android app, compare the server identity, then sign in with your Spool account.",
        )}
      />
      <div className="settings-card-actions">
        <button
          disabled={busy || !data.settings.relay_enabled}
          onClick={async () => {
            const request = ++revision.current;
            setBusy(true);
            setError("");
            try {
              const value = await settingsCall<Pairing>("RelayPairing");
              if (request === revision.current) setPairing(value);
            } catch (e) {
              setError(String(e));
            } finally {
              setBusy(false);
            }
          }}
        >
          {t(pairing ? "Refresh pairing code" : "Show pairing QR code")}
        </button>
        {pairing && (
          <button
            onClick={() => {
              revision.current++;
              setPairing(undefined);
            }}
          >
            {t("Hide pairing code")}
          </button>
        )}
      </div>
      {error && (
        <div className="settings-error" role="alert">
          {error}
        </div>
      )}
      {pairing && (
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
    </div>
  );
}
