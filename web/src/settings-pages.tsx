import { useEffect, useRef, useState } from "react";
import {
  ArrowUpRight,
  ChevronRight,
  FolderOpen,
  Github,
  Monitor,
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
import { ThemePicker } from "./theme-picker";
import { CustomSelect } from "./custom-select";
import { ServiceSettingsAutosave } from "./settings-autosave";
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
  Save,
  submit,
  knownMessage,
  type PageProps,
} from "./settings-ui";

export function NetworkPage({ data, run, t }: PageProps) {
  const [pending, setPending] = useState<boolean>();
  return (
    <>
      <Card title={t("Proxy")}>
        <Row
          title={t("Use system proxy")}
          help={t(
            "Use macOS system HTTP/HTTPS proxy settings for Spool's built-in network requests, including updates and Tailscale. Environment proxy variables take priority and remain active when this switch is off.",
          )}
        >
          <Toggle
            label={t("Use system proxy")}
            value={pending ?? data.settings.use_system_proxy ?? false}
            disabled={pending !== undefined}
            onChange={async (value) => {
              setPending(value);
              try {
                await run((latest) => ({
                  kind: "save-settings",
                  settings: {
                    ...(latest?.settings ?? data.settings),
                    use_system_proxy: value,
                  },
                }));
              } finally {
                setPending(undefined);
              }
            }}
          />
        </Row>
        <Hint>
          {t(
            "New built-in HTTP requests use this setting immediately. Restart the service to reconnect Tailscale. Local loopback access, push security checks and external agent tools keep their own network rules.",
          )}
        </Hint>
      </Card>
      {data.status.RestartRequired && (
        <div className="settings-notice" role="status">
          <RefreshCw size={18} />
          <div>
            <p>
              {t(
                "Restarting the service applies pending connection changes and interrupts active tasks.",
              )}
            </p>
            <button
              type="button"
              onClick={() => void run({ kind: "restart-service" })}
            >
              {t("Restart service")}
            </button>
          </div>
        </div>
      )}
    </>
  );
}

export function ServicePage({ data, run, t }: PageProps) {
  const runRef = useRef(run);
  runRef.current = run;
  const [, redraw] = useState(0);
  const [autosave] = useState(
    () =>
      new ServiceSettingsAutosave(
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
  const draft = autosave.draft;
  const [authKey, setAuthKey] = useState("");
  const [requestingLogin, setRequestingLogin] = useState(false);
  const [loginError, setLoginError] = useState("");
  const loginAttempt = useRef(0);
  const [registeringKey, setRegisteringKey] = useState(false);
  const [registrationError, setRegistrationError] = useState("");
  const [authRegistered, setAuthRegistered] = useState(false);
  const tailnet = data.status.TSNet;
  useEffect(() => {
    if (tailnet?.State === "Running") setAuthRegistered(false);
  }, [tailnet?.State]);
  const canLogin =
    !registeringKey &&
    !authRegistered &&
    (tailnet?.State === "NeedsLogin" ||
      tailnet?.State === "NoState" ||
      Boolean(tailnet?.AuthURL));
  const tailnetState =
    tailnet?.State === "Running"
      ? t("Connected to tailnet")
      : tailnet?.State === "Registered"
        ? t("Registered. Restart the service to activate Tailscale HTTPS.")
        : tailnet?.State === "Registering"
          ? t("Registering with Auth Key...")
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
      <Card title={t("Web service")}>
        <Row
          title={t(data.status.Running ? "Running" : "Stopped")}
          help={
            data.status.Address || `${data.settings.host}:${data.settings.port}`
          }
        >
          <span
            className={`settings-state${data.status.Running ? " online" : ""}`}
          >
            <span className="settings-status-dot" />
            {t(data.status.Running ? "Running" : "Stopped")}
          </span>
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
          <span className="settings-action-spacer" />
          <button type="button" onClick={() => void run({ kind: "open-chat" })}>
            {t("Open Chat")}
            <ChevronRight size={14} />
          </button>
          <button
            type="button"
            disabled={!data.status.Running}
            onClick={() => void run({ kind: "open-browser" })}
          >
            {t("Open in Browser")}
            <ArrowUpRight size={14} />
          </button>
        </div>
      </Card>
      {data.status.Error && (
        <div className="settings-error">
          {knownMessage(data.status.Error, t)}
        </div>
      )}
      {data.status.RestartRequired && (
        <div className="settings-notice" role="status">
          <RefreshCw size={18} />
          <div>
            <p>
              {t(
                "Settings saved. Restart the service to apply connection and idle changes. Restarting interrupts active tasks.",
              )}
            </p>
            <button
              type="button"
              data-restart-service
              onClick={() => void run({ kind: "restart-service" })}
            >
              {t("Restart service")}
            </button>
          </div>
        </div>
      )}
      <Card title={t("Connection")}>
        <TextField
          title={t("Listen IP (127.0.0.1 / 0.0.0.0)")}
          value={draft.host}
          onChange={(v) => autosave.edit("host", v)}
          onCommit={() => void autosave.commit("host")}
          required
        />
        <TextField
          title={t("Web port")}
          type="number"
          value={draft.port}
          min={1}
          max={65535}
          onChange={(v) => autosave.edit("port", v)}
          onCommit={() => void autosave.commit("port")}
          required
        />
        <TextField
          title={t("Public origin (HTTPS reverse proxy for remote PWA)")}
          value={draft.public_url}
          help={t(
            "This reverse-proxy origin stays independent of the Tailscale HTTPS address.",
          )}
          onChange={(v) => autosave.edit("public_url", v)}
          onCommit={() => void autosave.commit("public_url")}
        />
      </Card>
      <Card title="Tailscale (tsnet)">
        <Row
          title={t("Enable embedded Tailscale")}
          help={t(
            "Connect this workspace to your tailnet without a system VPN or a separate Serve proxy.",
          )}
        >
          <Toggle
            label={t("Enable embedded Tailscale")}
            value={draft.tsnet_enabled}
            onChange={(v) => {
              autosave.edit("tsnet_enabled", v);
              void autosave.commit("tsnet_enabled");
            }}
          />
        </Row>
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
        <TextField
          title={t("Tailscale Auth Key (optional)")}
          type="password"
          value={authKey}
          disabled={registeringKey}
          onChange={setAuthKey}
          help={t(
            "Click Use Auth Key to register now without stopping the current service. After success, restart the service to activate the saved identity. The key is never saved in settings.",
          )}
        />
        <div className="settings-card-actions">
          <button
            type="button"
            disabled={registeringKey || !authKey.trim() || !draft.tsnet_enabled}
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
              registeringKey ? "Registering with Auth Key..." : "Use Auth Key",
            )}
          </button>
        </div>
        {registrationError && (
          <div className="settings-error" role="alert">
            {knownMessage(registrationError, t)}
          </div>
        )}
        {(authRegistered || tailnet?.State === "Registered") && (
          <div className="settings-notice" role="status">
            <div>
              <p>
                {t(
                  "Registered. Restart the service to activate Tailscale HTTPS.",
                )}
              </p>
              {!data.status.RestartRequired && (
                <button
                  type="button"
                  onClick={() => void run({ kind: "restart-service" })}
                >
                  {t("Restart service")}
                </button>
              )}
            </div>
          </div>
        )}
        <Row title={t("Tailnet status")} help={tailnetState}>
          {canLogin && (
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
          )}
        </Row>
        {loginError && (
          <div className="settings-error" role="alert">
            {knownMessage(loginError, t)}
          </div>
        )}
        {canLogin && !tailnet?.AuthURL && (
          <Hint>
            {t(
              "This node is not signed in yet. Click Sign in to request its login link. If no link is available, check network access to Tailscale.",
            )}
          </Hint>
        )}
        {tailnet?.URL && (
          <Row
            title={t("Tailnet HTTPS address")}
            help={<code>{tailnet.URL}</code>}
          >
            <CopyAddressButton t={t} />
          </Row>
        )}
        {tailnet?.Error && (
          <div className="settings-error" role="alert">
            <p>{tailnet.Error}</p>
            <button
              type="button"
              onClick={() => void run({ kind: "restart-service" })}
            >
              {t("Restart service")}
            </button>
          </div>
        )}
      </Card>
      <Hint>
        {t(
          "Enable MagicDNS and HTTPS certificates in the Tailscale admin DNS settings. Spool login is still required. The existing Android APK only needs this HTTPS address.",
        )}
      </Hint>
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
        <Row title={t("Start Web service on application launch")}>
          <Toggle
            label={t("Start Web service on application launch")}
            value={draft.auto_start}
            onChange={(v) => {
              autosave.edit("auto_start", v);
              void autosave.commit("auto_start");
            }}
          />
        </Row>
      </Card>
      <Hint>
        {t(
          "Changes are saved on blur or Enter; switches save immediately. The service never restarts automatically. Closing this window keeps it in the tray; Quit stops it.",
        )}
      </Hint>
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
      </Card>
      <Hint>
        {t(
          "Users can execute agent tools in granted projects. Only grant access to trusted people. HTTPS is required for remote PWA installation.",
        )}
      </Hint>
    </>
  );
}

export function PeoplePage({ data, run, t }: PageProps) {
  const [draft, setDraft] = useState<PersonDraft>(newPerson);
  const [editing, setEditing] = useState(false);
  const save = async () => {
    const next = await run({ kind: "save-person", person: draft });
    if (next?.saved_id)
      setDraft((value) => ({ ...value, id: next.saved_id!, password: "" }));
  };
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
          icon: <UserAvatar person={person} size={34} />,
        }))}
        onSelect={(id) => {
          const person = data.users.find((person) => person.id === id);
          if (person) {
            setDraft({ ...person, password: "" });
            setEditing(true);
          }
        }}
        action={
          <button
            type="button"
            className="settings-primary"
            data-new-entity
            onClick={() => {
              setDraft(newPerson());
              setEditing(true);
            }}
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
      <form onSubmit={(e) => submit(e, () => void save())}>
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
        </Card>
        <Save title={t("Save person")} />
      </form>
      <Hint>
        {t(
          "Passwords are bcrypt-hashed in SQLite. Editing an account revokes its Web logins. Disabling keeps its chat history.",
        )}
      </Hint>
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
      <Hint>
        {props.t(
          "Discovery runs at launch and every 5 minutes, without submitting a model prompt. Failed discovery retains the last successful catalog.",
        )}
      </Hint>
    </>
  );
}
function AgentCard({ agent, run, t }: PageProps & { agent: ManagedAgent }) {
  const [path, setPath] = useState(agent.executable);
  const [enabled, setEnabled] = useState(agent.enabled);
  return (
    <form
      onSubmit={(e) =>
        submit(
          e,
          () =>
            void run({
              kind: "save-agent",
              backend: agent.backend,
              enabled,
              path,
            }),
        )
      }
    >
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
      <Save
        title={t("Save {agent}", {
          agent: agent.backend === "codex" ? "Codex" : "Claude Code",
        })}
      />
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
      <Hint>
        {t(
          "Heartbeat timeout: 90 seconds. A backgrounded mobile PWA may appear away or offline; its agent task continues.",
        )}
      </Hint>
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
      <div className="settings-info">
        <Monitor size={18} />
        <p>{t("Changes apply immediately and are saved in SQLite.")}</p>
      </div>
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
      <Hint>
        {t(
          "Automatic notifications are skipped while the desktop window has focus and are not replayed later. Test notifications still appear.",
        )}
      </Hint>
      <Hint>
        {t(
          "Notifications show sender and group names, but never message contents.",
        )}
      </Hint>
      <Hint>
        {t(
          "Private and group messages are live only and are not replayed after closing or refreshing the page.",
        )}
      </Hint>
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
            "Update installed. Restart when all chats have finished, or use it on the next launch.",
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
          title={t("Automatically install updates when no chats are running")}
        >
          <Toggle
            label={t("Automatically install updates when no chats are running")}
            value={data.update_preferences.automatic_installs}
            onChange={(automatic_installs) => save({ automatic_installs })}
          />
        </Row>
      </Card>
      <Hint>
        {t(
          "Automatic updates never restart the app. Busy updates are retried after chats finish.",
        )}
      </Hint>
      <Hint>
        {t(
          "Updates are signed and downloaded from GitHub Releases. Running chats always block installation and restart.",
        )}
      </Hint>
      {data.update_log && (
        <Card>
          <Row title={t("Update log: {path}", { path: data.update_log })}>
            <button
              type="button"
              onClick={() => void run({ kind: "show-file", target: "update" })}
            >
              {t("Show update log")}
              <ArrowUpRight size={14} />
            </button>
          </Row>
        </Card>
      )}
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
