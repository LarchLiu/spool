import { useEffect, useMemo, useState } from "react";
import {
  ChevronDown,
  ChevronLeft,
  File,
  GitBranch,
  LoaderCircle,
  RefreshCw,
  X,
} from "lucide-react";
import { api } from "./api";
import type { Translator } from "./i18n";
import { FilePanelTabs } from "./file-panel-tabs";
import { FilePreviewSplit } from "./file-preview-split";
import { FilePanelButton } from "./file-panel-button";
import {
  unifiedDiffLines,
  type GitView,
  type GitDiff,
  type GitFile,
} from "./git-panel-state";

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

export function GitPanel({
  base,
  workspace,
  active,
  ready,
  refresh,
  t,
}: {
  base: string;
  workspace?: string;
  active: boolean;
  ready: boolean;
  refresh: number;
  t: Translator;
}) {
  const [mode, setMode] = useState<"changes" | "history">("changes");
  const [commit, setCommit] = useState("");
  const [data, setData] = useState<GitView>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState("");
  const [diff, setDiff] = useState<GitDiff>();
  const [diffError, setDiffError] = useState("");
  const [retry, setRetry] = useState(0);
  const [diffRefresh, setDiffRefresh] = useState(0);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const query = (view: string, path?: string) =>
    `${base}/git?${new URLSearchParams({
      view,
      ...(workspace ? { workspace } : {}),
      ...(commit ? { commit } : {}),
      ...(path ? { path } : {}),
    })}`;
  const listURL = query(mode === "history" && !commit ? "history" : "changes");
  const diffURL = selected ? query("diff", selected) : "";
  useEffect(() => {
    if (!active || !ready) return;
    const controller = new AbortController();
    let pending = false;
    const load = async () => {
      if (pending || controller.signal.aborted) return;
      pending = true;
      setLoading(true);
      try {
        const next = await api<GitView>(listURL, undefined, controller.signal);
        if (controller.signal.aborted) return;
        setData(next);
        setError("");
        setSelected((path) =>
          next.files.some((file) => file.path === path) ? path : "",
        );
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
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void load();
    }, 10_000);
    window.addEventListener("focus", load);
    return () => {
      controller.abort();
      clearInterval(timer);
      window.removeEventListener("focus", load);
    };
  }, [active, ready, listURL, refresh, retry, t]);
  useEffect(() => {
    if (!active || !ready || !diffURL) return;
    const controller = new AbortController();
    setDiffError("");
    api<GitDiff>(diffURL, undefined, controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) setDiff(value);
      })
      .catch((err) => {
        if (!controller.signal.aborted)
          setDiffError(
            err instanceof Error ? err.message : t("Request failed"),
          );
      });
    return () => controller.abort();
  }, [active, ready, diffURL, diffRefresh, t]);
  function chooseMode(next: "changes" | "history") {
    if (next === mode && !commit) return;
    setMode(next);
    setCommit("");
    setSelected("");
    setData(undefined);
    setDiff(undefined);
  }
  const files = data?.files || [];
  const additions = files.reduce((sum, file) => sum + file.additions, 0);
  const deletions = files.reduce((sum, file) => sum + file.deletions, 0);
  function group(label: string, items: GitFile[]) {
    if (!items.length) return null;
    return (
      <div className="git-file-group">
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
        {!collapsed[label] &&
          items.map((file) => (
            <FilePanelButton
              key={file.path}
              className="git-file-row"
              aria-pressed={selected === file.path}
              title={`${file.previous_path ? file.previous_path + " → " : ""}${file.path} · ${file.status}${file.staged ? " · " + t("Staged") : ""}`}
              onClick={() => {
                if (selected === file.path) return;
                setSelected(file.path);
                setDiff(undefined);
                setDiffError("");
              }}
            >
              <span
                className={`git-file-status ${file.untracked ? "untracked" : ""}`}
              >
                {file.untracked ? "+" : file.status}
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
                    <b>+{file.additions}</b>
                    <i>−{file.deletions}</i>
                  </>
                )}
              </span>
            </FilePanelButton>
          ))}
      </div>
    );
  }
  return (
    <section className="git-panel" aria-label={t("Git panel")}>
      <FilePanelTabs
        label={t("Git views")}
        value={mode}
        onChange={chooseMode}
        options={[
          { value: "changes", label: t("Changes") },
          { value: "history", label: t("History") },
        ]}
      />
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
                  <FilePanelButton
                    className="git-history-back"
                    onClick={() => {
                      setCommit("");
                      setSelected("");
                      setData(undefined);
                    }}
                  >
                    <ChevronLeft size={14} />
                    {t("History")} · {commit.slice(0, 7)}
                  </FilePanelButton>
                )}
                {mode === "history" && !commit ? (
                  <div className="git-history-list">
                    {data.commits.map((item) => (
                      <FilePanelButton
                        key={item.id}
                        onClick={() => {
                          setCommit(item.id);
                          setData(undefined);
                          setSelected("");
                        }}
                        title={item.id}
                      >
                        <strong>{item.subject}</strong>
                        <small>
                          {item.id.slice(0, 7)} · {item.author} ·{" "}
                          {new Date(item.time * 1000).toLocaleDateString()}
                        </small>
                      </FilePanelButton>
                    ))}
                    {!data.commits.length && (
                      <p className="file-panel-empty">{t("No commits yet.")}</p>
                    )}
                  </div>
                ) : (
                  <div className="git-change-list">
                    {group(
                      t("Tracked"),
                      files.filter((file) => !file.untracked),
                    )}
                    {group(
                      t("Untracked"),
                      files.filter((file) => file.untracked),
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
              {diffError ? (
                <p className="file-panel-notice" role="alert">
                  {diffError}
                  <FilePanelButton
                    onClick={() => setDiffRefresh((value) => value + 1)}
                  >
                    {t("Retry")}
                  </FilePanelButton>
                </p>
              ) : !diff ? (
                <p className="file-panel-empty" role="status">
                  {t("Loading diff…")}
                </p>
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
    </section>
  );
}
