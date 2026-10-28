import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
} from "react";
import { FileTree, useFileTree, useFileTreeSearch } from "@pierre/trees/react";
import { Download, File, LoaderCircle, X } from "lucide-react";
import { api } from "./api";
import type { Translator } from "./i18n";
import type { Project, ProjectDirectory, ProjectFile } from "./types";
import { LocalFileContext } from "./local-file-context";
import { projectFileURL, type LocalFileTarget } from "./local-file-links";
import { MarkdownMessage } from "./markdown-message";
import { PanelResizeHandle } from "./panel-resize-handle";
import { FilePreviewSplit } from "./file-preview-split";
import { FileSidebarHeader } from "./file-sidebar-header";
import { FileSource } from "./file-source";

export type FileSelection = { target: LocalFileTarget; request: number };
type Props = {
  project?: Project;
  active: boolean;
  mobileOpen: boolean;
  width: number;
  selection: FileSelection | null;
  t: Translator;
  theme: "light" | "dark";
  onOpen: (target: LocalFileTarget) => void;
  onClose: () => void;
  onResize: (width: number) => void;
  onClear: () => void;
};

export function FileSidebar(props: Props) {
  return (
    <aside
      className={`file-sidebar ${props.mobileOpen ? "open" : ""}`}
      inert={!props.active}
      aria-label={props.t("Project files")}
    >
      {props.project ? (
        <ProjectFiles
          key={props.project.id}
          {...props}
          project={props.project}
        />
      ) : (
        <>
          <FileSidebarHeader t={props.t} onClose={props.onClose} />
          <p className="file-panel-empty">
            {props.t("Choose a project to browse its files.")}
          </p>
        </>
      )}
      <PanelResizeHandle
        side="right"
        width={props.width}
        onResize={props.onResize}
        t={props.t}
      />
    </aside>
  );
}

function ProjectFiles({
  project,
  active,
  selection,
  t,
  theme,
  onOpen,
  onClear,
  onClose,
}: Props & { project: Project }) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(0);
  const [count, setCount] = useState(0);
  const [truncated, setTruncated] = useState(false);
  const [loaded, setLoaded] = useState<{
    file: ProjectFile;
    selection: FileSelection;
  } | null>(null);
  const file = loaded?.file;
  const [fileError, setFileError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const requests = useRef(new Map<string, Promise<void>>());
  const controllers = useRef(new Set<AbortController>());
  const suppressSelection = useRef(false);
  const mounted = useRef(true);
  const openRef = useRef(onOpen);
  openRef.current = onOpen;
  const { model } = useFileTree({
    paths: [],
    initialExpansion: "closed",
    flattenEmptyDirectories: false,
    icons: { set: "standard", colored: true },
    density: "default",
    search: false,
    onSelectionChange: (paths) => {
      const path = paths.at(-1);
      if (!suppressSelection.current && path && !path.endsWith("/"))
        openRef.current({ path });
    },
  });
  const search = useFileTreeSearch(model);
  const loadDirectory = useCallback(
    (path: string): Promise<void> => {
      const key = path.replace(/\/$/, "") || ".";
      const previous = requests.current.get(key);
      if (previous) return previous;
      const controller = new AbortController();
      controllers.current.add(controller);
      setBusy((value) => value + 1);
      const pending = api<ProjectDirectory>(
        `/projects/${project.id}/files?${new URLSearchParams({ path: key })}`,
        undefined,
        controller.signal,
      )
        .then((listing) => {
          if (controller.signal.aborted || !mounted.current) return;
          model.batch(
            listing.entries.map((entry) => ({
              type: "add",
              path: entry.path + (entry.kind === "directory" ? "/" : ""),
            })),
          );
          setCount((value) => value + listing.entries.length);
          if (listing.truncated) setTruncated(true);
        })
        .catch((err: unknown) => {
          if (!controller.signal.aborted && mounted.current)
            setError(err instanceof Error ? err.message : t("Request failed"));
        })
        .finally(() => {
          controllers.current.delete(controller);
          if (mounted.current) setBusy((value) => value - 1);
        });
      requests.current.set(key, pending);
      return pending;
    },
    [model, project.id, t],
  );

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      for (const controller of controllers.current) controller.abort();
    };
  }, []);
  useEffect(() => {
    if (!active) return;
    void loadDirectory(".");
    return model.subscribe(() => {
      for (const row of model.getVisibleRows(0, model.getVisibleCount())) {
        if (row.kind === "directory" && row.isExpanded)
          void loadDirectory(row.path);
      }
    });
  }, [active, loadDirectory, model, refresh]);

  useEffect(() => {
    if (!active || !selection) return;
    const controller = new AbortController();
    setFileError("");
    api<ProjectFile>(
      projectFileURL(project.id, selection.target.path).slice(4),
      undefined,
      controller.signal,
    )
      .then(async (next) => {
        if (controller.signal.aborted) return;
        setLoaded({ file: next, selection });
        await loadDirectory(".");
        const segments = next.path.split("/");
        for (let i = 1; i < segments.length; i++) {
          if (controller.signal.aborted) return;
          const directory = segments.slice(0, i).join("/") + "/";
          const item = model.getItem(directory);
          if (item && "expand" in item) item.expand();
          await loadDirectory(directory);
        }
        if (controller.signal.aborted) return;
        suppressSelection.current = true;
        try {
          for (const path of model.getSelectedPaths())
            model.getItem(path)?.deselect();
          model.getItem(next.path)?.select();
          model.scrollToPath(next.path, { focus: false });
        } finally {
          suppressSelection.current = false;
        }
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) {
          setLoaded(null);
          setFileError(
            err instanceof Error ? err.message : t("Request failed"),
          );
        }
      });
    return () => controller.abort();
  }, [active, selection, project.id, model, loadDirectory, refresh, t]);

  function reload() {
    for (const controller of controllers.current) controller.abort();
    controllers.current.clear();
    requests.current.clear();
    model.resetPaths([]);
    setCount(0);
    setError("");
    setTruncated(false);
    setRefresh((value) => value + 1);
  }
  function clearPreview() {
    setLoaded(null);
    setFileError("");
    suppressSelection.current = true;
    try {
      for (const path of model.getSelectedPaths())
        model.getItem(path)?.deselect();
    } finally {
      suppressSelection.current = false;
    }
    onClear();
  }
  return (
    <>
      <FileSidebarHeader
        name={project.name}
        t={t}
        search={{
          isOpen: search.isOpen,
          onOpen: () => search.open(),
          onChange: search.setValue,
          onClose: search.close,
        }}
        onRefresh={reload}
        onClose={onClose}
      />
      <FilePreviewSplit
        t={t}
        tree={
          <>
            <div className="file-tree-section">
              <FileTree
                model={model}
                aria-label={t("Project file tree")}
                style={
                  {
                    height: "100%",
                    "--trees-fg-override": "var(--text)",
                    "--trees-bg-override": "var(--elevated)",
                    "--trees-selected-bg-override": "var(--selected)",
                    "--trees-border-color-override": "var(--line)",
                  } as CSSProperties
                }
              />
              {busy > 0 && (
                <span
                  className="file-tree-loading"
                  role="status"
                  aria-label={t("Loading files…")}
                >
                  <LoaderCircle size={14} className="stop-spinner" />
                </span>
              )}
              {!busy && !count && !error && (
                <p className="file-panel-empty">
                  {t("This directory is empty.")}
                </p>
              )}
            </div>
            {error && (
              <div className="file-panel-notice" role="alert">
                {error}
                <button onClick={reload}>{t("Retry")}</button>
              </div>
            )}
            {truncated && (
              <p className="file-panel-notice">
                {t("Large directories show up to 2,000 entries.")}
              </p>
            )}
          </>
        }
        preview={
          selection ? (
            <section className="file-preview" aria-label={t("File preview")}>
              <div className="file-preview-header">
                <span title={file?.path || selection.target.path}>
                  <File size={14} />
                  {file?.name || selection.target.path.split("/").pop()}
                </span>
                <div>
                  {file && (
                    <a
                      className="icon"
                      href={projectFileURL(project.id, file.path, "download")}
                      download={file.name}
                      aria-label={t("Download file")}
                      title={t("Download file")}
                    >
                      <Download size={14} />
                    </a>
                  )}
                  <button
                    className="icon"
                    onClick={clearPreview}
                    aria-label={t("Close file preview")}
                    title={t("Close file preview")}
                  >
                    <X size={15} />
                  </button>
                </div>
              </div>
              {fileError && (
                <p className="file-panel-notice" role="alert">
                  {fileError}
                </p>
              )}
              {loaded && (
                <FileContent
                  key={loaded.selection.request}
                  file={loaded.file}
                  project={project.id}
                  target={loaded.selection.target}
                  t={t}
                  theme={theme}
                  onOpen={onOpen}
                />
              )}
            </section>
          ) : null
        }
      />
    </>
  );
}

function FileContent({
  file,
  project,
  target,
  t,
  theme,
  onOpen,
}: {
  file: ProjectFile;
  project: string;
  target: LocalFileTarget;
  t: Translator;
  theme: "light" | "dark";
  onOpen: Props["onOpen"];
}) {
  const [renderMarkdown, setRenderMarkdown] = useState(false);
  const context = useMemo(
    () => ({ openFile: onOpen, basePath: file.path }),
    [onOpen, file.path],
  );
  return (
    <LocalFileContext.Provider value={context}>
      <div className="file-preview-meta">
        <span title={file.path}>
          {file.path}
          {target.line ? `:${target.line}` : ""}
        </span>
        <small>{formatFileSize(file.size)}</small>
      </div>
      {file.kind === "text" && /\.md(?:own|x)?$/i.test(file.name) && (
        <div className="file-preview-mode">
          <button
            aria-pressed={!renderMarkdown}
            onClick={() => setRenderMarkdown(false)}
          >
            {t("Source")}
          </button>
          <button
            aria-pressed={renderMarkdown}
            onClick={() => setRenderMarkdown(true)}
          >
            {t("Preview")}
          </button>
        </div>
      )}
      <div className="file-preview-scroll">
        {file.kind === "text" &&
          (renderMarkdown ? (
            <MarkdownMessage>{file.content || ""}</MarkdownMessage>
          ) : (
            <FileSource
              path={file.path}
              contents={file.content || ""}
              line={target.line}
              theme={theme}
            />
          ))}
        {file.kind === "image" && (
          <img
            className="project-file-image"
            src={projectFileURL(project, file.path, "image")}
            alt={file.name}
          />
        )}
        {file.kind === "binary" && (
          <p className="file-panel-empty">
            {t("This file has no text preview. Download it to open locally.")}
          </p>
        )}
      </div>
      {file.truncated && file.kind === "text" && (
        <p className="file-panel-notice">
          {t("Preview truncated. Download the full file to see all content.")}
        </p>
      )}
    </LocalFileContext.Provider>
  );
}

export function formatFileSize(size: number) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}
