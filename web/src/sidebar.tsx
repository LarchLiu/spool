import { useEffect, useMemo, useState } from "react";
import { Folder, FolderOpen, LogOut, Plus, Settings2, X } from "lucide-react";
import type { Translator } from "./i18n";
import type { Backend, Chat, Project, User } from "./types";
import { AgentIcon } from "./agent-icon";

type Props = {
  user: User;
  projects: Project[];
  chats: Chat[];
  project: string;
  chatID: string;
  activeBackend?: Backend;
  open: boolean;
  posting: boolean;
  t: Translator;
  onClose: () => void;
  onChat: (chat: Chat) => void;
  onNew: (project?: string) => void;
  onSettings: () => void;
  onLogout: () => void;
};

export function Sidebar(props: Props) {
  const { projects, chats, project, chatID, t } = props;
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [projectLimit, setProjectLimit] = useState(5);
  const [recentLimit, setRecentLimit] = useState(8);
  const [chatLimits, setChatLimits] = useState<Record<string, number>>({});
  const byProject = useMemo(() => {
    const groups = new Map<string, Chat[]>();
    for (const chat of chats) {
      const list = groups.get(chat.project_id) || [];
      list.push(chat);
      groups.set(chat.project_id, list);
    }
    for (const list of groups.values())
      list.sort((a, b) => b.updated_at - a.updated_at);
    return groups;
  }, [chats]);
  const recents = useMemo(
    () => [...chats].sort((a, b) => b.updated_at - a.updated_at),
    [chats],
  );
  useEffect(() => {
    if (!project) return;
    setExpanded((old) => new Set([...old, project]));
    const index = projects.findIndex((p) => p.id === project);
    setProjectLimit((old) => Math.max(old, index + 1));
    const chatIndex = (byProject.get(project) || []).findIndex(
      (chat) => chat.id === chatID,
    );
    setChatLimits((old) => ({
      ...old,
      [project]: Math.max(old[project] || 5, chatIndex + 1),
    }));
    // Metadata refreshes must not reopen a project the user just collapsed.
  }, [project, chatID]);
  function chatButton(chat: Chat, nested: boolean) {
    return (
      <button
        key={chat.id}
        className={
          "nav-chat " +
          (nested ? "nested " : "") +
          (chat.id === chatID ? "active" : "")
        }
        aria-current={chat.id === chatID ? "page" : undefined}
        title={chat.title === "New chat" ? t("New chat") : chat.title}
        onClick={() => {
          setExpanded((old) => new Set([...old, chat.project_id]));
          props.onChat(chat);
        }}
      >
        <AgentIcon
          backend={
            chat.id === chatID && props.activeBackend
              ? props.activeBackend
              : chat.backend
          }
        />
        <span className="nav-chat-title">
          {chat.title === "New chat" ? t("New chat") : chat.title}
        </span>
        {chat.status === "running" && (
          <span
            className="task-spinner"
            role="status"
            aria-label={t("Working")}
          />
        )}
      </button>
    );
  }
  return (
    <aside className={"sidebar " + (props.open ? "open" : "")}>
      <div className="brand">
        <img className="brand-mark" src="/logo.png" alt="" />
        Spool
        <button
          className="icon mobile"
          onClick={props.onClose}
          aria-label={t("Close sidebar")}
        >
          <X size={18} />
        </button>
      </div>
      <nav className="sidebar-scroll" aria-label={t("Workspace")}>
        <section className="nav-section" aria-labelledby="projects-label">
          <h2 id="projects-label">{t("Projects")}</h2>
          {projects.slice(0, projectLimit).map((p) => {
            const isOpen = expanded.has(p.id);
            const list = byProject.get(p.id) || [];
            const limit = chatLimits[p.id] || 5;
            return (
              <div className="project-group" key={p.id}>
                <div
                  className={
                    "project-row " + (p.id === project ? "current" : "")
                  }
                >
                  <button
                    type="button"
                    className="project-toggle"
                    aria-expanded={isOpen}
                    aria-controls={"project-chats-" + p.id}
                    title={p.name}
                    onClick={() => {
                      setExpanded((old) => {
                        const next = new Set(old);
                        if (next.has(p.id)) next.delete(p.id);
                        else next.add(p.id);
                        return next;
                      });
                    }}
                  >
                    {isOpen ? <FolderOpen size={15} /> : <Folder size={15} />}
                    <span>{p.name}</span>
                  </button>
                  <button
                    type="button"
                    className="project-new icon"
                    disabled={props.posting}
                    title={t("New chat in {project}", { project: p.name })}
                    aria-label={t("New chat in {project}", { project: p.name })}
                    onClick={() => props.onNew(p.id)}
                  >
                    <Plus size={14} />
                  </button>
                </div>
                {isOpen && (
                  <div id={"project-chats-" + p.id} className="project-chats">
                    {list.slice(0, limit).map((chat) => chatButton(chat, true))}
                    {!list.length && (
                      <p className="nav-empty nested">
                        {t("No conversations yet.")}
                      </p>
                    )}
                    {list.length > limit && (
                      <button
                        className="show-more nested"
                        onClick={() =>
                          setChatLimits((old) => ({
                            ...old,
                            [p.id]: limit + 5,
                          }))
                        }
                      >
                        {t("Show more")}
                      </button>
                    )}
                  </div>
                )}
              </div>
            );
          })}
          {!projects.length && (
            <p className="nav-empty">{t("No authorized projects")}</p>
          )}
          {projects.length > projectLimit && (
            <button
              className="show-more"
              onClick={() => setProjectLimit((old) => old + 5)}
            >
              {t("Show more")}
            </button>
          )}
        </section>
        <section
          className="nav-section recents"
          aria-labelledby="recents-label"
        >
          <h2 id="recents-label">{t("Recents")}</h2>
          {recents.slice(0, recentLimit).map((chat) => chatButton(chat, false))}
          {!recents.length && (
            <p className="nav-empty">{t("No conversations yet.")}</p>
          )}
          {recents.length > recentLimit && (
            <button
              className="show-more"
              onClick={() => setRecentLimit((old) => old + 8)}
            >
              {t("Show more")}
            </button>
          )}
        </section>
      </nav>
      <div className="side-foot">
        <span className="avatar">{props.user.nickname.slice(0, 1)}</span>
        <div>
          {props.user.nickname}
          <small>{t("Project chat visibility follows project settings")}</small>
        </div>
        <button
          className="icon"
          onClick={props.onSettings}
          aria-label={t("Appearance and language")}
          title={t("Settings")}
        >
          <Settings2 size={16} />
        </button>
        <button
          className="icon"
          onClick={props.onLogout}
          aria-label={t("Sign out")}
        >
          <LogOut size={16} />
        </button>
      </div>
    </aside>
  );
}
