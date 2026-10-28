import { useState } from "react";
import {
  ArrowUpRight,
  ChevronRight,
  FolderOpen,
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
  type PersonDraft,
  type ManagedAgent,
  type Backend,
} from "./settings-client";
import { SettingsBack, SettingsList } from "./settings-list";
import {
  Card,
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

export function ServicePage({ data, run, t }: PageProps) {
  const [draft, setDraft] = useState(() => ({ ...data.settings }));
  const field = <K extends keyof typeof draft>(
    key: K,
    value: (typeof draft)[K],
  ) => setDraft((old) => ({ ...old, [key]: value }));
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
      <form
        onSubmit={(e) =>
          submit(e, () => void run({ kind: "save-settings", settings: draft }))
        }
      >
        <Card title={t("Connection")}>
          <TextField
            title={t("Listen IP (127.0.0.1 / 0.0.0.0)")}
            value={draft.host}
            onChange={(v) => field("host", v)}
            required
          />
          <TextField
            title={t("Web port")}
            type="number"
            value={draft.port}
            min={1}
            max={65535}
            onChange={(v) => field("port", Number(v))}
            required
          />
          <TextField
            title={t("Public origin (HTTPS reverse proxy for remote PWA)")}
            value={draft.public_url}
            onChange={(v) => field("public_url", v)}
          />
        </Card>
        <Card title={t("Runtime")}>
          <TextField
            title={t("Release completed agents after idle minutes")}
            type="number"
            value={draft.idle_minutes}
            min={1}
            max={10080}
            onChange={(v) => field("idle_minutes", Number(v))}
            required
          />
          <TextField
            title={t("History cache size (MiB)")}
            type="number"
            value={draft.history_cache_mib}
            min={1}
            onChange={(v) => field("history_cache_mib", Number(v))}
            help={t(
              "Shared by all chats. Applies immediately after saving. A larger session is kept whole; history is never truncated.",
            )}
            required
          />
          <Row title={t("Start Web service on application launch")}>
            <Toggle
              label={t("Start Web service on application launch")}
              value={draft.auto_start}
              onChange={(v) => field("auto_start", v)}
            />
          </Row>
        </Card>
        <Hint>
          {t(
            "Listen address and idle settings apply after Stop → Start. Stopping interrupts active tasks. Closing this window keeps the server in the tray; Quit stops it.",
          )}
        </Hint>
        <Save title={t("Save settings")} />
      </form>
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
          status: t(person.disabled ? "Disabled" : "Enabled"),
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
    void run({
      kind: "save-appearance",
      preferences: { ...data.preferences, ...change },
    });
  return (
    <>
      <Card title={t("Appearance and language")}>
        <Row
          title={t("Theme")}
          help={t("One appearance for chat and settings.")}
        >
          <select
            aria-label={t("Theme")}
            value={data.preferences.theme}
            onChange={(e) =>
              save({ theme: e.target.value as UIPreferences["theme"] })
            }
          >
            <option value="system">{t("Follow system")}</option>
            <option value="light">{t("Light")}</option>
            <option value="dark">{t("Dark")}</option>
          </select>
        </Row>
        <Row title={t("Language")}>
          <select
            aria-label={t("Language")}
            value={data.preferences.language}
            onChange={(e) =>
              save({ language: e.target.value as UIPreferences["language"] })
            }
          >
            <option value="system">{t("Follow system")}</option>
            {languages.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </Row>
      </Card>
      <div className="settings-info">
        <Monitor size={18} />
        <p>{t("Changes apply immediately and are saved in SQLite.")}</p>
      </div>
    </>
  );
}
export function UpdatesPage({ data, run, t }: PageProps) {
  const state = data.updates;
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
            onChange={(automatic_checks) =>
              void run({
                kind: "save-updates",
                update_preferences: {
                  ...data.update_preferences,
                  automatic_checks,
                },
              })
            }
          />
        </Row>
        <Row
          title={t("Automatically install updates when no chats are running")}
        >
          <Toggle
            label={t("Automatically install updates when no chats are running")}
            value={data.update_preferences.automatic_installs}
            onChange={(automatic_installs) =>
              void run({
                kind: "save-updates",
                update_preferences: {
                  ...data.update_preferences,
                  automatic_installs,
                },
              })
            }
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
      {state.Notes && (
        <Card title={t("Release notes")}>
          <div className="settings-release-notes">{state.Notes}</div>
        </Card>
      )}
    </>
  );
}
