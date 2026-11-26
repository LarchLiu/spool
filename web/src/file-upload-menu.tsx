import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { LoaderCircle, Upload, X } from "lucide-react";
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
  const [position, setPosition] = useState<{ left: number; top: number }>();
  const [uploading, setUploading] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const picker = useRef<HTMLInputElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
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
  useLayoutEffect(() => {
    if (!menu || !menuRef.current) return;
    const rect = menuRef.current.getBoundingClientRect();
    setPosition({
      left: Math.max(8, Math.min(menu.left, innerWidth - rect.width - 8)),
      top: Math.max(8, Math.min(menu.top, innerHeight - rect.height - 8)),
    });
    menuRef.current.focus();
    menuRef.current
      .querySelector<HTMLButtonElement>("button:not(:disabled)")
      ?.focus();
  }, [menu]);
  useEffect(() => {
    if (!menu) return;
    const dismiss = () => setMenu(null);
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.isComposing) {
        event.preventDefault();
        event.stopPropagation();
        close(true);
      }
    };
    window.addEventListener("resize", dismiss);
    document.addEventListener("scroll", dismiss, true);
    document.addEventListener("keydown", escape, true);
    return () => {
      window.removeEventListener("resize", dismiss);
      document.removeEventListener("scroll", dismiss, true);
      document.removeEventListener("keydown", escape, true);
    };
  }, [menu]);

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
        setPosition(undefined);
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
      {menu &&
        createPortal(
          <>
            <div
              className="chat-menu-backdrop"
              aria-hidden="true"
              onPointerDown={(event) => {
                event.preventDefault();
                close();
              }}
              onContextMenu={(event) => {
                event.preventDefault();
                close();
              }}
            />
            <div
              ref={menuRef}
              role="menu"
              tabIndex={-1}
              aria-label={t("Project file actions")}
              className="chat-title-menu file-upload-menu"
              style={{
                ...position,
                visibility: position ? "visible" : "hidden",
              }}
              onKeyDown={(event) => {
                if (event.key === "Tab") close(true);
                if (
                  ["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)
                ) {
                  event.preventDefault();
                  menuRef.current
                    ?.querySelector<HTMLButtonElement>("button:not(:disabled)")
                    ?.focus();
                }
              }}
            >
              <small>
                {t("Upload to {path}", {
                  path: menu.directory === "." ? "/" : menu.directory,
                })}
              </small>
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
                <Upload size={14} />
                {t("Upload files")}
              </button>
              {!enabled && (
                <small>{t("File upload permission is disabled")}</small>
              )}
            </div>
          </>,
          document.body,
        )}
    </div>
  );
}
