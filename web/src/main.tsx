import React, { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  ArrowUp,
  ArrowDown,
  ChevronDown,
  Settings2,
  Folder,
  Users,
  Menu,
  X,
  WifiOff,
  Download,
  Command,
  Plus,
  Square,
  LoaderCircle,
} from "lucide-react";
import { api, setCSRF } from "./api";
import { subscribeChatEvents } from "./chat-stream";
import {
  adoptCreatedChat,
  canControlChat,
  optimisticChat,
  reconcile,
  reconcileMetadata,
  prependHistory,
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
import type { AvatarConfig } from "./avatar";
import { Sidebar } from "./sidebar";
import { AgentModelMenu } from "./agent-model-menu";
import { AgentIcon } from "./agent-icon";
import { AutosizeTextarea } from "./autosize-textarea";
import { SlashCommandMenu } from "./slash-command-menu";
import { goalSuggestion, parseGoalCommand } from "./slash-commands";
import { MarkdownMessage } from "./markdown-message";
import { ToolActivityList } from "./tool-activity";
import { chatSelection, chooseModel } from "./model-selection";
import { effortLabel as formatEffort } from "./i18n";
import type { Translator } from "./i18n";
import { defaultPreferences, type UIPreferences } from "./preferences";
import { usePresentation } from "./use-presentation";
import { AttachmentCards, ImagePreview } from "./attachments";
import {
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
const clientID = crypto.randomUUID();
function App() {
  const [preferences, setPreferences] =
    useState<UIPreferences>(defaultPreferences);
  const [appearanceOpen, setAppearanceOpen] = useState(false);
  const [avatarOpen, setAvatarOpen] = useState(false);
  const [savingPreferences, setSavingPreferences] = useState(false);
  const { t } = usePresentation(preferences);
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
    api<{ user: User; csrf: string; preferences: UIPreferences }>("/me")
      .then((v) => {
        setPreferences(v.preferences);
        setUser(v.user);
        setCSRF(v.csrf);
      })
      .catch(() => {})
      .finally(() => setReady(true));
    const expire = () => {
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
    if (!user) return;
    let cancelled = false;
    const loadNavigation = (initial = false) => {
      Promise.all([
        api<{ projects: Project[]; chats: Chat[] }>("/navigation"),
        api<Agent[]>("/agents"),
      ])
        .then(([navigation, a]) => {
          if (cancelled) return;
          const p = Array.isArray(navigation?.projects)
            ? navigation.projects
            : [];
          const navigationChats = Array.isArray(navigation?.chats)
            ? navigation.chats
            : [];
          const discoveredAgents = Array.isArray(a) ? a : [];
          setProjects(p);
          setAgents(discoveredAgents);
          setChats((old) => {
            const byID = new Map(
              navigationChats.map((chat) => [chat.id, chat]),
            );
            for (const chat of old) {
              if (!p.some((project) => project.id === chat.project_id))
                continue;
              const next = byID.get(chat.id);
              if (!next || next.updated_at < chat.updated_at)
                byID.set(chat.id, chat);
            }
            return [...byID.values()];
          });
          const current = selection.current;
          if (!p.some((v) => v.id === current.project)) {
            const first = p[0]?.id || "";
            setProject(first);
            setChatID(
              navigationChats.find((c) => c.project_id === first)?.id || "",
            );
          } else if (initial && !current.chatID) {
            setChatID(
              navigationChats.find((c) => c.project_id === current.project)
                ?.id || "",
            );
          }
          if (initial) {
            const available = discoveredAgents.find(
              (v) => v.enabled && v.installed,
            );
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
        })
        .catch((error) => {
          if (initial && !cancelled) fail(error);
        });
    };
    loadNavigation(true);
    const refreshData = () => loadNavigation();
    const refresh = setInterval(refreshData, 15000);
    window.addEventListener("focus", refreshData);
    return () => {
      cancelled = true;
      clearInterval(refresh);
      window.removeEventListener("focus", refreshData);
    };
  }, [user?.id]);
  useEffect(() => {
    if (!user) return;
    let cancelled = false;
    const heartbeat = () => {
      api<Online[]>("/presence", {
        client_id: clientID,
        visible: document.visibilityState === "visible",
      })
        .then((v) => {
          if (!cancelled) setOnline(v);
        })
        .catch(() => {});
    };
    heartbeat();
    const tick = setInterval(heartbeat, 20000);
    document.addEventListener("visibilitychange", heartbeat);
    const source = new EventSource("/api/presence/events");
    source.addEventListener("presence", (e) =>
      setOnline(JSON.parse((e as MessageEvent).data)),
    );
    return () => {
      cancelled = true;
      clearInterval(tick);
      source.close();
      document.removeEventListener("visibilitychange", heartbeat);
    };
  }, [user?.id]);
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
  const composerWritable =
    composerReady && !!user && canControlChat(chat, user.id);
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
    const element = content.current;
    if (!element) return;
    const resize = new ResizeObserver(() => {
      if (follow.current && history.current)
        history.current.scrollTop = history.current.scrollHeight;
    });
    resize.observe(element);
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
    const updated = await api<User>("/me/avatar", avatar);
    if (activeUser.current !== owner) return;
    setUser(updated);
    setOnline((people) =>
      people.map((person) =>
        person.id === updated.id
          ? { ...person, avatar: updated.avatar }
          : person,
      ),
    );
  }
  function newChat(projectID = project) {
    if (!projects.some((item) => item.id === projectID) || submission.current)
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
    if (savingPreferences) return;
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
  const appearance = appearanceOpen && (
    <AppearanceDialog
      preferences={preferences}
      saving={savingPreferences}
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
      </main>
    );
  if (!user)
    return (
      <>
        <main className="login">
          <button
            className="icon login-preferences"
            onClick={() => setAppearanceOpen(true)}
            aria-label={t("Appearance and language")}
          >
            <Settings2 size={20} />
          </button>
          <section className="login-story">
            <div className="brand">
              <img className="brand-mark" src="/logo.png" alt="" />
              Spool
              <span className="tag">{t("Local agent workspace")}</span>
            </div>
            <div>
              <span className="eyebrow">
                {t("Your projects. Your agents.")}
              </span>
              <h1>{t("Ideas, brought to life.")}</h1>
              <p>
                {t(
                  "Return to your projects from any device. Let your local agent keep going.",
                )}
              </p>
            </div>
            <span className="login-foot">
              {t("Private workspace · powered by your local CLI")}
            </span>
          </section>
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
  return (
    <div className="app">
      <Sidebar
        user={user}
        projects={projects}
        chats={chats}
        project={project}
        chatID={chatID}
        activeBackend={chat?.turns.length === 0 ? backend : undefined}
        open={sidebar}
        posting={posting}
        t={t}
        onClose={() => setSidebar(false)}
        onChat={openChat}
        onNew={newChat}
        onSettings={() => setAppearanceOpen(true)}
        onAvatar={() => setAvatarOpen(true)}
        onLogout={logout}
      />
      {sidebar && (
        <button
          className="scrim"
          aria-label={t("Close sidebar")}
          onClick={() => setSidebar(false)}
        />
      )}
      <main className="workspace">
        <header>
          <div className="heading">
            <button
              className="icon mobile"
              onClick={() => setSidebar(true)}
              aria-label={t("Open sidebar")}
            >
              <Menu size={20} />
            </button>
            <div className={chat ? "heading-copy has-agent" : "heading-copy"}>
              <div className="chat-heading">
                {chat && <AgentIcon backend={backend} size={17} />}
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
                  <AvatarStack people={online} />
                ) : (
                  <Users size={14} aria-hidden="true" />
                )}
                {t("{count} online", { count: online.length })}
              </summary>
              <div className="online-list">
                {online.map((u) => (
                  <div key={u.id}>
                    <UserAvatar person={u} size={26} />
                    <span
                      className={
                        u.state === "online" ? "online-dot" : "away-dot"
                      }
                    />
                    <span>{u.nickname}</span>
                    <small>
                      {u.state === "online" ? t("Online") : t("Away")}
                    </small>
                  </div>
                ))}
                {!online.length && <small>{t("Updating presence…")}</small>}
              </div>
            </details>
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
                const answer = parts
                  .filter(
                    (part) => part.role === "assistant" && part.kind === "text",
                  )
                  .map((part) => part.text)
                  .join("");
                const activities = parts.filter(
                  (part) =>
                    part.role !== "user" &&
                    (part.kind === "tool_use" ||
                      part.kind === "tool_result" ||
                      part.kind === "thinking"),
                );
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
                        {userText && (
                          <div className="user-bubble">
                            <MarkdownMessage>{userText}</MarkdownMessage>
                          </div>
                        )}
                      </div>
                    </div>
                    {activities.length ? (
                      <ToolActivityList
                        parts={activities}
                        backend={turn.backend}
                        t={t}
                        live={turn.status === "running"}
                        summary={workSummary(turn, t, clock, effortLabel)}
                      />
                    ) : (
                      <div className="work-label">
                        {workSummary(turn, t, clock, effortLabel)}
                      </div>
                    )}
                    {answer && (
                      <div className="assistant">
                        <div className="assistant-text">
                          <MarkdownMessage>{answer}</MarkdownMessage>
                        </div>
                      </div>
                    )}
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
                project
                  ? t("Message {agent}", {
                      agent: backend === "codex" ? "Codex" : "Claude",
                    })
                  : t("Waiting for project access")
              }
              value={draft}
              disabled={!composerWritable}
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
                        agents={agents}
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
      {appearance}
      {avatarOpen && (
        <AvatarDialog
          user={user}
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
) {
  const duration =
    turn.status === "running"
      ? working(turn, t, now)
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
createRoot(document.getElementById("root")!).render(<App />);
