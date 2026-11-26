import { apiPath } from "./app-path";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
} from "react";
import { FileTree, useFileTree, useFileTreeSearch } from "@pierre/trees/react";
import { Download, File, ListTree, GitBranch, LoaderCircle, RefreshCw, X } from "lucide-react";
import { api, APIError } from "./api";
import type { Translator } from "./i18n";
import type {
  Project,
  ProjectDirectory,
  ProjectFile,
  SessionWorkspace,
} from "./types";
import { LocalFileContext } from "./local-file-context";
import {
  fileAPIBase,
  projectFileURL,
  type LocalFileTarget,
} from "./local-file-links";
import { MarkdownMessage } from "./markdown-message";
import { PanelResizeHandle } from "./panel-resize-handle";
import { FilePreviewSplit } from "./file-preview-split";
import { FileSidebarHeader } from "./file-sidebar-header";
import { FileSource } from "./file-source";
import { filePreviewKind, HtmlFilePreview } from "./file-preview";
import { OfficePreview, type OfficeDocument } from "./office-preview";
import { desktopFileAccessURL, subscribeDesktopFileAccess } from "./runtime";
import { FilePanelTabs } from "./file-panel-tabs";
import { GitPanel } from "./git-panel";
import { FilePanelButton } from "./file-panel-button";
import { imageSource } from "./image-resource";
import { ResourceImage } from "./resource-image";
import { FileUploadMenu } from "./file-upload-menu";

const readOffice = (path: string, signal: AbortSignal) => api<OfficeDocument>(apiPath(path), undefined, signal);

export type FileSelection = {
  target: LocalFileTarget;
  request: number;
  scope: string;
};
type Props = {
  project?: Project;
  chatID?: string;
  workspace?: SessionWorkspace;
  workspaces?: SessionWorkspace[];
  onSelectWorkspace: (path: string) => void;
  workspaceError?: string;
  onRefreshWorkspace: () => void;
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
  const [panel, setPanel] = useState<"project" | "git">("project");
  const context = {
    projectID: props.project?.id,
    chatID: props.chatID,
    workspace: props.workspace,
    workspaces: props.workspaces,
  };
  const [resolved, setResolved] = useState(context);
  const pending = !!props.chatID && !props.workspace && !props.workspaceError;
  // Keep the directory mounted until the next chat's workspace is known.
  const retain = pending && resolved.projectID === context.projectID;
  const directory = retain ? resolved : context;
  if (
    !retain &&
    (resolved.projectID !== context.projectID ||
      resolved.chatID !== context.chatID ||
      resolved.workspace !== context.workspace ||
      resolved.workspaces !== context.workspaces)
  ) setResolved(context);
  const directoryScope = JSON.stringify([
    props.project?.id,
    directory.workspace?.path,
    !!directory.workspace?.available,
  ]);
  useEffect(() => { if (props.selection) setPanel("project"); }, [props.selection]);
  return (
    <aside
      className={`file-sidebar ${props.mobileOpen ? "open" : ""}`}
      inert={!props.active || pending}
      aria-label={props.t("Project files")}
    >
      {props.project ? (
        <ProjectFiles
          key={directoryScope}
          {...props}
          chatID={directory.chatID}
          workspace={directory.workspace}
          workspaces={directory.workspaces}
          directoryScope={directoryScope}
          panel={panel}
          onSelectPanel={setPanel}
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
  chatID,
  workspace,
  workspaces,
  onSelectWorkspace,
  workspaceError,
  onRefreshWorkspace,
  active,
  selection,
  t,
  theme,
  onOpen,
  onClear,
  onClose,
  panel,
  onSelectPanel,
  directoryScope,
}: Props & { project: Project; panel: "project" | "git"; onSelectPanel: (panel: "project" | "git") => void; directoryScope: string }) {
  const ready = !chatID || !!workspace?.available;
  const workspacePath = workspace?.path;
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(0);
  const [count, setCount] = useState(0);
  const [truncated, setTruncated] = useState(false);
  const [loaded, setLoaded] = useState<{
    file: ProjectFile;
    selection: FileSelection;
    revision: number;
  } | null>(null);
  const file = loaded?.file;
  const [fileError, setFileError] = useState<{
    message: string;
    code?: string;
  } | null>(null);
  const [refresh, setRefresh] = useState(0);
  const [gitRefresh, setGitRefresh] = useState(0);
  const [previewRefresh, setPreviewRefresh] = useState(0);
  const accessURL =
    fileError?.code === "file_read_denied" && selection
      ? desktopFileAccessURL(chatID, selection.target.path)
      : undefined;
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
        `${fileAPIBase(project.id, chatID).slice(4)}/files?${new URLSearchParams({ path: key, ...(chatID && workspacePath ? { workspace: workspacePath } : {}) })}`,
        undefined,
        controller.signal,
      )
        .then((listing) => {
          if (controller.signal.aborted || !mounted.current) return;
          const additions = listing.entries
            .map((entry) => entry.path + (entry.kind === "directory" ? "/" : ""))
            .filter((path) => !model.getItem(path));
          model.batch(
            additions.map((path) => ({
              type: "add",
              path,
            })),
          );
          setCount((value) => value + additions.length);
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
    [model, project.id, chatID, workspacePath, t],
  );

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      for (const controller of controllers.current) controller.abort();
    };
  }, []);
  useEffect(() => {
    if (!active || !ready) return;
    void loadDirectory(".");
    return model.subscribe(() => {
      for (const row of model.getVisibleRows(0, model.getVisibleCount())) {
        if (row.kind === "directory" && row.isExpanded)
          void loadDirectory(row.path);
      }
    });
  }, [active, ready, loadDirectory, model, refresh]);

  useEffect(() => {
    if (!selection) {
      setLoaded(null);
      setFileError(null);
      suppressSelection.current = true;
      try {
        for (const path of model.getSelectedPaths())
          model.getItem(path)?.deselect();
      } finally {
        suppressSelection.current = false;
      }
      return;
    }
    if (!active || !ready) return;
    const controller = new AbortController();
    setFileError(null);
    api<ProjectFile>(
      apiPath(projectFileURL(
        project.id,
        selection.target.path,
        undefined,
        chatID,
        workspacePath,
      )),
      undefined,
      controller.signal,
    )
      .then(async (next) => {
        if (controller.signal.aborted) return;
        setLoaded({ file: next, selection, revision: previewRefresh });
        if (next.external) return;
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
          setFileError({
            message: err instanceof Error ? err.message : t("Request failed"),
            code: err instanceof APIError ? err.code : undefined,
          });
        }
      });
    return () => controller.abort();
  }, [
    active,
    ready,
    selection,
    project.id,
    chatID,
    workspacePath,
    model,
    loadDirectory,
    previewRefresh,
    t,
  ]);

  useEffect(
    () => subscribeDesktopFileAccess((result) => {
      if (!active || result.chat_id !== chatID || result.path !== selection?.target.path) return;
      if (result.error) setFileError({ message: result.error, code: "file_read_denied" });
      else setPreviewRefresh((value) => value + 1);
    }),
    [active, chatID, selection],
  );

  function reload() {
    if (panel === "git") {
      setGitRefresh((value) => value + 1);
      return;
    }
    onRefreshWorkspace();
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
    setFileError(null);
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
        workspace={workspace}
        workspaces={workspaces}
        onSelectWorkspace={onSelectWorkspace}
        workspaceError={workspaceError}
        showWorkspaceInfo={!!chatID}
        t={t}
        search={panel === "project" ? {
          isOpen: search.isOpen,
          onOpen: () => search.open(),
          onChange: search.setValue,
          onClose: search.close,
        } : undefined}
        onRefresh={reload}
        refreshLabel={t(panel === "git" ? "Refresh Git information" : "Refresh files")}
        onClose={onClose}
      />
      <FilePanelTabs variant="underline" label={t("Project panels")} value={panel} onChange={onSelectPanel} options={[
        { value: "project", label: t("Project panel"), icon: <ListTree size={14} /> },
        { value: "git", label: t("Git panel"), icon: <GitBranch size={14} /> },
      ]} />
      {panel === "git" && <GitPanel base={fileAPIBase(project.id, chatID).slice(4)} scope={directoryScope} workspace={chatID ? workspacePath : undefined}
        active={active} ready={ready} refresh={gitRefresh} t={t} />}
      <div className="file-project-panel" hidden={panel !== "project"}>
      <FilePreviewSplit
        t={t}
        tree={
          <>
            <FileUploadMenu base={fileAPIBase(project.id, chatID).slice(4)} workspace={chatID ? workspacePath : undefined}
              enabled={project.can_upload === true} ready={ready} active={active && panel === "project"} t={t}
              onUploaded={async (directory, entry) => {
                await requests.current.get(directory);
                if (!mounted.current) return;
                requests.current.delete(directory);
                await loadDirectory(directory);
                if (!mounted.current) return;
                if (!model.getItem(entry.path)) {
                  model.add(entry.path);
                  setCount((value) => value + 1);
                }
                if (directory !== ".") {
                  const item = model.getItem(directory + "/");
                  if (item && "expand" in item) item.expand();
                }
              }}>
              {!ready && (
                <p
                  className="file-panel-notice"
                  role={workspaceError ? "alert" : "status"}
                >
                  {workspaceError ||
                    t(
                      workspace
                        ? "Session directory is unavailable"
                        : "Loading session directory…",
                    )}
                  {workspaceError && (
                    <button onClick={reload}>{t("Retry")}</button>
                  )}
                </p>
              )}
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
              {ready && !busy && !count && !error && (
                <p className="file-panel-empty">
                  {t("This directory is empty.")}
                </p>
              )}
            </FileUploadMenu>
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
                  {loaded?.selection.target.line
                    ? `:${loaded.selection.target.line}`
                    : ""}
                </span>
                <div>
                  {file?.external && <small>{t("External file")}</small>}
                  {file && <small>{formatFileSize(file.size)}</small>}
                  {file && (
                    <a
                      className="icon"
                      href={(file.download_url && imageSource(file.download_url, chatID)) || projectFileURL(
                        project.id,
                        file.path,
                        "download",
                        chatID,
                        workspacePath,
                      )}
                      download={file.name}
                      aria-label={t("Download file")}
                      title={t("Download file")}
                    >
                      <Download size={14} />
                    </a>
                  )}
                  <div className="file-preview-controls">
                    <FilePanelButton
                      className="icon"
                      onClick={() => setPreviewRefresh((value) => value + 1)}
                      aria-label={t("Refresh file preview")}
                      title={t("Refresh file preview")}
                    >
                      <RefreshCw size={14} />
                    </FilePanelButton>
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
              </div>
              {fileError && (
                <p className="file-panel-notice" role="alert">
                  {fileError.message}
                  {accessURL && (
                    <a href={accessURL}>{t("Allow access to attached file")}</a>
                  )}
                </p>
              )}
              {loaded && (
                <FileContent
                  key={`${loaded.selection.request}:${loaded.revision}`}
                  file={loaded.file}
                  project={project.id}
                  chatID={chatID}
                  workspacePath={workspacePath}
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
      </div>
    </>
  );
}

function FileContent({
  file,
  project,
  chatID,
  workspacePath,
  target,
  t,
  theme,
  onOpen,
}: {
  file: ProjectFile;
  project: string;
  chatID?: string;
  workspacePath?: string;
  target: LocalFileTarget;
  t: Translator;
  theme: "light" | "dark";
  onOpen: Props["onOpen"];
}) {
  const [renderPreview, setRenderPreview] = useState(false);
  const previewKind = filePreviewKind(file);
  const office = previewKind === "word" || previewKind === "spreadsheet";
  const context = useMemo(
    () => ({ openFile: onOpen, basePath: file.path }),
    [onOpen, file.path],
  );
  return (
    <LocalFileContext.Provider value={context}>
      {(previewKind === "markdown" || previewKind === "html") && (
        <div className="file-preview-mode">
          <button
            aria-pressed={!renderPreview}
            onClick={() => setRenderPreview(false)}
          >
            {t("Source")}
          </button>
          <button
            aria-pressed={renderPreview}
            onClick={() => setRenderPreview(true)}
          >
            {t("Preview")}
          </button>
        </div>
      )}
      <div className={`file-preview-scroll${office ? " office-preview-container" : ""}`}>
        {office && <OfficePreview key={file.path} path={projectFileURL(project, file.path, undefined, chatID, workspacePath)} read={readOffice} t={t} />}
        {previewKind === "pdf" && (
          <iframe
            className="file-pdf-preview"
            src={projectFileURL(project, file.path, "preview", chatID, workspacePath)}
            title={t("Preview {name}", { name: file.name })}
            referrerPolicy="no-referrer"
          />
        )}
        {file.kind === "text" && previewKind !== "pdf" &&
          (renderPreview && previewKind === "html" ? (
            <HtmlFilePreview
              contents={file.content || ""}
              title={t("Preview {name}", { name: file.name })}
            />
          ) : renderPreview && previewKind === "markdown" ? (
            <MarkdownMessage t={t}>{file.content || ""}</MarkdownMessage>
          ) : (
            <FileSource
              path={file.path}
              contents={file.content || ""}
              line={target.line}
              theme={theme}
            />
          ))}
        {file.kind === "image" && (
          <ResourceImage
            key={file.path}
            className="project-file-image"
            url={(file.preview_url && imageSource(file.preview_url, chatID)) || projectFileURL(project, file.path, "image", chatID, workspacePath)}
            name={file.name}
            t={t}
          />
        )}
        {file.kind === "binary" && previewKind !== "pdf" && !office && (
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
