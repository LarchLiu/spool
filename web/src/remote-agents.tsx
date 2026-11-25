import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import {
  ArrowLeft,
  ChevronRight,
  Folder,
  History,
  Server as ServerIcon,
  X,
} from "lucide-react";
import { api } from "./api";
import { ApprovalCard } from "./approval-card";
import { chooseModel, type ModelSelection } from "./model-selection";
import { MarkdownMessage } from "./markdown-message";
import { TaskActivity } from "./tool-activity";
import type { Translator } from "./i18n";
import type { Agent, ApprovalRequest, Project, ToolSummary } from "./types";

type Server = {
  id: string;
  name: string;
  summary?: Project["summary"];
  can_create?: boolean;
};
type RemoteTarget = { server: Server; project: Project; conversation?: string };
export type RemoteTask = {
  task_id: string;
  request_id: string;
  conversation_id: string;
  turn_id?: string;
  project_id: string;
  project_name?: string;
  agent: "codex" | "claude";
  model?: string;
  reasoning_effort?: string;
  status: string;
  text?: string;
  progress?: string;
  truncated?: boolean;
  error?: string;
  pending_approval?: ApprovalRequest;
};
type RemoteCall = {
  server: string;
  request_id: string;
  message?: string;
  task: RemoteTask;
};
type HistoryPage = {
  conversation_id: string;
  turns: {
    id: string;
    messages: { role: string; text: string }[];
    truncated: boolean;
  }[];
  next: string;
};

const noServers: Server[] = [];
// A mention is a composer control. Selecting a target removes only this token;
// the destination is sent in API fields and never becomes part of the prompt.
export function remoteMention(draft: string) {
  return /(?:^|\s)@([^\s]*)$/.exec(draft)?.[1] ?? null;
}
export function consumeRemoteMention(draft: string) {
  return draft.replace(/(^|\s)@[^\s]*$/, "$1").trimEnd();
}
const running = (task: RemoteTask) =>
  ["running", "awaiting_approval"].includes(task.status);
const mergeCalls = (latest: RemoteCall[], prior: RemoteCall[]) =>
  [
    ...latest,
    ...prior.filter(
      (item) =>
        !latest.some((value) => value.task.task_id === item.task.task_id),
    ),
  ].slice(0, 20);

export function useRemoteAgents({
  account,
  originChat,
  scope,
  enabled,
  draft,
  focused,
  disabled,
  onDraft,
  onError,
}: {
  account: string;
  originChat: string;
  scope: string;
  enabled: boolean;
  draft: string;
  focused: boolean;
  disabled: boolean;
  onDraft: (value: string) => void;
  onError: (error: unknown) => void;
}) {
  // The list belongs to the account that loaded it. It is kept while the
  // composer is briefly not writable or a refresh fails, so a passing state
  // does not look like "no trusted servers" to the MCP selection.
  const [directory, setDirectory] = useState<{
    account: string;
    servers: Server[];
  }>();
  const serversLoaded = !!account && directory?.account === account;
  const servers = serversLoaded && directory ? directory.servers : noServers;
  const [menuServer, setMenuServer] = useState<Server>();
  const [projects, setProjects] = useState<Project[]>([]);
  const [target, setTarget] = useState<RemoteTarget>();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [selection, setSelection] = useState<ModelSelection>({
    backend: "codex",
    model: "",
    effort: "",
  });
  const [calls, setCalls] = useState<RemoteCall[]>([]);
  const [history, setHistory] = useState<HistoryPage>();
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  const [active, setActive] = useState({ key: "", index: 0 });
  const lifetime = useRef(new AbortController());
  const adopted = useRef<string | undefined>(undefined);
  const current = useRef({ account, originChat, scope });
  const retry = useRef({ key: "", id: "" });
  const operation = <T,>(
    operation: string,
    fields: Record<string, unknown> = {},
    signal = lifetime.current.signal,
  ) =>
    api<T>(
      "/federation/agents",
      { operation, origin_chat: originChat, ...fields },
      signal,
    );
  const fail = (error: unknown) => {
    if (
      !lifetime.current.signal.aborted &&
      !(error instanceof DOMException && error.name === "AbortError")
    )
      onError(error);
  };
  useLayoutEffect(() => {
    const keep =
      adopted.current === originChat &&
      current.current.account === account &&
      current.current.scope === scope;
    adopted.current = undefined;
    if (!keep) {
      lifetime.current.abort();
      lifetime.current = new AbortController();
      setTarget(undefined);
      setAgents([]);
      setCalls([]);
      setHistory(undefined);
      setBusy(false);
      retry.current = { key: "", id: "" };
    }
    setMenuServer(undefined);
    setDismissed(false);
    current.current = { account, originChat, scope };
  }, [account, originChat, scope]);
  useEffect(() => () => lifetime.current.abort(), []);
  const query = remoteMention(draft);
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    void operation<Server[]>("servers", {}, controller.signal)
      .then((value) => {
        if (!controller.signal.aborted)
          setDirectory({ account, servers: value });
      })
      .catch(fail);
    return () => controller.abort();
  }, [enabled, account, focused && query !== null]);
  const rows = (menuServer ? projects : servers).filter((item) =>
    `${item.name} ${"summary" in item ? item.summary?.text : ""}`
      .toLocaleLowerCase()
      .includes((query ?? "").toLocaleLowerCase()),
  );
  // Ordinary text may end in "@word". The server list opens only when it has
  // something to offer; otherwise Enter and the send button keep their meaning.
  // A chosen server keeps its project list open, also while loading or empty.
  const open =
    enabled &&
    focused &&
    !disabled &&
    !dismissed &&
    query !== null &&
    !!servers.length &&
    (!!menuServer || !!rows.length);
  useEffect(() => {
    setDismissed(false);
  }, [draft]);
  useEffect(() => {
    if (query === null) setMenuServer(undefined);
  }, [query]);
  useEffect(() => {
    if (!open || !menuServer) return;
    const controller = new AbortController();
    setProjects([]);
    setLoading(true);
    void operation<Project[]>(
      "projects",
      { server: menuServer.id },
      controller.signal,
    )
      .then((value) => {
        if (!controller.signal.aborted) setProjects(value);
      })
      .catch(fail)
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [open, menuServer?.id]);
  useEffect(() => {
    setAgents([]);
    if (!target) return;
    const controller = new AbortController();
    void operation<Agent[]>(
      "agents",
      { server: target.server.id, project: target.project.id },
      controller.signal,
    )
      .then((value) => {
        if (controller.signal.aborted) return;
        setAgents(value);
        const first = value.find((item) => item.installed && item.enabled);
        if (first && !target.conversation)
          setSelection(
            chooseModel(
              first,
              first.catalog.defaultModel ||
                first.catalog.models[0]?.model ||
                "",
            ),
          );
      })
      .catch(fail);
    return () => controller.abort();
  }, [target?.server.id, target?.project.id, !!target?.conversation]);
  useEffect(() => {
    if (!enabled || !originChat || !servers.length) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    // An unreachable server would otherwise fail every 1.5 seconds and keep
    // replacing the error banner. Each distinct failure is reported once until
    // polling works again, and a failing server is asked less and less often.
    const reported = new Set<string>();
    const paused = new Map<string, { until: number; wait: number }>();
    let delay = 1500;
    const report = (error: unknown) => {
      if (controller.signal.aborted) return;
      const text = error instanceof Error ? error.message : String(error);
      if (reported.has(text)) return;
      reported.add(text);
      fail(error);
    };
    const refresh = async () => {
      let quiet = true;
      try {
        let values = await operation<RemoteCall[]>(
          "calls",
          {},
          controller.signal,
        );
        delay = 1500;
        values = await Promise.all(
          values.map(async (call) => {
            if (!running(call.task)) return call;
            const pause = paused.get(call.server);
            if (pause && Date.now() < pause.until) {
              quiet = false;
              return call;
            }
            try {
              const task = await operation<RemoteTask>(
                "task",
                { server: call.server, task_id: call.task.task_id },
                controller.signal,
              );
              paused.delete(call.server);
              return { ...call, task };
            } catch (error) {
              quiet = false;
              const wait = Math.min((pause?.wait ?? 1500) * 2, 15000);
              paused.set(call.server, { until: Date.now() + wait, wait });
              report(error);
              return call;
            }
          }),
        );
        if (!controller.signal.aborted)
          setCalls((prior) => mergeCalls(values, prior));
      } catch (error) {
        quiet = false;
        delay = Math.min(delay * 2, 15000);
        report(error);
      }
      if (quiet) {
        reported.clear();
        paused.clear();
      }
      if (!controller.signal.aborted) timer = setTimeout(refresh, delay);
    };
    void refresh();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [enabled, account, originChat, scope, !!servers.length]);
  const key = `${menuServer?.id ?? ""}\n${query}\n${rows.map((item) => item.id).join(",")}`;
  const index =
    active.key === key
      ? Math.min(active.index, Math.max(0, rows.length - 1))
      : 0;
  const focus = () =>
    requestAnimationFrame(() =>
      document.getElementById("chat-message")?.focus(),
    );
  const select = (item = rows[index]) => {
    if (!item) return;
    if (!menuServer) {
      setMenuServer(item as Server);
      onDraft(draft.replace(/@[^\s]*$/, "@"));
    } else {
      if (!item.can_create) return;
      setTarget({ server: menuServer, project: item as Project });
      setHistory(undefined);
      onDraft(consumeRemoteMention(draft));
      setMenuServer(undefined);
    }
    focus();
  };
  const back = () => {
    setMenuServer(undefined);
    onDraft(draft.replace(/@[^\s]*$/, "@"));
    focus();
  };
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (!open) return false;
    // Left and right move the caret unless they step between the two lists.
    if (
      event.key === (menuServer ? "ArrowRight" : "ArrowLeft") ||
      !(
        [
          "ArrowDown",
          "ArrowUp",
          "Escape",
          "Tab",
          "ArrowLeft",
          "ArrowRight",
        ].includes(event.key) ||
        (event.key === "Enter" &&
          !event.shiftKey &&
          !event.metaKey &&
          !event.altKey)
      )
    )
      return false;
    event.preventDefault();
    event.stopPropagation();
    if (event.key === "Escape") setDismissed(true);
    else if (event.key === "ArrowLeft") back();
    else if (event.key === "ArrowUp" || event.key === "ArrowDown") {
      if (rows.length)
        setActive({
          key,
          index:
            (index + (event.key === "ArrowDown" ? 1 : rows.length - 1)) %
            rows.length,
        });
    } else select();
    return true;
  };
  const send = async (message: string, origin = originChat) => {
    if (!target) throw new Error("No remote target selected");
    if (!target.project.can_create) throw new Error("Read-only");
    const fields = {
      server: target.server.id,
      project: target.project.id,
      agent: selection.backend,
      model: selection.model,
      reasoning_effort: selection.effort,
      conversation_id: target.conversation ?? "",
      message,
      origin_chat: origin,
    };
    const key = JSON.stringify(fields);
    if (retry.current.key !== key)
      retry.current = { key, id: crypto.randomUUID() };
    const source = current.current;
    setBusy(true);
    try {
      const task = await operation<RemoteTask>("delegate", {
        ...fields,
        request_id: retry.current.id,
        wait_seconds: 1,
      });
      if (current.current === source) {
        setCalls((prior) =>
          mergeCalls(
            [
              {
                server: target.server.id,
                request_id: task.request_id,
                message,
                task,
              },
            ],
            prior,
          ),
        );
        setTarget({ ...target, conversation: task.conversation_id });
        retry.current = { key: "", id: "" };
      }
      return task;
    } finally {
      if (current.current === source) setBusy(false);
    }
  };
  const resume = async (call: RemoteCall) => {
    try {
      const source = current.current;
      const projects = await operation<Project[]>("projects", {
        server: call.server,
      });
      const project = projects.find((item) => item.id === call.task.project_id);
      if (!project || current.current !== source) return;
      if (!project.can_create) throw new Error("Read-only");
      setTarget({
        server: servers.find((item) => item.id === call.server) ?? {
          id: call.server,
          name: call.server.slice(0, 12),
        },
        project,
        conversation: call.task.conversation_id,
      });
      setSelection({
        backend: call.task.agent,
        model: call.task.model ?? "",
        effort: call.task.reasoning_effort ?? "",
      });
      focus();
    } catch (error) {
      fail(error);
    }
  };
  const readHistory = async (call: RemoteCall, before?: string) => {
    try {
      const source = current.current;
      const value = await operation<HistoryPage>("history", {
        server: call.server,
        conversation_id: call.task.conversation_id,
        before,
      });
      if (current.current === source) setHistory(value);
    } catch (error) {
      fail(error);
    }
  };
  const chosen = agents.find((item) => item.backend === selection.backend);
  return {
    servers,
    serversLoaded,
    target,
    clearTarget: () => setTarget(undefined),
    newConversation: () =>
      target && setTarget({ ...target, conversation: undefined }),
    agents,
    selection,
    setSelection,
    busy,
    ready:
      !!chosen?.enabled && !!chosen?.installed && !!target?.project.can_create,
    open,
    menuServer,
    rows,
    index,
    loading,
    select,
    back,
    onKeyDown,
    onActive: (index: number) => setActive({ key, index }),
    send,
    adoptOrigin: (origin: string) => {
      adopted.current = origin;
    },
    calls,
    history,
    resume,
    readHistory,
    approve: (call: RemoteCall, approval: Record<string, unknown>) =>
      operation("approve", {
        server: call.server,
        task_id: call.task.task_id,
        approval,
      }),
    cancel: async (call: RemoteCall) => {
      try {
        const source = current.current;
        const task = await operation<RemoteTask>("cancel", {
          server: call.server,
          task_id: call.task.task_id,
        });
        if (current.current === source)
          setCalls((prior) => mergeCalls([{ ...call, task }], prior));
      } catch (error) {
        fail(error);
      }
    },
  };
}
export type RemoteAgents = ReturnType<typeof useRemoteAgents>;

export function RemoteTargetMenu({
  remote,
  t,
}: {
  remote: RemoteAgents;
  t: Translator;
}) {
  const active = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    active.current?.scrollIntoView({ block: "nearest" });
  }, [remote.index, remote.rows.length]);
  return (
    <div
      className="slash-command-menu remote-target-menu"
      id="remote-target-menu"
      role="listbox"
      aria-label={t(remote.menuServer ? "Project" : "Server")}
    >
      {remote.menuServer && (
        <button
          type="button"
          onMouseDown={(event) => event.preventDefault()}
          onClick={remote.back}
        >
          <ArrowLeft size={14} />
          <span>{remote.menuServer.name}</span>
        </button>
      )}
      {remote.loading && remote.menuServer ? (
        <p className="slash-command-empty" role="status">
          {t("Loading conversation…")}
        </p>
      ) : (
        !remote.rows.length && (
          <p className="slash-command-empty" role="status">
            {t("No authorized remote projects.")}
          </p>
        )
      )}
      {remote.rows.map((item, index) => (
        <button
          key={item.id}
          ref={index === remote.index ? active : undefined}
          id={`remote-target-${index}`}
          type="button"
          role="option"
          disabled={!!remote.menuServer && !item.can_create}
          aria-selected={index === remote.index}
          onMouseDown={(event) => event.preventDefault()}
          onMouseEnter={() => remote.onActive(index)}
          onClick={() => remote.select(item)}
        >
          {remote.menuServer ? <Folder size={16} /> : <ServerIcon size={16} />}
          <span className="slash-command-content">
            <strong>{item.name}</strong>
            {"summary" in item && item.summary?.text && (
              <small>{item.summary.text}</small>
            )}
            {remote.menuServer && !item.can_create && (
              <small>{t("Read-only")}</small>
            )}
          </span>
          {!remote.menuServer && (
            <ChevronRight
              size={16}
              className="remote-server-next"
              aria-hidden="true"
            />
          )}
        </button>
      ))}
    </div>
  );
}

export function RemoteTargetChip({
  remote,
  t,
}: {
  remote: RemoteAgents;
  t: Translator;
}) {
  if (!remote.target) return null;
  return (
    <div className="remote-target-chip">
      <span
        title={`@${remote.target.server.name}/${remote.target.project.name}`}
      >
        @{remote.target.server.name}/{remote.target.project.name}
      </span>
      {remote.target.conversation && (
        <button
          type="button"
          disabled={remote.busy}
          onClick={remote.newConversation}
        >
          {t("New remote conversation")}
        </button>
      )}
      <button
        type="button"
        className="icon"
        disabled={remote.busy}
        aria-label={t("Remove remote target")}
        onClick={remote.clearTarget}
      >
        <X size={14} />
      </button>
    </div>
  );
}

export function RemoteCalls({
  remote,
  t,
  onError,
  onContext,
}: {
  remote: RemoteAgents;
  t: Translator;
  onError: (error: unknown) => void;
  onContext: (value: string) => void;
}) {
  if (!remote.calls.length) return null;
  return (
    <section className="remote-calls" aria-label={t("Remote calls")}>
      <h3>{t("Remote calls")}</h3>
      {[...remote.calls].reverse().map((call) => (
        <article className="remote-agent-task" key={call.task.task_id}>
          <TaskActivity
            t={t}
            part={{
              id: call.task.task_id,
              role: "assistant",
              kind: "tool_use",
              name: `@${remote.servers.find((item) => item.id === call.server)?.name || call.server.slice(0, 12)}/${call.task.project_name || call.task.project_id.slice(0, 12)}`,
              text: "",
              tool: {
                kind: "task",
                status: running(call.task)
                  ? "running"
                  : ["completed", "failed", "stopped"].includes(
                        call.task.status,
                      )
                    ? (call.task.status as ToolSummary["status"])
                    : "unknown",
                version: String(call.task.status),
                bytes: 0,
                task: {
                  id: call.task.task_id,
                  agent_type: [
                    call.task.agent,
                    call.task.model,
                    call.task.reasoning_effort,
                  ]
                    .filter(Boolean)
                    .join(" · "),
                  prompt: call.message,
                  summary: running(call.task) ? call.task.progress : undefined,
                },
              },
            }}
            outcome={t(
              call.task.status === "running"
                ? "Running"
                : call.task.status === "completed"
                  ? "Completed"
                  : call.task.status === "stopped"
                    ? "Stopped"
                    : call.task.status === "failed"
                      ? "Failed"
                      : call.task.status === "awaiting_approval"
                        ? "Approval required"
                        : "Interrupted",
            )}
          />
          {call.task.status === "completed" && call.task.text && (
            <MarkdownMessage t={t}>{call.task.text}</MarkdownMessage>
          )}
          {call.task.truncated && (
            <p>{t("Output truncated. Read remote history for details.")}</p>
          )}
          {call.task.error && <p className="error">{call.task.error}</p>}
          {call.task.pending_approval && (
            <ApprovalCard
              key={call.task.pending_approval.id}
              request={call.task.pending_approval}
              chatID={call.task.conversation_id}
              turnID={call.task.turn_id ?? ""}
              t={t}
              onError={onError}
              onDecision={(approval) => remote.approve(call, approval)}
            />
          )}
          <div className="remote-agent-actions">
            {running(call.task) ? (
              <button type="button" onClick={() => void remote.cancel(call)}>
                {t("Stop response")}
              </button>
            ) : (
              <button
                type="button"
                disabled={remote.busy}
                onClick={() => void remote.resume(call)}
              >
                {t("Continue conversation")}
              </button>
            )}
            <button type="button" onClick={() => void remote.readHistory(call)}>
              <History size={13} />
              {t("Remote history")}
            </button>
            {call.task.status === "completed" && call.task.text && (
              <button
                type="button"
                onClick={() => {
                  const text = call.task.text!.slice(0, 16000);
                  onContext(
                    JSON.stringify({
                      remote_server: call.server,
                      remote_conversation: call.task.conversation_id,
                      remote_task: call.task.task_id,
                      reference_output: text,
                      truncated:
                        !!call.task.truncated ||
                        text.length < call.task.text!.length,
                    }),
                  );
                }}
              >
                {t("Use result as context")}
              </button>
            )}
          </div>
        </article>
      ))}
      {remote.history && (
        <section className="remote-agent-history">
          <h3>{t("Remote history")}</h3>
          {remote.history.turns.map((turn) => (
            <article key={turn.id}>
              {turn.messages.map((message, index) => (
                <div key={index}>
                  <small>{message.role}</small>
                  <pre>{message.text}</pre>
                </div>
              ))}
              {turn.truncated && (
                <small>
                  {t("Output truncated. Read remote history for details.")}
                </small>
              )}
            </article>
          ))}
          {remote.history.next && (
            <button
              type="button"
              onClick={() => {
                const call = remote.calls.find(
                  (item) =>
                    item.task.conversation_id ===
                    remote.history?.conversation_id,
                );
                if (call) void remote.readHistory(call, remote.history!.next);
              }}
            >
              {t("Load older messages")}
            </button>
          )}
        </section>
      )}
    </section>
  );
}
