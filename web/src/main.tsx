import { userName } from "./user-name";
import { appURL, appBasePath } from "./app-path";
import React, {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type CSSProperties,
} from "react";
import { createRoot } from "react-dom/client";
import { ApplicationBoundary } from "./application-boundary";
import {
  ArrowUp,
  ArrowDown,
  ArrowLeft,
  ChevronDown,
  Palette,
  Folder,
  Users,
  Menu,
  PanelLeftOpen,
  PanelRightOpen,
  X,
  WifiOff,
  Download,
  Command,
  Plus,
  MessageCircle,
} from "lucide-react";
import { api, APIError, getCSRF, setCSRF } from "./api";
import {
  isDesktopChat,
  isLocalDesktopChat,
  notifyDesktopViewChanged,
  subscribeDesktopNotifications,
  takeDesktopNotification,
  notifyDesktopServerName,
  notifyDesktopServerReady,
  notifyDesktopFederationTargets,
  reportDesktopServerState,
  subscribeDesktopServerReadyAck,
  notifyDesktopSidebarWidth,
  desktopSidebarWidth,
  desktopServers,
  subscribeDesktopServers,
  subscribeDesktopServerSelection,
  connectDesktopServer,
  requestDesktopFederationLogin,
} from "./runtime";
import { DesktopSettingsLink } from "./desktop-settings";
import { DesktopServerWelcome } from "./desktop-servers";
import { DesktopWindowChrome } from "./desktop-window-chrome";
import { LoginStory } from "./login-story";
import { ComposerSendButton } from "./composer-send-button";
import { RemoteCalls, RemoteTargetChip, RemoteTargetMenu, useRemoteAgents } from "./remote-agents";
import { mcpCommand, MCPChips, MCPMenu, useComposerMCP } from "./composer-mcp";
import { DeliveryStatus, deliveryReceipt, type LocalDelivery } from "./message-delivery";
import {
  composerEnterAction,
  inputMethodKey,
  QueuedMessages,
} from "./queued-messages";
import { subscribeChatEvents } from "./chat-stream";
import {
  mergeNavigationChats,
  subscribeWorkspaceEvents,
  type Navigation,
} from "./workspace-stream";
import {
  adoptCreatedChat,
  canControlChat,
  optimisticChat,
  reconcile,
  reconcileMetadata,
  prependHistory,
  turnOperator,
} from "./chat-state";
import {
  fetchOlderHistory,
  restoreHistoryScroll,
  type HistoryAnchor,
} from "./history-loading";
import { hasRunningTurn, runningTurn, stopReply } from "./turn-control";
import type {
  User,
  Project,
  Agent,
  Chat,
  ChatMetadata,
  Turn,
  Online,
  Attachment,
  TranscriptPart,
} from "./types";
import { AppearanceDialog } from "./appearance-dialog";
import { NotificationSettings } from "./notification-settings";
import {
  clearDevicePush,
  notificationChatFromURL,
  notificationInboxFromURL,
  syncExistingPush,
} from "./web-push";
import { AvatarDialog } from "./avatar-dialog";
import { AvatarStack } from "./user-avatar";
import { useRemoteDesktopWindow } from "./remote-desktop-window";
import { DirectMessagePanel } from "./direct-message-panel";
import type { FloatingWindowBounds } from "./floating-window";
import { PeopleGroupPicker } from "./people-group-picker";
import { OnlinePeopleMenu } from "./online-people-menu";
import { ChatTitleMenu } from "./chat-title-menu";
import { useSidebarPreferences } from "./use-sidebar-preferences";
import { useSessionNotifications } from "./use-session-notifications";
import { sortedSidebarChats } from "./sidebar-state";
import {
  createDirectInbox,
  formatUnreadCount,
  DirectMessageError,
  messagePeer,
  peopleConversationIDs,
  onlineChatRoom,
  isPeopleGroup,
  type PeopleChatGroup,
  type DirectMessage,
} from "./direct-messages";
import type { AvatarConfig } from "./avatar";
import {
  createProfileCache,
  profileLookup,
  type ProfileSnapshot,
} from "./user-profiles";
import { Sidebar } from "./sidebar";
import { loadFederationTargets, type FederationTarget } from "./desktop-federation";
import { FileSidebar, type FileSelection } from "./file-sidebar";
import { LocalFileContext } from "./local-file-context";
import { projectFileURL, type LocalFileTarget } from "./local-file-links";
import { useSessionWorkspace } from "./use-session-workspace";
import { panelStorageKey, parsePanelLayout } from "./panel-layout";
import { AgentModelMenu } from "./agent-model-menu";
import { AgentIcon } from "./agent-icon";
import { AutosizeTextarea } from "./autosize-textarea";
import { useComposerHistory } from "./composer-history";
import { SlashCommandMenu } from "./slash-command-menu";
import {
  commandSuggestions,
  findSlashCommand,
  parseSlashCommand,
  parseGoalCommand,
} from "./slash-commands";
import { hasUserMessage, UserMessage } from "./user-message";
import { MessageFooter } from "./message-footer";
import { TurnActions, TurnOperatorAvatar } from "./turn-identity";
import { TurnError } from "./turn-error";
import { TurnResponse } from "./tool-activity";
import { exchangeFiles, turnExchanges } from "./turn-exchanges";
import { turnWorkLabel } from "./turn-presentation";
import { chatSelection, chooseModel } from "./model-selection";
import { effortLabel as formatEffort } from "./i18n";
import type { Translator } from "./i18n";
import { parsePreferences, preferencesStorageKey, type UIPreferences } from "./preferences";
import { usePresentation } from "./use-presentation";
import { AttachmentCards, ImagePreview } from "./attachments";
import {
  pasteAttachments,
  prepareAttachments,
  queuedDraftFiles,
  releaseAttachments,
  validateAttachments,
  type DraftAttachment,
  type QueuedDraft,
} from "./attachment-selection";
import {
  PermissionPicker,
  ApprovalCard,
  usePermissionModes,
  PermissionStatus,
} from "./permission-picker";
import type { MessageKey } from "./i18n";
import "./style.css";
function App() {
  const [preferences, setPreferences] = useState<UIPreferences>(() => {
    try { return parsePreferences(localStorage.getItem((appBasePath() === "/" ? "" : appBasePath()) + preferencesStorageKey)); }
    catch { return parsePreferences(null); }
  });
  useEffect(() => {
    if (isDesktopChat()) return;
    try { localStorage.setItem((appBasePath() === "/" ? "" : appBasePath()) + preferencesStorageKey, JSON.stringify(preferences)); }
    catch { /* Storage can be disabled. */ }
  }, [preferences]);
  const [appearanceOpen, setAppearanceOpen] = useState(false);
  const [avatarOpen, setAvatarOpen] = useState(false);
  const [version, setVersion] = useState("");
  const [serverName, setServerName] = useState("Home");
  const liveServerName = useRef(false);
  useEffect(() => {
    document.title = `Spool · ${serverName}`;
  }, [serverName]);
  const { t, theme, locale } = usePresentation(preferences);
  const effortLabel = (value: string) => formatEffort(value, t);
  const [user, setUser] = useState<User | null>(null),
    [ready, setReady] = useState(false),
    [error, setError] = useState("");
  const [federationDisplay, setFederationDisplay] = useState<{ account: string; targets: FederationTarget[] }>();
  const [desktopLoginOpen, setDesktopLoginOpen] = useState(false);
  const [serverSelection, setServerSelection] = useState(0);
  const [rememberedAccount, setRememberedAccount] = useState<User | null>(null);
  useEffect(() => {
    if (user) setRememberedAccount(user);
  }, [user]);
  const [loginDraft, setLoginDraft] = useState({ username: "", password: "" });
  const servers = useSyncExternalStore(
    subscribeDesktopServers,
    desktopServers,
    desktopServers,
  );
  const desktopServer = servers.entries.find(
    (server) => server.id === servers.selected,
  );
  const desktopServerName = desktopServer?.name || serverName;
  const remoteDesktop = useRemoteDesktopWindow(user?.id, t, fail, desktopServerName);
  const desktopSelection = servers.selection || `${servers.selected}:${servers.transition?.started_at || 0}`;
  const [projects, setProjects] = useState<Project[]>([]),
    [agents, setAgents] = useState<Agent[]>([]),
    [project, setProject] = useState(""),
    [chats, setChats] = useState<Chat[]>([]),
    [chatID, setChatID] = useState(""),
    [chat, setChat] = useState<Chat | null>(null);
  const selectedChat = !user
    ? undefined
    : !chatID || chat?.id === chatID
      ? chat
      : chats.find((item) => item.id === chatID);
  const chatLoading = !!chatID && chat?.id !== chatID;
  const sidebarPreferences = useSidebarPreferences(user?.id, fail);
  const sessionNotifications = useSessionNotifications(user?.id, chatID);
  const [online, setOnline] = useState<Online[]>([]),
    [draft, setDraft] = useState(""),
    [posting, setPosting] = useState(false),
    [connected, setConnected] = useState(true),
    [sidebar, setSidebar] = useState(false),
    [showLatest, setShowLatest] = useState(false);
  const [serverAvailable, setServerAvailable] = useState(true);
  const [profileCache] = useState(createProfileCache);
  const [directInbox] = useState(createDirectInbox);
  const inbox = useSyncExternalStore(
    directInbox.subscribe,
    directInbox.snapshot,
  );
  const messageStream = useRef<ReturnType<
    typeof subscribeWorkspaceEvents
  > | null>(null);
  const [messagingConnected, setMessagingConnected] = useState(false);
  const desktopAuthenticated = !!user;
  const federationTargets = user && federationDisplay?.account === user.id ? federationDisplay.targets : undefined;
  useEffect(() => {
    if (user && federationTargets) notifyDesktopFederationTargets();
  }, [user?.id, federationTargets]);
  const desktopPageState = !ready
    ? "connecting"
    : !serverAvailable
      ? desktopServer?.status === "disconnected" ? "disconnected" : "error"
      : user ? messagingConnected ? "connected" : "connecting" : "login";
  // Native availability checks keep cached login pages current even when their
  // renderer is suspended. Report the page's own state so those checks continue
  // through an outage and can detect recovery.
  const desktopFederationPending = desktopServer?.federation_source &&
    servers.federation_pending?.source === desktopServer.federation_source &&
    servers.federation_pending.target === desktopServer.federation_target;
  const desktopConnectionState = desktopFederationPending ||
    (!user && desktopServer?.federation_target && desktopServer.status === "connecting")
    ? "connecting"
    : desktopPageState === "login" && desktopServer?.status === "error"
      ? "error"
      : desktopPageState;
  const [groupPicker, setGroupPicker] = useState<string | null>(null);
  const chatWindowBounds = useRef<FloatingWindowBounds | null>(null);
  const rememberChatWindowBounds = useCallback((bounds: FloatingWindowBounds) => {
    chatWindowBounds.current = bounds;
  }, []);
  const unreadMessages = Object.values(inbox.unread).reduce(
    (sum, count) => sum + count,
    0,
  );
  const profiles = useSyncExternalStore(
    profileCache.subscribe,
    profileCache.snapshot,
  );
  const resolveProfile = useMemo(() => profileLookup(profiles), [profiles]);
  useEffect(() => {
    directInbox.profiles(profiles.people);
  }, [directInbox, profiles, inbox.groups]);
  const openDirectChat = (id: string) => directInbox.open(id, profiles.people);
  const openDirectInbox = () => {
    const unread = Object.keys(inbox.unread).find((id) => inbox.unread[id] > 0);
    const last = inbox.messages.at(-1);
    openDirectChat(
      unread || (last && messagePeer(last, user!.id)) || onlineChatRoom,
    );
  };
  const chatPeerIdentity = (id: string) => {
    if (!isPeopleGroup(id))
      return (
        profiles.people.find((person) => person.id === id) ||
        inbox.people.find((person) => person.id === id) ||
        (id === user?.id ? user : undefined) ||
        resolveProfile(id)
      );
    const name = inbox.groupNames[id];
    return {
      id,
      nickname:
        id === onlineChatRoom
          ? t("Online group chat")
          : !name || name === "Group chat"
            ? t("Group chat")
            : name,
    };
  };
  const sendDirectMessage = (
    to: string,
    text: string,
  ): Promise<DirectMessage> =>
    directInbox.send(
      to,
      text,
      (id, value) =>
        messageStream.current?.sendMessage(id, value) ||
        Promise.reject(new DirectMessageError("unavailable")),
    );
  const changePeopleGroup = (
    name: string,
    members: string[],
  ): Promise<PeopleChatGroup> => {
    const stream = messageStream.current;
    if (!stream) return Promise.reject(new DirectMessageError("unavailable"));
    return groupPicker
      ? stream.inviteGroup(groupPicker, members)
      : stream.createGroup(name, members);
  };
  const displayUser = user
    ? {
        ...user,
        ...(profiles.people.find((person) => person.id === user.id) || {}),
      }
    : null;
  const displayOnline = online.map((person) => ({
    ...person,
    ...resolveProfile(person.id),
  }));
  const missingProfileIDs = useMemo(() => {
    const known = new Set(profiles.people.map((person) => person.id));
    const ids = new Set([
      ...chats.flatMap(
        (item) => item.participants?.map((person) => person.id) || [],
      ),
      ...(chat?.participants?.map((person) => person.id) || []),
      ...(chat?.turns.flatMap((turn) => [
        turn.user_id || chat.user_id || "",
        ...(turn.actions?.map((action) => action.user_id) || []),
      ]) || []),
      ...online.map((person) => person.id),
    ]);
    return [...ids]
      .filter((id) => id && id !== "spool-bot" && !known.has(id))
      .sort()
      .join(",");
  }, [profiles, chats, chat, online]);
  const [panels, setPanels] = useState(() => {
    try {
      const saved = parsePanelLayout(
        localStorage.getItem(
          (appBasePath() === "/" ? "" : appBasePath()) + panelStorageKey,
        ),
      );
      return isDesktopChat()
        ? { ...saved, leftWidth: desktopSidebarWidth(servers) }
        : saved;
    } catch {
      return parsePanelLayout(null);
    }
  });
  useEffect(() => {
    if (isDesktopChat())
      setPanels((old) => ({ ...old, leftWidth: desktopSidebarWidth(servers) }));
  }, [servers.sidebar_width]);
  const [fileSidebarMobile, setFileSidebarMobile] = useState(false);
  const [narrowLayout, setNarrowLayout] = useState(
    () => matchMedia("(max-width: 900px)").matches,
  );
  useEffect(() => {
    if (!isDesktopChat()) return;
    const reveal = () => {
      setServerSelection((value) => value + 1);
      setDesktopLoginOpen(false);
      setSidebar(matchMedia("(max-width: 900px)").matches);
      setFileSidebarMobile(false);
      setPanels((old) => ({ ...old, leftOpen: true }));
    };
    reveal();
    return subscribeDesktopServerSelection(reveal);
  }, [servers.selected]);
  useEffect(
    () => reportDesktopServerState(desktopPageState, globalThis, desktopAuthenticated),
    [desktopPageState, desktopAuthenticated],
  );
  useEffect(() => {
    if (!isDesktopChat()) return;
    // The incoming native view is mounted behind the current page. Two frames
    // let its committed shell and selection render before native reveals it.
    let retry: ReturnType<typeof setTimeout> | undefined;
    let acknowledged = false;
    const unsubscribe = subscribeDesktopServerReadyAck((paint) => {
      if (paint.state !== desktopPageState || paint.selection !== desktopSelection || paint.authenticated !== desktopAuthenticated) return;
      acknowledged = true;
      clearTimeout(retry);
    });
    const notify = () => {
      // Other navigation hints may replace this one before WebKit delivers it.
      // Send the rendered connection state with it, retrying until native
      // acknowledges that state so signed-out servers cannot stay connecting.
      notifyDesktopServerReady(desktopPageState, desktopSelection, globalThis, desktopAuthenticated);
      if (!acknowledged) retry = setTimeout(notify, 100);
    };
    let frame = requestAnimationFrame(() => {
      frame = requestAnimationFrame(notify);
    });
    return () => {
      cancelAnimationFrame(frame);
      clearTimeout(retry);
      unsubscribe();
    };
  }, [serverSelection, desktopSelection, desktopPageState, desktopAuthenticated, desktopLoginOpen]);
  const [fileSelection, setFileSelection] = useState<FileSelection | null>(
    null,
  );
  const [fileContext, setFileContext] = useState({ project, chatID });
  if (
    fileContext.project !== project ||
    (chatID && fileContext.chatID !== chatID)
  ) setFileContext({ project, chatID });
  // A same-project draft keeps browsing the current directory until its first
  // message creates a chat with its own workspace and file transport.
  const fileChatID = (
    chatID || (fileContext.project === project ? fileContext.chatID : "")
  ) || undefined;
  const sessionWorkspace = useSessionWorkspace(
    fileChatID,
    chat?.id === chatID ? chat.updated_at : undefined,
  );
  const fileScope = JSON.stringify([
    project,
    chatID,
    sessionWorkspace.workspace?.path,
  ]);
  const openLocalFile = useCallback(
    (target: LocalFileTarget) => {
      setFileSelection((old) => ({
        target,
        request: (old?.request || 0) + 1,
        scope: fileScope,
      }));
      setPanels((old) => ({ ...old, rightOpen: true }));
      setFileSidebarMobile(matchMedia("(max-width: 900px)").matches);
      setSidebar(false);
    },
    [fileScope],
  );
  useEffect(() => {
    try {
      localStorage.setItem((appBasePath() === "/" ? "" : appBasePath()) + panelStorageKey, JSON.stringify(panels));
    } catch {
      /* Storage can be disabled. */
    }
  }, [panels]);
  useEffect(() => {
    setFileSelection(null);
  }, [fileScope]);
  useLayoutEffect(() => {
    const media = matchMedia("(max-width: 900px)");
    const changed = () => {
      setNarrowLayout(media.matches);
      setSidebar(false);
      setFileSidebarMobile(false);
    };
    media.addEventListener("change", changed);
    // Embedded views can acquire their final size between render and
    // subscription. Recheck before paint instead of waiting for another resize.
    changed();
    return () => media.removeEventListener("change", changed);
  }, []);
  useEffect(() => {
    const closePanels = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !document.querySelector("dialog:modal")) {
        setSidebar(false);
        setFileSidebarMobile(false);
      }
    };
    window.addEventListener("keydown", closePanels);
    return () => window.removeEventListener("keydown", closePanels);
  }, []);
  const [clock, setClock] = useState(() => Date.now());
  const [historyLoadError, setHistoryLoadError] = useState(false);
  const historyAnchor = useRef<HistoryAnchor | null>(null);
  const initialHistoryScroll = useRef("");
  const [model, setModel] = useState(""),
    [effort, setEffort] = useState(""),
    [picker, setPicker] = useState(false),
    [backend, setBackend] = useState<"codex" | "claude">("codex");
  const [install, setInstall] = useState<BeforeInstallPromptEvent | null>(null),
    [update, setUpdate] = useState(false);
  const [files, setFiles] = useState<DraftAttachment[]>([]);
  const [localDeliveries, setLocalDeliveries] = useState<LocalDelivery[]>([]);
  const localDeliveriesRef = useRef(localDeliveries);
  localDeliveriesRef.current = localDeliveries;
  function retireDelivery(value: LocalDelivery, keepFiles = false) {
    if (value.retired) return;
    value.retired = true;
    if (!keepFiles) releaseAttachments(value.files);
    setLocalDeliveries(current => current.filter(item => item !== value));
  }
  const visibleDeliveries = localDeliveries.filter(value => value.owner === user?.id && (value.first?.chatID || value.chatID) === chat?.id);
  useEffect(() => {
    if (!chat) return;
    for (const value of visibleDeliveries) {
      if (!deliveryReceipt(chat, value.id).accepted) continue;
      retireDelivery(value);
    }
  }, [chat, localDeliveries]);
  const queuedMessages = [
    ...(chat?.queued_messages || []),
    ...visibleDeliveries.filter(value => value.delivery === "queue" && !deliveryReceipt(chat!, value.id).present).map(value => ({
      id: value.id, message: value.message, attachments: value.files.map(file => file.name),
      created_at: value.createdAt, delivery_status: value.status,
    })),
  ];
  function editLocalDelivery(value: LocalDelivery) {
    if (!composerWritable || value.status !== "failed" || value.retired) return;
    const issue = validateAttachments([...files, ...value.files]);
    if (issue) { setError(t(issue as MessageKey)); return; }
    setDraft(current => [value.message, current].filter(Boolean).join("\n\n"));
    setFiles(current => [...current, ...value.files]);
    retireDelivery(value, true);
    requestAnimationFrame(() => document.getElementById("chat-message")?.focus());
  }
  const [draftVersion, setDraftVersion] = useState(0);
  const composerHistory = useComposerHistory(
    JSON.stringify([user?.id, project, chatID, draftVersion]),
    chat?.id === chatID ? chat : null,
    draft,
    setDraft,
  );
  const [commandDismissed, setCommandDismissed] = useState(false);
  const [commandFocused, setCommandFocused] = useState(false);
  const [commandSelection, setCommandSelection] = useState({
    key: "",
    index: 0,
  });

  const [commandNotice, setCommandNotice] = useState<{
    chatID: string;
    project: string;
    text: string;
  } | null>(null);
  const [preparingFiles, setPreparingFiles] = useState(false);
  const [editingQueue, setEditingQueue] = useState(false);
  const editedQueueDrafts = useRef(
    new Map<string, { message: string; files: DraftAttachment[] }>(),
  );
  const editedDraftKey = (owner: string | undefined, id: string) =>
    `${owner}/${id}`;
  function restoreEditedQueueDraft(id: string) {
    const key = editedDraftKey(activeUser.current, id);
    const restored = editedQueueDrafts.current.get(key);
    if (!restored) return;
    editedQueueDrafts.current.delete(key);
    setDraft(current => [restored.message, current].filter(Boolean).join("\n\n"));
    setFiles(current => [...current, ...restored.files]);
    requestAnimationFrame(() =>
      document.getElementById("chat-message")?.focus(),
    );
  }
  const [stoppingTurns, setStoppingTurns] = useState<Set<string>>(new Set());
  const stopRequests = useRef(new Set<string>());
  const pendingSend = useRef<{
    chatID: string;
    turnID: string;
    accepted: Promise<Chat | null>;
  } | null>(null);
  const [permissionOpen, setPermissionOpen] = useState(false);
  const [preview, setPreview] = useState<{
    file: Attachment;
    url: string;
  } | null>(null);
  const localFileContext = useMemo(
    () => ({
      openFile: project ? openLocalFile : undefined,
      imageURL: project
        ? (target: LocalFileTarget) => projectFileURL(project, target.path, "image", fileChatID)
        : undefined,
      previewImage: (name: string, url: string) => setPreview({
        file: { id: url, name, media_type: "image/unknown", size: 0 },
        url,
      }),
    }),
    [project, fileChatID, openLocalFile],
  );
  const fileInput = useRef<HTMLInputElement>(null);
  const filesRef = useRef(files);
  filesRef.current = files;
  useEffect(() => () => {
    releaseAttachments(filesRef.current);
    for (const value of localDeliveriesRef.current) releaseAttachments(value.files);
    for (const draft of editedQueueDrafts.current.values())
      releaseAttachments(draft.files);
  }, []);
  const history = useRef<HTMLDivElement>(null),
    content = useRef<HTMLDivElement>(null),
    pickerRef = useRef<HTMLDivElement>(null),
    serviceWorker = useRef<ServiceWorkerRegistration | null>(null),
    follow = useRef(true),
    activeID = useRef(chatID);
  const notificationChat = useRef(notificationChatFromURL(window.location.href));
  const desktopAccount = useRef<string | null>(null);
  const [notificationInbox, setNotificationInbox] = useState(() =>
    notificationInboxFromURL(window.location.href),
  );
  const [desktopNotification, setDesktopNotification] = useState(() =>
    takeDesktopNotification(),
  );
  activeID.current = chatID;
  const selection = useRef({ project, chatID, draftVersion });
  const activeUser = useRef(user?.id);
  const submission = useRef(false);
  const deliverRef = useRef(false);
  const [delivering, setDelivering] = useState(false);
  const adoptedChatID = useRef("");
  selection.current = { project, chatID, draftVersion };
  activeUser.current = user?.id;
  function latest() {
    follow.current = true;
    history.current?.scrollTo({
      top: history.current.scrollHeight,
      behavior: "smooth",
    });
    setShowLatest(false);
  }
  function fail(e: unknown) {
    setError(e instanceof Error ? e.message : t("Request failed"));
  }
  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 8000);
    const version = api<{ version: string; server_name?: string }>(
      "/version",
      undefined,
      controller.signal,
    ).then((value) => {
      if (typeof value?.version !== "string" || !value.version)
        throw new Error(t("The server returned an invalid response"));
      if (!active) return;
      setVersion(value.version);
      if (!liveServerName.current) {
        setServerName(value.server_name?.trim() || "Home");
        notifyDesktopServerName(value.server_name?.trim() || "Home");
      }
    });
    const session = api<{ user: User; csrf: string }>(
      "/me",
      undefined,
      controller.signal,
    ).then((v) => {
      if (!v?.user?.id || typeof v.csrf !== "string")
        throw new Error(t("The server returned an invalid response"));
      if (!active) return;
      setUser(v.user);
      setCSRF(v.csrf);
    });
    void Promise.allSettled([version, session]).then(([server, account]) => {
      if (!active) return;
      clearTimeout(timeout);
      const failure = server.status === "rejected"
        ? server.reason
        : account.status === "rejected" &&
            !(account.reason instanceof APIError && account.reason.status === 401)
          ? account.reason
          : undefined;
      setServerAvailable(!failure);
      if (failure) fail(failure);
      setReady(true);
    });
    const expire = () => {
      // An authentication rejection still confirms the server is reachable.
      setServerAvailable(true);
      for (const value of localDeliveriesRef.current) retireDelivery(value);
      for (const draft of editedQueueDrafts.current.values())
        releaseAttachments(draft.files);
      editedQueueDrafts.current.clear();
      directInbox.reset();
      setGroupPicker(null);
      profileCache.reset();
      setUser(null);
      setFederationDisplay(undefined);
      setDesktopLoginOpen(false);
      setChat(null);
      setChats([]);
      setProjects([]);
      setProject("");
      setChatID("");
      setAppearanceOpen(false);
      setAvatarOpen(false);
      setDraft("");
      setFiles((old) => {
        releaseAttachments(old);
        return [];
      });
      setPreview(null);
      setCSRF("");
    };
    window.addEventListener("auth-expired", expire);
    return () => {
      active = false;
      clearTimeout(timeout);
      controller.abort();
      window.removeEventListener("auth-expired", expire);
    };
  }, []);
  useEffect(() => {
    if (!ready || !isDesktopChat()) return;
    const account = user?.id || "";
    const changed = desktopAccount.current !== account;
    desktopAccount.current = account;
    void notifyDesktopViewChanged(user ? chatID : "", changed).catch(() => {});
  }, [ready, user?.id, chatID]);
  useEffect(() => {
    messageStream.current?.view();
  }, [user?.id, chatID]);
  useEffect(() => {
    if (!user || isDesktopChat()) return;
    const controller = new AbortController();
    void syncExistingPush(locale, controller.signal).catch(() => {});
    return () => controller.abort();
  }, [user?.id, locale]);
  useEffect(() => {
    const receive = () => {
      const target = takeDesktopNotification();
      if (target) setDesktopNotification(target);
    };
    const unsubscribe = subscribeDesktopNotifications(receive);
    receive();
    return unsubscribe;
  }, []);
  useEffect(() => {
    if (!user || !desktopNotification) return;
    setDesktopNotification(null);
    if (desktopNotification.user !== user.id) return;
    const url = new URL(desktopNotification.url, window.location.origin);
    const inbox = notificationInboxFromURL(url.href);
    if (inbox && inbox.user === user.id) {
      setNotificationInbox(inbox);
      return;
    }
    const id = notificationChatFromURL(url.href);
    if (!id) return;
    const owner = user.id;
    notificationChat.current = id;
    void api<Chat>("/chats/" + encodeURIComponent(id))
      .then((chat) => {
        if (activeUser.current !== owner || notificationChat.current !== id)
          return;
        notificationChat.current = "";
        directInbox.close();
        openChat(chat);
      })
      .catch(fail);
  }, [desktopNotification, user?.id]);
  useEffect(() => {
    if (!user || !messagingConnected || !notificationInbox) return;
    setNotificationInbox(null);
    const url = new URL(window.location.href);
    url.searchParams.delete("inbox");
    url.searchParams.delete("recipient");
    window.history.replaceState(null, "", url);
    if (notificationInbox.user !== user.id) return;
    const peer = notificationInbox.peer;
    const known = isPeopleGroup(peer)
      ? peer === onlineChatRoom ||
        inbox.groups.some((group) => group.id === peer) ||
        inbox.messages.some((message) => message.group && message.to === peer)
      : profiles.people.some((person) => person.id === peer) ||
        inbox.people.some((person) => person.id === peer);
    if (known) {
      directInbox.open(peer, [...profiles.people, ...inbox.people]);
      setSidebar(false);
    } else {
      setError(
        t(
          "This conversation is no longer available. Messages are only kept in an open page.",
        ),
      );
    }
  }, [
    notificationInbox,
    user?.id,
    messagingConnected,
    profiles,
    inbox.groups,
    inbox.people,
    inbox.messages,
  ]);
  useEffect(() => {
    if (isDesktopChat() || !("serviceWorker" in navigator)) return;
    const owner = user?.id;
    const receive = (event: MessageEvent) => {
      if (
        event.data?.type === "OPEN_INBOX" &&
        typeof event.data.peerID === "string" &&
        typeof event.data.userID === "string"
      ) {
        const target = notificationInboxFromURL(
          new URL(
            "/?" +
              new URLSearchParams({
                inbox: event.data.peerID,
                recipient: event.data.userID,
              }),
            window.location.origin,
          ).href,
        );
        if (!target) return;
        event.ports[0]?.postMessage({ handled: true });
        setNotificationInbox(target);
        return;
      }
      if (
        event.data?.type !== "OPEN_CHAT" ||
        typeof event.data.chatID !== "string"
      )
        return;
      const id = notificationChatFromURL(
        new URL(
          "/?chat=" + encodeURIComponent(event.data.chatID),
          window.location.origin,
        ).href,
      );
      if (!id) return;
      notificationChat.current = id;
      event.ports[0]?.postMessage({ handled: true });
      if (!owner) return;
      void api<Chat>("/chats/" + encodeURIComponent(id))
        .then((chat) => {
          if (activeUser.current !== owner || notificationChat.current !== id)
            return;
          notificationChat.current = "";
          openChat(chat);
        })
        .catch(fail);
    };
    navigator.serviceWorker.addEventListener("message", receive);
    return () =>
      navigator.serviceWorker.removeEventListener("message", receive);
  }, [user?.id]);
  useEffect(() => {
    // Desktop updates ship these assets with the executable, not a PWA.
    if (isDesktopChat()) return;
    const handler = (e: Event) => {
      e.preventDefault();
      setInstall(e as BeforeInstallPromptEvent);
    };
    window.addEventListener("beforeinstallprompt", handler);
    if ("serviceWorker" in navigator && window.isSecureContext) {
      navigator.serviceWorker
        .register(appURL("/sw.js"), { scope: appURL("/") })
        .then((reg) => {
          serviceWorker.current = reg;
          if (reg.waiting) setUpdate(true);
          reg.addEventListener("updatefound", () => {
            const next = reg.installing;
            next?.addEventListener("statechange", () => {
              if (
                next.state === "installed" &&
                navigator.serviceWorker.controller
              )
                setUpdate(true);
            });
          });
        })
        .catch(() => {});
    }
    return () => window.removeEventListener("beforeinstallprompt", handler);
  }, []);
  useEffect(() => {
    if (!user) {
      setOnline([]);
      setMessagingConnected(false);
      directInbox.reset();
      return;
    }
    let cancelled = false;
    let realtimeConnected = false;
    let navigationVersion = 0;
    let previousIDs: Set<string> | undefined;
    let initializedAgents = false;
    const controller = new AbortController();
    const receiveProfiles = (value: ProfileSnapshot) => {
      if (cancelled) return;
      profileCache.accept(value);
    };
    const receiveNavigation = (navigation: Navigation) => {
      if (cancelled) return;
      sidebarPreferences.accept(navigation.sidebar);
      navigationVersion++;
      const p = Array.isArray(navigation?.projects) ? navigation.projects : [];
      const navigationChats = Array.isArray(navigation?.chats)
        ? navigation.chats
        : [];
      setChat((old) => {
        const next = navigationChats.find((item) => item.id === old?.id);
        if (!old || !next) return old;
        return {
          ...old,
          ...(next.updated_at >= old.updated_at
            ? { title: next.title, participants: next.participants }
            : {}),
          can_control: next.can_control,
        };
      });
      setProjects(p);
      setChats((old) => mergeNavigationChats(old, navigationChats));
      const current = selection.current;
      const requested = navigationChats.find(
        (item) => item.id === notificationChat.current,
      );
      if (requested) {
        notificationChat.current = "";
        const url = new URL(window.location.href);
        url.searchParams.delete("chat");
        window.history.replaceState(null, "", url);
        openChat(requested);
      } else if (
        !p.some((v) => v.id === current.project) ||
        (!previousIDs && !current.chatID)
      ) {
        activeID.current = "";
        setChat(null);
        const recent = sortedSidebarChats(navigationChats)[0];
        setProject(recent?.project_id || p[0]?.id || "");
        setChatID(recent?.id || "");
      } else if (
        previousIDs?.has(current.chatID) &&
        !navigationChats.some((c) => c.id === current.chatID)
      ) {
        activeID.current = "";
        setChat(null);
        setChatID("");
      }
      previousIDs = new Set(navigationChats.map((item) => item.id));
    };
    const receiveAgents = (value: Agent[]) => {
      if (cancelled) return;
      const discovered = Array.isArray(value) ? value : [];
      setAgents(discovered);
      if (!initializedAgents) {
        initializedAgents = true;
        const available = discovered.find((v) => v.enabled && v.installed);
        if (available) {
          const selected = chooseModel(
            available,
            available.catalog.defaultModel ||
              available.catalog.models?.[0]?.model ||
              "",
          );
          setBackend(selected.backend);
          setModel(selected.model);
          setEffort(selected.effort);
        }
      }
    };
    let loading = false;
    const loadNavigation = (initial = false) => {
      if (loading) return;
      loading = true;
      const version = navigationVersion;
      void profileCache
        .load(
          () => api<ProfileSnapshot>("/profiles", undefined, controller.signal),
          controller.signal,
        )
        .catch(() => {});
      void Promise.all([
        api<Navigation>("/navigation", undefined, controller.signal),
        api<Agent[]>("/agents", undefined, controller.signal),
      ])
        .then(([navigation, discovered]) => {
          // A delayed fallback must not overwrite a newer WebSocket snapshot.
          if (cancelled || version !== navigationVersion) return;
          receiveNavigation(navigation);
          receiveAgents(discovered);
        })
        .catch((error) => {
          if (initial && !cancelled && version === navigationVersion)
            fail(error);
        })
        .finally(() => {
          loading = false;
        });
    };
    const desktop = isDesktopChat();
    let federationSyncedBySocket = false;
    let federationLoading = false;
    let federationRevision = 0;
    const loadTargets = () => {
      if (!desktop || federationSyncedBySocket || federationLoading) return;
      federationLoading = true;
      const revision = federationRevision;
      void loadFederationTargets(controller.signal)
        .then(
          (value) => {
            // 迟到的 HTTP 响应不能覆盖更新的 WebSocket 快照。
            if (!cancelled && revision === federationRevision)
              setFederationDisplay({ account: user.id, targets: value });
          },
          () => {},
        )
        .finally(() => {
          federationLoading = false;
        });
    };
    loadNavigation(true);
    loadTargets();
    const offSelection = desktop ? subscribeDesktopServerSelection(loadTargets) : undefined;
    const stream = subscribeWorkspaceEvents(
      {
        navigation: receiveNavigation,
        sessionNotification: (value) => {
          if (!cancelled) sessionNotifications.receive(value);
        },
        agents: receiveAgents,
        profiles: receiveProfiles,
        server: (value) => {
          if (cancelled) return;
          liveServerName.current = true;
          setServerName(value.server_name.trim() || "Home");
          notifyDesktopServerName(value.server_name.trim() || "Home");
        },
        federationTargets: desktop
          ? (targets) => {
              if (cancelled) return;
              federationSyncedBySocket = true;
              ++federationRevision;
              setFederationDisplay({ account: user.id, targets });
            }
          : undefined,
        presence: (people) => {
          if (!cancelled) setOnline(people);
        },
        connected: (value) => {
          realtimeConnected = value;
          if (!value) federationSyncedBySocket = false;
          if (!cancelled) setMessagingConnected(value);
          if (!cancelled) setServerAvailable(value);
          if (!value && !cancelled) setOnline([]);
        },
        expired: () => window.dispatchEvent(new Event("auth-expired")),
        groups: (groups) => {
          if (!cancelled) directInbox.groups(groups);
        },
        directMessage: (message) => {
          if (!cancelled)
            directInbox.receive(
              message,
              user.id,
              document.visibilityState === "visible",
              profileCache.snapshot().people,
            );
        },
      },
      {
        open: () => {
          const url = new URL(appURL("/api/events"), window.location.href);
          url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
          return new WebSocket(url);
        },
        csrf: getCSRF,
        visible: () => document.visibilityState === "visible",
        view: () => ({
          focused: !isLocalDesktopChat() && document.visibilityState === "visible" && document.hasFocus(),
          chat: activeID.current,
        }),
        checkAuth: (signal) => api("/me", undefined, signal),
      },
    );
    messageStream.current = stream;
    const resume = () => {
      if (document.visibilityState === "visible") directInbox.read();
      stream.resume();
      loadTargets();
      if (!realtimeConnected) loadNavigation();
    };
    const visibility = () => {
      stream.visibility();
      if (document.visibilityState === "visible") resume();
    };
    const blur = () => stream.view();
    // 各自按是否收到实时快照回退；旧服务可能支持 WebSocket 却不发送互信事件。
    const refresh = setInterval(() => {
      if (!realtimeConnected) loadNavigation();
      loadTargets();
    }, 15000);
    window.addEventListener("focus", resume);
    window.addEventListener("blur", blur);
    window.addEventListener("online", resume);
    document.addEventListener("visibilitychange", visibility);
    return () => {
      cancelled = true;
      controller.abort();
      offSelection?.();
      stream.close();
      if (messageStream.current === stream) messageStream.current = null;
      clearInterval(refresh);
      window.removeEventListener("focus", resume);
      window.removeEventListener("blur", blur);
      window.removeEventListener("online", resume);
      document.removeEventListener("visibilitychange", visibility);
    };
  }, [user?.id]);
  useEffect(() => {
    if (!user || !missingProfileIDs) return;
    const controller = new AbortController();
    // One batched lookup for missing identities, never one request per avatar.
    const timer = setTimeout(() => {
      void profileCache
        .load(
          () => api<ProfileSnapshot>("/profiles", undefined, controller.signal),
          controller.signal,
        )
        .catch(() => {});
    }, 50);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [user?.id, missingProfileIDs]);
  useEffect(() => {
    setPicker(false);
    setPermissionOpen(false);
    setPreview(null);
    setCommandNotice(null);
    setCommandDismissed(false);
    // First-send adoption is not navigation: keep its optimistic turn and draft.
    if (adoptedChatID.current !== chatID || !chatID) {
      setError("");
      setChat(null);
      setDraft("");
      setFiles((old) => {
        releaseAttachments(old);
        return [];
      });
    }
    adoptedChatID.current = "";
    historyAnchor.current = null;
    initialHistoryScroll.current = chatID;
    setHistoryLoadError(false);
    setConnected(true);
    follow.current = true;
    if (!chatID || !user) return;
    return subscribeChatEvents(
      chatID,
      {
        snapshot: (v) => {
          if (activeID.current !== chatID) return;
          restoreEditedQueueDraft(chatID);
          setChat((old) => reconcile(old, v));
          setChats((old) =>
            old.map((item) =>
              item.id === v.id && item.updated_at <= v.updated_at
                ? { ...item, title: v.title, participants: v.participants }
                : item,
            ),
          );
        },
        metadata: (v) => {
          if (activeID.current !== chatID) return;
          setChat((old) => reconcileMetadata(old, v));
          setChats((old) =>
            old.map((item) => reconcileMetadata(item, v) ?? item),
          );
        },
        turn: (v) => {
          if (activeID.current !== chatID) return;
          setChat((old) => {
            if (old?.id !== chatID) return old;
            const turns =
              v.turns || old.turns.filter((t) => !t.id.startsWith("pending-"));
            const index = turns.findIndex((t) => t.id === v.turn.id);
            if (index < 0) turns.push(v.turn);
            else turns[index] = v.turn;
            return reconcile(old, {
              ...old,
              title: v.title,
              backend: v.backend,
              model: v.model,
              reasoning_effort: v.reasoning_effort,
              permission_mode: v.permission_mode,
              status: v.status,
              transcript: v.transcript,
              history: v.history,
              updated_at: v.updated_at,
              queued_messages: v.queued_messages,
              turns,
            });
          });
          setChats((old) =>
            old.map((c) =>
              c.id === chatID
                ? {
                    ...c,
                    title: v.title,
                    backend: v.backend,
                    model: v.model,
                    reasoning_effort: v.reasoning_effort,
                    status: v.status,
                    updated_at: v.updated_at,
                  }
                : c,
            ),
          );
        },
        connected: (value) => {
          if (activeID.current === chatID) setConnected(value);
        },
        error: fail,
      },
      {
        open: (path) => new EventSource(appURL(path)),
        snapshot: (signal) => api<Chat>("/chats/" + chatID, undefined, signal),
        checkAuth: (signal) => api("/me", undefined, signal),
      },
    );
  }, [project, chatID, draftVersion, user?.id]);
  useEffect(() => {
    if (
      !chat ||
      chat.id !== chatID ||
      !user ||
      historyLoadError ||
      !chat.history?.has_more ||
      !chat.history.before ||
      chat.history.error
    )
      return;
    const controller = new AbortController();
    const before = chat.history.before;
    fetchOlderHistory(
      chat,
      (path, signal) => api<Chat>(path, undefined, signal),
      controller.signal,
    )
      .then((page) => {
        if (!page || controller.signal.aborted || activeID.current !== page.id)
          return;
        const element = history.current;
        if (element)
          historyAnchor.current = {
            id: page.id,
            height: element.scrollHeight,
            top: element.scrollTop,
          };
        setChat((old) =>
          old?.id === page.id && old.history?.before === before
            ? prependHistory(old, page)
            : old,
        );
      })
      .catch(() => {
        if (!controller.signal.aborted) setHistoryLoadError(true);
      });
    return () => controller.abort();
  }, [
    chat?.id,
    chat?.history?.before,
    chat?.history?.has_more,
    chat?.history?.error,
    chatID,
    user?.id,
    historyLoadError,
  ]);
  const selectedAgent = agents.find((agent) => agent.backend === backend);
  const models = selectedAgent?.catalog.models || [];
  const selectedAvailable =
    !!selectedAgent?.enabled && !!selectedAgent?.installed;
  const composerReady =
    projects.some((p) => p.id === project) && (!chatID || chat?.id === chatID);
  const canCreate =
    projects.find((p) => p.id === project)?.can_create !== false;
  const composerWritable =
    composerReady &&
    !!user &&
    (chat ? canControlChat(chat, user.id) : canCreate);
  const remote = useRemoteAgents({
    account: user?.id || "",
    originChat: chatID,
    scope: JSON.stringify([project, draftVersion]),
    enabled: composerWritable && !!user && !user.source_server_id,
    draft,
    focused: commandFocused,
    disabled: !composerWritable || posting,
    onDraft: setDraft,
    onError: fail,
  });
  const draftGoal = parseGoalCommand(draft);
  const hasDraft = !!draft.trim() || !!files.length;
  const activeTurn = runningTurn(chat);
  const activeSubmission =
    pendingSend.current &&
    (pendingSend.current.turnID === activeTurn?.id ||
      pendingSend.current.chatID === chat?.id)
      ? pendingSend.current
      : undefined;
  const stopping =
    !!activeTurn &&
    (stoppingTurns.has(activeTurn.id) ||
      (!!activeSubmission && stoppingTurns.has(activeSubmission.turnID)));
  const permissions = usePermissionModes(
    project,
    chatID,
    backend,
    !!user && composerWritable && selectedAvailable && !remote.target,
    chat?.backend === backend ? chat.permission_mode : undefined,
  );
  const mcp = useComposerMCP({
    scope: JSON.stringify([user?.id, project, draftVersion]),
    chatID,
    draft,
    focused: commandFocused,
    enabled: composerWritable && !remote.target,
    disabled: posting || chat?.status === "running",
    available: user?.source_server_id
      ? false
      : remote.serversLoaded
        ? !!remote.servers.length
        : undefined,
    onDraft: setDraft,
  });
  const agentCommands = useMemo(
    () => [mcpCommand, ...(permissions.catalog?.commands || selectedAgent?.catalog.commands || [])
      .filter(command => command.name !== "mcp")],
    [permissions.catalog?.commands, selectedAgent?.catalog.commands],
  );
  const suggestions = useMemo(
    () => commandSuggestions(draft, agentCommands),
    [draft, agentCommands],
  );
  const commandSelectionKey = `${backend}\n${draft}\n${suggestions?.map((command) => command.name).join(",")}`;
  const commandIndex =
    commandSelection.key === commandSelectionKey
      ? Math.min(
          commandSelection.index,
          Math.max(0, (suggestions?.length || 0) - 1),
        )
      : 0;
  const commandOpen =
    !remote.target &&
    !remote.open &&
    !mcp.open &&
    composerWritable &&
    commandFocused &&
    !posting &&
    chat?.status !== "running" &&
    !commandDismissed &&
    selectedAvailable &&
    suggestions !== null;
  useEffect(() => {
    if (!chat || chat.turns.at(-1)?.id.startsWith("pending-")) return;
    const selected = chatSelection(agents, chat);
    setBackend(selected.backend);
    setModel(selected.model);
    setEffort(selected.effort);
    // Catalog refreshes must not discard an unsent model/agent choice.
  }, [chat?.id, chat?.backend]);
  useEffect(() => {
    if (!canCreate && chat && backend !== chat.backend) {
      const selected = chatSelection(agents, chat);
      setBackend(selected.backend);
      setModel(selected.model);
      setEffort(selected.effort);
    }
  }, [canCreate, chat?.id, chat?.backend, backend]);
  useEffect(() => {
    const element = content.current;
    const scroller = history.current;
    if (!element || !scroller) return;
    const resize = new ResizeObserver(() => {
      scroller.parentElement?.style.setProperty(
        "--history-scrollbar-width",
        `${scroller.offsetWidth - scroller.clientWidth}px`,
      );
      if (follow.current) scroller.scrollTop = scroller.scrollHeight;
    });
    resize.observe(element);
    resize.observe(scroller);
    return () => resize.disconnect();
  }, [chat?.id, user?.id]);
  useLayoutEffect(() => {
    if (!chat || !history.current) return;
    if (initialHistoryScroll.current === chat.id) {
      initialHistoryScroll.current = "";
      follow.current = true;
      setShowLatest(false);
    }
    const anchor = historyAnchor.current;
    restoreHistoryScroll(
      history.current,
      follow.current,
      anchor?.id === chat.id ? anchor : null,
    );
    historyAnchor.current = null;
  }, [chat?.turns]);
  const turnRunning = hasRunningTurn(chat);
  useEffect(() => {
    if (!turnRunning) return;
    setClock(Date.now());
    const timer = window.setInterval(() => setClock(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [turnRunning]);
  useEffect(() => {
    if (!user || !chatID || !turnRunning) return;
    let cancelled = false;
    const syncStatus = () => {
      api<Chat>("/chats/" + chatID)
        .then((next) => {
          if (cancelled || activeID.current !== chatID) return;
          setChat((old) => (old?.id === chatID ? reconcile(old, next) : old));
          setChats((old) =>
            old.map((item) =>
              item.id === chatID ? reconcile(item, next) : item,
            ),
          );
        })
        .catch(() => {});
    };
    // SSE is still the primary realtime path. This lightweight status
    // refresh closes the turn if a proxy/browser drops the terminal frame.
    syncStatus();
    const timer = window.setInterval(syncStatus, 1000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [user?.id, chatID, turnRunning]);
  useEffect(() => {
    if (!picker) return;
    const click = (e: PointerEvent) => {
      if (!pickerRef.current?.contains(e.target as Node)) setPicker(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") setPicker(false);
    };
    document.addEventListener("pointerdown", click);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("pointerdown", click);
      document.removeEventListener("keydown", key);
    };
  }, [picker]);
  async function login(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setPosting(true);
    setError("");
    try {
      const v = await api<{
        user: User;
        csrf: string;
      }>("/login", {
        username: form.get("username"),
        password: form.get("password"),
      });
      setCSRF(v.csrf);
      setUser(v.user);
      setLoginDraft({ username: "", password: "" });
      setConnected(true);
      setServerAvailable(true);
    } catch (e) {
      fail(e);
    } finally {
      setPosting(false);
    }
  }
  async function updateAvatar(avatar: AvatarConfig) {
    const owner = user?.id;
    const updated = await api<User & { profiles: ProfileSnapshot }>(
      "/me/avatar",
      avatar,
    );
    if (!owner || activeUser.current !== owner) return;
    profileCache.acceptSaved(updated.profiles, owner);
  }
  function newChat(projectID = project) {
    if (
      !projects.some(
        (item) => item.id === projectID && item.can_create !== false,
      ) ||
      submission.current
    )
      return;
    // A new chat is a local draft. Persist it only when its first message is sent.
    activeID.current = "";
    adoptedChatID.current = "";
    setProject(projectID);
    setChatID("");
    setChat(null);
    setDraftVersion((version) => version + 1);
    setCommandNotice(null);
    setError("");
    setSidebar(false);
  }
  function selectCommand(command = suggestions?.[commandIndex]) {
    if (!command) return;
    if (command.name === "mcp") mcp.show();
    else setDraft(`/${command.name} `);
    setCommandDismissed(false);
    requestAnimationFrame(() =>
      document.getElementById("chat-message")?.focus(),
    );
  }
  async function runSessionCommand(name: string, argument: string) {
    const target = chat,
      owner = user?.id,
      origin = selection.current;
    const current = () =>
      activeUser.current === owner &&
      selection.current.project === origin.project &&
      selection.current.chatID === origin.chatID &&
      selection.current.draftVersion === origin.draftVersion;
    submission.current = true;
    setPosting(true);
    setError("");
    setCommandNotice(null);
    try {
      if (name === "clear" || name === "new") {
        if (argument) throw new Error(`Usage: /${name}`);
        if (!current()) return;
        submission.current = false;
        newChat();
      } else if (name === "agents" || name === "resume") {
        if (argument) {
          const found = chats.find(
            (item) =>
              item.backend === backend &&
              (item.id === argument || item.title === argument),
          );
          if (!found) throw new Error(t("Conversation not found"));
          openChat(found);
        } else {
          setSidebar(true);
          setPanels(old => ({ ...old, leftOpen: true }));
        }
      } else {
        if (!target || target.backend !== backend)
          throw new Error(
            t("Select this chat's current agent to run session commands"),
          );
        if (name === "rename") {
          if (!argument) throw new Error(t("Enter a conversation name"));
          await renameChat(target, argument);
        } else if (name === "archive" || name === "delete") {
          if (argument) throw new Error(`Usage: /${name}`);
          await removeChat(target, name);
        } else if (name === "fork") {
          if (argument) throw new Error("Usage: /fork");
          const fork = await api<Chat>(`/chats/${target.id}/fork`, {
            backend,
            model,
            reasoning_effort: effort,
          });
          if (!current()) return;
          setChats((old) => [
            fork,
            ...old.filter((item) => item.id !== fork.id),
          ]);
          openChat(fork);
        }
      }
      if (
        activeUser.current === owner &&
        (current() ||
          name === "fork" ||
          name === "resume" ||
          name === "new" ||
          name === "clear")
      )
        setDraft("");
    } catch (error) {
      if (current()) fail(error);
    } finally {
      submission.current = false;
      setPosting(false);
    }
  }
  async function runAgentControl(command: string) {
    const owner = user?.id,
      projectID = project,
      message = draft,
      origin = selection.current;
    let target = chat;
    const current = () =>
      activeUser.current === owner &&
      selection.current.project === origin.project &&
      selection.current.chatID === origin.chatID &&
      selection.current.draftVersion === origin.draftVersion;
    submission.current = true;
    setPosting(true);
    setError("");
    setCommandNotice(null);
    try {
      if (target && target.backend !== backend)
        throw new Error(
          t("Select this chat's current agent to run session commands"),
        );
      if (!target) {
        const created = await api<Chat>("/chats", { project_id: projectID, backend });
        target = {
          ...created,
          transcript: created.transcript || { parts: [] },
          turns: created.turns || [],
        };
        if (!current()) return;
        setChats((old) => [
          target!,
          ...old.filter((item) => item.id !== target!.id),
        ]);
        permissions.adoptChat(target.id);
        mcp.adoptChat(target.id);
        adoptedChatID.current = target.id;
        activeID.current = target.id;
        origin.chatID = target.id;
        setChatID(target.id);
        setChat(target);
      }
      const result = await api<{
        message: string;
        model: string;
        reasoning_effort: string;
        permission_mode: string;
      }>(`/chats/${target.id}/commands`, {
        command,
        model,
        reasoning_effort: effort,
        permission_mode: permissions.mode,
      });
      if (!current()) return;
      setModel(result.model);
      setEffort(result.reasoning_effort);
      setChat((old) =>
        old?.id === target!.id
          ? {
              ...old,
              model: result.model,
              reasoning_effort: result.reasoning_effort,
              permission_mode: result.permission_mode,
            }
          : old,
      );
      permissions.retry();
      setCommandNotice({
        chatID: target.id,
        project: projectID,
        text: result.message,
      });
      setDraft((value) => (value === message ? "" : value));
    } catch (error) {
      if (current()) fail(error);
    } finally {
      submission.current = false;
      setPosting(false);
    }
  }
  async function runGoalControl(action: "get" | "clear") {
    const target = chatID,
      owner = user?.id,
      projectID = project,
      message = draft;
    if (files.length) {
      setError(t("Goal commands do not accept attachments"));
      return;
    }
    if (chat && backend !== chat.backend) {
      setError(t("Select this chat's current agent to view or clear its goal"));
      return;
    }
    if (!target) {
      setCommandNotice({
        chatID: target,
        project: projectID,
        text: t("No goal set."),
      });
      setDraft("");
      return;
    }
    submission.current = true;
    setPosting(true);
    setError("");
    setCommandNotice(null);
    try {
      const result = await api<{ message: string }>(
        "/chats/" + target + "/goal",
        { action },
      );
      if (
        activeUser.current === owner &&
        activeID.current === target &&
        selection.current.project === projectID
      ) {
        setCommandNotice({
          chatID: target,
          project: projectID,
          text: result.message,
        });
        setDraft((current) => (current === message ? "" : current));
      }
    } catch (error) {
      if (activeUser.current === owner && activeID.current === target)
        fail(error);
    } finally {
      submission.current = false;
      setPosting(false);
    }
  }
  async function sendRemote() {
    if (!remote.target || !remote.ready || !composerWritable || !draft.trim() || posting || submission.current || remote.busy || preparingFiles) return;
    if (files.length) {
      fail(new Error(t("Remote calls currently accept text only")));
      return;
    }
    submission.current = true;
    setPosting(true);
    setError("");
    const message = draft, owner = user?.id, origin = selection.current;
    let id = chatID, created: Chat | undefined;
    const isCurrent = () => activeUser.current === owner && selection.current.project === origin.project && selection.current.chatID === origin.chatID && selection.current.draftVersion === origin.draftVersion;
    try {
      if (!id) {
        created = await api<Chat>("/chats", { project_id: project, backend });
        id = created.id;
      }
      if (!isCurrent()) return;
      await remote.send(message, id);
      if (isCurrent()) setDraft(value => value === message ? "" : value);
    } catch (error) {
      if (isCurrent()) fail(error);
    } finally {
      if (created && activeUser.current === owner) {
        const adopted = created;
        setChats(old => [adopted, ...old.filter(item => item.id !== adopted.id)]);
        if (isCurrent()) {
          remote.adoptOrigin(id);
          permissions.adoptChat(id);
          mcp.adoptChat(id);
          adoptedChatID.current = id;
          activeID.current = id;
          selection.current = { ...origin, chatID: id };
          setChat(created);
          setChatID(id);
          setSidebar(false);
        }
      }
      submission.current = false;
      setPosting(false);
    }
  }
  function useRemoteContext(text: string) {
    const next = [draft, text].filter(Boolean).join("\n\n");
    if (new TextEncoder().encode(next).length > 131072) {
      fail(new Error(t("Message and remote result exceed the message limit.")));
      return;
    }
    remote.clearTarget();
    setDraft(next);
    requestAnimationFrame(() => document.getElementById("chat-message")?.focus());
  }
  async function send() {
    if (
      !composerWritable ||
      (!draft.trim() && !files.length) ||
      posting ||
      submission.current ||
      chat?.status === "running" ||
      !selectedAvailable ||
      !permissions.ready ||
      preparingFiles
    )
      return;
    const command = parseGoalCommand(draft);
    const slash = parseSlashCommand(draft);
    if (slash?.name.toLowerCase() === "mcp") {
      mcp.show();
      return;
    }
    const definition = slash
      ? findSlashCommand(agentCommands, slash.name)
      : undefined;
    if (slash && files.length) {
      setError(t("Agent commands do not accept attachments"));
      return;
    }
    if (slash && !definition) {
      setError(t("No matching agent commands"));
      return;
    }
    if (command && files.length) {
      setError(t("Goal commands do not accept attachments"));
      return;
    }
    if (command?.objective && Array.from(command.objective).length > 4000) {
      setError(t("Goal conditions must be at most 4000 characters"));
      return;
    }
    if (command && command.action !== "set") {
      await runGoalControl(command.action);
      return;
    }
    if (slash && definition?.kind === "client") {
      await runSessionCommand(definition.name, slash.arguments);
      return;
    }
    if (
      slash &&
      definition?.kind === "control" &&
      !command &&
      !(definition.name === "plan" && slash.arguments)
    ) {
      await runAgentControl(
        `/${definition.name}${slash.arguments ? " " + slash.arguments : ""}`,
      );
      return;
    }
    setCommandNotice(null);
    submission.current = true;
    let id = chatID;
    const message = draft,
      projectID = project,
      owner = user?.id;
    const attached = files;
    const inputParts = makeInputTranscriptParts(message, attached);
    let restoredFiles = false;
    const isCurrent = () =>
      activeUser.current === owner &&
      activeID.current === id &&
      selection.current.project === projectID;
    setDraft("");
    setFiles([]);
    setPreview(null);
    setPermissionOpen(false);
    setPicker(false);
    setPosting(true);
    setError("");
    follow.current = true;
    const optimistic: Turn = {
      id: "pending-" + crypto.randomUUID(),
      user_id: user?.id,
      part_start: 0,
      input_end: 0,
      part_end: 0,
      backend,
      requested_model: model,
      reasoning_effort: effort,
      permission_mode: permissions.mode,
      attachments: attached,
      started_at: Date.now(),
      status: "running",
    };
    const pending = optimisticChat(chat, projectID, optimistic, inputParts);
    let acknowledge!: (value: Chat | null) => void;
    const sending = {
      chatID: id,
      turnID: optimistic.id,
      accepted: new Promise<Chat | null>((resolve) => {
        acknowledge = resolve;
      }),
    };
    pendingSend.current = sending;
    setChat(pending);
    try {
      if (!id) {
        const created = await api<Chat>("/chats", {
          project_id: projectID,
          backend,
        });
        const stillCurrent = isCurrent();
        id = created.id;
        sending.chatID = id;
        if (activeUser.current !== owner) return;
        setChats((old) => [created, ...old.filter((c) => c.id !== id)]);
        if (stillCurrent) {
          permissions.adoptChat(id);
          mcp.adoptChat(id);
          adoptedChatID.current = id;
          activeID.current = id;
          selection.current = { project: projectID, chatID: id, draftVersion };
          setChat(adoptCreatedChat(pending, created));
          setChatID(id);
          setSidebar(false);
        }
      }
      const payload = {
        message,
        backend,
        model,
        reasoning_effort: effort,
        permission_mode: permissions.mode,
        mcp_servers: mcp.selected,
      };
      let body: typeof payload | FormData = payload;
      if (attached.length) {
        body = new FormData();
        for (const [key, value] of Object.entries(payload))
          body.set(key, typeof value === "string" ? value : JSON.stringify(value));
        for (const file of attached) body.append("files", file.file, file.name);
      }

      const v = await api<Chat>("/chats/" + id + "/messages", body);
      acknowledge(v);
      const stillCurrent = isCurrent();
      if (v.id !== id) {
        if (activeUser.current !== owner) return;
        setChats((old) => [v, ...old.filter((item) => item.id !== v.id)]);
        id = v.id;
        sending.chatID = id;
        if (stillCurrent) {
          permissions.adoptChat(id);
          mcp.adoptChat(id);
          adoptedChatID.current = id;
          activeID.current = id;
          selection.current = { project: projectID, chatID: id, draftVersion };
          setChat(v);
          setChatID(id);
        }
      } else if (stillCurrent) setChat((old) => reconcile(old, v));
    } catch (e) {
      if (isCurrent()) {
        setDraft(message);
        setFiles(attached);
        restoredFiles = true;
        fail(e);
        if (
          e instanceof APIError &&
          e.code === "permission_discovery_unavailable"
        )
          permissions.retry();
        if (!id) setChat(null);
        else
          api<Chat>("/chats/" + id)
            .then((v) => {
              if (isCurrent()) setChat(v);
            })
            .catch(() => {});
      }
    } finally {
      acknowledge(null);
      if (pendingSend.current === sending) pendingSend.current = null;
      if (!restoredFiles) releaseAttachments(attached);
      submission.current = false;
      setPosting(false);
    }
  }
  // While the agent is answering, a message waits in the chat's queue or,
  // steered, joins the running turn.
  async function deliver(delivery: "queue" | "steer") {
    if (
      !chat ||
      !activeTurn ||
      !composerWritable ||
      (!draft.trim() && !files.length) ||
      deliverRef.current ||
      preparingFiles
    )
      return;
    const command = parseGoalCommand(draft);
    if (parseSlashCommand(draft)) {
      setError(t("Wait for the running response before using agent commands"));
      return;
    }
    if (command && files.length) {
      setError(t("Goal commands do not accept attachments"));
      return;
    }
    if (command && command.action !== "set") return;
    const message = draft,
      attached = files,
      owner = user?.id,
      first = activeSubmission,
      turnID = activeTurn.id;
    const local: LocalDelivery = {
      id: crypto.randomUUID(), owner, chatID: chat.id, first: first || undefined,
      turnID, delivery, message, files: attached, createdAt: Date.now(), status: "sending",
    };
    setLocalDeliveries(current => [...current, local]);
    follow.current = true;
    deliverRef.current = true;
    setDelivering(true);
    setDraft("");
    setFiles([]);
    setPreview(null);
    setError("");
    try {
      // The turn being answered may still be waiting for its own chat ID.
      const accepted = first ? await first.accepted : chat;
      const id = accepted?.id;
      if (!id || id.startsWith("pending-")) throw new Error("message was not sent");
      const payload = {
        message,
        backend: accepted.backend,
        model,
        reasoning_effort: effort,
        permission_mode: permissions.mode,
        delivery,
        client_message_id: local.id,
        mcp_servers: mcp.selected,
      };
      let body: typeof payload | FormData = payload;
      if (attached.length) {
        body = new FormData();
        for (const [key, value] of Object.entries(payload))
          body.set(key, typeof value === "string" ? value : JSON.stringify(value));
        for (const file of attached) body.append("files", file.file, file.name);
      }
      const v = await api<Chat>("/chats/" + id + "/messages", body);
      if (activeUser.current === owner && activeID.current === v.id)
        setChat((old) => reconcile(old, v));
      retireDelivery(local);
    } catch (e) {
      if (!local.retired && activeUser.current === owner) {
        local.status = "failed";
        setLocalDeliveries(current => [...current]);
        if (activeID.current === (first?.chatID || chat.id)) fail(e);
      }
    } finally {
      deliverRef.current = false;
      setDelivering(false);
    }
  }
  function submitDraft(steer = false) {
    if (mcp.open) mcp.choose();
    else if (/^\/mcp\s*$/i.test(draft) && !remote.target) mcp.show();
    else if (remote.open) remote.select();
    else if (remote.target) void sendRemote();
    else if (activeTurn) void deliver(steer ? "steer" : "queue");
    else void send();
  }
  async function queueAction(
    id: string,
    action: "steer" | "delete" | "move",
    beforeID = "",
  ) {
    if (!chat || deliverRef.current) return;
    const local = visibleDeliveries.find(value => value.id === id);
    if (local) {
      if (action === "delete" && local.status === "failed") retireDelivery(local);
      return;
    }
    const target = chat.id,
      owner = user?.id;
    deliverRef.current = true;
    setDelivering(true);
    setError("");
    try {
      const v = await api<Chat>("/chats/" + target + "/queue", {
        id, action, before_id: beforeID,
      });
      if (activeUser.current === owner && activeID.current === target)
        setChat((old) => reconcile(old, v));
    } catch (e) {
      if (activeUser.current === owner && activeID.current === target) fail(e);
    } finally {
      deliverRef.current = false;
      setDelivering(false);
    }
  }
  async function editQueuedMessage(id: string) {
    const local = visibleDeliveries.find(value => value.id === id);
    if (local) { if (local.status === "failed") editLocalDelivery(local); return; }
    if (!chat || !composerWritable || deliverRef.current || preparingFiles) return;
    const target = chat.id,
      owner = user?.id,
      origin = selection.current;
    const isCurrent = () =>
      activeUser.current === owner &&
      activeID.current === target &&
      selection.current.draftVersion === origin.draftVersion;
    let prepared: DraftAttachment[] = [],
      retained = false;
    deliverRef.current = true;
    setDelivering(true);
    setEditingQueue(true);
    setPreparingFiles(true);
    setError("");
    try {
      const restored = await api<QueuedDraft>("/chats/" + target + "/queue", {
        id, action: "draft",
      });
      if (!isCurrent()) return;
      const sourceFiles = queuedDraftFiles(restored);
      const validation = validateAttachments([...filesRef.current, ...sourceFiles]);
      if (validation) throw new Error(t(validation as MessageKey));
      prepared = await prepareAttachments(sourceFiles);
      if (!isCurrent()) return;
      const next = await api<Chat>("/chats/" + target + "/queue", {
        id, action: "edit",
      });
      if (activeUser.current !== owner) return;
      const key = editedDraftKey(owner, target);
      const earlier = editedQueueDrafts.current.get(key);
      editedQueueDrafts.current.set(key, {
        message: [earlier?.message, restored.message]
          .filter(Boolean)
          .join("\n\n"),
        files: [...(earlier?.files || []), ...prepared],
      });
      retained = true;
      if (isCurrent()) {
        setChat((old) => reconcile(old, next));
        restoreEditedQueueDraft(target);
      }
    } catch (error) {
      if (isCurrent()) fail(error);
    } finally {
      if (!retained) releaseAttachments(prepared);
      deliverRef.current = false;
      setDelivering(false);
      setEditingQueue(false);
      setPreparingFiles(false);
    }
  }
  async function stop() {
    if (!chat || !activeTurn || !composerWritable) return;
    const target = chat;
    const owner = user?.id;
    const pending = activeSubmission;
    const keys = new Set([activeTurn.id, pending?.turnID || activeTurn.id]);
    if ([...keys].some((key) => stopRequests.current.has(key))) return;
    const syncStopping = () => setStoppingTurns(new Set(stopRequests.current));
    for (const key of keys) stopRequests.current.add(key);
    syncStopping();
    setError("");
    try {
      const result = await stopReply(
        target,
        pending?.accepted,
        (id, turnID) => {
          keys.add(turnID);
          stopRequests.current.add(turnID);
          syncStopping();
          return api<Chat>("/chats/" + id + "/stop", { turn_id: turnID });
        },
      );
      if (activeUser.current !== owner || !result) return;
      if (activeID.current === result.id)
        setChat((old) => reconcile(old, result));
      setChats((old) =>
        old.map((item) =>
          item.id === result.id ? reconcile(item, result) : item,
        ),
      );
    } catch (error) {
      if (
        activeUser.current === owner &&
        (activeID.current === target.id || activeID.current === pending?.chatID)
      )
        fail(error);
    } finally {
      for (const key of keys) stopRequests.current.delete(key);
      syncStopping();
    }
  }
  async function addFiles(selected: File[]) {
    if (!selected.length || preparingFiles || posting || !composerWritable)
      return;
    const validation = validateAttachments([...filesRef.current, ...selected]);
    if (validation) {
      setError(t(validation as MessageKey));
      return;
    }
    const key = `${project}/${chatID}/${draftVersion}/${user?.id}`;
    setPreparingFiles(true);
    setError("");
    try {
      const prepared = await prepareAttachments(selected);
      if (
        key !==
        `${selection.current.project}/${activeID.current}/${selection.current.draftVersion}/${activeUser.current}`
      ) {
        releaseAttachments(prepared);
        return;
      }
      setFiles((old) => [...old, ...prepared]);
    } catch (error) {
      fail(error);
    } finally {
      setPreparingFiles(false);
    }
  }
  async function logout() {
    try {
      await api("/logout", {});
      setRememberedAccount(null);
      window.dispatchEvent(new Event("auth-expired"));
      void clearDevicePush().catch(() => {});
    } catch (e) {
      fail(e);
    }
  }
  function updatePreferences(next: UIPreferences) {
    if (isDesktopChat()) return;
    setPreferences(next);
  }
  function openChat(chat: Chat) {
    sessionNotifications.dismiss(chat.id);
    activeID.current = chat.id;
    selection.current = {
      project: chat.project_id,
      chatID: chat.id,
      draftVersion,
    };
    setProject(chat.project_id);
    setChatID(chat.id);
    setSidebar(false);
  }
  async function renameChat(chat: Chat, title: string) {
    const owner = user!.id;
    const renamed = await api<ChatMetadata>("/chats/" + chat.id + "/rename", { title });
    if (activeUser.current !== owner) return;
    setChat((old) => reconcileMetadata(old, renamed));
    setChats((old) => old.map((item) => reconcileMetadata(item, renamed) ?? item));
  }
  async function removeChat(chat: Chat, action: "archive" | "delete") {
    const owner = user!.id;
    const navigation = await api<Navigation>("/chats/" + chat.id + "/" + action, {});
    if (activeUser.current !== owner) return;
    sidebarPreferences.accept(navigation.sidebar);
    setProjects(navigation.projects);
    setChats((old) => mergeNavigationChats(old, navigation.chats));
    if (activeID.current === chat.id && !navigation.chats.some((item) => item.id === chat.id)) {
      activeID.current = "";
      setChat(null);
      setChatID("");
    }
    const url = new URL(window.location.href);
    if (url.searchParams.get("chat") === chat.id) {
      url.searchParams.delete("chat");
      window.history.replaceState(null, "", url);
    }
  }
  function applyUpdate() {
    const waiting = serviceWorker.current?.waiting;
    if (!waiting) {
      location.reload();
      return;
    }
    navigator.serviceWorker.addEventListener(
      "controllerchange",
      () => location.reload(),
      { once: true },
    );
    waiting.postMessage({ type: "SKIP_WAITING" });
  }
  const composerSelection = remote.target ? remote.selection : { backend, model, effort };
  const currentModel = (remote.target ? remote.agents.find(item => item.backend === remote.selection.backend)?.catalog.models || [] : models).find((m) => m.model === composerSelection.model);
  useEffect(() => {
    if (!currentModel || remote.target) return;
    const supported = currentModel.supportedReasoningEfforts || [];
    if (!supported.length) {
      setEffort(
        backend === "claude" ? "" : currentModel.defaultReasoningEffort || "",
      );
    } else if (backend === "claude" && !effort) {
      return;
    } else if (!supported.some((v) => v.reasoningEffort === effort)) {
      setEffort(
        backend === "claude"
          ? ""
          : currentModel.defaultReasoningEffort || supported[0].reasoningEffort,
      );
    }
  }, [currentModel, effort, backend]);
  const appearance = !isDesktopChat() && appearanceOpen && (
    <AppearanceDialog
      preferences={preferences}
      saving={false}
      version={version}
      t={t}
      onChange={updatePreferences}
      onClose={() => setAppearanceOpen(false)}
    >
      {user && <NotificationSettings key={user.id} t={t} language={locale} />}
    </AppearanceDialog>
  );
  const serverStart = isDesktopChat() && !user;
  const connectServer = () => {
    if (desktopFederationPending) return;
    if (!user && desktopServer?.federation_source && desktopServer.federation_target)
      requestDesktopFederationLogin(desktopServer.federation_source, desktopServer.federation_target);
    else if (!serverAvailable || user) connectDesktopServer();
    else setDesktopLoginOpen(true);
  };
  if (!ready && !isDesktopChat())
    return (
      <main className="loading">
        <img className="brand-mark" src={appURL("/logo.png")} alt="Spool" />
        <p>{t("Connecting to your workspace…")}</p>
        <DesktopSettingsLink t={t} className="desktop-settings-corner" />
      </main>
    );
  if (!user && (!isDesktopChat() || desktopLoginOpen))
    return (
      <>
        <main className={isDesktopChat() ? "login desktop-login" : "login"}>
          {!isDesktopChat() && (
            <button
              className="icon login-preferences"
              onClick={() => setAppearanceOpen(true)}
              aria-label={t("Appearance and language")}
            >
              <Palette size={20} />
            </button>
          )}
          <LoginStory t={t} />
          <section className="login-panel">
            {isDesktopChat() && (
              <div className="desktop-login-header">
                <button
                  className="icon"
                  aria-label={t("Back to list")}
                  title={t("Back to list")}
                  onClick={() => setDesktopLoginOpen(false)}
                >
                  <ArrowLeft size={18} />
                </button>
                <span title={desktopServerName}>{desktopServerName}</span>
                <DesktopSettingsLink t={t} />
              </div>
            )}
            <form onSubmit={login}>
              <span className="eyebrow">{t("Welcome back")}</span>
              <h2>{t("Sign in to your workspace")}</h2>
              <p>
                {t("Use the account created by your desktop administrator.")}
              </p>
              <label>
                {t("Username")}
                <input
                  name="username"
                  value={loginDraft.username}
                  onChange={(event) =>
                    setLoginDraft((old) => ({ ...old, username: event.target.value }))
                  }
                  autoComplete="username"
                  required
                  maxLength={64}
                />
              </label>
              <label>
                {t("Password")}
                <input
                  name="password"
                  value={loginDraft.password}
                  onChange={(event) =>
                    setLoginDraft((old) => ({ ...old, password: event.target.value }))
                  }
                  type="password"
                  autoComplete="current-password"
                  required
                  maxLength={72}
                />
              </label>
              {error && (
                <p className="error" role="alert">
                  {error}
                </p>
              )}
              <button className="primary" disabled={posting}>
                {posting ? t("Signing in…") : t("Enter workspace")}
                <ArrowUp size={17} />
              </button>
              <small>
                {t("Need an account? Contact your desktop administrator.")}
              </small>
            </form>
          </section>
        </main>
        {appearance}
      </>
    );
  const workspace = (
    <div
      className={`app ${serverStart ? "desktop-server-start" : ""} ${panels.leftOpen ? "" : "left-collapsed"} ${panels.rightOpen && user ? "" : "right-collapsed"}`}
      style={
        {
          "--left-panel-width": `${panels.leftWidth}px`,
          "--right-panel-width": `${panels.rightWidth}px`,
        } as CSSProperties
      }
    >
      <Sidebar
        user={displayUser}
        account={rememberedAccount}
        federationTargets={federationTargets}
        serverName={serverName}
        serverLabel={desktopServerName}
        connectionState={desktopConnectionState}
        onConnect={connectServer}
        resolveProfile={resolveProfile}
        projects={projects}
        chats={chats}
        notifications={sessionNotifications.notifications}
        pinned={sidebarPreferences.pinned}
        collapsed={sidebarPreferences.collapsed}
        onToggleSection={sidebarPreferences.toggleSection}
        project={project}
        chatID={chatID}
        activeBackend={chat?.turns.length === 0 ? backend : undefined}
        open={narrowLayout && sidebar}
        active={narrowLayout ? sidebar : panels.leftOpen}
        posting={posting}
        width={panels.leftWidth}
        t={t}
        onClose={() => {
          setSidebar(false);
          if (!narrowLayout) setPanels((old) => ({ ...old, leftOpen: false }));
        }}
        onResize={(width) => {
          setPanels((old) => ({ ...old, leftWidth: width }));
          notifyDesktopSidebarWidth(width);
        }}
        onChat={openChat}
        onTogglePin={sidebarPreferences.togglePin}
        onRename={renameChat}
        onArchive={(chat) => removeChat(chat, "archive")}
        onDelete={(chat) => removeChat(chat, "delete")}
        onError={(error) => {
          if (activeUser.current === user!.id) fail(error);
        }}
        onNew={newChat}
        onSettings={() => setAppearanceOpen(true)}
        onRemoteConnect={remoteDesktop.open}
        remoteConnected={remoteDesktop.connected}
        onAvatar={() => setAvatarOpen(true)}
        onLogout={logout}
      />
      <main className="workspace">
        <header>
          <div className="heading">
            {!(narrowLayout ? sidebar : panels.leftOpen) && (
              <button
                className="icon panel-toggle-left"
                onClick={() => {
                  if (narrowLayout) {
                    setSidebar(true);
                    setFileSidebarMobile(false);
                  } else setPanels((old) => ({ ...old, leftOpen: true }));
                }}
                aria-label={t("Open sidebar")}
                title={t("Open sidebar")}
              >
                {narrowLayout ? <Menu size={18} /> : <PanelLeftOpen size={18} />}
              </button>
            )}
            <div className={selectedChat ? "heading-copy has-agent" : "heading-copy"}>
              <div className="chat-heading">
                {selectedChat && <AgentIcon backend={selectedChat.backend} size={14} />}
                {selectedChat && !selectedChat.id.startsWith("pending-") ? (
                  <ChatTitleMenu
                    key={selectedChat.id}
                    title={selectedChat.title === "New chat" ? t("New chat") : selectedChat.title}
                    t={t}
                    pinned={sidebarPreferences.pinned.includes(selectedChat.id)}
                    onTogglePin={() => sidebarPreferences.togglePin(selectedChat.id)}
                    canManage={canControlChat(selectedChat, user!.id)}
                    onRename={(title) => renameChat(selectedChat, title)}
                    onArchive={() => removeChat(selectedChat, "archive")}
                    onDelete={() => removeChat(selectedChat, "delete")}
                    onError={(error) => {
                      if (activeUser.current === user!.id) fail(error);
                    }}
                  />
                ) : (
                  <strong>
                    {(selectedChat?.title === "New chat" ? t("New chat") : selectedChat?.title) ||
                      (serverStart ? desktopServerName : chatID ? t("Loading conversation…") : project ? t("New chat") : t("Workspace"))}
                  </strong>
                )}
              </div>
              <span>
                {projects.find((p) => p.id === project)?.name ||
                  (serverStart ? t("Workspace") : t("Local agent workspace"))}
              </span>
            </div>
          </div>
          {user && <div className="header-actions">
            {install && (
              <button
                className="quiet"
                onClick={async () => {
                  await install.prompt();
                  setInstall(null);
                }}
              >
                <Download size={15} />
                {t("Install app")}
              </button>
            )}
            <details className="online">
              <summary
                aria-label={[
                  t("{count} online", { count: online.length }),
                  ...(unreadMessages
                    ? [t("{count} unread messages", { count: unreadMessages })]
                    : []),
                ].join(", ")}
              >
                {online.length ? (
                  <AvatarStack people={displayOnline} />
                ) : (
                  <Users size={14} aria-hidden="true" />
                )}
                <span className="online-count">
                  {t("{count} online", { count: online.length })}
                </span>
                {!!unreadMessages && (
                  <span className="online-unread" aria-hidden="true">
                    {formatUnreadCount(unreadMessages)}
                  </span>
                )}
              </summary>
              <OnlinePeopleMenu
                self={user!.id}
                people={displayOnline}
                groups={inbox.groups}
                unread={inbox.unread}
                unreadMessages={unreadMessages}
                resolvePerson={chatPeerIdentity}
                t={t}
                onInbox={openDirectInbox}
                onPeer={openDirectChat}
                onNewGroup={() => setGroupPicker("")}
              />
            </details>
            <button
              className="icon direct-inbox-button"
              aria-label={
                unreadMessages
                  ? t("{count} unread messages", {
                      count: unreadMessages,
                    })
                  : t("People chat")
              }
              title={t("People chat")}
              onClick={openDirectInbox}
            >
              <MessageCircle size={17} />
              {!!unreadMessages && (
                <span className="direct-unread">
                  {formatUnreadCount(unreadMessages)}
                </span>
              )}
            </button>
            {!(narrowLayout ? fileSidebarMobile : panels.rightOpen) && (
              <button
                className="icon"
                aria-label={t("Open file sidebar")}
                title={t("Open file sidebar")}
                onClick={() => {
                  if (narrowLayout) {
                    setFileSidebarMobile(true);
                    setSidebar(false);
                  } else setPanels((old) => ({ ...old, rightOpen: true }));
                }}
              >
                <PanelRightOpen size={18} />
              </button>
            )}
          </div>}
        </header>
        {serverStart ? (
          <DesktopServerWelcome t={t} name={desktopServerName} state={desktopConnectionState} onConnect={connectServer} />
        ) : <>
        {!!chat?.artifacts?.length && (
          <nav className="chat-artifacts" aria-label={t("Pull requests")}>
            {chat.artifacts.map((artifact) => (
              <a
                key={artifact.url}
                href={artifact.url}
                target="_blank"
                rel="noopener noreferrer"
              >
                {t("Pull request")} #{artifact.url.split("/").pop()}
              </a>
            ))}
          </nav>
        )}
        {(!connected || !navigator.onLine) && chat && (
          <div className="connection">
            <WifiOff size={14} />
            {t("Connection lost. Reconnecting; submitted tasks keep running.")}
          </div>
        )}
        {update && (
          <div className="connection">
            {t("An update is ready.")}
            <button onClick={applyUpdate}>{t("Reload")}</button>
          </div>
        )}
        <div
          className="history"
          ref={history}
          onScroll={() => {
            const e = history.current;
            if (e) {
              follow.current =
                e.scrollHeight - e.scrollTop - e.clientHeight < 90;
              setShowLatest(!follow.current);
            }
          }}
        >
          <div className="history-inner" ref={content}>
            {chat?.history?.error && (
              <p role="status" className="history-error">
                {t("Native session history is unavailable")}
              </p>
            )}
            {chat?.history?.has_more && !historyLoadError && (
              <p role="status" className="history-error">
                {t("Loading earlier messages…")}
              </p>
            )}
            {historyLoadError && (
              <button
                className="quiet history-older"
                onClick={() => setHistoryLoadError(false)}
              >
                {t("Retry loading earlier messages")}
              </button>
            )}
            {chatLoading ? (
              <p role="status" className="history-error">{t("Loading conversation…")}</p>
            ) : !chat?.turns.length ? (
              <section className="welcome">
                <img className="welcome-mark" src={appURL("/logo.png")} alt="Spool" />
                <span className="eyebrow">
                  {t("A little space for big ideas")}
                </span>
                <h1>
                  {projects.length
                    ? t("What shall we build today?")
                    : t("Prepare a project first.")}
                </h1>
                <p>
                  {projects.length
                    ? t("Describe your idea. Your agent takes it from here.")
                    : t(
                        "Ask your desktop administrator to create a project and grant you access.",
                      )}
                </p>
                <div className="welcome-notes">
                  <span>
                    <Folder size={14} />
                    {projects.find((p) => p.id === project)?.name ||
                      t("Waiting for project access")}
                  </span>
                  <span>
                    <Command size={13} />
                    {t("Run locally. Pick up anytime.")}
                  </span>
                </div>
              </section>
            ) : (
              chat.turns.map((turn) => {
                const operator = turnOperator(
                  chat,
                  turn,
                  displayUser,
                  resolveProfile,
                );
                const parts = chat.transcript.parts.slice(
                  turn.part_start,
                  turn.part_end,
                );
                const inputEnd = turnExchanges(parts)[0].inputEnd;
                const response = parts.slice(inputEnd);
                const renderInput = (
                  input: TranscriptPart[],
                  initial = false,
                ) => {
                  const start = parts.indexOf(input[0]);
                  const first = initial || start === 0;
                  const userText = input
                    .filter(
                      (part) => part.role === "user" && part.kind === "text",
                    )
                    .map((part) => part.text)
                    .join("\n");

                  const nativeFiles = exchangeFiles(
                    input,
                    turn.id,
                    chat.id,
                    start,
                  );
                  const turnFiles =
                    first && turn.attachments?.length
                      ? turn.attachments
                      : nativeFiles;
                  return hasUserMessage(userText, turnFiles) ? (
                    <div className="user-row">
                      <div className="user-message">
                        {!!turnFiles.length && (
                          <AttachmentCards
                            files={turnFiles}
                            chatID={chat.id}
                            t={t}
                            onPreview={(file, url) => setPreview({ file, url })}
                          />
                        )}
                        {userText && <UserMessage text={userText} t={t} />}
                        <MessageFooter
                          role="user"
                          leading={visibleDeliveries.filter(value => input.some(part => part.client_message_id === value.id)).map(value => (
                            <DeliveryStatus key={value.id} status={value.status} t={t} onEdit={() => editLocalDelivery(value)} />
                          ))}
                          text={userText}
                          timestamp={
                            input.find((part) => part.timestamp)?.timestamp ||
                            turn.started_at
                          }
                          author={operator ? userName(operator) : undefined}
                          locale={locale}
                          t={t}
                        />
                      </div>
                      <TurnOperatorAvatar person={operator} />
                    </div>
                  ) : null;
                };
                return (
                  <article className="turn" key={turn.render_key || turn.id}>
                    {renderInput(parts.slice(0, inputEnd), true)}
                    <TurnResponse
                      parts={response}
                      backend={turn.backend}
                      t={t}
                      running={turn.status === "running"}
                      timestamp={turn.finished_at}
                      renderUser={renderInput}
                      locale={locale}
                      actions={
                        <TurnActions
                          chat={chat}
                          turn={turn}
                          locale={locale}
                          t={t}
                          resolve={resolveProfile}
                        />
                      }
                      summary={workSummary(
                        turn,
                        t,
                        clock,
                        effortLabel,
                        response
                          .slice(
                            response
                              .map((part) => part.role)
                              .lastIndexOf("user") + 1,
                          )
                          .some(
                            (part) =>
                              part.phase === "final_answer" &&
                              part.role === "assistant" &&
                              part.kind === "text" &&
                              !!part.text.trim(),
                          ),
                      )}
                    />
                    {turn.pending_approval && (
                      <ApprovalCard
                        key={turn.pending_approval.id}
                        request={turn.pending_approval}
                        chatID={chat.id}
                        turnID={turn.id}
                        t={t}
                        onError={fail}
                      />
                    )}
                    <TurnError turn={turn} t={t} />
                    {turn.status === "running" && <ChatStatusIndicator t={t} />}
                    {visibleDeliveries.filter(value => value.delivery === "steer" &&
                      !deliveryReceipt(chat, value.id).present &&
                      (value.turnID === turn.id || (!chat.turns.some(item => item.id === value.turnID) && turn === chat.turns.at(-1)))
                    ).map(value => <div className="user-row local-delivery" key={value.id}>
                      <div className="user-message">
                        {!!value.files.length && <AttachmentCards files={value.files} chatID={chat.id} t={t} onPreview={(file, url) => setPreview({ file, url })} />}
                        {!!value.message.trim() && <UserMessage text={value.message} t={t} />}
                        <MessageFooter role="user" text={value.message} author={displayUser ? userName(displayUser) : undefined} timestamp={value.createdAt} locale={locale} t={t}
                          leading={<DeliveryStatus status={value.status} t={t} onEdit={() => editLocalDelivery(value)} />} />
                      </div>
                      <TurnOperatorAvatar person={displayUser || undefined} />
                    </div>)}
                  </article>
                );
              })
            )}
          </div>
        </div>
        <footer className="composer-wrap">
          {showLatest && (
            <button
              className="scroll-latest"
              onClick={latest}
              aria-label={t("Scroll to latest")}
            >
              <ArrowDown size={18} />
            </button>
          )}
          {error && (
            <div className="error dismissible-error" role="alert">
              <span>{error}</span>
              <button
                type="button"
                className="icon"
                onClick={() => setError("")}
                aria-label={t("Dismiss")}
                title={t("Dismiss")}
              >
                <X size={15} />
              </button>
            </div>
          )}
          {composerWritable && selectedAvailable && !activeTurn && !remote.target && (
            <PermissionStatus
              value={permissions}
              t={t}
              onChoose={() => {
                setPermissionOpen(true);
                setPicker(false);
              }}
            />
          )}
          {commandNotice?.chatID === chatID &&
            commandNotice.project === project && (
              <div className="goal-command-notice" role="status">
                <pre>{commandNotice.text}</pre>
                <button
                  type="button"
                  className="icon"
                  onClick={() => setCommandNotice(null)}
                  aria-label={t("Dismiss")}
                >
                  <X size={15} />
                </button>
              </div>
            )}
          {composerWritable && !!queuedMessages.length && (
            <QueuedMessages
              messages={queuedMessages}
              running={!!activeTurn}
              busy={delivering}
              t={t}
              onSteer={(id) => void queueAction(id, "steer")}
              onDelete={(id) => void queueAction(id, "delete")}
              onEdit={(id) => void editQueuedMessage(id)}
              onMove={(id, beforeID) => void queueAction(id, "move", beforeID)}
            />
          )}
          <form
            className="composer"
            onSubmit={(e) => {
              e.preventDefault();
              submitDraft();
            }}
          >
            {remote.open && <RemoteTargetMenu remote={remote} t={t} />}
            {mcp.open && <MCPMenu mcp={mcp} t={t} />}
            {commandOpen && (
              <SlashCommandMenu
                t={t}
                commands={suggestions || []}
                activeIndex={commandIndex}
                onActiveIndex={(index) =>
                  setCommandSelection({ key: commandSelectionKey, index })
                }
                onSelect={selectCommand}
              />
            )}
            <RemoteTargetChip remote={remote} t={t} />
            {!remote.target && <MCPChips mcp={mcp} t={t} disabled={posting || chat?.status === "running"} />}
            <RemoteCalls remote={remote} t={t} onError={fail} onContext={useRemoteContext} />
            {!!files.length && (
              <div className="composer-attachments">
                <AttachmentCards
                  files={files}
                  t={t}
                  onPreview={(file, url) => setPreview({ file, url })}
                  onRemove={(id) =>
                    setFiles((old) => {
                      releaseAttachments(old.filter((file) => file.id === id));
                      return old.filter((file) => file.id !== id);
                    })
                  }
                />
              </div>
            )}
            <AutosizeTextarea
              id="chat-message"
              aria-label={t("Message")}
              aria-haspopup="listbox"
              aria-controls={mcp.open ? "mcp-menu" : remote.open ? "remote-target-menu" : commandOpen ? "slash-command-menu" : undefined}
              aria-expanded={mcp.open || remote.open || commandOpen}
              aria-activedescendant={
                mcp.open && mcp.rows.length ? `mcp-option-${mcp.index}` : remote.open && remote.rows.length ? `remote-target-${remote.index}` : commandOpen && suggestions?.length
                  ? `slash-command-${commandIndex}`
                  : undefined
              }
              placeholder={
                !composerWritable && project
                  ? t("Read-only")
                  : project
                    ? t("Message {agent}", {
                        agent: (remote.target ? remote.selection.backend : backend) === "codex" ? "Codex" : "Claude",
                      })
                    : t("Waiting for project access")
              }
              value={draft}
              disabled={!composerWritable || editingQueue || remote.busy}
              onPaste={(event) => pasteAttachments(event, addFiles)}
              onChange={(e) => {
                composerHistory.reset();
                setDraft(e.target.value);
                setCommandDismissed(false);
              }}
              onFocus={() => setCommandFocused(true)}
              onBlur={(event) => {
                if (
                  !(event.relatedTarget instanceof Element) ||
                  !event.relatedTarget.closest(".slash-command-menu")
                )
                  setCommandFocused(false);
              }}
              onKeyDown={(e) => {
                if (inputMethodKey(e.nativeEvent)) return;
                if (mcp.onKeyDown(e)) return;
                if (remote.onKeyDown(e)) return;
                if (
                  commandOpen &&
                  !!suggestions?.length &&
                  (e.key === "Tab" ||
                    (e.key === "ArrowRight" && suggestions[commandIndex]?.name === "mcp") ||
                    (e.key === "Enter" &&
                      !e.metaKey &&
                      !e.altKey &&
                      !e.shiftKey))
                ) {
                  e.preventDefault();
                  selectCommand();
                  return;
                }
                if (
                  commandOpen &&
                  (e.key === "ArrowDown" || e.key === "ArrowUp")
                ) {
                  e.preventDefault();
                  const count = suggestions?.length || 0;
                  if (count)
                    setCommandSelection({
                      key: commandSelectionKey,
                      index:
                        (commandIndex +
                          (e.key === "ArrowDown" ? 1 : count - 1)) %
                        count,
                    });
                  return;
                }
                if (commandOpen && e.key === "Escape") {
                  e.preventDefault();
                  setCommandDismissed(true);
                  return;
                }
                if (!remote.target && composerHistory.onKeyDown(e)) {
                  setCommandDismissed(true);
                  return;
                }
                const action = composerEnterAction(
                  e.nativeEvent,
                  matchMedia("(pointer: coarse)").matches,
                );
                if (action) {
                  e.preventDefault();
                  if (
                    action === "steer" &&
                    !draft.trim() &&
                    !files.length
                  ) {
                    const next = chat?.queued_messages?.[0];
                    if (next && composerWritable && !preparingFiles)
                      void queueAction(next.id, "steer");
                  } else submitDraft(action === "steer");
                }
              }}
              maxLength={131072}
            />
            {draftGoal && !remote.target && (
              <div className="goal-command-hint">
                {t("/goal <condition> · /goal to view · /goal clear to remove")}
              </div>
            )}
            <div className="composer-bottom">
              <div className="composer-tools">
                <input
                  ref={fileInput}
                  type="file"
                  multiple
                  hidden
                  aria-label={t("Add files or images")}
                  onChange={(event) => {
                    const selected = [...(event.target.files || [])];
                    event.target.value = "";
                    void addFiles(selected);
                  }}
                />
                <button
                  type="button"
                  className="icon attachment-add"
                  aria-label={t("Add files or images")}
                  title={t("Add files or images")}
                  disabled={!composerWritable || posting || preparingFiles || !!remote.target}
                  onClick={() => fileInput.current?.click()}
                >
                  <Plus size={20} />
                </button>
                {project && !remote.target && (
                  <PermissionPicker
                    backend={backend}
                    value={permissions}
                    t={t}
                    disabled={
                      !composerWritable || chat?.status === "running" || posting
                    }
                    open={permissionOpen}
                    onOpen={(open) => {
                      setPermissionOpen(open);
                      if (open) setPicker(false);
                    }}
                  />
                )}
              </div>
              <div className="composer-actions">
                {project && (
                  <div className="model-picker" ref={pickerRef}>
                    <button
                      className="picker-summary"
                      type="button"
                      aria-label={t("Choose agent and model")}
                      aria-expanded={picker}
                      disabled={
                        !composerWritable ||
                        (!remote.target && chat?.status === "running") ||
                        posting
                      }
                      onClick={() => {
                        setPicker(!picker);
                        setPermissionOpen(false);
                      }}
                    >
                      <AgentIcon backend={composerSelection.backend} size={14} />
                      <span>
                        {currentModel?.displayName || composerSelection.model || t("CLI default")}
                      </span>
                      {composerSelection.effort && <span>· {effortLabel(composerSelection.effort)}</span>}
                      <ChevronDown size={12} />
                    </button>
                    {picker && (
                      <AgentModelMenu
                        agents={
                          remote.target ? remote.agents.filter(item => !remote.target?.conversation || item.backend === remote.selection.backend) : canCreate || !chat
                            ? agents
                            : agents.filter(
                                (agent) => agent.backend === chat.backend,
                              )
                        }
                        selection={composerSelection}
                        currentBackend={
                          remote.target ? remote.target.conversation ? remote.selection.backend : undefined : chat?.turns.length ? chat.backend : undefined
                        }
                        t={t}
                        onChange={(next) => {
                          if (remote.target) { remote.setSelection(next); return; }
                          setBackend(next.backend);
                          setCommandDismissed(false);
                          setPermissionOpen(false);
                          setModel(next.model);
                          setEffort(next.effort);
                        }}
                      />
                    )}
                  </div>
                )}
                <ComposerSendButton
                  active={!remote.target && !!activeTurn && !hasDraft}
                  stopping={!remote.target && stopping && !hasDraft}
                  t={t}
                  onStop={() => void stop()}
                  disabled={
                    remote.target ? !composerWritable || !remote.ready || !draft.trim() || !!files.length || posting || remote.busy || preparingFiles : activeTurn
                      ? !composerWritable ||
                        (hasDraft ? preparingFiles || delivering : stopping)
                      : !composerWritable ||
                        !selectedAvailable ||
                        !hasDraft ||
                        !permissions.ready ||
                        preparingFiles ||
                        posting
                  }
                />
              </div>
            </div>
          </form>
        </footer>
        </>}
      </main>
      {user && inbox.active && (
        <DirectMessagePanel
          user={user!.id}
          self={displayUser!}
          peer={chatPeerIdentity(inbox.active)}
          people={peopleConversationIDs(
            inbox,
            user!.id,
            online.map((person) => person.id),
          ).map(chatPeerIdentity)}
          groups={inbox.groups}
          resolvePerson={chatPeerIdentity}
          presence={Object.fromEntries(
            online.map((person) => [person.id, person.state]),
          )}
          unread={inbox.unread}
          messages={inbox.messages.filter(
            (message) => messagePeer(message, user!.id) === inbox.active,
          )}
          connected={messagingConnected}
          status={online.find((person) => person.id === inbox.active)?.state}
          onlineCount={online.length}
          group={inbox.groups.find((group) => group.id === inbox.active)}
          draft={inbox.drafts[inbox.active]?.text || ""}
          sending={!!inbox.sending[inbox.active]}
          locale={locale}
          t={t}
          onSend={sendDirectMessage}
          onDraftChange={(text) => directInbox.draft(inbox.active, text)}
          onPeer={openDirectChat}
          onClose={directInbox.close}
          onInvite={() => setGroupPicker(inbox.active)}
          onNewGroup={() => setGroupPicker("")}
          windowBounds={chatWindowBounds.current}
          onWindowBoundsChange={rememberChatWindowBounds}
        />
      )}
      {user && groupPicker !== null && (
        <PeopleGroupPicker
          key={`people-picker-${groupPicker}`}
          group={inbox.groups.find((group) => group.id === groupPicker)}
          people={displayOnline.filter(
            (person) =>
              person.id !== user!.id &&
              !inbox.groups
                .find((group) => group.id === groupPicker)
                ?.members.includes(person.id),
          )}
          t={t}
          onSubmit={changePeopleGroup}
          onClose={() => setGroupPicker(null)}
          onDone={(group) => {
            setGroupPicker(null);
            openDirectChat(group.id);
          }}
        />
      )}
      {user && <FileSidebar
        project={projects.find((item) => item.id === project)}
        chatID={fileChatID}
        workspace={sessionWorkspace.workspace}
        workspaces={sessionWorkspace.workspaces}
        onSelectWorkspace={sessionWorkspace.select}
        workspaceError={sessionWorkspace.error}
        onRefreshWorkspace={sessionWorkspace.reload}
        active={narrowLayout ? fileSidebarMobile : panels.rightOpen}
        mobileOpen={narrowLayout && fileSidebarMobile}
        width={panels.rightWidth}
        selection={fileSelection?.scope === fileScope ? fileSelection : null}
        t={t}
        theme={theme}
        onOpen={openLocalFile}
        onClear={() => setFileSelection(null)}
        onClose={() => {
          setFileSidebarMobile(false);
          if (!narrowLayout) setPanels((old) => ({ ...old, rightOpen: false }));
        }}
        onResize={(width) =>
          setPanels((old) => ({ ...old, rightWidth: width }))
        }
      />}
      {appearance}
      {user && avatarOpen && (
        <AvatarDialog
          user={displayUser!}
          t={t}
          onSave={updateAvatar}
          onClose={() => setAvatarOpen(false)}
        />
      )}
      {preview && (
        <ImagePreview
          file={preview.file}
          url={preview.url}
          t={t}
          onClose={() => setPreview(null)}
        />
      )}
    </div>
  );
  return (
    <LocalFileContext.Provider value={localFileContext}>
      {workspace}
    </LocalFileContext.Provider>
  );
}
function workSummary(
  turn: Turn,
  t: Translator,
  now: number,
  effortLabel: (value: string) => string,
  answered = false,
) {
  const duration = turnWorkLabel(turn, t, now, answered);
  const metadata = [
    turn.backend === "codex"
      ? "Codex"
      : turn.backend === "claude"
        ? "Claude"
        : "",
    turn.model,
    turn.reasoning_effort ? effortLabel(turn.reasoning_effort) : "",
  ].filter(Boolean);
  return (
    <>
      <span>{duration}</span>
      {!!metadata.length && (
        <span className="work-metadata"> · {metadata.join(" · ")}</span>
      )}
    </>
  );
}

function ChatStatusIndicator({ t }: { t: Translator }) {
  return (
    <div
      className="chat-status-grid-beat"
      role="status"
      aria-label={t("Working")}
    >
      {Array.from({ length: 9 }, (_, index) => (
        <span key={index} aria-hidden="true" />
      ))}
    </div>
  );
}

function makeInputTranscriptParts(
  message: string,
  attachments: Attachment[],
): TranscriptPart[] {
  const parts: TranscriptPart[] = [];
  if (message.trim()) {
    parts.push({ role: "user", kind: "text", text: message.trim() });
  }
  for (const attachment of attachments) {
    const image = attachment.media_type.startsWith("image/");
    parts.push({
      role: "user",
      kind: image ? "image" : "context",
      name: attachment.name,
      text: image ? "[image]" : attachment.name,
    });
  }
  return parts;
}
interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>;
}
createRoot(document.getElementById("root")!).render(
  <>
    <DesktopWindowChrome />
    <ApplicationBoundary>
      <App />
    </ApplicationBoundary>
  </>,
);
