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
import {
  ArrowUp,
  ArrowDown,
  ChevronDown,
  Settings2,
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
  Square,
  LoaderCircle,
  MessageCircle,
} from "lucide-react";
import { api, getCSRF, setCSRF } from "./api";
import { isDesktopChat } from "./runtime";
import { DesktopSettingsLink } from "./desktop-settings";
import { DesktopWindowChrome } from "./desktop-window-chrome";
import { LoginStory } from "./login-story";
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
import { runningTurn, stopReply } from "./turn-control";
import type {
  User,
  Project,
  Agent,
  Chat,
  Turn,
  Online,
  Attachment,
  TranscriptPart,
} from "./types";
import { AppearanceDialog } from "./appearance-dialog";
import { AvatarDialog } from "./avatar-dialog";
import { AvatarStack, UserAvatar } from "./user-avatar";
import { DirectMessagePanel } from "./direct-message-panel";
import type { FloatingWindowBounds } from "./floating-window";
import { PeopleGroupPicker } from "./people-group-picker";
import {
  createDirectInbox,
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
import { FileSidebar, type FileSelection } from "./file-sidebar";
import { LocalFileContext } from "./local-file-context";
import type { LocalFileTarget } from "./local-file-links";
import { panelStorageKey, parsePanelLayout } from "./panel-layout";
import { AgentModelMenu } from "./agent-model-menu";
import { AgentIcon } from "./agent-icon";
import { AutosizeTextarea } from "./autosize-textarea";
import { SlashCommandMenu } from "./slash-command-menu";
import { goalSuggestion, parseGoalCommand } from "./slash-commands";
import { UserMessage } from "./user-message";
import { MessageFooter } from "./message-footer";
import { TurnActions, TurnOperatorAvatar } from "./turn-identity";
import { TurnResponse } from "./tool-activity";
import { chatSelection, chooseModel } from "./model-selection";
import { effortLabel as formatEffort } from "./i18n";
import type { Translator } from "./i18n";
import { defaultPreferences, type UIPreferences } from "./preferences";
import { usePresentation } from "./use-presentation";
import { AttachmentCards, ImagePreview } from "./attachments";
import {
  pasteAttachments,
  prepareAttachments,
  releaseAttachments,
  validateAttachments,
  type DraftAttachment,
} from "./attachment-selection";
import {
  PermissionPicker,
  ApprovalCard,
  usePermissionModes,
} from "./permission-picker";
import type { MessageKey } from "./i18n";
import "./style.css";
function App() {
  const [preferences, setPreferences] =
    useState<UIPreferences>(defaultPreferences);
  const [appearanceOpen, setAppearanceOpen] = useState(false);
  const [avatarOpen, setAvatarOpen] = useState(false);
  const [savingPreferences, setSavingPreferences] = useState(false);
  const [version, setVersion] = useState("");
  const { t, theme, locale } = usePresentation(preferences);
  const effortLabel = (value: string) => formatEffort(value, t);
  const [user, setUser] = useState<User | null>(null),
    [ready, setReady] = useState(false),
    [error, setError] = useState("");
  const [projects, setProjects] = useState<Project[]>([]),
    [agents, setAgents] = useState<Agent[]>([]),
    [project, setProject] = useState(""),
    [chats, setChats] = useState<Chat[]>([]),
    [chatID, setChatID] = useState(""),
    [chat, setChat] = useState<Chat | null>(null);
  const [online, setOnline] = useState<Online[]>([]),
    [draft, setDraft] = useState(""),
    [posting, setPosting] = useState(false),
    [connected, setConnected] = useState(true),
    [sidebar, setSidebar] = useState(false),
    [showLatest, setShowLatest] = useState(false);
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
  }, [directInbox, profiles]);
  const openDirectChat = (id: string) => directInbox.open(id, profiles.people);
  const chatPeerIdentity = (id: string) => {
    if (!isPeopleGroup(id))
      return (
        profiles.people.find((person) => person.id === id) ||
        inbox.people.find((person) => person.id === id) ||
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
      return parsePanelLayout(localStorage.getItem(panelStorageKey));
    } catch {
      return parsePanelLayout(null);
    }
  });
  const [fileSidebarMobile, setFileSidebarMobile] = useState(false);
  const [narrowLayout, setNarrowLayout] = useState(
    () => matchMedia("(max-width: 900px)").matches,
  );
  const [fileSelection, setFileSelection] = useState<FileSelection | null>(
    null,
  );
  const openLocalFile = useCallback((target: LocalFileTarget) => {
    setFileSelection((old) => ({ target, request: (old?.request || 0) + 1 }));
    setPanels((old) => ({ ...old, rightOpen: true }));
    setFileSidebarMobile(matchMedia("(max-width: 900px)").matches);
    setSidebar(false);
  }, []);
  const localFileContext = useMemo(
    () => ({ openFile: project ? openLocalFile : undefined }),
    [project, openLocalFile],
  );
  useEffect(() => {
    try {
      localStorage.setItem(panelStorageKey, JSON.stringify(panels));
    } catch {
      /* Storage can be disabled. */
    }
  }, [panels]);
  useEffect(() => {
    setFileSelection(null);
  }, [project]);
  useEffect(() => {
    const media = matchMedia("(max-width: 900px)");
    const changed = () => {
      setNarrowLayout(media.matches);
      setSidebar(false);
      setFileSidebarMobile(false);
    };
    media.addEventListener("change", changed);
    return () => media.removeEventListener("change", changed);
  }, []);
  useEffect(() => {
    const closePanels = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
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
  const [draftVersion, setDraftVersion] = useState(0);
  const [commandDismissed, setCommandDismissed] = useState(false);
  const [commandFocused, setCommandFocused] = useState(false);
  const [commandNotice, setCommandNotice] = useState<{
    chatID: string;
    project: string;
    text: string;
  } | null>(null);
  const [preparingFiles, setPreparingFiles] = useState(false);
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
  const fileInput = useRef<HTMLInputElement>(null);
  const filesRef = useRef(files);
  filesRef.current = files;
  useEffect(() => () => releaseAttachments(filesRef.current), []);
  const history = useRef<HTMLDivElement>(null),
    content = useRef<HTMLDivElement>(null),
    pickerRef = useRef<HTMLDivElement>(null),
    serviceWorker = useRef<ServiceWorkerRegistration | null>(null),
    follow = useRef(true),
    activeID = useRef(chatID);
  activeID.current = chatID;
  const selection = useRef({ project, chatID, draftVersion });
  const activeUser = useRef(user?.id);
  const submission = useRef(false);
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
    void api<{ version: string }>("/version")
      .then((value) => setVersion(value.version))
      .catch(() => {});
    api<{ user: User; csrf: string; preferences: UIPreferences }>("/me")
      .then((v) => {
        setPreferences(v.preferences);
        setUser(v.user);
        setCSRF(v.csrf);
      })
      .catch(() => {})
      .finally(() => setReady(true));
    const expire = () => {
      directInbox.reset();
      setGroupPicker(null);
      profileCache.reset();
      setUser(null);
      setChat(null);
      setChats([]);
      setProjects([]);
      setProject("");
      setChatID("");
      setAppearanceOpen(false);
      setAvatarOpen(false);
      setPreferences(defaultPreferences);
      setDraft("");
      setFiles((old) => {
        releaseAttachments(old);
        return [];
      });
      setPreview(null);
      setCSRF("");
    };
    window.addEventListener("auth-expired", expire);
    return () => window.removeEventListener("auth-expired", expire);
  }, []);
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
        .register("/sw.js")
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
      if (!p.some((v) => v.id === current.project)) {
        activeID.current = "";
        setChat(null);
        const first = p[0]?.id || "";
        setProject(first);
        setChatID(
          navigationChats.find((c) => c.project_id === first)?.id || "",
        );
      } else if (!previousIDs && !current.chatID) {
        setChatID(
          navigationChats.find((c) => c.project_id === current.project)?.id ||
            "",
        );
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
    loadNavigation(true);
    const stream = subscribeWorkspaceEvents(
      {
        navigation: receiveNavigation,
        agents: receiveAgents,
        profiles: receiveProfiles,
        presence: (people) => {
          if (!cancelled) setOnline(people);
        },
        connected: (value) => {
          realtimeConnected = value;
          if (!cancelled) setMessagingConnected(value);
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
          const url = new URL("/api/events", window.location.href);
          url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
          return new WebSocket(url);
        },
        csrf: getCSRF,
        visible: () => document.visibilityState === "visible",
        checkAuth: (signal) => api("/me", undefined, signal),
      },
    );
    messageStream.current = stream;
    const resume = () => {
      if (document.visibilityState === "visible") directInbox.read();
      stream.resume();
      if (!realtimeConnected) loadNavigation();
    };
    const visibility = () => {
      stream.visibility();
      if (document.visibilityState === "visible") resume();
    };
    // Poll only as a fallback for unavailable/blocked WebSocket connections.
    const refresh = setInterval(() => {
      if (!realtimeConnected) loadNavigation();
    }, 15000);
    window.addEventListener("focus", resume);
    window.addEventListener("online", resume);
    document.addEventListener("visibilitychange", visibility);
    return () => {
      cancelled = true;
      controller.abort();
      stream.close();
      if (messageStream.current === stream) messageStream.current = null;
      clearInterval(refresh);
      window.removeEventListener("focus", resume);
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
        open: (path) => new EventSource(path),
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
  const commandOpen =
    composerWritable &&
    commandFocused &&
    !posting &&
    chat?.status !== "running" &&
    !commandDismissed &&
    goalSuggestion(draft);
  const draftGoal = parseGoalCommand(draft);
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
    !!user && composerWritable && selectedAvailable,
    chat?.backend === backend ? chat.permission_mode : undefined,
  );
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
    if (!element) return;
    const resize = new ResizeObserver(() => {
      if (follow.current && history.current)
        history.current.scrollTop = history.current.scrollHeight;
    });
    resize.observe(element);
    if (history.current) resize.observe(history.current);
    return () => resize.disconnect();
  }, [chat?.id]);
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
  const turnRunning =
    chat?.turns.some((turn) => turn.status === "running") ?? false;
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
        preferences: UIPreferences;
      }>("/login", {
        username: form.get("username"),
        password: form.get("password"),
      });
      setCSRF(v.csrf);
      setPreferences(v.preferences);
      setUser(v.user);
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
  function selectGoalCommand() {
    setDraft("/goal ");
    setCommandDismissed(false);
    requestAnimationFrame(() =>
      document.getElementById("chat-message")?.focus(),
    );
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
      };
      let body: typeof payload | FormData = payload;
      if (attached.length) {
        body = new FormData();
        for (const [key, value] of Object.entries(payload))
          body.set(key, value);
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
      window.dispatchEvent(new Event("auth-expired"));
    } catch (e) {
      fail(e);
    }
  }
  async function updatePreferences(next: UIPreferences) {
    if (isDesktopChat() || savingPreferences) return;
    const previous = preferences;
    const owner = user?.id;
    setPreferences(next);
    setError("");
    if (!owner) return;
    setSavingPreferences(true);
    try {
      await api<UIPreferences>("/preferences", next);
    } catch (error) {
      if (activeUser.current === owner) {
        setPreferences(previous);
        fail(error);
      }
    } finally {
      setSavingPreferences(false);
    }
  }
  function openChat(chat: Chat) {
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
  const currentModel = models.find((m) => m.model === model);
  useEffect(() => {
    if (!currentModel) return;
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
      saving={savingPreferences}
      version={version}
      t={t}
      onChange={updatePreferences}
      onClose={() => setAppearanceOpen(false)}
    />
  );
  if (!ready)
    return (
      <main className="loading">
        <img className="brand-mark" src="/logo.png" alt="Spool" />
        <p>{t("Connecting to your workspace…")}</p>
        <DesktopSettingsLink t={t} className="desktop-settings-corner" />
      </main>
    );
  if (!user)
    return (
      <>
        <main className={isDesktopChat() ? "login desktop-login" : "login"}>
          <DesktopSettingsLink t={t} className="desktop-settings-corner" />
          {!isDesktopChat() && (
            <button
              className="icon login-preferences"
              onClick={() => setAppearanceOpen(true)}
              aria-label={t("Appearance and language")}
            >
              <Settings2 size={20} />
            </button>
          )}
          <LoginStory t={t} />
          <section className="login-panel">
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
                  autoComplete="username"
                  required
                  maxLength={64}
                />
              </label>
              <label>
                {t("Password")}
                <input
                  name="password"
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
      className={`app ${panels.leftOpen ? "" : "left-collapsed"} ${panels.rightOpen ? "" : "right-collapsed"}`}
      style={
        {
          "--left-panel-width": `${panels.leftWidth}px`,
          "--right-panel-width": `${panels.rightWidth}px`,
        } as CSSProperties
      }
    >
      <Sidebar
        user={displayUser!}
        resolveProfile={resolveProfile}
        projects={projects}
        chats={chats}
        project={project}
        chatID={chatID}
        activeBackend={chat?.turns.length === 0 ? backend : undefined}
        open={sidebar}
        active={narrowLayout ? sidebar : panels.leftOpen}
        posting={posting}
        width={panels.leftWidth}
        t={t}
        onClose={() => {
          setSidebar(false);
          setPanels((old) => ({ ...old, leftOpen: false }));
        }}
        onResize={(width) => setPanels((old) => ({ ...old, leftWidth: width }))}
        onChat={openChat}
        onNew={newChat}
        onSettings={() => setAppearanceOpen(true)}
        onAvatar={() => setAvatarOpen(true)}
        onLogout={logout}
      />
      {(sidebar || fileSidebarMobile) && (
        <button
          className="scrim"
          aria-label={t("Close sidebar")}
          onClick={() => {
            setSidebar(false);
            setFileSidebarMobile(false);
          }}
        />
      )}
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
            <div className={chat ? "heading-copy has-agent" : "heading-copy"}>
              <div className="chat-heading">
                {chat && <AgentIcon backend={backend} size={14} />}
                <strong>
                  {(chat?.title === "New chat" ? t("New chat") : chat?.title) ||
                    (project ? t("New chat") : t("Workspace"))}
                </strong>
              </div>
              <span>
                {projects.find((p) => p.id === project)?.name ||
                  t("Local agent workspace")}
              </span>
            </div>
          </div>
          <div className="header-actions">
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
              <summary>
                {online.length ? (
                  <AvatarStack people={displayOnline} />
                ) : (
                  <Users size={14} aria-hidden="true" />
                )}
                {t("{count} online", { count: online.length })}
              </summary>
              <div className="online-list">
                <div className="online-group-row">
                  <button
                    className="online-chat-person"
                    onClick={(event) => {
                      openDirectChat(onlineChatRoom);
                      event.currentTarget
                        .closest("details")
                        ?.removeAttribute("open");
                    }}
                  >
                    <Users size={16} aria-hidden="true" />
                    {t("Online group chat")}
                    {!!inbox.unread[onlineChatRoom] && (
                      <span className="direct-unread">
                        {inbox.unread[onlineChatRoom]}
                      </span>
                    )}
                  </button>
                  <button
                    className="quiet online-new-group"
                    onClick={(event) => {
                      setGroupPicker("");
                      event.currentTarget
                        .closest("details")
                        ?.removeAttribute("open");
                    }}
                  >
                    <Plus size={14} />
                    {t("New group chat")}
                  </button>
                </div>
                {inbox.groups.map((group) => (
                  <div className="online-group-row" key={group.id}>
                    <button
                      className="online-chat-person"
                      onClick={(event) => {
                        openDirectChat(group.id);
                        event.currentTarget
                          .closest("details")
                          ?.removeAttribute("open");
                      }}
                    >
                      <Users size={16} aria-hidden="true" />
                      {chatPeerIdentity(group.id).nickname}
                      {!!inbox.unread[group.id] && (
                        <span className="direct-unread">
                          {inbox.unread[group.id]}
                        </span>
                      )}
                    </button>
                  </div>
                ))}
                {displayOnline.map((u) => (
                  <div key={u.id}>
                    <UserAvatar person={u} size={26} />
                    <span
                      className={
                        u.state === "online" ? "online-dot" : "away-dot"
                      }
                    />
                    {u.id === user!.id ? (
                      <span>{u.nickname}</span>
                    ) : (
                      <button
                        className="online-chat-person"
                        onClick={(event) => {
                          openDirectChat(u.id);
                          event.currentTarget
                            .closest("details")
                            ?.removeAttribute("open");
                        }}
                        title={t("Chat with {name}", { name: u.nickname })}
                      >
                        {u.nickname}
                        {!!inbox.unread[u.id] && (
                          <span className="direct-unread">
                            {inbox.unread[u.id]}
                          </span>
                        )}
                        <MessageCircle size={13} aria-hidden="true" />
                      </button>
                    )}
                    <small>
                      {u.state === "online" ? t("Online") : t("Away")}
                    </small>
                  </div>
                ))}
                {!online.length && <small>{t("Updating presence…")}</small>}
              </div>
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
              onClick={() => {
                const unread = Object.keys(inbox.unread).find(
                  (id) => inbox.unread[id] > 0,
                );
                const last = inbox.messages.at(-1);
                const peer =
                  unread ||
                  (last && messagePeer(last, user!.id)) ||
                  onlineChatRoom;
                if (peer) openDirectChat(peer);
              }}
            >
              <MessageCircle size={17} />
              {!!unreadMessages && (
                <span className="direct-unread">
                  {Math.min(99, unreadMessages)}
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
          </div>
        </header>
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
            {!chat?.turns.length ? (
              <section className="welcome">
                <img className="welcome-mark" src="/logo.png" alt="Spool" />
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
                const userText = parts
                  .filter(
                    (part) => part.role === "user" && part.kind === "text",
                  )
                  .map((part) => part.text)
                  .join("\n");

                const nativeImages: Attachment[] = parts.flatMap(
                  (part, index) =>
                    part.role === "user" &&
                    part.kind === "image" &&
                    part.image_url
                      ? [
                          {
                            id: turn.id + "-image-" + index,
                            name: part.name || "image",
                            media_type: part.image_url.slice(
                              5,
                              part.image_url.indexOf(";"),
                            ),
                            size: 0,
                            preview_url: part.image_url,
                          },
                        ]
                      : [],
                );
                const turnFiles = turn.attachments?.length
                  ? turn.attachments
                  : nativeImages;
                return (
                  <article className="turn" key={turn.id}>
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
                          text={userText}
                          timestamp={turn.started_at}
                          author={operator?.nickname}
                          locale={locale}
                          t={t}
                        />
                      </div>
                      <TurnOperatorAvatar person={operator} />
                    </div>
                    <TurnResponse
                      parts={parts}
                      backend={turn.backend}
                      t={t}
                      running={turn.status === "running"}
                      timestamp={turn.finished_at}
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
                        parts.some(
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
                    {turn.error && turn.status !== "stopped" && (
                      <p className="error">{turn.error}</p>
                    )}
                    {turn.status === "running" && <ChatStatusIndicator t={t} />}
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
            <div className="error" role="alert">
              {error}
            </div>
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
          <form
            className="composer"
            onSubmit={(e) => {
              e.preventDefault();
              void send();
            }}
          >
            {commandOpen && (
              <SlashCommandMenu t={t} onSelect={selectGoalCommand} />
            )}
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
              aria-controls={commandOpen ? "slash-command-menu" : undefined}
              aria-expanded={commandOpen}
              aria-activedescendant={
                commandOpen ? "slash-command-goal" : undefined
              }
              placeholder={
                !composerWritable && project
                  ? t("Read-only")
                  : project
                    ? t("Message {agent}", {
                        agent: backend === "codex" ? "Codex" : "Claude",
                      })
                    : t("Waiting for project access")
              }
              value={draft}
              disabled={!composerWritable}
              onPaste={(event) => pasteAttachments(event, addFiles)}
              onChange={(e) => {
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
                if (e.nativeEvent.isComposing) return;
                if (
                  commandOpen &&
                  (e.key === "Tab" ||
                    (e.key === "Enter" &&
                      !e.metaKey &&
                      !e.altKey &&
                      !e.shiftKey))
                ) {
                  e.preventDefault();
                  selectGoalCommand();
                  return;
                }
                if (
                  commandOpen &&
                  (e.key === "ArrowDown" || e.key === "ArrowUp")
                ) {
                  e.preventDefault();
                  return;
                }
                if (commandOpen && e.key === "Escape") {
                  e.preventDefault();
                  setCommandDismissed(true);
                  return;
                }
                if (
                  e.key === "Enter" &&
                  (e.metaKey || e.altKey) &&
                  !e.nativeEvent.isComposing
                ) {
                  e.preventDefault();
                  void send();
                }
              }}
              maxLength={131072}
            />
            {draftGoal && (
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
                  disabled={!composerWritable || posting || preparingFiles}
                  onClick={() => fileInput.current?.click()}
                >
                  <Plus size={20} />
                </button>
                {project && (
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
                        chat?.status === "running" ||
                        posting
                      }
                      onClick={() => {
                        setPicker(!picker);
                        setPermissionOpen(false);
                      }}
                    >
                      <AgentIcon backend={backend} size={14} />
                      <span>
                        {currentModel?.displayName || model || t("CLI default")}
                      </span>
                      {effort && <span>· {effortLabel(effort)}</span>}
                      <ChevronDown size={12} />
                    </button>
                    {picker && (
                      <AgentModelMenu
                        agents={
                          canCreate || !chat
                            ? agents
                            : agents.filter(
                                (agent) => agent.backend === chat.backend,
                              )
                        }
                        selection={{ backend, model, effort }}
                        currentBackend={
                          chat?.turns.length ? chat.backend : undefined
                        }
                        t={t}
                        onChange={(next) => {
                          setBackend(next.backend);
                          setPermissionOpen(false);
                          setModel(next.model);
                          setEffort(next.effort);
                        }}
                      />
                    )}
                  </div>
                )}
                <button
                  className="send"
                  type={activeTurn ? "button" : "submit"}
                  aria-label={
                    activeTurn
                      ? t(stopping ? "Stopping…" : "Stop response")
                      : t("Send message")
                  }
                  title={
                    activeTurn
                      ? t(stopping ? "Stopping…" : "Stop response")
                      : t("Send message")
                  }
                  aria-busy={stopping || undefined}
                  onClick={activeTurn ? () => void stop() : undefined}
                  disabled={
                    activeTurn
                      ? !composerWritable || stopping
                      : !composerWritable ||
                        !selectedAvailable ||
                        (!draft.trim() && !files.length) ||
                        !permissions.ready ||
                        preparingFiles ||
                        posting
                  }
                >
                  {activeTurn ? (
                    stopping ? (
                      <LoaderCircle className="stop-spinner" size={18} />
                    ) : (
                      <Square size={14} fill="currentColor" />
                    )
                  ) : (
                    <ArrowUp size={20} />
                  )}
                </button>
              </div>
            </div>
          </form>
        </footer>
      </main>
      {inbox.active && (
        <DirectMessagePanel
          key={`people-chat-${inbox.active}`}
          user={user!.id}
          peer={chatPeerIdentity(inbox.active)}
          people={peopleConversationIDs(
            inbox,
            user!.id,
            online.map((person) => person.id),
          ).map(chatPeerIdentity)}
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
          windowBounds={chatWindowBounds.current}
          onWindowBoundsChange={rememberChatWindowBounds}
        />
      )}
      {groupPicker !== null && (
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
      <FileSidebar
        project={projects.find((item) => item.id === project)}
        active={narrowLayout ? fileSidebarMobile : panels.rightOpen}
        mobileOpen={fileSidebarMobile}
        width={panels.rightWidth}
        selection={fileSelection}
        t={t}
        theme={theme}
        onOpen={openLocalFile}
        onClear={() => setFileSelection(null)}
        onClose={() => {
          setFileSidebarMobile(false);
          setPanels((old) => ({ ...old, rightOpen: false }));
        }}
        onResize={(width) =>
          setPanels((old) => ({ ...old, rightWidth: width }))
        }
      />
      {appearance}
      {avatarOpen && (
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
function worked(turn: Turn, t: Translator) {
  if (!turn.started_at || !turn.finished_at) return t("Completed");
  return durationLabel(turn.finished_at - turn.started_at, t, "worked");
}

function working(turn: Turn, t: Translator, now: number) {
  return durationLabel(now - turn.started_at, t, "working");
}

function workSummary(
  turn: Turn,
  t: Translator,
  now: number,
  effortLabel: (value: string) => string,
  answered = false,
) {
  const duration =
    turn.status === "running"
      ? answered
        ? durationLabel(now - turn.started_at, t, "worked")
        : working(turn, t, now)
      : turn.status === "stopped"
        ? t("Stopped")
        : worked(turn, t);
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

function durationLabel(
  milliseconds: number,
  t: Translator,
  mode: "working" | "worked",
) {
  const seconds = Math.max(0, Math.round(milliseconds / 1000));
  if (seconds < 60) {
    return mode === "working"
      ? t("Working for {seconds}s", { seconds })
      : t("Worked for {seconds}s", { seconds });
  }
  return mode === "working"
    ? t("Working for {minutes}m {seconds}s", {
        minutes: Math.floor(seconds / 60),
        seconds: seconds % 60,
      })
    : t("Worked for {minutes}m {seconds}s", {
        minutes: Math.floor(seconds / 60),
        seconds: seconds % 60,
      });
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
    <App />
  </>,
);
