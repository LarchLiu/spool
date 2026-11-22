import { appURL } from "./app-path";
import { useEffect, useMemo, useState } from "react";
import {
  Archive,
  Folder,
  FolderOpen,
  LogOut,
  PanelLeftClose,
  Plus,
  Pin,
  PinOff,
  X,
} from "lucide-react";
import type { Translator } from "./i18n";
import type { Backend, Chat, Project, User } from "./types";
import type { AvatarIdentity } from "./avatar";
import { AgentIcon } from "./agent-icon";
import { UserAvatar } from "./user-avatar";
import { ChatOperatorAvatars } from "./turn-identity";
import { PanelResizeHandle } from "./panel-resize-handle";
import { ChatSettingsControl } from "./desktop-settings";
import { DesktopServerAction, DesktopServerTabs } from "./desktop-servers";
import { ADBShareControl } from "./adb-share-settings";
import { RemoteDesktopControl } from "./remote-desktop-settings";
import {
  isDesktopChat,
  isMacDesktop,
  type DesktopServerStatus,
} from "./runtime";
import { canControlChat } from "./chat-state";
import { ChatActionMenu } from "./chat-title-menu";
import {
  sortedSidebarChats,
  revealSidebarProject,
  revealSidebarChat,
  pinnedSidebarChats,
  type SidebarPreferences,
  type SidebarSection as Section,
} from "./sidebar-state";
import { SidebarSection } from "./sidebar-section";
import { INITIAL_CHAT_LIMIT, SidebarChatList } from "./sidebar-chat-list";
import { SessionNotificationIcon } from "./session-notification-icon";
import type { SessionNotifications } from "./use-session-notifications";

type Props = {
  user: User | null;
  account?: User | null;
  serverLabel?: string;
  serverName: string;
  projects: Project[];
  chats: Chat[];
  notifications: SessionNotifications;
  pinned: string[];
  collapsed: SidebarPreferences["collapsed"];
  onToggleSection: (section: Section) => void;
  resolveProfile: (id: string) => AvatarIdentity;
  project: string;
  chatID: string;
  activeBackend?: Backend;
  open: boolean;
  active: boolean;
  posting: boolean;
  width: number;
  t: Translator;
  onClose: () => void;
  onResize: (width: number) => void;
  onChat: (chat: Chat) => void;
  onTogglePin: (id: string) => void;
  onRename: (chat: Chat, title: string) => Promise<void>;
  onArchive: (chat: Chat) => Promise<void>;
  onDelete: (chat: Chat) => Promise<void>;
  onError: (error: unknown) => void;
  onNew: (project?: string) => void;
  onSettings: () => void;
  onRemoteConnect: () => void;
  remoteConnected: boolean;
  onAvatar: () => void;
  onLogout: () => void;
  connectionState?: DesktopServerStatus;
  onConnect?: () => void;
};

export function Sidebar(props: Props) {
  const { projects, chats, project, chatID, t } = props;
  const identity = props.user || props.account;
  const authenticated =
    !!props.user && (!isDesktopChat() || props.connectionState === "connected");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [projectLimit, setProjectLimit] = useState(5);
  const [recentLimit, setRecentLimit] = useState(INITIAL_CHAT_LIMIT);
  const [chatLimits, setChatLimits] = useState<Record<string, number>>({});
  const byProject = useMemo(() => {
    const groups = new Map<string, Chat[]>();
    for (const chat of chats) {
      const list = groups.get(chat.project_id) || [];
      list.push(chat);
      groups.set(chat.project_id, list);
    }
    for (const [id, list] of groups) groups.set(id, sortedSidebarChats(list));
    return groups;
  }, [chats]);
  const pinned = useMemo(
    () => pinnedSidebarChats(chats, props.pinned),
    [chats, props.pinned],
  );
  const recents = useMemo(() => sortedSidebarChats(chats), [chats]);
  useEffect(() => {
    if (!project) return;
    setExpanded((old) => new Set([...old, project]));
    setProjectLimit((old) => revealSidebarProject(projects, project, old));
    setChatLimits((old) => ({
      ...old,
      [project]: revealSidebarChat(
        byProject.get(project) || [],
        chatID,
        old[project] || INITIAL_CHAT_LIMIT,
      ),
    }));
    // Metadata refreshes must not reopen a project the user just collapsed.
  }, [project, chatID]);
  function chatButton(chat: Chat, nested: boolean) {
    const title = chat.title === "New chat" ? t("New chat") : chat.title;
    const pinned = props.pinned.includes(chat.id);
    const canManage = canControlChat(chat, props.user?.id || "");
    return (
      <ChatActionMenu
        key={chat.id}
        title={title}
        tooltipSide="right"
        t={t}
        pinned={pinned}
        canManage={canManage}
        onTogglePin={() => props.onTogglePin(chat.id)}
        onRename={(title) => props.onRename(chat, title)}
        onArchive={() => props.onArchive(chat)}
        onDelete={() => props.onDelete(chat)}
        onError={props.onError}
      >
        {({ trigger, id, open, archiving, show, archive, tooltipProps }) => (
          <div
            className={
              "nav-chat " +
              (nested ? "nested " : "") +
              (chat.id === chatID ? "active " : "") +
              (open ? "menu-open" : "")
            }
            onContextMenu={(event) => {
              event.preventDefault();
              show({ left: event.clientX, top: event.clientY });
            }}
            onPointerDown={(event) => {
              if (event.button === 2) event.preventDefault();
            }}
          >
            {!!chat.participants?.length && (
              <ChatOperatorAvatars chat={chat} resolve={props.resolveProfile} />
            )}
            <button
              {...tooltipProps}
              ref={trigger}
              type="button"
              className="nav-chat-open"
              aria-current={chat.id === chatID ? "page" : undefined}
              aria-haspopup="menu"
              aria-expanded={open}
              aria-controls={open ? id : undefined}
              onKeyDown={(event) => {
                if (
                  event.key === "ContextMenu" ||
                  (event.shiftKey && event.key === "F10")
                ) {
                  event.preventDefault();
                  show();
                }
              }}
              onClick={() => {
                setExpanded((old) => new Set([...old, chat.project_id]));
                props.onChat(chat);
              }}
            >
              {!chat.participants?.length && (
                <span className="nav-chat-agent">
                  <AgentIcon
                    backend={
                      chat.id === chatID && props.activeBackend
                        ? props.activeBackend
                        : chat.backend
                    }
                  />
                </span>
              )}
              <span className="nav-chat-title">{title}</span>
              {chat.status === "running" && (
                <span
                  className="task-spinner"
                  role="status"
                  aria-label={t("Working")}
                />
              )}
            </button>
            <div className="nav-chat-actions">
              <button
                type="button"
                className="icon"
                title={t(pinned ? "Unpin chat" : "Pin chat")}
                aria-label={t(pinned ? "Unpin chat" : "Pin chat")}
                disabled={archiving}
                onClick={() => props.onTogglePin(chat.id)}
              >
                {pinned ? <PinOff size={14} /> : <Pin size={14} />}
              </button>
              {canManage && (
                <button
                  type="button"
                  className="icon"
                  title={t("Archive")}
                  aria-label={t("Archive")}
                  disabled={archiving}
                  aria-busy={archiving}
                  onClick={archive}
                >
                  <Archive size={14} />
                </button>
              )}
            </div>
            <SessionNotificationIcon
              kind={props.notifications[chat.id]}
              t={t}
            />
          </div>
        )}
      </ChatActionMenu>
    );
  }
  return (
    <aside
      className={"sidebar " + (props.open ? "open" : "")}
      inert={!props.active}
    >
      <div className="brand">
        {!isMacDesktop() && (
          <>
            <img className="brand-mark" src={appURL("/logo.png")} alt="" />
            <span className="brand-name" title={`Spool · ${props.serverName}`}>
              Spool · {props.serverName}
            </span>
          </>
        )}
        <button
          className="icon sidebar-collapse"
          onClick={props.onClose}
          aria-label={t(props.open ? "Close sidebar" : "Collapse sidebar")}
          title={t(props.open ? "Close sidebar" : "Collapse sidebar")}
        >
          {props.open ? <X size={18} /> : <PanelLeftClose size={18} />}
        </button>
      </div>
      <DesktopServerTabs t={t} account={props.user?.id} />
      <nav className="sidebar-scroll" aria-label={t("Workspace")}>
        {!props.user ? (
          <p className="nav-empty">
            {t(
              props.connectionState === "login"
                ? "Sign in to view this server’s workspace."
                : "Connect to view this server’s workspace.",
            )}
          </p>
        ) : (
          <>
            <SidebarSection
              id="pinned"
              title={t("Pinned")}
              collapsed={props.collapsed.pinned}
              onToggle={() => props.onToggleSection("pinned")}
            >
              {pinned.map((chat) => chatButton(chat, false))}
              {!pinned.length && (
                <p className="nav-empty">{t("No pinned chats")}</p>
              )}
            </SidebarSection>
            <SidebarSection
              id="projects"
              title={t("Projects")}
              collapsed={props.collapsed.projects}
              onToggle={() => props.onToggleSection("projects")}
            >
              {projects.slice(0, projectLimit).map((p) => {
                const isOpen = expanded.has(p.id);
                const list = byProject.get(p.id) || [];
                const limit = chatLimits[p.id] || INITIAL_CHAT_LIMIT;
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
                          if (isOpen) {
                            setChatLimits((old) => ({
                              ...old,
                              [p.id]: INITIAL_CHAT_LIMIT,
                            }));
                          }
                          setExpanded((old) => {
                            const next = new Set(old);
                            if (next.has(p.id)) next.delete(p.id);
                            else next.add(p.id);
                            return next;
                          });
                        }}
                      >
                        {isOpen ? (
                          <FolderOpen size={15} />
                        ) : (
                          <Folder size={15} />
                        )}
                        <span>{p.name}</span>
                      </button>
                      <button
                        type="button"
                        className="project-new icon"
                        disabled={props.posting || p.can_create === false}
                        title={t("New chat in {project}", { project: p.name })}
                        aria-label={t("New chat in {project}", {
                          project: p.name,
                        })}
                        onClick={() => props.onNew(p.id)}
                      >
                        <Plus size={14} />
                      </button>
                    </div>
                    {isOpen && (
                      <div
                        id={"project-chats-" + p.id}
                        className="project-chats"
                      >
                        <SidebarChatList
                          chats={list}
                          limit={limit}
                          nested
                          t={t}
                          renderChat={(chat) => chatButton(chat, true)}
                          onLimit={(nextLimit) =>
                            setChatLimits((old) => ({
                              ...old,
                              [p.id]: nextLimit,
                            }))
                          }
                        />
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
            </SidebarSection>
            <SidebarSection
              id="recents"
              title={t("Recents")}
              collapsed={props.collapsed.recents}
              onToggle={() => props.onToggleSection("recents")}
            >
              <SidebarChatList
                chats={recents}
                limit={recentLimit}
                t={t}
                renderChat={(chat) => chatButton(chat, false)}
                onLimit={setRecentLimit}
              />
            </SidebarSection>
          </>
        )}
      </nav>
      <div className="side-foot">
        {identity && (
          <>
            <button
              type="button"
              className="avatar-button"
              onClick={props.onAvatar}
              disabled={!authenticated}
              aria-label={t("Change avatar")}
              title={t("Change avatar")}
            >
              <UserAvatar person={identity} />
            </button>
            <div>
              {identity.nickname}
              <small title={props.serverName}>{props.serverName}</small>
            </div>
          </>
        )}
        {!identity && (
          <div title={props.serverLabel || props.serverName}>
            {props.serverLabel || props.serverName}
          </div>
        )}
        {(!isDesktopChat() || authenticated) && (
          <>
            <RemoteDesktopControl
              t={t}
              active={props.active}
              connected={props.remoteConnected}
              onConnect={props.onRemoteConnect}
            />
            <ADBShareControl t={t} active={props.active} />
          </>
        )}
        {isDesktopChat() &&
          identity &&
          props.connectionState &&
          props.onConnect && (
            <DesktopServerAction
              t={t}
              state={props.connectionState}
              onConnect={props.onConnect}
              iconOnly
            />
          )}
        <ChatSettingsControl t={t} onAppearance={props.onSettings} />
        {!identity && props.connectionState && props.onConnect && (
          <DesktopServerAction
            t={t}
            state={props.connectionState}
            onConnect={props.onConnect}
          />
        )}
        {identity && (
          <button
            className="icon"
            onClick={props.onLogout}
            disabled={!authenticated}
            aria-label={t("Sign out")}
            title={t("Sign out")}
          >
            <LogOut size={16} />
          </button>
        )}
      </div>
      <PanelResizeHandle
        side="left"
        width={props.width}
        onResize={props.onResize}
        t={t}
      />
    </aside>
  );
}
