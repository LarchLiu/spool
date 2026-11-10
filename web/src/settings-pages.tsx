import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import {
  ArrowUpRight,
  FolderOpen,
  Github,
  Globe2,
  Monitor,
  MonitorUp,
  Plus,
  Radio,
  RefreshCw,
  ShieldCheck,
} from "lucide-react";
import { AgentIcon } from "./agent-icon";
import { UserAvatar } from "./user-avatar";
import { languages, type UIPreferences } from "./preferences";
import {
  newPerson,
  settingsCall,
  type PersonDraft,
  type ManagedAgent,
  type Backend,
  type UpdatePreferences,
} from "./settings-client";
import { SettingsBack, SettingsList } from "./settings-list";
import { usePendingSettings, usePendingSettingsSave } from "./settings-pending";
import { ThemePicker } from "./theme-picker";
import { VoxlSettings } from "./voxl-settings";
import { RelayPairing, RelayRegistration } from "./relay-settings";
import { CustomSelect } from "./custom-select";
import { AddressLink, AddressQRButton } from "./address-actions";
import {
  NetworkSettingsAutosave,
  ServiceSettingsAutosave,
} from "./settings-autosave";
import { UpdateCheckInterval } from "./update-controls";
import { ReleaseNotesPanel } from "./release-notes";
import {
  Card,
  CopyAddressButton,
  Row,
  Toggle,
  TextField,
  PathField,
  Hint,
  submit,
  knownMessage,
  type PageProps,
} from "./settings-ui";

function useSettingsAutosave<
  T extends ServiceSettingsAutosave | NetworkSettingsAutosave,
>(
  { data, run }: Pick<PageProps, "data" | "run">,
  Autosave: new (
    ...args: ConstructorParameters<typeof ServiceSettingsAutosave>
  ) => T,
) {
  const { registerServiceDraft } = usePendingSettings();
  const runRef = useRef(run);
  runRef.current = run;
  const [, redraw] = useState(0);
  const [autosave] = useState(
    () =>
      new Autosave(
        data.settings,
        async (settings) =>
          (
            await runRef.current((latest) => ({
              kind: "save-settings",
              settings: settings(latest?.settings),
            }))
          )?.settings,
        () => redraw((value) => value + 1),
      ),
  );
  useEffect(() => autosave.sync(data.settings), [autosave, data.settings]);
  useEffect(
    () => registerServiceDraft(autosave),
    [registerServiceDraft, autosave],
  );
  return autosave;
}

export function NetworkPage({ data, run, t }: PageProps) {
  const autosave = useSettingsAutosave({ data, run }, NetworkSettingsAutosave);
  const draft = autosave.draft;
  const customDisabled = !draft.proxy_enabled || draft.use_system_proxy;
  return (
    <>
      <Card title={t("Proxy")}>
        <Row
          title={t("Enable proxy")}
          help={t(
            "Allow proxies for Spool's built-in requests. Turn off to connect directly, ignoring system, custom and environment proxies.",
          )}
        >
          <Toggle
            label={t("Enable proxy")}
            value={draft.proxy_enabled}
            onChange={(value) => {
              autosave.edit("proxy_enabled", value);
              void autosave.commit("proxy_enabled");
            }}
          />
        </Row>
        <Row
          title={t("Use system proxy")}
          help={t(
            "Use macOS system HTTP/HTTPS proxies, including updates and Tailscale. Environment proxies take priority; custom settings below are ignored while this is on.",
          )}
        >
          <Toggle
            label={t("Use system proxy")}
            value={draft.use_system_proxy}
            disabled={!draft.proxy_enabled}
            onChange={(value) => {
              autosave.edit("use_system_proxy", value);
              void autosave.commit("use_system_proxy");
            }}
          />
        </Row>
        <TextField
          icon={<Globe2 size={15} />}
          title={t("Proxy URL")}
          value={draft.proxy_url}
          placeholder="http://127.0.0.1:8889"
          disabled={customDisabled}
          help={t(
            "Custom HTTP(S) or SOCKS5 proxy for Spool's built-in requests. Used when proxy is enabled and system proxy is off. Leave blank to inherit environment proxies.",
          )}
          onChange={(value) => autosave.edit("proxy_url", value)}
          onCommit={() => void autosave.commit("proxy_url")}
        />
        <TextField
          icon={<Globe2 size={15} />}
          title={t("No proxy")}
          value={draft.no_proxy}
          placeholder="localhost,127.0.0.1"
          disabled={customDisabled}
          help={t(
            "Comma-separated hosts or IP ranges that should bypass the proxy. Leave blank to inherit NO_PROXY. Loopback always connects directly.",
          )}
          onChange={(value) => autosave.edit("no_proxy", value)}
          onCommit={() => void autosave.commit("no_proxy")}
        />
      </Card>
    </>
  );
}

const serviceTabs = [
  { key: "web", label: "Web", icon: Globe2 },
  { key: "relay", label: "Relay", icon: Radio },
  { key: "tsnet", label: "Tailscale", icon: ShieldCheck },
  { key: "desktop", label: "Remote desktop", icon: Monitor },
] as const;
type ServiceTab = (typeof serviceTabs)[number]["key"];

function ServiceTabPanel({
  id,
  tab,
  active,
  children,
}: {
  id: string;
  tab: ServiceTab;
  active: ServiceTab;
  children: ReactNode;
}) {
  return (
    <div
      className="settings-service-panel"
      role="tabpanel"
      id={`${id}-panel-${tab}`}
      aria-labelledby={`${id}-tab-${tab}`}
      hidden={active !== tab}
      tabIndex={0}
    >
      {children}
    </div>
  );
}

export function ServicePage({ data, run, t }: PageProps) {
  const autosave = useSettingsAutosave({ data, run }, ServiceSettingsAutosave);
  const draft = autosave.draft;
  const tabsID = useId();
  const [activeTab, setActiveTab] = useState<ServiceTab>("web");
  const [detectingWeb, setDetectingWeb] = useState(false);
  const [detectedWebURL, setDetectedWebURL] = useState<string>();
  const webDetecting = detectingWeb || data.status.WebDetecting;
  const verifiedWebURL = detectedWebURL ?? data.settings.web_detected_origin;
  const webDetected =
    !webDetecting &&
    !data.status.WebError &&
    verifiedWebURL &&
    verifiedWebURL === draft.public_url;
  const [registeredRelayURL, setRegisteredRelayURL] = useState<string>();
  const [registeringRelay, setRegisteringRelay] = useState(false);
  const [unregisterError, setUnregisterError] = useState("");
  useEffect(() => setUnregisterError(""), [data.settings.relay_url]);
  useEffect(() => {
    if (
      data.status.Relay?.Registered ||
      registeredRelayURL !== data.settings.relay_url
    )
      setRegisteredRelayURL(undefined);
  }, [
    data.status.Relay?.Registered,
    data.settings.relay_url,
    registeredRelayURL,
  ]);
  const relayRegistered =
    draft.relay_url === data.settings.relay_url &&
    (data.status.Relay?.Registered || registeredRelayURL === draft.relay_url);
  const relayRegistering = registeringRelay || data.status.Relay?.Registering;
  const relayActive =
    draft.relay_enabled ||
    data.status.Relay?.State === "connecting" ||
    data.status.Relay?.State === "reconnecting" ||
    data.status.Relay?.State === "connected";
  const relayAddress = (
    <div
      className="settings-relay-section"
      role="group"
      aria-label={t("Relay HTTPS address")}
    >
      <TextField
        title={t("Relay HTTPS address")}
        value={draft.relay_url}
        disabled={relayRegistering || draft.relay_enabled}
        onChange={(v) => autosave.edit("relay_url", v)}
        onCommit={() => void autosave.commit("relay_url")}
      />
      {!relayRegistered && (
        <RelayRegistration
          data={data}
          t={t}
          disabled={draft.relay_url !== data.settings.relay_url}
          onRegistered={() => setRegisteredRelayURL(data.settings.relay_url)}
          onBusyChange={setRegisteringRelay}
        />
      )}
      {!relayActive &&
        (relayRegistered ||
          (draft.relay_url === data.settings.relay_url &&
            data.status.Relay?.RegistrationTokenSet)) && (
          <div className="settings-card-actions">
            <button
              type="button"
              disabled={
                relayRegistering || draft.relay_url !== data.settings.relay_url
              }
              onClick={async () => {
                setRegisteringRelay(true);
                setUnregisterError("");
                try {
                  const result = await run({
                    kind: "unregister-relay",
                    target: data.settings.relay_url ?? "",
                  });
                  if (result) setRegisteredRelayURL(undefined);
                } catch (error) {
                  setUnregisterError(
                    error instanceof Error ? error.message : String(error),
                  );
                } finally {
                  setRegisteringRelay(false);
                }
              }}
            >
              {t(relayRegistering ? "Unregistering…" : "Unregister")}
            </button>
          </div>
        )}
      {unregisterError && (
        <div className="settings-error" role="alert">
          {knownMessage(unregisterError, t)}
        </div>
      )}
    </div>
  );
  const [authKey, setAuthKey] = useState("");
  const [requestingLogin, setRequestingLogin] = useState(false);
  const [loginError, setLoginError] = useState("");
  const loginAttempt = useRef(0);
  const [registeringKey, setRegisteringKey] = useState(false);
  const [registrationError, setRegistrationError] = useState("");
  const [authRegistered, setAuthRegistered] = useState(false);
  const tailnet = data.status.TSNet;
  useEffect(() => {
    if (tailnet?.State === "Running" || !data.settings.tsnet_enabled)
      setAuthRegistered(false);
  }, [tailnet?.State, data.settings.tsnet_enabled]);
  const canLogin =
    !registeringKey &&
    (!draft.tsnet_enabled ||
      (!authRegistered &&
        (tailnet?.State === "NeedsLogin" ||
          tailnet?.State === "NoState" ||
          Boolean(tailnet?.AuthURL))));
  const tailnetState =
    tailnet?.State === "Running"
      ? t("Connected")
      : authRegistered || tailnet?.State === "Registered"
        ? t("Registered")
        : registeringKey || tailnet?.State === "Registering"
          ? t("Registering…")
          : tailnet?.State === "NeedsLogin"
            ? t("Waiting for Tailscale login")
            : tailnet?.State === "NeedsMachineAuth"
              ? t("Waiting for device approval")
              : tailnet?.State === "Error"
                ? t("Tailscale unavailable")
                : tailnet?.State === "Starting" || tailnet?.State === "NoState"
                  ? t("Starting Tailscale")
                  : t("Stopped");
  return (
    <>
      <Card title={t("Spool service")}>
        <Row title={t("Service status")}>
          <span
            className={`settings-state${data.status.Running ? " online" : ""}`}
          >
            <span className="settings-status-dot" />
            {t(data.status.Running ? "Running" : "Stopped")}
          </span>
        </Row>
        <Row title={t("Start Spool service on application launch")}>
          <Toggle
            label={t("Start Spool service on application launch")}
            value={draft.auto_start}
            onChange={(v) => {
              autosave.edit("auto_start", v);
              void autosave.commit("auto_start");
            }}
          />
        </Row>
        <div className="settings-card-actions">
          <button
            type="button"
            className="settings-primary"
            disabled={data.status.Running}
            onClick={() => void run({ kind: "start-service" })}
          >
            {t("Start service")}
          </button>
          <button
            type="button"
            disabled={!data.status.Running}
            onClick={() => void run({ kind: "stop-service" })}
          >
            {t("Stop service")}
          </button>
        </div>
      </Card>
      {data.status.Error && (
        <div className="settings-error">
          {knownMessage(data.status.Error, t)}
        </div>
      )}
      <div
        className="settings-service-tabs"
        role="tablist"
        aria-label={t("Service")}
      >
        {serviceTabs.map(({ key, label, icon: Icon }, index) => (
          <button
            key={key}
            type="button"
            role="tab"
            id={`${tabsID}-tab-${key}`}
            aria-controls={`${tabsID}-panel-${key}`}
            aria-selected={activeTab === key}
            tabIndex={activeTab === key ? 0 : -1}
            data-service-tab={key}
            onClick={() => setActiveTab(key)}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing) return;
              const next =
                event.key === "ArrowRight"
                  ? (index + 1) % serviceTabs.length
                  : event.key === "ArrowLeft"
                    ? (index - 1 + serviceTabs.length) % serviceTabs.length
                    : event.key === "Home"
                      ? 0
                      : event.key === "End"
                        ? serviceTabs.length - 1
                        : -1;
              if (next < 0) return;
              event.preventDefault();
              setActiveTab(serviceTabs[next].key);
              event.currentTarget.parentElement
                ?.querySelector<HTMLButtonElement>(
                  `[data-service-tab="${serviceTabs[next].key}"]`,
                )
                ?.focus();
            }}
          >
            <Icon size={15} aria-hidden="true" />
            {t(label)}
          </button>
        ))}
      </div>
      <ServiceTabPanel id={tabsID} tab="web" active={activeTab}>
        <Card title={t("Web access")}>
          <Row
            title={t("Enable Web access")}
            help={t("Access Spool in your browser.")}
          >
            <Toggle
              label={t("Enable Web access")}
              value={draft.web_enabled}
              onChange={(value) => {
                autosave.edit("web_enabled", value);
                void autosave.commit("web_enabled");
              }}
            />
          </Row>
          {data.status.Running && data.status.Address && (
            <Row title={t("Active listen address")}>
              <AddressLink
                address={`http://${data.status.Address.replace(/^0\.0\.0\.0:/, "127.0.0.1:").replace(/^\[::\]:/, "[::1]:")}`}
                t={t}
              />
              <code>{data.status.Address}</code>
            </Row>
          )}
          <Row
            title={t("Allow LAN access")}
            help={t(
              "Listen on all network interfaces. Remote access requires HTTPS through a trusted reverse proxy; direct HTTP is blocked.",
            )}
          >
            <Toggle
              label={t("Allow LAN access")}
              value={
                !["localhost", "::1", "0:0:0:0:0:0:0:1"].includes(draft.host) &&
                !draft.host.startsWith("127.")
              }
              disabled={!draft.web_enabled}
              onChange={(value) => {
                autosave.edit("host", value ? "0.0.0.0" : "127.0.0.1");
                void autosave.commit("host");
              }}
            />
          </Row>
          <TextField
            title={t("HTTP port")}
            type="number"
            value={draft.port}
            min={1}
            max={65535}
            disabled={!draft.web_enabled}
            onChange={(v) => autosave.edit("port", v)}
            onCommit={() => void autosave.commit("port")}
            required
          />
          <TextField
            title={t("Public URL")}
            beforeInput={
              <>
                <AddressLink
                  address={draft.public_url}
                  t={t}
                  disabled={!draft.web_enabled}
                />
                <AddressQRButton
                  address={draft.public_url}
                  title={t("Public URL")}
                  t={t}
                  disabled={!draft.web_enabled}
                />
              </>
            }
            value={draft.public_url}
            disabled={!draft.web_enabled || webDetecting}
            placeholder="https://spool.example.com"
            help={t(
              "Enter your public HTTPS address. Spool checks it automatically when you finish editing and each time Web access starts.",
            )}
            onChange={(v) => {
              setDetectedWebURL(undefined);
              autosave.edit("public_url", v);
            }}
            onCommit={() => void autosave.commit("public_url")}
          />
          <div className="settings-card-actions">
            <button
              type="button"
              data-detect-web
              disabled={
                webDetecting ||
                !draft.web_enabled ||
                !data.status.Running ||
                draft.public_url !== data.settings.public_url ||
                !draft.public_url.trim()
              }
              onClick={async () => {
                setDetectingWeb(true);
                setDetectedWebURL("");
                try {
                  const result = await run({
                    kind: "detect-web-proxy",
                    address: draft.public_url,
                  });
                  if (result) setDetectedWebURL(result.settings.public_url);
                } finally {
                  setDetectingWeb(false);
                }
              }}
            >
              {t(webDetecting ? "Detecting…" : "Detect")}
            </button>
          </div>
          {webDetected && (
            <p className="settings-hint" role="status">
              {t("Detection succeeded. Your public address is ready to use.")}
            </p>
          )}
          <details className="settings-advanced">
            <summary>{t("Advanced settings")}</summary>
            {data.settings.web_detected_origin === draft.public_url &&
              data.settings.web_detected_proxy && (
                <p className="settings-hint" data-detected-web-proxy>
                  {t("Detected proxy address")}:{" "}
                  <code>{data.settings.web_detected_proxy}</code>
                </p>
              )}
            <TextField
              title={t("Web trusted proxies")}
              value={draft.web_trusted_proxies}
              disabled={!draft.web_enabled || webDetecting}
              placeholder="127.0.0.0/8,::1/128"
              help={t(
                "Detected addresses are managed automatically. Add entries here only for a custom proxy setup.",
              )}
              onChange={(v) => autosave.edit("web_trusted_proxies", v)}
              onCommit={() => void autosave.commit("web_trusted_proxies")}
            />
          </details>
          {data.status.WebError && (
            <div className="settings-error" role="alert">
              {knownMessage(data.status.WebError, t)}
            </div>
          )}
        </Card>
      </ServiceTabPanel>
      <ServiceTabPanel id={tabsID} tab="relay" active={activeTab}>
        <Card title={t("Spool Relay")}>
          <Row
            title={t("Enable Spool Relay")}
            help={t(
              relayRegistered
                ? "Phones prefer a verified LAN connection and use the relay when away."
                : "Register this server with the relay before enabling it.",
            )}
          >
            {draft.relay_enabled && (
              <span
                className={`settings-state${data.status.Relay?.State === "connected" ? " online" : ""}`}
                role="status"
                aria-label={t("Relay status")}
                title={
                  data.status.Relay?.DiscoveryError
                    ? knownMessage(data.status.Relay.DiscoveryError, t)
                    : undefined
                }
              >
                <span className="settings-status-dot" />
                {knownMessage(
                  data.status.Relay?.State === "disabled"
                    ? "stopped"
                    : (data.status.Relay?.State ?? "stopped"),
                  t,
                )}
              </span>
            )}
            <Toggle
              label={t("Enable Spool Relay")}
              value={draft.relay_enabled}
              disabled={
                relayRegistering || (!draft.relay_enabled && !relayRegistered)
              }
              onChange={(v) => {
                autosave.edit("relay_enabled", v);
                void autosave.commit("relay_enabled");
              }}
            />
          </Row>
          {relayAddress}
          <TextField
            title={t("Secure LAN port")}
            value={draft.relay_lan_port}
            type="number"
            min={1}
            max={65535}
            onChange={(v) => autosave.edit("relay_lan_port", v)}
            onCommit={() => void autosave.commit("relay_lan_port")}
          />
          <div
            className="settings-relay-section"
            role="group"
            aria-label={t("Public internet access")}
          >
            <Row
              title={t("Public internet access")}
              help={t(
                "Access this workspace at the relay HTTPS address. The relay can read Web data. Use caution when enabling this for an untrusted third-party service. The mobile app uses end-to-end encryption and is not affected.",
              )}
            >
              <Toggle
                label={t("Public internet access")}
                value={draft.relay_web_enabled}
                disabled={!draft.relay_enabled}
                onChange={(value) => {
                  autosave.edit("relay_web_enabled", value);
                  void autosave.commit("relay_web_enabled");
                }}
              />
            </Row>
            {draft.relay_web_enabled && data.status.Relay?.WebURL && (
              <Row
                title={t("Public Web address")}
                help={data.status.Relay.WebURL}
              >
                <AddressQRButton
                  address={data.status.Relay.WebURL}
                  title={t("Public Web address")}
                  t={t}
                />
                <CopyAddressButton
                  key={data.status.Relay.WebURL}
                  t={t}
                  kind="copy-relay-web-url"
                  iconOnly
                />
              </Row>
            )}
          </div>
          {data.status.Relay?.Error && (
            <div className="settings-error" role="alert">
              {data.status.Relay.Error}
            </div>
          )}
          <RelayPairing data={data} t={t} active={activeTab === "relay"} />
        </Card>
      </ServiceTabPanel>
      <ServiceTabPanel id={tabsID} tab="tsnet" active={activeTab}>
        <Card title="Tailscale">
          <div
            className="settings-relay-section"
            role="group"
            aria-label={t("Enable embedded Tailscale")}
          >
            <Row
              title={t("Enable embedded Tailscale")}
              help={t(
                "Enable MagicDNS and HTTPS certificates in the Tailscale admin DNS settings. Spool login is still required. The existing Android APK only needs this HTTPS address.",
              )}
            >
              <span
                className={`settings-state${tailnet?.State === "Running" ? " online" : ""}`}
                role="status"
                aria-label={t("Tailnet status")}
              >
                <span className="settings-status-dot" />
                {tailnetState}
              </span>
              <Toggle
                label={t("Enable embedded Tailscale")}
                value={draft.tsnet_enabled}
                onChange={(v) => {
                  autosave.edit("tsnet_enabled", v);
                  void autosave.commit("tsnet_enabled");
                }}
              />
            </Row>
            {tailnet?.State === "Running" && tailnet.URL && (
              <Row
                title={t("Tailnet HTTPS address")}
                help={<code>{tailnet.URL}</code>}
              >
                <AddressQRButton
                  address={tailnet.URL}
                  title={t("Tailnet HTTPS address")}
                  t={t}
                />
                <CopyAddressButton key={tailnet.URL} t={t} iconOnly />
              </Row>
            )}
          </div>
          <TextField
            title={t("Tailscale node name")}
            value={draft.tsnet_hostname}
            onChange={(v) => autosave.edit("tsnet_hostname", v)}
            onCommit={() => void autosave.commit("tsnet_hostname")}
            required
          />
          <TextField
            title={t("Tailnet HTTPS port")}
            value={draft.tsnet_port}
            type="number"
            min={1}
            max={65535}
            onChange={(v) => autosave.edit("tsnet_port", v)}
            onCommit={() => void autosave.commit("tsnet_port")}
            help={t(
              "This port belongs to the embedded node, not the host computer.",
            )}
            required
          />
          <div
            className="settings-relay-section"
            role="group"
            aria-label={t("Tailscale Auth Key (optional)")}
          >
            <TextField
              title={t("Tailscale Auth Key (optional)")}
              type="password"
              value={authKey}
              disabled={registeringKey}
              onChange={setAuthKey}
              help={t(
                "Click Use Auth Key to register now without stopping Spool. The key is never saved in settings.",
              )}
            />
            <div className="settings-card-actions">
              <button
                type="button"
                disabled={
                  registeringKey || !authKey.trim() || !draft.tsnet_enabled
                }
                onClick={async () => {
                  loginAttempt.current++;
                  setRequestingLogin(false);
                  setLoginError("");
                  setRegistrationError("");
                  setAuthRegistered(false);
                  setRegisteringKey(true);
                  try {
                    await settingsCall<boolean>("TSNetRegister", authKey);
                    setAuthKey("");
                    setAuthRegistered(true);
                  } catch (error) {
                    setRegistrationError(
                      error instanceof Error ? error.message : String(error),
                    );
                  } finally {
                    setRegisteringKey(false);
                  }
                }}
              >
                {t(
                  registeringKey
                    ? "Registering with Auth Key..."
                    : "Use Auth Key",
                )}
              </button>
            </div>
            {registrationError && (
              <div className="settings-error" role="alert">
                {knownMessage(registrationError, t)}
              </div>
            )}
          </div>
          {canLogin && (
            <div className="settings-card-actions">
              <button
                type="button"
                disabled={requestingLogin}
                onClick={async () => {
                  const attempt = ++loginAttempt.current;
                  setRequestingLogin(true);
                  setLoginError("");
                  try {
                    await settingsCall<boolean>("TSNetLogin");
                  } catch (error) {
                    if (attempt === loginAttempt.current)
                      setLoginError(
                        error instanceof Error ? error.message : String(error),
                      );
                  } finally {
                    if (attempt === loginAttempt.current)
                      setRequestingLogin(false);
                  }
                }}
              >
                {t(
                  requestingLogin
                    ? "Getting login link..."
                    : "Sign in to Tailscale",
                )}
                <ArrowUpRight size={14} />
              </button>
            </div>
          )}
          {loginError && (
            <div className="settings-error" role="alert">
              {knownMessage(loginError, t)}
            </div>
          )}
          {canLogin && draft.tsnet_enabled && !tailnet?.AuthURL && (
            <Hint>
              {t(
                "This node is not signed in yet. Click Sign in to request its login link. If no link is available, check network access to Tailscale.",
              )}
            </Hint>
          )}
          {tailnet?.Error && (
            <div className="settings-error" role="alert">
              <p>{tailnet.Error}</p>
              <button
                type="button"
                onClick={() =>
                  void run({ kind: "apply-service-changes", target: "tsnet" })
                }
              >
                {t("Reconnect Tailscale")}
              </button>
            </div>
          )}
        </Card>
      </ServiceTabPanel>
      <ServiceTabPanel id={tabsID} tab="desktop" active={activeTab}>
        <VoxlSettings data={data} run={run} t={t} />
      </ServiceTabPanel>
    </>
  );
}

export function PeoplePage({ data, run, t }: PageProps) {
  const [draft, setDraft] = useState<PersonDraft>(newPerson);
  const [editing, setEditing] = useState(false);
  const [original, setOriginal] = useState<PersonDraft>(newPerson);
  const form = useRef<HTMLFormElement>(null);
  const edit = (person: PersonDraft) => {
    setDraft(person);
    setOriginal(person);
    setEditing(true);
  };
  const save = async () => {
    const next = await run({ kind: "save-person", person: draft });
    const person = next?.users.find((person) => person.id === next.saved_id);
    if (person) edit({ ...person, password: "" });
  };
  usePendingSettingsSave({
    id: "person",
    section: "People",
    title: `${t("People")} · ${draft.nickname || draft.username || t("New person")}`,
    label: t("Save person"),
    dirty:
      editing &&
      (!draft.id || JSON.stringify(draft) !== JSON.stringify(original)),
    save: () => form.current?.requestSubmit(),
    cancel: () => {
      const person = data.users.find((person) => person.id === draft.id);
      if (person) edit({ ...person, password: "" });
      else {
        setDraft(newPerson());
        setEditing(false);
      }
    },
  });
  if (!editing)
    return (
      <SettingsList
        label={t("People")}
        summary={t("{count} people", { count: data.users.length })}
        empty={t("No people yet. Create an account to get started.")}
        items={data.users.map((person) => ({
          id: person.id,
          title: person.nickname || person.username,
          description: person.username,
          status: person.disabled ? t("Disabled") : "",
          action: person.remote_desktop && (
            <div className="settings-person-remote">
              <span
                className="settings-person-remote-icon"
                role="img"
                aria-label={t("Remote desktop allowed")}
                title={t("Remote desktop allowed")}
              >
                <MonitorUp size={17} aria-hidden="true" />
              </span>
              <span
                className={`settings-remote-count${(data.status.RemoteDesktopConnections?.[person.id] ?? 0) > 0 ? " is-connected" : ""}`}
                role="status"
                aria-label={t("Remote connections · {count}", {
                  count: data.status.RemoteDesktopConnections?.[person.id] ?? 0,
                })}
                title={t("Remote connections · {count}", {
                  count: data.status.RemoteDesktopConnections?.[person.id] ?? 0,
                })}
              >
                {data.status.RemoteDesktopConnections?.[person.id] ?? 0}
              </span>
              {(data.status.RemoteDesktopConnections?.[person.id] ?? 0) > 0 && (
                <button
                  type="button"
                  aria-label={`${t("Disconnect desktop")} · ${person.nickname || person.username}`}
                  onClick={() =>
                    void run({
                      kind: "disconnect-remote-desktop",
                      target: person.id,
                    })
                  }
                >
                  {t("Disconnect desktop")}
                </button>
              )}
            </div>
          ),
          icon: <UserAvatar person={person} size={34} />,
        }))}
        onSelect={(id) => {
          const person = data.users.find((person) => person.id === id);
          if (person) {
            edit({ ...person, password: "" });
          }
        }}
        action={
          <button
            type="button"
            className="settings-primary"
            data-new-entity
            onClick={() => edit(newPerson())}
          >
            <Plus size={15} />
            {t("New person")}
          </button>
        }
      />
    );
  return (
    <>
      <SettingsBack
        label={t("Back to list")}
        onClick={() => {
          setDraft((value) => ({ ...value, password: "" }));
          setEditing(false);
        }}
      />
      <form ref={form} onSubmit={(e) => submit(e, () => void save())}>
        <Card title={draft.id ? t("Edit person") : t("New person")}>
          <TextField
            title={t("Username")}
            value={draft.username}
            onChange={(v) => setDraft({ ...draft, username: v })}
            required
          />
          <TextField
            title={t("Display name")}
            value={draft.nickname}
            onChange={(v) => setDraft({ ...draft, nickname: v })}
            required
          />
          <Row
            title={t("Password")}
            help={t("Password (12–72 bytes; blank keeps existing password)")}
          >
            <input
              aria-label={t("Password")}
              type="password"
              value={draft.password}
              autoComplete="new-password"
              onChange={(e) => setDraft({ ...draft, password: e.target.value })}
              required={!draft.id}
            />
          </Row>
          <Row title={t("Disable account")}>
            <Toggle
              label={t("Disable account")}
              value={draft.disabled}
              onChange={(disabled) => setDraft({ ...draft, disabled })}
            />
          </Row>
          <Row
            title={t("Allow remote desktop connection")}
            help={t(
              "Allows control of this computer's entire desktop, beyond project permissions.",
            )}
          >
            <Toggle
              label={t("Allow remote desktop connection")}
              value={!!draft.remote_desktop}
              onChange={(remote_desktop) =>
                setDraft({ ...draft, remote_desktop })
              }
            />
          </Row>
        </Card>
        <button type="submit" hidden />
      </form>
    </>
  );
}

export function AgentsPage(props: PageProps) {
  const [selected, setSelected] = useState<Backend>();
  const agent = props.data.agents.find((agent) => agent.backend === selected);
  return (
    <>
      {agent ? (
        <>
          <SettingsBack
            label={props.t("Back to list")}
            onClick={() => setSelected(undefined)}
          />
          <AgentCard key={agent.backend} agent={agent} {...props} />
        </>
      ) : (
        <SettingsList
          label={props.t("Agents")}
          summary={props.t("{count} agents", {
            count: props.data.agents.length,
          })}
          empty={props.t("No agents available.")}
          items={props.data.agents.map((agent) => ({
            id: agent.backend,
            title: agent.backend === "codex" ? "Codex" : "Claude Code",
            description:
              [agent.version, agent.resolved_path || agent.executable]
                .filter(Boolean)
                .join(" · ") || props.t("CLI executable (blank = auto-detect)"),
            status: `${props.t(agent.installed ? "Installed" : "Not installed")} · ${props.t(agent.enabled ? "Enabled" : "Disabled")}`,
            icon: <AgentIcon backend={agent.backend} size={20} decorative />,
          }))}
          onSelect={(id) =>
            setSelected(
              props.data.agents.find((agent) => agent.backend === id)?.backend,
            )
          }
          action={
            <button
              type="button"
              onClick={() => void props.run({ kind: "refresh-agents" })}
            >
              <RefreshCw size={15} />
              {props.t("Refresh installation & models")}
            </button>
          }
        />
      )}
    </>
  );
}
function AgentCard({ agent, run, t }: PageProps & { agent: ManagedAgent }) {
  const [path, setPath] = useState(agent.executable);
  const [enabled, setEnabled] = useState(agent.enabled);
  const [original, setOriginal] = useState({
    path: agent.executable,
    enabled: agent.enabled,
  });
  const form = useRef<HTMLFormElement>(null);
  const name = agent.backend === "codex" ? "Codex" : "Claude Code";
  const reset = (value: ManagedAgent) => {
    setPath(value.executable);
    setEnabled(value.enabled);
    setOriginal({ path: value.executable, enabled: value.enabled });
  };
  const save = async () => {
    const next = await run({
      kind: "save-agent",
      backend: agent.backend,
      enabled,
      path,
    });
    const saved = next?.agents.find((value) => value.backend === agent.backend);
    if (saved) reset(saved);
  };
  usePendingSettingsSave({
    id: `agent-${agent.backend}`,
    section: "Agents",
    title: `${t("Agents")} · ${name}`,
    label: t("Save {agent}", { agent: name }),
    dirty: path !== original.path || enabled !== original.enabled,
    save: () => form.current?.requestSubmit(),
    cancel: () => reset(agent),
  });
  return (
    <form ref={form} onSubmit={(e) => submit(e, () => void save())}>
      <Card title={agent.backend === "codex" ? "Codex" : "Claude Code"}>
        <Row
          title={t("Enabled")}
          help={`${t(agent.installed ? "Installed" : "Not installed")}${agent.version ? ` · ${agent.version}` : ""}`}
        >
          <Toggle
            label={`${agent.backend} · ${t("Enabled")}`}
            value={enabled}
            onChange={setEnabled}
          />
        </Row>
        <PathField
          title={t("CLI executable (blank = auto-detect)")}
          value={path}
          kind="executable"
          onChange={setPath}
          t={t}
        />
        {agent.resolved_path && (
          <Row title={t("Resolved executable")} help={agent.resolved_path} />
        )}
        {agent.error && (
          <Row title={t("Status")} help={knownMessage(agent.error, t)} />
        )}
        <details className="settings-models">
          <summary>
            {t("{count} models · checked {time}", {
              count: agent.catalog.models?.length ?? 0,
              time: agent.checked_at
                ? new Date(agent.checked_at).toLocaleTimeString()
                : "—",
            })}
          </summary>
          {agent.catalog.models?.map((model) => (
            <div key={model.model}>
              <span>{model.displayName || model.model}</span>
              <small>
                {model.supportedReasoningEfforts
                  ?.map((effort) => effort.reasoningEffort)
                  .join(" / ")}
              </small>
            </div>
          ))}
        </details>
      </Card>
      <button type="submit" hidden />
    </form>
  );
}
export function OnlinePage({ data, t }: PageProps) {
  const online = data.status.Online ?? [];
  return (
    <>
      <Card title={t("{count} people connected", { count: online.length })}>
        {online.length ? (
          online.map((person) => (
            <Row
              key={person.id}
              icon={<UserAvatar person={person} size={34} />}
              title={person.nickname}
              help={t("{count} connections", { count: person.devices })}
            >
              <span
                className={`settings-state${person.state === "online" ? " online" : ""}`}
              >
                <span className="settings-status-dot" />
                {t(person.state === "online" ? "Online" : "Away")}
              </span>
            </Row>
          ))
        ) : (
          <div className="settings-empty">
            <Radio size={25} />
            <p>{t("No one is connected right now.")}</p>
          </div>
        )}
      </Card>
    </>
  );
}
export function AppearancePage({ data, run, t }: PageProps) {
  const save = (change: Partial<UIPreferences>) =>
    void run((latest) => ({
      kind: "save-appearance",
      preferences: { ...(latest ?? data).preferences, ...change },
    }));
  return (
    <>
      <Card title={t("Appearance and language")}>
        <Row
          title={t("Theme")}
          help={t("One appearance for chat and settings.")}
        >
          <ThemePicker
            value={data.preferences.theme}
            onChange={(theme) => save({ theme })}
            t={t}
          />
        </Row>
        <Row title={t("Language")}>
          <CustomSelect
            label={t("Language")}
            value={data.preferences.language}
            onChange={(value) =>
              save({ language: value as UIPreferences["language"] })
            }
            options={[
              { value: "system", label: t("Follow system") },
              ...languages.map(([value, label]) => ({ value, label })),
            ]}
          />
        </Row>
      </Card>
      <Card title={t("System notifications")}>
        <Row
          title={t("Enable notifications")}
          help={t(
            "Notify this desktop about messages and tasks for the account signed in to Chat, even when the window is hidden. Spool must keep running.",
          )}
        >
          <Toggle
            label={t("Enable notifications")}
            value={data.notifications_enabled === true}
            disabled={!data.notifications_supported}
            onChange={(enabled) =>
              void run({ kind: "save-notifications", enabled })
            }
          />
        </Row>
        <div className="settings-card-actions">
          <button
            type="button"
            disabled={
              !data.notifications_enabled || !data.notifications_supported
            }
            onClick={() => void run({ kind: "test-notification" })}
          >
            {t("Send test notification")}
          </button>
        </div>
      </Card>
      {!data.notifications_supported && (
        <Hint>
          {t(
            "System notifications are unavailable. On macOS, use the packaged desktop app.",
          )}
        </Hint>
      )}
    </>
  );
}
export function AboutPage({ data, run, t }: PageProps) {
  const autosave = useSettingsAutosave({ data, run }, ServiceSettingsAutosave);
  const draft = autosave.draft;
  const state = data.updates;
  const save = (change: Partial<UpdatePreferences>) =>
    void run((latest) => ({
      kind: "save-updates",
      update_preferences: {
        ...(latest ?? data).update_preferences,
        ...change,
      },
    }));
  const busy = state.Checking || state.Installing;
  const message = state.Checking
    ? t("Checking for updates...")
    : state.Installing
      ? t("Installing update. New chat responses are temporarily paused.")
      : state.Installed
        ? t(
            data.update_preferences.automatic_installs
              ? "Update installed. The app will restart automatically when no chats are running."
              : "Update installed. Restart when all chats have finished, or use it on the next launch.",
          )
        : state.Version
          ? t("Update {version} is available.", { version: state.Version })
          : state.LastCheck && !state.LastCheck.startsWith("0001")
            ? t("You are up to date.")
            : t("Check for updates");
  return (
    <>
      <Card title="Spool">
        <Row
          title={t("Current version: {version}", {
            version: data.version || "dev",
          })}
          help={
            state.Enabled
              ? message
              : t(
                  "Updates are unavailable in development builds or read-only installations. Install an official release in a writable location.",
                )
          }
        >
          <ShieldCheck size={22} />
        </Row>
        {state.Enabled && (
          <div className="settings-card-actions">
            <button
              type="button"
              disabled={busy || state.Installed}
              onClick={() => void run({ kind: "check-updates" })}
            >
              <RefreshCw size={15} />
              {t("Check for updates")}
            </button>
            {state.Version && !state.Installed && (
              <button
                type="button"
                className="settings-primary"
                disabled={busy}
                onClick={() => void run({ kind: "install-update" })}
              >
                {t("Install update")}
              </button>
            )}
            {state.Installed && (
              <button
                type="button"
                className="settings-primary"
                onClick={() => void run({ kind: "restart-update" })}
              >
                {t("Restart to update")}
              </button>
            )}
          </div>
        )}
        {state.Installing && state.Total > 0 && (
          <div className="settings-progress">
            <progress value={state.Downloaded} max={state.Total} />
            <span>{Math.round((state.Downloaded * 100) / state.Total)}%</span>
          </div>
        )}
      </Card>
      {state.Version && (
        <ReleaseNotesPanel
          key={`${data.version}/${state.Version}`}
          current={data.version}
          target={state.Version}
          t={t}
        />
      )}
      {state.Error && (
        <div className="settings-error" role="alert">
          {knownMessage(state.Error, t)}
          <Hint>
            {t(
              "Check your network and installation permissions, then retry. Signature failures are never bypassed.",
            )}
          </Hint>
        </div>
      )}
      {state.LastCheck && !state.LastCheck.startsWith("0001") && (
        <Hint>
          {t("Last checked: {time}", {
            time: new Date(state.LastCheck).toLocaleString(),
          })}
        </Hint>
      )}
      <Card title={t("Automatic updates")}>
        <Row title={t("Automatically check for updates")}>
          <Toggle
            label={t("Automatically check for updates")}
            value={data.update_preferences.automatic_checks}
            onChange={(automatic_checks) => save({ automatic_checks })}
          />
        </Row>
        <Row title={t("Check interval")}>
          <UpdateCheckInterval
            value={data.update_preferences.check_interval_minutes}
            disabled={!data.update_preferences.automatic_checks}
            onChange={(check_interval_minutes) =>
              save({ check_interval_minutes })
            }
            t={t}
          />
        </Row>
        <Row
          title={t(
            "Automatically install updates and restart when no chats are running",
          )}
        >
          <Toggle
            label={t(
              "Automatically install updates and restart when no chats are running",
            )}
            value={data.update_preferences.automatic_installs}
            onChange={(automatic_installs) => save({ automatic_installs })}
          />
        </Row>
      </Card>
      <Card title={t("Runtime")}>
        <TextField
          title={t("Release completed agents after idle minutes")}
          type="number"
          value={draft.idle_minutes}
          min={1}
          max={10080}
          onChange={(v) => autosave.edit("idle_minutes", v)}
          onCommit={() => void autosave.commit("idle_minutes")}
          required
        />
        <TextField
          title={t("History cache size (MiB)")}
          type="number"
          value={draft.history_cache_mib}
          min={1}
          onChange={(v) => autosave.edit("history_cache_mib", v)}
          onCommit={() => void autosave.commit("history_cache_mib")}
          help={t(
            "Shared by all chats. Changes are saved automatically and apply immediately. A larger session is kept whole; history is never truncated.",
          )}
          required
        />
      </Card>
      <Card title={t("Data & diagnostics")}>
        <Row title="SQLite" help={data.database}>
          <button
            type="button"
            onClick={() => void run({ kind: "show-file", target: "database" })}
          >
            <FolderOpen size={15} />
            {t("Show data directory")}
          </button>
        </Row>
        {data.application_log && (
          <Row title={t("Application log")} help={data.application_log}>
            <button
              type="button"
              onClick={() =>
                void run({ kind: "show-file", target: "application" })
              }
            >
              {t("Show application log")}
              <ArrowUpRight size={14} />
            </button>
          </Row>
        )}
        {data.crash_log && (
          <Row title={t("A previous crash report is available.")}>
            <button
              type="button"
              onClick={() => void run({ kind: "show-file", target: "crash" })}
            >
              {t("Show crash report")}
            </button>
          </Row>
        )}
        {data.update_log && (
          <Row title={t("Update log")} help={data.update_log}>
            <button
              type="button"
              onClick={() => void run({ kind: "show-file", target: "update" })}
            >
              {t("Show update log")}
              <ArrowUpRight size={14} />
            </button>
          </Row>
        )}
      </Card>
      <Card>
        <Row title={t("GitHub repository")} help="LarchLiu/spool">
          <a
            className="icon settings-repository-link"
            href="https://github.com/LarchLiu/spool"
            target="_blank"
            rel="noopener noreferrer"
            aria-label={t("Open GitHub repository")}
            title={t("Open GitHub repository")}
          >
            <Github size={21} aria-hidden="true" />
          </a>
        </Row>
      </Card>
    </>
  );
}
