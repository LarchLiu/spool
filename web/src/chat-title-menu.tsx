import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Archive, ChevronDown, Pencil, X } from "lucide-react";
import type { Translator } from "./i18n";

export function ChatTitleMenu({
  title,
  t,
  onRename,
  onArchive,
  onError,
}: {
  title: string;
  t: Translator;
  onRename: (title: string) => Promise<void>;
  onArchive: () => Promise<void>;
  onError: (error: unknown) => void;
}) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [archiving, setArchiving] = useState(false);
  const [position, setPosition] = useState<{ left: number; top: number }>();
  function close(restoreFocus = false) {
    setOpen(false);
    if (restoreFocus) trigger.current?.focus({ preventScroll: true });
  }
  useLayoutEffect(() => {
    if (!open || !trigger.current || !menu.current) return;
    const anchor = trigger.current.getBoundingClientRect();
    const panel = menu.current.getBoundingClientRect();
    setPosition({
      left: Math.max(8, Math.min(anchor.left, innerWidth - panel.width - 8)),
      top: Math.max(
        8,
        Math.min(anchor.bottom + 6, innerHeight - panel.height - 8),
      ),
    });
  }, [open]);
  useLayoutEffect(() => {
    if (open && position)
      menu.current?.querySelector<HTMLButtonElement>("button")?.focus();
  }, [open, position]);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      if (
        !trigger.current?.contains(event.target as Node) &&
        !menu.current?.contains(event.target as Node)
      )
        close();
    };
    const escape = (event: KeyboardEvent) => {
      if (event.isComposing || event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      close(true);
    };
    const reposition = () => close();
    document.addEventListener("pointerdown", outside, true);
    document.addEventListener("keydown", escape, true);
    window.addEventListener("resize", reposition);
    document.addEventListener("scroll", reposition, true);
    return () => {
      document.removeEventListener("pointerdown", outside, true);
      document.removeEventListener("keydown", escape, true);
      window.removeEventListener("resize", reposition);
      document.removeEventListener("scroll", reposition, true);
    };
  }, [open]);
  return (
    <>
      <button
        ref={trigger}
        type="button"
        className="chat-title-trigger"
        title={title}
        aria-label={`${t("Chat options")}: ${title}`}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        disabled={archiving}
        onClick={() => setOpen(!open)}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            setOpen(true);
          }
        }}
      >
        <strong>{title}</strong>
        <ChevronDown size={13} aria-hidden="true" />
      </button>
      {open &&
        createPortal(
          <div
            id={id}
            ref={menu}
            className="chat-title-menu"
            role="menu"
            aria-label={t("Chat options")}
            style={{ ...position, visibility: position ? "visible" : "hidden" }}
            onBlur={(event) => {
              // WebKit blurs buttons without a new focus target on mouse down,
              // before their click handler can open the rename dialog.
              if (
                event.relatedTarget &&
                !event.currentTarget.contains(event.relatedTarget as Node)
              )
                close();
            }}
            onKeyDown={(event) => {
              if (event.key === "Tab") {
                close(true);
                return;
              }
              if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
                event.preventDefault();
                const items = Array.from(
                  menu.current?.querySelectorAll<HTMLButtonElement>(
                    "button:not(:disabled)",
                  ) || [],
                );
                const current = items.findIndex(
                  (item) => item === document.activeElement,
                );
                const index =
                  event.key === "Home"
                    ? 0
                    : event.key === "End"
                      ? items.length - 1
                      : (current +
                          (event.key === "ArrowDown" ? 1 : -1) +
                          items.length) %
                        items.length;
                items[index]?.focus();
              }
            }}
          >
            <button
              type="button"
              role="menuitem"
              disabled={archiving}
              onClick={() => {
                close();
                setRenaming(true);
              }}
            >
              <Pencil size={15} aria-hidden="true" />
              {t("Rename")}
            </button>
            <button
              type="button"
              role="menuitem"
              disabled={archiving}
              aria-busy={archiving}
              onClick={async () => {
                setArchiving(true);
                try {
                  await onArchive();
                  close();
                } catch (error) {
                  close(true);
                  onError(error);
                } finally {
                  setArchiving(false);
                }
              }}
            >
              <Archive size={15} aria-hidden="true" />
              {t(archiving ? "Archiving…" : "Archive")}
            </button>
          </div>,
          document.body,
        )}
      {renaming &&
        createPortal(
          <RenameChatDialog
            title={title}
            t={t}
            onRename={onRename}
            onClose={() => {
              setRenaming(false);
              trigger.current?.focus({ preventScroll: true });
            }}
          />,
          document.body,
        )}
    </>
  );
}

function RenameChatDialog({
  title,
  t,
  onRename,
  onClose,
}: {
  title: string;
  t: Translator;
  onRename: (title: string) => Promise<void>;
  onClose: () => void;
}) {
  const id = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const [draft, setDraft] = useState(title);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    dialog.current?.showModal();
    input.current?.focus();
    input.current?.select();
  }, []);
  return (
    <dialog
      ref={dialog}
      className="rename-chat-dialog"
      aria-labelledby={id}
      onCancel={(event) => {
        event.preventDefault();
        if (!saving) onClose();
      }}
      onClick={(event) => {
        if (event.target !== event.currentTarget || saving) return;
        const box = event.currentTarget.getBoundingClientRect();
        if (
          event.clientX < box.left ||
          event.clientX > box.right ||
          event.clientY < box.top ||
          event.clientY > box.bottom
        )
          onClose();
      }}
    >
      <form
        onSubmit={async (event) => {
          event.preventDefault();
          const title = draft.trim();
          if (!title || saving) return;
          setSaving(true);
          setError("");
          try {
            await onRename(title);
            onClose();
          } catch (error) {
            setError(
              error instanceof Error
                ? error.message
                : t("Could not rename chat"),
            );
          } finally {
            setSaving(false);
          }
        }}
      >
        <div className="dialog-heading">
          <h2 id={id}>{t("Rename chat")}</h2>
          <button
            className="icon"
            type="button"
            disabled={saving}
            aria-label={t("Cancel")}
            onClick={onClose}
          >
            <X size={18} aria-hidden="true" />
          </button>
        </div>
        <label htmlFor={`${id}-input`}>{t("Chat title")}</label>
        <input
          id={`${id}-input`}
          ref={input}
          value={draft}
          maxLength={200}
          disabled={saving}
          autoComplete="off"
          aria-describedby={error ? `${id}-error` : undefined}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (
              event.key === "Enter" &&
              (event.nativeEvent.isComposing || event.keyCode === 229)
            )
              event.preventDefault();
          }}
        />
        {error && (
          <p id={`${id}-error`} className="error" role="alert">
            {error}
          </p>
        )}
        <div className="rename-chat-actions">
          <button
            type="button"
            className="quiet"
            disabled={saving}
            onClick={onClose}
          >
            {t("Cancel")}
          </button>
          <button
            type="submit"
            className="primary"
            disabled={saving || !draft.trim()}
          >
            {t(saving ? "Saving…" : "Rename")}
          </button>
        </div>
      </form>
    </dialog>
  );
}
