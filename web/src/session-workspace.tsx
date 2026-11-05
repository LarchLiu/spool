import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { createPortal } from "react-dom";
import { FolderGit2, GitBranch, GitCommitHorizontal, Info } from "lucide-react";
import type { Translator } from "./i18n";
import type { SessionWorkspace } from "./types";

export function SessionWorkspacePopover({
  workspace,
  error,
  t,
}: {
  workspace?: SessionWorkspace;
  error?: string;
  t: Translator;
}) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  const hovered = useRef(false);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined,
  );
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<{ left: number; top: number }>();
  function show() {
    clearTimeout(closeTimer.current);
    setOpen(true);
  }
  function hide() {
    clearTimeout(closeTimer.current);
    setOpen(false);
  }
  function leave() {
    clearTimeout(closeTimer.current);
    closeTimer.current = setTimeout(() => {
      if (!hovered.current && document.activeElement !== trigger.current)
        hide();
    }, 150);
  }
  function enter(event: ReactPointerEvent) {
    if (event.pointerType === "touch") return;
    hovered.current = true;
    show();
  }
  function exit(event: ReactPointerEvent) {
    if (event.pointerType === "touch") return;
    hovered.current = false;
    leave();
  }
  useEffect(() => () => clearTimeout(closeTimer.current), []);
  useLayoutEffect(() => {
    if (!open || !trigger.current || !popup.current) return;
    const anchor = trigger.current.getBoundingClientRect();
    const panel = popup.current.getBoundingClientRect();
    setPosition({
      left: Math.max(
        8,
        Math.min(anchor.left, window.innerWidth - panel.width - 8),
      ),
      top: Math.max(
        8,
        Math.min(anchor.bottom + 8, window.innerHeight - panel.height - 8),
      ),
    });
  }, [open, workspace, error]);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      if (
        !trigger.current?.contains(event.target as Node) &&
        !popup.current?.contains(event.target as Node)
      )
        hide();
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        hide();
      }
    };
    const scroll = (event: Event) => {
      if (!popup.current?.contains(event.target as Node)) hide();
    };
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape, true);
    document.addEventListener("scroll", scroll, true);
    window.addEventListener("resize", hide);
    return () => {
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("keydown", escape, true);
      document.removeEventListener("scroll", scroll, true);
      window.removeEventListener("resize", hide);
    };
  }, [open]);
  return (
    <>
      <button
        ref={trigger}
        type="button"
        className="icon session-workspace-info"
        aria-label={t("Session directory")}
        aria-expanded={open}
        aria-describedby={open ? id : undefined}
        onPointerEnter={enter}
        onPointerLeave={exit}
        onFocus={show}
        onBlur={leave}
        onClick={show}
      >
        <Info size={14} aria-hidden="true" />
      </button>
      {open &&
        createPortal(
          <div
            ref={popup}
            id={id}
            role="tooltip"
            className="session-workspace-popover"
            style={{ ...position, visibility: position ? "visible" : "hidden" }}
            onPointerEnter={enter}
            onPointerLeave={exit}
          >
            {workspace ? (
              <SessionWorkspaceInfo workspace={workspace} t={t} />
            ) : (
              <p role={error ? "alert" : "status"}>
                {error || t("Loading session directory…")}
              </p>
            )}
          </div>,
          document.body,
        )}
    </>
  );
}

export function SessionWorkspaceInfo({
  workspace,
  t,
}: {
  workspace: SessionWorkspace;
  t: Translator;
}) {
  return (
    <div className="session-workspace" aria-label={t("Session directory")}>
      <div className="session-workspace-location">
        <FolderGit2 size={14} aria-hidden="true" />
        <span className="session-workspace-kind">
          {t(
            workspace.kind === "worktree"
              ? "Worktree"
              : workspace.kind === "main"
                ? "Main checkout"
                : "Directory",
          )}
        </span>
        <span className="session-workspace-path" title={workspace.path}>
          {workspace.path}
        </span>
      </div>
      {(workspace.branch || workspace.commit) && (
        <div
          className="session-workspace-branch"
          title={workspace.branch || workspace.commit}
        >
          {workspace.branch ? (
            <GitBranch size={14} aria-hidden="true" />
          ) : (
            <GitCommitHorizontal size={14} aria-hidden="true" />
          )}
          <span>
            {workspace.branch ||
              t("Detached HEAD · {commit}", { commit: workspace.commit || "" })}
          </span>
        </div>
      )}
      {!workspace.available && (
        <p className="file-panel-notice" role="status">
          {t("Session directory is unavailable")}
        </p>
      )}
    </div>
  );
}
