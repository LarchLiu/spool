import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  CornerDownRight,
  Ellipsis,
  GripVertical,
  ListEnd,
  Pencil,
  Trash2,
} from "lucide-react";
import type { Translator } from "./i18n";
import type { QueuedMessage } from "./types";

// Messages sent while the agent was still answering. Each waits for the
// running turn to end unless it is steered into that turn or removed.
export function QueuedMessages({
  messages,
  running,
  busy,
  t,
  onSteer,
  onDelete,
  onEdit,
  onMove,
}: {
  messages: readonly QueuedMessage[];
  running: boolean;
  busy: boolean;
  t: Translator;
  onSteer: (id: string) => void;
  onDelete: (id: string) => void;
  onEdit: (id: string) => void;
  onMove: (id: string, beforeID: string) => void;
}) {
  const list = useRef<HTMLUListElement>(null);
  const pointer = useRef<{
    id: string;
    x: number;
    y: number;
    latestY: number;
    moved: boolean;
    before: string;
  } | null>(null);
  const [drag, setDrag] = useState<{ id: string; before: string } | null>(null);
  const [menu, setMenu] = useState<{
    id: string;
    left: number;
    top: number;
  } | null>(null);
  const trigger = useRef<HTMLButtonElement | null>(null);
  const popup = useRef<HTMLDivElement>(null);
  const closeMenu = () => {
    setMenu(null);
    trigger.current?.focus({ preventScroll: true });
  };
  const updateDrag = () => {
    const current = pointer.current,
      queue = list.current;
    if (!current?.moved || !queue) return;
    const bounds = queue.getBoundingClientRect();
    if (current.latestY < bounds.top + 20) queue.scrollTop -= 8;
    else if (current.latestY > bounds.bottom - 20) queue.scrollTop += 8;
    const rows = Array.from(
      queue.querySelectorAll<HTMLElement>("[data-queue-id]"),
    );
    current.before =
      rows.find(
        (row) =>
          current.latestY <
          row.getBoundingClientRect().top + row.offsetHeight / 2,
      )?.dataset.queueId || "";
    setDrag((old) =>
      old?.id === current.id && old.before === current.before
        ? old
        : { id: current.id, before: current.before },
    );
  };
  useEffect(() => {
    if (!drag) return;
    let frame: number;
    const tick = () => {
      updateDrag();
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [drag?.id]);
  useEffect(() => {
    if (!menu) return;
    const outside = (event: PointerEvent) => {
      if (
        !popup.current?.contains(event.target as Node) &&
        !trigger.current?.contains(event.target as Node)
      )
        setMenu(null);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        closeMenu();
      } else if (event.key === "Tab") setMenu(null);
    };
    const close = () => setMenu(null);
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape);
    window.addEventListener("resize", close);
    window.addEventListener("scroll", close, true);
    return () => {
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("keydown", escape);
      window.removeEventListener("resize", close);
      window.removeEventListener("scroll", close, true);
    };
  }, [menu]);
  useEffect(() => {
    if (busy || (menu && !messages.some((message) => message.id === menu.id)))
      setMenu(null);
    if (
      busy ||
      (pointer.current &&
        !messages.some((message) => message.id === pointer.current?.id))
    ) {
      pointer.current = null;
      setDrag(null);
    }
  }, [messages, busy]);
  if (!messages.length) return null;
  return (
    <>
      <ul
        ref={list}
        className="queued-messages"
        aria-label={t("Queued messages")}
      >
        {messages.map((message) => (
          <li
            className={
              "queued-message" +
              (drag?.id === message.id ? " dragging" : "") +
              (drag?.before === message.id && drag.id !== message.id
                ? " drop-before"
                : "") +
              (drag && !drag.before && messages.at(-1)?.id === message.id
                ? " drop-after"
                : "")
            }
            key={message.id}
            data-queue-id={message.id}
          >
            {messages.length > 1 ? (
              <button
                type="button"
                className="icon queued-message-grip"
                disabled={busy}
                aria-label={t("Drag to reorder")}
                title={t("Drag to reorder")}
                onKeyDown={(event) => {
                  const at = messages.findIndex(
                    (item) => item.id === message.id,
                  );
                  if (event.key === "ArrowUp" && at > 0) {
                    event.preventDefault();
                    onMove(message.id, messages[at - 1].id);
                  } else if (
                    event.key === "ArrowDown" &&
                    at < messages.length - 1
                  ) {
                    event.preventDefault();
                    onMove(message.id, messages[at + 2]?.id || "");
                  }
                }}
                onPointerDown={(event) => {
                  if (event.button !== 0 || busy) return;
                  setMenu(null);
                  pointer.current = {
                    id: message.id,
                    x: event.clientX,
                    y: event.clientY,
                    latestY: event.clientY,
                    moved: false,
                    before: message.id,
                  };
                  event.currentTarget.setPointerCapture(event.pointerId);
                }}
                onPointerMove={(event) => {
                  const current = pointer.current;
                  if (!current) return;
                  if (
                    !current.moved &&
                    Math.hypot(
                      event.clientX - current.x,
                      event.clientY - current.y,
                    ) < 5
                  )
                    return;
                  current.moved = true;
                  current.latestY = event.clientY;
                  updateDrag();
                }}
                onPointerUp={() => {
                  const current = pointer.current;
                  pointer.current = null;
                  setDrag(null);
                  if (current?.moved && current.before !== current.id)
                    onMove(current.id, current.before);
                }}
                onPointerCancel={() => {
                  pointer.current = null;
                  setDrag(null);
                }}
                onLostPointerCapture={() => {
                  pointer.current = null;
                  setDrag(null);
                }}
              >
                <GripVertical size={15} aria-hidden="true" />
              </button>
            ) : (
              <ListEnd size={15} aria-hidden="true" />
            )}
            <span className="queued-message-text">
              {message.message.trim() || message.attachments?.join(", ")}
            </span>
            <button
              type="button"
              className="queued-message-steer"
              disabled={busy}
              onClick={() => onSteer(message.id)}
            >
              <CornerDownRight size={14} aria-hidden="true" />
              {t(running ? "Steer" : "Send now")}
            </button>
            <button
              type="button"
              className="icon"
              disabled={busy}
              aria-label={t("Delete queued message")}
              title={t("Delete queued message")}
              onClick={() => onDelete(message.id)}
            >
              <Trash2 size={15} />
            </button>
            <button
              type="button"
              className="icon queued-message-more"
              disabled={busy}
              aria-label={t("Queued message actions")}
              title={t("Queued message actions")}
              aria-haspopup="menu"
              aria-expanded={menu?.id === message.id}
              onClick={(event) => {
                if (menu?.id === message.id) {
                  closeMenu();
                  return;
                }
                trigger.current = event.currentTarget;
                const bounds = event.currentTarget.getBoundingClientRect();
                setMenu({
                  id: message.id,
                  left: Math.max(
                    8,
                    Math.min(innerWidth - 188, bounds.right - 180),
                  ),
                  top: bounds.top > 54 ? bounds.top - 48 : bounds.bottom + 4,
                });
              }}
            >
              <Ellipsis size={16} />
            </button>
          </li>
        ))}
      </ul>
      {menu &&
        createPortal(
          <div
            ref={popup}
            className="queued-message-menu"
            role="menu"
            style={{ left: menu.left, top: menu.top }}
          >
            <button
              type="button"
              role="menuitem"
              autoFocus
              onClick={() => {
                const id = menu.id;
                closeMenu();
                onEdit(id);
              }}
            >
              <Pencil size={14} />
              {t("Edit message")}
            </button>
          </div>,
          document.body,
        )}
    </>
  );
}

// A key that belongs to an input method choosing text, not to the composer.
// WebKit reports the Enter that confirms a choice after the composition has
// ended, marked only by key code 229.
export function inputMethodKey(event: {
  isComposing: boolean;
  keyCode: number;
}) {
  return event.isComposing || event.keyCode === 229;
}

// Enter sends and Shift+Enter breaks the line. A touch keyboard has no
// Shift+Enter, so there Enter keeps its line break and the button sends.
export function composerEnterAction(
  event: {
    key: string;
    shiftKey: boolean;
    metaKey: boolean;
    ctrlKey: boolean;
    altKey: boolean;
    isComposing: boolean;
    keyCode: number;
  },
  touchKeyboard: boolean,
): "send" | "steer" | null {
  if (inputMethodKey(event)) return null;
  if (event.key !== "Enter" || event.shiftKey) return null;
  if (event.metaKey || event.ctrlKey) return "steer";
  if (event.altKey) return "send";
  return touchKeyboard ? null : "send";
}
