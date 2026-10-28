import { useCallback, useEffect, useRef, useState } from "react";
import {
  ArrowLeft,
  Check,
  ChevronRight,
  FolderOpen,
  Radio,
  RefreshCw,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  Users,
  Workflow,
} from "lucide-react";
import type { MessageKey } from "./i18n";
import { defaultPreferences } from "./preferences";
import { usePresentation } from "./use-presentation";
import {
  hasSettingsBridge,
  settingsCall,
  settingsSections,
  subscribeSettingsSection,
  type SettingsSection,
  type SettingsSnapshot,
} from "./settings-client";
import { Hint, knownMessage, type Run } from "./settings-ui";
import {
  ServicePage,
  PeoplePage,
  AgentsPage,
  OnlinePage,
  AppearancePage,
  UpdatesPage,
} from "./settings-pages";
import { ProjectsPage } from "./settings-projects";

const icons = {
  Service: Server,
  People: Users,
  Projects: FolderOpen,
  Agents: Workflow,
  Online: Radio,
  Appearance: SlidersHorizontal,
  Updates: RefreshCw,
};
const descriptions: Record<SettingsSection, MessageKey> = {
  Service: "Manage your local server and workspace access.",
  People:
    "Accounts are created here. Web clients cannot register or administer users.",
  Projects: "Local directories, project access and scheduled tasks.",
  Agents: "Your local coding tools, connected to Spool.",
  Online: "See who is connected to your workspace.",
  Appearance: "Make this workspace feel like yours.",
  Updates: "Keep Spool up to date, on your terms.",
};
export function SettingsApp() {
  const { t } = usePresentation(defaultPreferences);
  const [data, setData] = useState<SettingsSnapshot>();
  const [section, setSection] = useState<SettingsSection>("Service");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const operation = useRef(false);
  const revision = useRef(0);
  const content = useRef<HTMLElement>(null);
  const available = hasSettingsBridge();
  const refresh = useCallback(async (initial = false) => {
    const generation = revision.current;
    if (operation.current) return;
    try {
      const next = await settingsCall<SettingsSnapshot>("Snapshot");
      if (operation.current || generation !== revision.current) return;
      setData(next);
      if (initial && settingsSections.includes(next.section as SettingsSection))
        setSection(next.section as SettingsSection);
    } catch (e) {
      if (initial) setError(String(e instanceof Error ? e.message : e));
    }
  }, []);
  useEffect(() => {
    content.current?.scrollTo(0, 0);
  }, [section]);
  useEffect(() => {
    if (!available) return;
    void refresh(true);
    const timer = setInterval(() => void refresh(), 3000);
    const unsubscribe = subscribeSettingsSection((next) => {
      setSection(next);
      setSaved(false);
      void refresh();
    });
    return () => {
      clearInterval(timer);
      unsubscribe();
    };
  }, [available, refresh]);
  useEffect(() => {
    if (!saved) return;
    const timer = setTimeout(() => setSaved(false), 2500);
    return () => clearTimeout(timer);
  }, [saved]);
  const run: Run = async (command) => {
    if (operation.current) return;
    operation.current = true;
    revision.current++;
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      const next = await settingsCall<SettingsSnapshot>("Execute", command);
      setData(next);
      if (command.kind.startsWith("save-")) setSaved(true);
      return next;
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
    } finally {
      operation.current = false;
      setBusy(false);
    }
  };
  const navigate = (next: SettingsSection) => {
    if (operation.current) return;
    setSection(next);
    setSaved(false);
    setError("");
    void run({ kind: "navigate", section: next });
  };
  const props = data ? { data, run, t } : undefined;
  return (
    <div className="desktop-settings-shell">
      <aside className="settings-sidebar">
        <div className="settings-sidebar-content">
          <h1>{t("Settings")}</h1>
          <nav aria-label={t("Settings")}>
            {[settingsSections.slice(0, 5), settingsSections.slice(5)].map(
              (group, index) => (
                <div className="settings-nav-group" key={index}>
                  <p>{t(index ? "Application" : "Workspace")}</p>
                  {group.map((key) => {
                    const Icon = icons[key];
                    return (
                      <button
                        key={key}
                        type="button"
                        className={section === key ? "selected" : ""}
                        aria-current={section === key ? "page" : undefined}
                        disabled={busy}
                        onClick={() => navigate(key)}
                        data-section={key}
                      >
                        <Icon size={17} />
                        <span>{t(key)}</span>
                        {section === key && <ChevronRight size={14} />}
                      </button>
                    );
                  })}
                </div>
              ),
            )}
          </nav>
        </div>
        <div className="settings-sidebar-bottom">
          <button
            type="button"
            className="icon"
            aria-label={t("Back to Chat")}
            title={t("Back to Chat")}
            disabled={busy || !data}
            onClick={() => void run({ kind: "open-chat" })}
          >
            <ArrowLeft size={18} />
          </button>
          <span className="settings-sidebar-status" role="status">
            <span
              className={`settings-status-dot${data?.status.Running ? " running" : ""}`}
            />
            {t(data?.status.Running ? "Running" : "Stopped")}
          </span>
        </div>
      </aside>
      <main ref={content} className="settings-main" id="settings-content">
        <div className="settings-page">
          <header className="settings-page-header">
            <h1>{t(section)}</h1>
            <p>{t(descriptions[section])}</p>
          </header>
          {data?.message && (
            <div role="alert" className="settings-error">
              {knownMessage(data.message, t)}
            </div>
          )}
          {!available ? (
            <div className="settings-notice">
              <ShieldCheck size={20} />
              <p>
                {t(
                  "Desktop settings are only available in the Spool desktop app.",
                )}
              </p>
            </div>
          ) : (
            <>
              {error && (
                <div role="alert" className="settings-error">
                  {knownMessage(error, t)}
                  {!data && (
                    <button type="button" onClick={() => void refresh(true)}>
                      {t("Retry")}
                    </button>
                  )}
                </div>
              )}
              {!props ? (
                <Hint>{t("Working…")}</Hint>
              ) : (
                <fieldset
                  className="settings-content"
                  disabled={busy}
                  aria-busy={busy}
                >
                  <div data-page="Service" hidden={section !== "Service"}>
                    <ServicePage {...props} />
                  </div>
                  <div data-page="People" hidden={section !== "People"}>
                    <PeoplePage {...props} />
                  </div>
                  <div data-page="Projects" hidden={section !== "Projects"}>
                    <ProjectsPage {...props} />
                  </div>
                  <div data-page="Agents" hidden={section !== "Agents"}>
                    <AgentsPage {...props} />
                  </div>
                  <div data-page="Online" hidden={section !== "Online"}>
                    <OnlinePage {...props} />
                  </div>
                  <div data-page="Appearance" hidden={section !== "Appearance"}>
                    <AppearancePage {...props} />
                  </div>
                  <div data-page="Updates" hidden={section !== "Updates"}>
                    <UpdatesPage {...props} />
                  </div>
                </fieldset>
              )}
            </>
          )}
          <div
            role="status"
            className={`settings-save-status${saved || busy ? " visible" : ""}`}
          >
            {busy ? (
              <>
                <RefreshCw size={14} className="settings-spinning" />
                {t("Working…")}
              </>
            ) : (
              <>
                <Check size={14} />
                {t("Saved")}
              </>
            )}
          </div>
        </div>
      </main>
    </div>
  );
}
