import {
  useEffect,
  useEffectEvent,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  ArrowDown,
  ArrowUp,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronUp,
  CircleAlert,
  Copy,
  File,
  GitBranch,
  LoaderCircle,
  RefreshCw,
  Tag,
  X,
} from "lucide-react";
import { api } from "./api";
import type { Translator } from "./i18n";
import { FilePanelTabs } from "./file-panel-tabs";
import { FilePreviewSplit } from "./file-preview-split";
import { FilePanelButton } from "./file-panel-button";
import { CustomSelect } from "./custom-select";
import { AutosizeTextarea } from "./autosize-textarea";
import { useGitHistory } from "./git-history";
import { copyMessageText } from "./copy-text";
import {
  unifiedDiffLines,
  type GitView,
  type GitDiff,
  type GitFile,
  type GitCommit,
} from "./git-panel-state";

function GitCommitBadges({ commit, t }: { commit: GitCommit; t: Translator }) {
  return (
    <>
      {commit.tags?.map((tag) => (
        <span className="git-history-badge git-history-tag" key={tag} title={tag}>
          <Tag size={10} aria-hidden="true" />
          {tag}
        </span>
      ))}
      {commit.unpushed && (
        <span
          className="git-history-badge"
          aria-label={t("Not pushed")}
          title={t("Not pushed")}
        >
          <ArrowUp size={11} aria-hidden="true" />
        </span>
      )}
    </>
  );
}

function GitCommitSHA({ sha, t }: { sha: string; t: Translator }) {
  const [feedback, setFeedback] = useState<"copied" | "failed">();
  const request = useRef(0);
  useEffect(
    () => () => {
      request.current++;
    },
    [],
  );
  useEffect(() => {
    if (!feedback) return;
    const timer = window.setTimeout(() => setFeedback(undefined), 2000);
    return () => clearTimeout(timer);
  }, [feedback]);
  const label = t(
    feedback === "copied"
      ? "Copied"
      : feedback === "failed"
        ? "Could not copy SHA"
        : "Copy SHA",
  );
  const Icon =
    feedback === "copied" ? Check : feedback === "failed" ? CircleAlert : Copy;
  return (
    <div className="git-commit-sha">
      <code>{sha}</code>
      <FilePanelButton
        className="icon"
        aria-label={label}
        title={label}
        onClick={async () => {
          const current = ++request.current;
          try {
            await copyMessageText(sha);
            if (request.current === current) setFeedback("copied");
          } catch {
            if (request.current === current) setFeedback("failed");
          }
        }}
      >
        <Icon size={13} aria-hidden="true" />
      </FilePanelButton>
      <span className="message-copy-status" role="status">
        {feedback ? label : ""}
      </span>
    </div>
  );
}

export function UnifiedDiff({ patch, t }: { patch: string; t: Translator }) {
  const lines = useMemo(() => unifiedDiffLines(patch), [patch]);
  if (!lines.length)
    return <p className="file-panel-empty">{t("No textual changes.")}</p>;
  return (
    <pre className="git-diff-source">
      {lines.map((line, index) => (
        <span key={index} className={`git-diff-line ${line.kind}`}>
          <span className="git-line-number" aria-hidden="true">
            {line.old ?? ""}
          </span>
          <span className="git-line-number" aria-hidden="true">
            {line.next ?? ""}
          </span>
          <span className="git-line-sign" aria-hidden="true">
            {line.kind === "add" ? "+" : line.kind === "delete" ? "−" : " "}
          </span>
          <code>{line.text || " "}</code>
        </span>
      ))}
    </pre>
  );
}

function DiscardGitDialog({
  paths,
  t,
  onClose,
  onConfirm,
}: {
  paths: string[];
  t: Translator;
  onClose: () => void;
  onConfirm: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const id = useId();
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  return (
    <dialog
      ref={dialog}
      className="git-discard-dialog"
      aria-labelledby={id}
      aria-describedby={`${id}-description`}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <div className="dialog-heading">
        <h2 id={id}>{t("Discard changes?")}</h2>
        <FilePanelButton
          className="icon"
          aria-label={t("Close")}
          onClick={onClose}
        >
          <X size={18} />
        </FilePanelButton>
      </div>
      <p id={`${id}-description`}>
        {t(
          "Tracked files will be restored to their staged version. Untracked files will be deleted. Staged changes are kept. This cannot be undone.",
        )}
      </p>
      <ul>
        {paths.map((path) => (
          <li key={path}>{path}</li>
        ))}
      </ul>
      <div className="git-discard-actions">
        <FilePanelButton autoFocus onClick={onClose}>
          {t("Cancel")}
        </FilePanelButton>
        <FilePanelButton className="destructive" onClick={onConfirm}>
          {t("Discard changes")}
        </FilePanelButton>
      </div>
    </dialog>
  );
}

export function GitPanel({
  base,
  scope,
  workspace,
  active,
  ready,
  refresh,
  t,
}: {
  base: string;
  scope: string;
  workspace?: string;
  active: boolean;
  ready: boolean;
  refresh: number;
  t: Translator;
}) {
  const [mode, setMode] = useState<"changes" | "history">("changes");
  const [commit, setCommit] = useState("");
  const [changesData, setData] = useState<GitView>();
  const [changesError, setError] = useState("");
  const [changesLoading, setLoading] = useState(false);
  const [selected, setSelected] = useState("");
  const [side, setSide] = useState<"staged" | "unstaged" | "">("");
  const [diff, setDiff] = useState<GitDiff>();
  const [diffError, setDiffError] = useState("");
  const [retry, setRetry] = useState(0);
  const [diffRefresh, setDiffRefresh] = useState(0);
  const [diffPoll, setDiffPoll] = useState(0);
  const reloadDiff = useRef<(() => void) | undefined>(undefined);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const [operation, setOperation] = useState("");
  const [operationError, setOperationError] = useState("");
  const [composer, setComposer] = useState(false);
  const [remoteAction, setRemoteAction] = useState<"push" | "fetch">("push");
  const [message, setMessage] = useState("");
  const [discard, setDiscard] = useState<{
    paths: string[];
    snapshot: GitView;
  }>();
  useEffect(() => {
    if (!active) setDiscard(undefined);
  }, [active]);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const query = (view: string, path?: string) =>
    `/git?${new URLSearchParams({
      view,
      ...(workspace ? { workspace } : {}),
      ...(commit ? { commit } : {}),
      ...(path ? { path } : {}),
      ...(path && !commit && side ? { side } : {}),
    })}`;
  const listURL = query(mode === "history" && !commit ? "history" : "changes");
  const diffURL = selected ? query("diff", selected) : "";
  const historyList = mode === "history" && !commit;
  const history = useGitHistory(
    base,
    scope,
    query("history"),
    active && ready && historyList,
    refresh,
    retry,
    t,
  );
  const data = historyList ? history.data : changesData;
  const error = historyList ? history.error : changesError;
  const loading = historyList ? history.loading : changesLoading;
  const historyEnd = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const marker = historyEnd.current;
    if (
      !active ||
      !historyList ||
      !marker ||
      !data?.next_cursor ||
      history.moreError ||
      loading ||
      history.moreLoading
    )
      return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) history.loadMore();
      },
      {
        root: marker.closest(".file-tree-pane"),
        rootMargin: "0px 0px 80px 0px",
      },
    );
    observer.observe(marker);
    return () => observer.disconnect();
  }, [
    active,
    historyList,
    data?.next_cursor,
    history.moreError,
    history.moreLoading,
    history.loadMore,
    loading,
  ]);
  const validateSelection = useEffectEvent((files: GitFile[]) => {
    setSelected((path) =>
      files.some(
        (file) =>
          file.path === path &&
          (commit ||
            !side ||
            (side === "staged" ? file.staged : isUnstaged(file))),
      )
        ? path
        : "",
    );
  });
  // Chat changes update the transport without invalidating directory data.
  const readChanges = useEffectEvent((url: string, signal: AbortSignal) =>
    api<GitView>(base + url, undefined, signal),
  );
  const readDiff = useEffectEvent((url: string, signal: AbortSignal) =>
    api<GitDiff>(base + url, undefined, signal),
  );
  useEffect(() => {
    if (!active || !ready || historyList) return;
    const controller = new AbortController();
    let pending = false;
    const load = async (updatePreview = false) => {
      if (pending || controller.signal.aborted) return;
      pending = true;
      setLoading(true);
      try {
        const next = await readChanges(listURL, controller.signal);
        if (controller.signal.aborted) return;
        setData(next);
        setError("");
        validateSelection(next.files);
        if (updatePreview) setDiffPoll((value) => value + 1);
      } catch (err) {
        if (!controller.signal.aborted) {
          setData(undefined);
          setError(err instanceof Error ? err.message : t("Request failed"));
        }
      } finally {
        pending = false;
        if (!controller.signal.aborted) setLoading(false);
      }
    };
    void load();
    const poll = () => {
      if (document.visibilityState === "visible") void load(true);
    };
    const timer = window.setInterval(poll, 10_000);
    window.addEventListener("focus", poll);
    return () => {
      controller.abort();
      clearInterval(timer);
      window.removeEventListener("focus", poll);
    };
  }, [scope, active, ready, historyList, listURL, refresh, retry, t]);
  useEffect(() => {
    if (!active || !ready || !diffURL) return;
    const controller = new AbortController();
    setDiffError("");
    let pending = false;
    const load = async () => {
      if (pending || controller.signal.aborted) return;
      pending = true;
      try {
        const value = await readDiff(diffURL, controller.signal);
        if (controller.signal.aborted) return;
        setDiff((previous) =>
          previous?.path === value.path &&
          previous.patch === value.patch &&
          previous.binary === value.binary &&
          previous.truncated === value.truncated
            ? previous
            : value,
        );
        setDiffError("");
      } catch (err) {
        if (!controller.signal.aborted)
          setDiffError(
            err instanceof Error ? err.message : t("Request failed"),
          );
      } finally {
        pending = false;
      }
    };
    reloadDiff.current = () => {
      void load();
    };
    void load();
    return () => {
      controller.abort();
      reloadDiff.current = undefined;
    };
  }, [scope, active, ready, diffURL, diffRefresh, t]);
  useEffect(() => {
    reloadDiff.current?.();
  }, [diffPoll]);
  function chooseMode(next: "changes" | "history") {
    if (next === mode && !commit) return;
    setMode(next);
    setCommit("");
    setSelected("");
    setSide("");
    setData(undefined);
    setDiff(undefined);
    setDiscard(undefined);
  }
  const files = data?.files || [];
  const additions = files.reduce((sum, file) => sum + file.additions, 0);
  const deletions = files.reduce((sum, file) => sum + file.deletions, 0);
  const staged = files.filter((file) => file.staged);
  const pushRemote = data?.push_remote ?? data?.remote;
  const pushBranch = data?.push_remote_branch ?? data?.remote_branch;
  const pushUpstream = data?.push_upstream ?? data?.upstream;
  const targetRemote = remoteAction === "fetch" ? data?.remote : pushRemote;
  const targetBranch =
    remoteAction === "fetch" ? data?.remote_branch : pushBranch;
  const canRemote = !!(
    active &&
    !operation &&
    data?.writable &&
    data.remote_allowed &&
    (data.remote || pushRemote)
  );
  const canFetch = !!(canRemote && data?.remote);
  const canPush = !!(canRemote && pushRemote && data?.branch && data.commit);
  const pushLabel = t(pushUpstream ? "Push" : "Publish branch");
  const remoteLabel = remoteAction === "fetch" ? t("Fetch") : pushLabel;
  function isUnstaged(file: GitFile) {
    return (
      file.unstaged ??
      (!file.staged || (file.status.length === 2 && file.status[1] !== " "))
    );
  }
  async function mutate(action: string, paths?: string[], snapshot = data) {
    if (operation || !data?.writable) return;
    if (!snapshot) return;
    setOperation(action);
    setOperationError("");
    try {
      await api(base + query("changes"), {
        action,
        paths,
        message: action === "commit" ? message : undefined,
        branch: snapshot.branch || "",
        head: snapshot.commit || "",
        index: snapshot.index,
        remote:
          action === "push"
            ? (snapshot.push_remote ?? snapshot.remote)
            : snapshot.remote,
        remote_branch:
          action === "push"
            ? (snapshot.push_remote_branch ?? snapshot.remote_branch)
            : snapshot.remote_branch,
      });
      if (!mounted.current) return;
      if (action === "commit") {
        setMessage("");
        setComposer(false);
      }
    } catch (err) {
      if (!mounted.current) return;
      setOperationError(
        err instanceof Error ? err.message : t("Request failed"),
      );
    } finally {
      if (mounted.current) {
        setOperation("");
        setRetry((value) => value + 1);
        setDiffRefresh((value) => value + 1);
      }
    }
  }
  function group(
    label: string,
    items: GitFile[],
    groupSide: "staged" | "unstaged" | "" = "",
  ) {
    if (!items.length) return null;
    return (
      <div className="git-file-group">
        <div className="git-group-header">
          <FilePanelButton
            className="git-group-toggle"
            aria-expanded={!collapsed[label]}
            onClick={() =>
              setCollapsed((old) => ({ ...old, [label]: !old[label] }))
            }
          >
            <ChevronDown
              size={12}
              className={collapsed[label] ? "collapsed" : ""}
            />
            {label}
            <small>{items.length}</small>
          </FilePanelButton>
          {groupSide && (
            <div className="git-group-actions">
              <FilePanelButton
                className="git-stage-box"
                aria-label={t(
                  groupSide === "staged" ? "Unstage all" : "Stage all",
                )}
                title={t(groupSide === "staged" ? "Unstage all" : "Stage all")}
                disabled={!!operation || !data?.writable}
                onClick={() =>
                  void mutate(
                    groupSide === "staged" ? "unstage" : "stage",
                    items.map((file) => file.path),
                  )
                }
              >
                {groupSide === "staged" ? "−" : "+"}
              </FilePanelButton>
              {groupSide === "unstaged" && (
                <FilePanelButton
                  className="git-stage-box git-discard-box"
                  aria-label={t("Discard all changes")}
                  title={t("Discard all changes")}
                  disabled={!!operation || !data?.writable}
                  onClick={() =>
                    data &&
                    setDiscard({
                      paths: items.map((file) => file.path),
                      snapshot: data,
                    })
                  }
                >
                  −
                </FilePanelButton>
              )}
            </div>
          )}
        </div>
        {!collapsed[label] &&
          items.map((file) => (
            <div className="git-file-entry" key={file.path}>
              <FilePanelButton
                className="git-file-row"
                aria-pressed={selected === file.path && side === groupSide}
                title={`${file.previous_path ? file.previous_path + " → " : ""}${file.path} · ${file.status}${file.staged ? " · " + t("Staged") : ""}`}
                onClick={() => {
                  if (selected === file.path && side === groupSide) return;
                  setSelected(file.path);
                  setSide(groupSide);
                  setDiff(undefined);
                  setDiffError("");
                }}
              >
                <span
                  className={`git-file-status ${file.untracked ? "untracked" : ""}`}
                >
                  {file.untracked ? (
                    "A"
                  ) : groupSide === "staged" ? (
                    <>
                      <span data-index-status={file.status[0]}>
                        {file.status[0]}
                      </span>
                      {file.status.slice(1)}
                    </>
                  ) : (
                    file.status
                  )}
                </span>
                <span className="git-file-name">
                  {file.path.split("/").pop()}
                </span>
                <span className="git-file-directory">
                  {file.path.includes("/")
                    ? file.path.slice(0, file.path.lastIndexOf("/"))
                    : ""}
                </span>
                <span className="git-file-stats">
                  {file.untracked ? null : file.binary ? (
                    t("Binary file")
                  ) : (
                    <>
                      <b>
                        +
                        {groupSide
                          ? (file[`${groupSide}_additions`] ?? file.additions)
                          : file.additions}
                      </b>
                      <i>
                        −
                        {groupSide
                          ? (file[`${groupSide}_deletions`] ?? file.deletions)
                          : file.deletions}
                      </i>
                    </>
                  )}
                </span>
              </FilePanelButton>
              {groupSide && (
                <div className="git-file-actions">
                  <FilePanelButton
                    className="git-stage-box"
                    aria-label={t(
                      groupSide === "staged"
                        ? "Unstage {path}"
                        : "Stage {path}",
                      { path: file.path },
                    )}
                    title={t(
                      groupSide === "staged"
                        ? "Unstage {path}"
                        : "Stage {path}",
                      { path: file.path },
                    )}
                    disabled={!!operation || !data?.writable}
                    onClick={() =>
                      void mutate(
                        groupSide === "staged" ? "unstage" : "stage",
                        [file.path],
                      )
                    }
                  >
                    {groupSide === "staged" ? "−" : "+"}
                  </FilePanelButton>
                  {groupSide === "unstaged" && (
                    <FilePanelButton
                      className="git-stage-box git-discard-box"
                      aria-label={t("Discard {path}", { path: file.path })}
                      title={t("Discard {path}", { path: file.path })}
                      disabled={!!operation || !data?.writable}
                      onClick={() =>
                        data &&
                        setDiscard({ paths: [file.path], snapshot: data })
                      }
                    >
                      −
                    </FilePanelButton>
                  )}
                </div>
              )}
            </div>
          ))}
      </div>
    );
  }
  return (
    <section className="git-panel" aria-label={t("Git panel")}>
      {discard && (
        <DiscardGitDialog
          paths={discard.paths}
          t={t}
          onClose={() => setDiscard(undefined)}
          onConfirm={() => {
            setDiscard(undefined);
            void mutate("discard", discard.paths, discard.snapshot);
          }}
        />
      )}
      <FilePanelTabs
        label={t("Git views")}
        value={mode}
        onChange={chooseMode}
        options={[
          { value: "changes", label: t("Changes") },
          { value: "history", label: t("History") },
        ]}
      />
      {operationError && (
        <div className="git-operation-notice" role="alert">
          <span className="git-operation-message">{operationError}</span>
          <FilePanelButton
            className="icon git-operation-dismiss"
            aria-label={t("Dismiss")}
            title={t("Dismiss")}
            onClick={() => setOperationError("")}
          >
            <X size={14} aria-hidden="true" />
          </FilePanelButton>
        </div>
      )}
      <FilePreviewSplit
        t={t}
        tree={
          <>
            {loading && !data && (
              <p className="file-panel-empty" role="status">
                <LoaderCircle size={14} className="stop-spinner" />{" "}
                {t("Loading Git information…")}
              </p>
            )}
            {!ready && (
              <p className="file-panel-notice">
                {t("Session directory is unavailable")}
              </p>
            )}
            {error && (
              <p className="file-panel-notice" role="alert">
                {error}{" "}
                <FilePanelButton onClick={() => setRetry((value) => value + 1)}>
                  {t("Retry")}
                </FilePanelButton>
              </p>
            )}
            {data && !data.repository && (
              <p className="file-panel-empty">
                {t("This directory is not a Git repository.")}
              </p>
            )}
            {data?.repository && (
              <>
                <div className="git-summary">
                  <span>
                    <GitBranch size={13} />
                    {data.branch || data.commit?.slice(0, 7) || "HEAD"}
                  </span>
                  {(mode === "changes" || commit) && (
                    <span>
                      {files.length} <b>+{additions}</b> <i>−{deletions}</i>
                    </span>
                  )}
                </div>
                {commit && (
                  <div className="git-commit-detail">
                    <div className="git-commit-heading">
                      <FilePanelButton
                        className="git-history-back"
                        title={commit}
                        onClick={() => {
                          setCommit("");
                          setSelected("");
                          setData(undefined);
                        }}
                      >
                        <ChevronLeft size={14} />
                        {t("History")} · {commit.slice(0, 7)}
                      </FilePanelButton>
                      {data.selected_commit && (
                        <GitCommitBadges commit={data.selected_commit} t={t} />
                      )}
                    </div>
                    <GitCommitSHA key={commit} sha={commit} t={t} />
                    {data.selected_commit && (
                      <>
                        <div className="git-commit-metadata">
                          <span>{data.selected_commit.author}</span>
                          {!!data.selected_commit.time && (
                            <time
                              dateTime={new Date(
                                data.selected_commit.time * 1000,
                              ).toISOString()}
                            >
                              {new Date(
                                data.selected_commit.time * 1000,
                              ).toLocaleString()}
                            </time>
                          )}
                        </div>
                        <p className="git-commit-detail-message">
                          {data.selected_commit.message ||
                            data.selected_commit.subject}
                        </p>
                      </>
                    )}
                  </div>
                )}
                {mode === "history" && !commit ? (
                  <div className="git-history-list">
                    {data.commits.map((item) => (
                      <FilePanelButton
                        key={item.id}
                        onClick={() => {
                          setCommit(item.id);
                          setSide("");
                          setData(undefined);
                          setSelected("");
                        }}
                        title={item.id}
                      >
                        <span className="git-history-title">
                          <strong>{item.subject}</strong>
                          <GitCommitBadges commit={item} t={t} />
                        </span>
                        <small>
                          {item.id.slice(0, 7)} · {item.author} ·{" "}
                          {new Date(item.time * 1000).toLocaleDateString()}
                        </small>
                      </FilePanelButton>
                    ))}
                    {!data.commits.length && (
                      <p className="file-panel-empty">{t("No commits yet.")}</p>
                    )}
                    {data.next_cursor && (
                      <div ref={historyEnd} className="git-history-end">
                        {history.moreLoading && (
                          <span role="status">
                            <LoaderCircle size={14} className="stop-spinner" />
                            {t("Loading Git information…")}
                          </span>
                        )}
                        {history.moreError && (
                          <p role="alert">
                            {history.moreError}{" "}
                            <FilePanelButton onClick={history.loadMore}>
                              {t("Retry")}
                            </FilePanelButton>
                          </p>
                        )}
                      </div>
                    )}
                  </div>
                ) : (
                  <div className="git-change-list">
                    {commit ? (
                      group(
                        t("Tracked"),
                        files.filter((file) => !file.untracked),
                      )
                    ) : (
                      <>
                        {group(t("Staged"), staged, "staged")}
                        {group(
                          t("Unstaged"),
                          files.filter(
                            (file) => !file.untracked && isUnstaged(file),
                          ),
                          "unstaged",
                        )}
                      </>
                    )}
                    {group(
                      t("Untracked"),
                      files.filter((file) => file.untracked),
                      commit ? "" : "unstaged",
                    )}
                    {!files.length && (
                      <p className="file-panel-empty">{t("No changes.")}</p>
                    )}
                    {data.truncated && (
                      <p className="file-panel-notice">
                        {t("Large directories show up to 2,000 entries.")}
                      </p>
                    )}
                  </div>
                )}
              </>
            )}
          </>
        }
        preview={
          selected ? (
            <section
              className="file-preview git-diff-preview"
              aria-label={t("Git diff")}
            >
              <div className="file-preview-header">
                <span title={selected}>
                  <File size={14} />
                  {selected}
                  {side && !commit && (
                    <small>
                      {t(side === "staged" ? "Staged" : "Unstaged")}
                    </small>
                  )}
                </span>
                <div className="file-preview-controls">
                  <FilePanelButton
                    className="icon"
                    aria-label={t("Refresh diff preview")}
                    title={t("Refresh diff preview")}
                    onClick={() => setDiffRefresh((value) => value + 1)}
                  >
                    <RefreshCw size={14} />
                  </FilePanelButton>
                  <FilePanelButton
                    className="icon"
                    aria-label={t("Close diff")}
                    onClick={() => setSelected("")}
                  >
                    <X size={15} />
                  </FilePanelButton>
                </div>
              </div>
              {diffError && (
                <p className="file-panel-notice" role="alert">
                  {diffError}
                  <FilePanelButton
                    onClick={() => setDiffRefresh((value) => value + 1)}
                  >
                    {t("Retry")}
                  </FilePanelButton>
                </p>
              )}
              {!diff ? (
                !diffError && (
                  <p className="file-panel-empty" role="status">
                    {t("Loading diff…")}
                  </p>
                )
              ) : diff.binary ? (
                <p className="file-panel-notice">{t("Binary file")}</p>
              ) : diff.patch ? (
                <div className="file-preview-scroll">
                  <UnifiedDiff patch={diff.patch} t={t} />
                </div>
              ) : (
                <p className="file-panel-empty">{t("No textual changes.")}</p>
              )}
              {diff?.truncated && (
                <p className="file-panel-notice">
                  {t("Diff preview is truncated.")}
                </p>
              )}
            </section>
          ) : null
        }
      />
      {mode === "changes" && data?.repository && !selected && (
        <div className="git-commit-area">
          {!composer ? (
            <div className="git-commit-toggle">
              <FilePanelButton
                className="git-commit-expand"
                aria-label={t("Expand commit area")}
                title={t("Expand commit area")}
                aria-expanded={false}
                onClick={() => setComposer(true)}
                disabled={!data.writable || !!operation}
              >
                <ChevronUp size={14} aria-hidden="true" />
              </FilePanelButton>
              <span className="git-commit-label">
                {t("Commit staged changes")} ({staged.length})
              </span>
            </div>
          ) : (
            <>
              <AutosizeTextarea
                className="git-commit-message"
                aria-label={t("Commit message")}
                placeholder={t("Enter commit message")}
                value={message}
                maxLength={32768}
                disabled={!!operation}
                onChange={(event) => setMessage(event.target.value)}
              />
              <div className="git-commit-actions">
                <FilePanelButton
                  className="git-commit-collapse"
                  aria-label={t("Collapse commit area")}
                  title={t("Collapse commit area")}
                  aria-expanded={true}
                  onClick={() => setComposer(false)}
                  disabled={!!operation}
                >
                  <ChevronDown size={14} aria-hidden="true" />
                </FilePanelButton>
                <FilePanelButton
                  onClick={() => void mutate("commit")}
                  disabled={
                    !!operation ||
                    !data.writable ||
                    !staged.length ||
                    !message.trim()
                  }
                >
                  {t("Commit staged changes")} ({staged.length})
                </FilePanelButton>
              </div>
            </>
          )}
        </div>
      )}
      {mode === "changes" && data?.repository && (
        <div className="git-remote-area">
          <div className="git-remote-actions">
            <span
              className="git-remote-target"
              title={
                targetRemote
                  ? `${targetRemote}/${targetBranch || ""}`
                  : t("No Git remote configured")
              }
            >
              <span className="git-remote-name">
                {targetRemote
                  ? `${targetRemote}/${targetBranch || ""}`
                  : t("No Git remote configured")}
              </span>
              {data.upstream && (
                <span className="git-remote-count">
                  <ArrowDown size={12} aria-hidden="true" />
                  {data.behind || 0}
                </span>
              )}
            </span>
            <div className="git-remote-split">
              <FilePanelButton
                disabled={remoteAction === "fetch" ? !canFetch : !canPush}
                aria-label={remoteLabel}
                onClick={() => void mutate(remoteAction)}
              >
                {remoteAction === "fetch" ? (
                  <ArrowDown size={12} aria-hidden="true" />
                ) : (
                  <ArrowUp size={12} aria-hidden="true" />
                )}
                {(remoteAction === "fetch" ? data.upstream : pushUpstream) && (
                  <span aria-hidden="true">
                    {(remoteAction === "fetch"
                      ? data.behind
                      : (data.push_ahead ?? data.ahead)) || 0}
                  </span>
                )}
                {remoteLabel}
              </FilePanelButton>
              <CustomSelect
                value={remoteAction}
                label={t("Git remote actions")}
                disabled={!canRemote}
                options={[
                  {
                    value: "push",
                    label: pushLabel,
                    disabled: !canPush,
                    icon: <ArrowUp size={14} aria-hidden="true" />,
                  },
                  {
                    value: "fetch",
                    label: t("Fetch"),
                    disabled: !canFetch,
                    icon: <ArrowDown size={14} aria-hidden="true" />,
                  },
                ]}
                onChange={(action) =>
                  setRemoteAction(action as "push" | "fetch")
                }
              />
            </div>
          </div>
          {data.remote && !data.remote_allowed && (
            <p className="file-panel-notice">
              {t("This project does not grant access to the repository root")}
            </p>
          )}
        </div>
      )}
      {mode === "changes" && data?.repository && (
        <div
          className="git-last-commit"
          title={
            data.last_commit
              ? `${data.last_commit.id} · ${data.last_commit.author}`
              : ""
          }
        >
          {data.last_commit ? (
            <>
              <span>{data.last_commit.subject}</span>
              <code>{data.last_commit.id.slice(0, 7)}</code>
            </>
          ) : (
            t("No commits yet.")
          )}
        </div>
      )}
    </section>
  );
}
