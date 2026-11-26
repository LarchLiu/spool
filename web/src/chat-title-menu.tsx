import { useEffect, useId, useRef, useState } from "react";
import type { ReactNode, RefObject } from "react";
import { createPortal } from "react-dom";
import {
  Archive,
  ChevronDown,
  Pencil,
  Pin,
  PinOff,
  Trash2,
  X,
} from "lucide-react";
import type { Translator } from "./i18n";
import { ActionMenu } from "./action-menu";
import {
  useChatTitleTooltip,
  type ChatTitleTooltipSession,
} from "./chat-title-tooltip";

type ChatActionProps = {
  title: string;
  t: Translator;
  pinned: boolean;
  onTogglePin: () => void;
  canManage?: boolean;
  tooltipSide?: "below" | "right";
  tooltipSession?: ChatTitleTooltipSession;
  onRename: (title: string) => Promise<void>;
  onArchive: () => Promise<void>;
  onDelete: () => Promise<void>;
  onError: (error: unknown) => void;
};

export function ChatTitleMenu(props: ChatActionProps) {
  return (
    <ChatActionMenu {...props}>
      {({ trigger, id, open, archiving, show, close, tooltipProps }) => (
        <button
          {...tooltipProps}
          ref={trigger}
          type="button"
          className="chat-title-trigger"
          aria-label={`${props.t("Chat options")}: ${props.title}`}
          aria-haspopup="menu"
          aria-expanded={open}
          aria-controls={open ? id : undefined}
          disabled={archiving}
          onClick={() => (open ? close() : show())}
          onKeyDown={(event) => {
            if (event.key === "ArrowDown" || event.key === "ArrowUp") {
              event.preventDefault();
              show();
            }
          }}
        >
          <strong>{props.title}</strong>
          <ChevronDown size={13} aria-hidden="true" />
        </button>
      )}
    </ChatActionMenu>
  );
}

export function ChatActionMenu({
  title,
  t,
  pinned,
  onTogglePin,
  canManage = true,
  tooltipSide,
  tooltipSession,
  onRename,
  onArchive,
  onDelete,
  onError,
  children,
}: ChatActionProps & {
  children: (controls: {
    trigger: RefObject<HTMLButtonElement | null>;
    id: string;
    open: boolean;
    archiving: boolean;
    show: (point?: { left: number; top: number }) => void;
    close: () => void;
    archive: () => Promise<void>;
    tooltipProps: ReturnType<typeof useChatTitleTooltip>["tooltipProps"];
  }) => ReactNode;
}) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [archiving, setArchiving] = useState(false);
  const { tooltip, tooltipProps } = useChatTitleTooltip(
    title,
    trigger,
    open || renaming || deleting || archiving,
    tooltipSide,
    tooltipSession,
  );
  const archivePending = useRef(false);
  const [point, setPoint] = useState<{ left?: number; top?: number }>({});
  function show(origin?: { left: number; top: number }) {
    if (archivePending.current) return;
    setPoint(origin ?? {});
    setOpen(true);
  }
  function close(restoreFocus = false) {
    setOpen(false);
    if (restoreFocus) trigger.current?.focus({ preventScroll: true });
  }
  async function archive() {
    if (!canManage || archivePending.current) return;
    archivePending.current = true;
    setArchiving(true);
    try {
      await onArchive();
      close();
    } catch (error) {
      close(true);
      onError(error);
    } finally {
      archivePending.current = false;
      setArchiving(false);
    }
  }
  return (
    <>
      {children({
        trigger,
        id,
        open,
        archiving,
        show,
        close,
        archive,
        tooltipProps,
      })}
      {tooltip}
      {open && (
        <ActionMenu
          id={id}
          label={t("Chat options")}
          anchor={trigger}
          point={point}
          onClose={close}
        >
          {canManage && (
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
          )}
          <button
            type="button"
            role="menuitem"
            disabled={archiving}
            onClick={(event) => {
              onTogglePin();
              close(event.detail === 0);
            }}
          >
            {pinned ? (
              <PinOff size={15} aria-hidden="true" />
            ) : (
              <Pin size={15} aria-hidden="true" />
            )}
            {t(pinned ? "Unpin chat" : "Pin chat")}
          </button>
          {canManage && (
            <div className="chat-title-menu-separator" role="separator" />
          )}
          {canManage && (
            <button
              type="button"
              role="menuitem"
              disabled={archiving}
              aria-busy={archiving}
              onClick={archive}
            >
              <Archive size={15} aria-hidden="true" />
              {t(archiving ? "Archiving…" : "Archive")}
            </button>
          )}
          {canManage && (
            <button
              type="button"
              role="menuitem"
              className="destructive"
              disabled={archiving}
              onClick={() => {
                close();
                setDeleting(true);
              }}
            >
              <Trash2 size={15} aria-hidden="true" />
              {t("Delete")}
            </button>
          )}
        </ActionMenu>
      )}
      {deleting &&
        createPortal(
          <DeleteChatDialog
            title={title}
            t={t}
            onDelete={onDelete}
            onError={onError}
            onClose={() => {
              setDeleting(false);
              trigger.current?.focus({ preventScroll: true });
            }}
          />,
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

function DeleteChatDialog({
  title,
  t,
  onDelete,
  onError,
  onClose,
}: {
  title: string;
  t: Translator;
  onDelete: () => Promise<void>;
  onError: (error: unknown) => void;
  onClose: () => void;
}) {
  const id = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  const cancel = useRef<HTMLButtonElement>(null);
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    dialog.current?.showModal();
    cancel.current?.focus();
  }, []);
  return (
    <dialog
      ref={dialog}
      className="delete-chat-dialog"
      aria-labelledby={id}
      aria-describedby={`${id}-description`}
      onCancel={(event) => {
        event.preventDefault();
        if (!saving) onClose();
      }}
    >
      <form
        onSubmit={async (event) => {
          event.preventDefault();
          if (saving) return;
          setSaving(true);
          try {
            await onDelete();
            onClose();
          } catch (error) {
            onClose();
            onError(error);
          } finally {
            setSaving(false);
          }
        }}
      >
        <div className="dialog-heading">
          <h2 id={id}>{t("Delete chat")}</h2>
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
        <p className="delete-chat-title">{title}</p>
        <p id={`${id}-description`}>
          {t(
            "This permanently deletes this chat and its subagent history. This action cannot be undone.",
          )}
        </p>
        <div className="rename-chat-actions">
          <button
            ref={cancel}
            type="button"
            className="quiet"
            disabled={saving}
            onClick={onClose}
          >
            {t("Cancel")}
          </button>
          <button type="submit" className="quiet destructive" disabled={saving}>
            {t(saving ? "Deleting…" : "Delete")}
          </button>
        </div>
      </form>
    </dialog>
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
