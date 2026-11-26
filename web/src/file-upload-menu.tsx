import { useEffect, useRef, useState, type ReactNode } from "react";
import { LoaderCircle, Upload, X } from "lucide-react";
import { ActionMenu } from "./action-menu";
import { api } from "./api";
import type { Translator } from "./i18n";
import type { ProjectFileEntry } from "./types";

// Tree rows live in a shadow root. The composed path retains the actual row,
// including its full width and indentation, while event.target is the host.
function uploadDirectory(event: Event): string {
  for (const element of event.composedPath()) {
    if (!(element instanceof HTMLElement)) continue;
    const path = element.dataset.itemPath;
    if (!path) continue;
    if (element.dataset.itemType === "folder")
      return path.replace(/\/$/, "") || ".";
    return path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : ".";
  }
  return ".";
}

export function FileUploadMenu({
  base,
  workspace,
  enabled,
  ready,
  active,
  t,
  onUploaded,
  children,
}: {
  base: string;
  workspace?: string;
  enabled: boolean;
  ready: boolean;
  active: boolean;
  t: Translator;
  onUploaded: (directory: string, file: ProjectFileEntry) => Promise<void>;
  children: ReactNode;
}) {
  const [menu, setMenu] = useState<{
    left: number;
    top: number;
    directory: string;
  } | null>(null);
  const [uploading, setUploading] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const picker = useRef<HTMLInputElement>(null);
  const section = useRef<HTMLDivElement>(null);
  const destination = useRef(".");
  const controller = useRef<AbortController | null>(null);
  const origin = useRef<HTMLElement | null>(null);
  const close = (restoreFocus = false) => {
    setMenu(null);
    if (restoreFocus)
      (origin.current?.isConnected ? origin.current : section.current)?.focus({
        preventScroll: true,
      });
  };
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => {
    if (!active || !ready) setMenu(null);
  }, [active, ready]);

  async function upload(files: File[]) {
    if (!files.length || controller.current || !enabled || !ready) return;
    const directory = destination.current;
    const pending = new AbortController();
    controller.current = pending;
    setUploading(true);
    setError("");
    let completed = 0;
    const failures: string[] = [];
    try {
      for (const file of files) {
        setStatus(t("Uploading {name}…", { name: file.name }));
        try {
          if (file.size > 64 * 1024 * 1024)
            throw new Error(t("Each uploaded file must be at most 64 MB"));
          const body = new FormData();
          body.append("file", file);
          const entry = await api<ProjectFileEntry>(
            `${base}/upload?${new URLSearchParams({ path: directory, ...(workspace ? { workspace } : {}) })}`,
            body,
            pending.signal,
          );
          if (pending.signal.aborted) return;
          completed++;
          await onUploaded(directory, entry);
        } catch (err) {
          if (pending.signal.aborted) return;
          failures.push(
            `${file.name}: ${err instanceof Error ? err.message : t("Request failed")}`,
          );
        }
      }
      setStatus(
        completed
          ? t("Uploaded {count} files to {path}", {
              count: completed,
              path: directory === "." ? "/" : directory,
            })
          : "",
      );
      setError(failures.join("\n"));
    } finally {
      if (!pending.signal.aborted) setUploading(false);
      controller.current = null;
    }
  }
  return (
    <div
      ref={section}
      className="file-tree-section"
      tabIndex={-1}
      onContextMenuCapture={(event) => {
        if (!active || !ready) return;
        // Portal events bubble through React's tree, including the menu backdrop.
        if (!event.currentTarget.contains(event.target as Node)) return;
        event.preventDefault();
        event.stopPropagation();
        origin.current =
          (event.nativeEvent
            .composedPath()
            .find(
              (item) =>
                item instanceof HTMLElement &&
                item.hasAttribute("data-item-path"),
            ) as HTMLElement | undefined) ?? null;
        setMenu({
          left: event.clientX,
          top: event.clientY,
          directory: uploadDirectory(event.nativeEvent),
        });
      }}
    >
      {children}
      <input
        ref={picker}
        type="file"
        multiple
        hidden
        aria-label={t("Upload files")}
        onChange={(event) => {
          const files = Array.from(event.currentTarget.files || []);
          event.currentTarget.value = "";
          void upload(files);
        }}
      />
      {(uploading || status || error) && (
        <div className="file-upload-status">
          {status && (
            <p role="status">
              {uploading && <LoaderCircle size={14} className="stop-spinner" />}
              {status}
            </p>
          )}
          {error && <p role="alert">{error}</p>}
          {!uploading && (
            <button
              type="button"
              className="icon file-upload-dismiss"
              aria-label={t("Dismiss")}
              onClick={() => {
                setStatus("");
                setError("");
              }}
            >
              <X size={14} />
            </button>
          )}
        </div>
      )}
      {menu && (
        <ActionMenu
          label={t("Project file actions")}
          anchor={origin}
          point={menu}
          onClose={close}
        >
          <button
            type="button"
            role="menuitem"
            disabled={!enabled || uploading}
            onClick={() => {
              destination.current = menu.directory;
              close();
              picker.current?.click();
            }}
          >
            <Upload size={15} aria-hidden="true" />
            {t("Upload files")}
          </button>
        </ActionMenu>
      )}
    </div>
  );
}
