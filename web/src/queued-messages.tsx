import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
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
import { DeliveryStatus } from "./message-delivery";

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
    scrollTop: number;
    moved: boolean;
    before: string;
    originalBefore: string;
  } | null>(null);
  const [drag, setDrag] = useState<{
    id: string;
    before: string;
    offset: number;
    height: number;
  } | null>(null);
  const [pendingMove, setPendingMove] = useState<{
    id: string;
    before: string;
  } | null>(null);
  const positions = useRef(new Map<string, number>());
  const orderedMessages = useMemo(() => {
    if (!pendingMove) return messages;
    const moving = messages.find((message) => message.id === pendingMove.id);
    if (!moving) return messages;
    const ordered = messages.filter((message) => message.id !== moving.id);
    const at = ordered.findIndex(
      (message) => message.id === pendingMove.before,
    );
    ordered.splice(at < 0 ? ordered.length : at, 0, moving);
    return ordered;
  }, [messages, pendingMove]);
  const [menu, setMenu] = useState<{
    id: string;
    left: number;
    top: number;
  } | null>(null);
  const trigger = useRef<HTMLButtonElement | null>(null);
  const canReorder =
    !busy && !messages.some((message) => !!message.delivery_status);
  const popup = useRef<HTMLDivElement>(null);
  const closeMenu = () => {
    setMenu(null);
    trigger.current?.focus({ preventScroll: true });
  };
  const rememberPositions = () => {
    const queue = list.current;
    if (!queue) return;
    const top = queue.getBoundingClientRect().top + queue.clientTop;
    positions.current = new Map(
      Array.from(
        queue.querySelectorAll<HTMLElement>("[data-queue-id]"),
        (row) => [
          row.dataset.queueId!,
          row.getBoundingClientRect().top - top + queue.scrollTop,
        ],
      ),
    );
  };
  const move = (id: string, before: string) => {
    setPendingMove({ id, before });
    onMove(id, before);
  };
  const finishDrag = (commit: boolean) => {
    const current = pointer.current;
    if (!current) return;
    rememberPositions();
    pointer.current = null;
    setDrag(null);
    if (commit && current.moved && current.before !== current.originalBefore)
      move(current.id, current.before);
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
    const moving = rows.find((row) => row.dataset.queueId === current.id);
    if (!moving) return;
    const pointerY =
      current.latestY - bounds.top - queue.clientTop + queue.scrollTop;
    current.before =
      rows.find(
        (row) =>
          row !== moving && pointerY < row.offsetTop + row.offsetHeight / 2,
      )?.dataset.queueId || "";
    const offset = Math.max(
      queue.scrollTop - moving.offsetTop,
      Math.min(
        current.latestY - current.y + queue.scrollTop - current.scrollTop,
        queue.scrollTop +
          queue.clientHeight -
          moving.offsetTop -
          moving.offsetHeight,
      ),
    );
    setDrag((old) =>
      old?.id === current.id &&
      old.before === current.before &&
      old.offset === offset
        ? old
        : {
            id: current.id,
            before: current.before,
            offset,
            height: moving.offsetHeight,
          },
    );
  };
  useLayoutEffect(() => {
    const queue = list.current;
    if (!queue || drag) return;
    const next = new Map<string, number>();
    const reducedMotion = matchMedia(
      "(prefers-reduced-motion: reduce)",
    ).matches;
    for (const row of queue.querySelectorAll<HTMLElement>("[data-queue-id]")) {
      const id = row.dataset.queueId!;
      const previous = positions.current.get(id);
      next.set(id, row.offsetTop);
      if (previous === undefined || reducedMotion) continue;
      const offset = previous - row.offsetTop;
      if (Math.abs(offset) < 0.5) continue;
      for (const animation of row.getAnimations()) animation.cancel();
      row.animate(
        [
          { transform: `translateY(${offset}px)` },
          { transform: "translateY(0)" },
        ],
        { duration: 180, easing: "cubic-bezier(0.2, 0, 0, 1)" },
      );
    }
    positions.current = next;
  }, [orderedMessages, drag?.id]);
  useEffect(() => {
    if (!busy) setPendingMove(null);
  }, [busy, pendingMove]);
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
      finishDrag(false);
    }
  }, [messages, busy]);
  const from = orderedMessages.findIndex((message) => message.id === drag?.id);
  const before = orderedMessages.findIndex(
    (message) => message.id === drag?.before,
  );
  const to =
    before < 0 ? orderedMessages.length - 1 : before - (before > from ? 1 : 0);
  if (!messages.length) return null;
  return (
    <>
      <ul
        ref={list}
        className={"queued-messages" + (drag ? " reordering" : "")}
        aria-label={t("Queued messages")}
      >
        {orderedMessages.map((message, index) => (
          <li
            className={
              "queued-message" + (drag?.id === message.id ? " dragging" : "")
            }
            style={
              drag
                ? {
                    transform: `translateY(${
                      index === from
                        ? drag.offset
                        : index > from && index <= to
                          ? -drag.height
                          : index < from && index >= to
                            ? drag.height
                            : 0
                    }px)`,
                  }
                : undefined
            }
            key={message.id}
            data-queue-id={message.id}
          >
            {messages.length > 1 ? (
              <button
                type="button"
                className="icon queued-message-grip"
                disabled={!canReorder}
                aria-label={t("Drag to reorder")}
                title={t("Drag to reorder")}
                onKeyDown={(event) => {
                  if (!canReorder) return;
                  const at = orderedMessages.findIndex(
                    (item) => item.id === message.id,
                  );
                  if (event.key === "ArrowUp" && at > 0) {
                    event.preventDefault();
                    rememberPositions();
                    move(message.id, orderedMessages[at - 1].id);
                  } else if (
                    event.key === "ArrowDown" &&
                    at < orderedMessages.length - 1
                  ) {
                    event.preventDefault();
                    rememberPositions();
                    move(message.id, orderedMessages[at + 2]?.id || "");
                  }
                }}
                onPointerDown={(event) => {
                  if (event.button !== 0 || !canReorder) return;
                  setMenu(null);
                  for (const row of list.current!.querySelectorAll(
                    "[data-queue-id]",
                  ))
                    for (const animation of row.getAnimations())
                      animation.cancel();
                  const at = orderedMessages.findIndex(
                    (item) => item.id === message.id,
                  );
                  const before = orderedMessages[at + 1]?.id || "";
                  pointer.current = {
                    id: message.id,
                    x: event.clientX,
                    y: event.clientY,
                    latestY: event.clientY,
                    scrollTop: list.current!.scrollTop,
                    moved: false,
                    before,
                    originalBefore: before,
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
                onPointerUp={() => finishDrag(true)}
                onPointerCancel={() => finishDrag(false)}
                onLostPointerCapture={() => finishDrag(false)}
              >
                <GripVertical size={15} aria-hidden="true" />
              </button>
            ) : (
              <ListEnd size={15} aria-hidden="true" />
            )}
            <span className="queued-message-text">
              {message.message.trim() || message.attachments?.join(", ")}
            </span>
            {message.delivery_status && (
              <DeliveryStatus status={message.delivery_status} t={t} />
            )}
            {!message.delivery_status && (
              <button
                type="button"
                className="queued-message-steer"
                disabled={busy}
                onClick={() => onSteer(message.id)}
              >
                <CornerDownRight size={14} aria-hidden="true" />
                {t(running ? "Steer" : "Send now")}
              </button>
            )}
            {message.delivery_status !== "sending" && (
              <>
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
                      top:
                        bounds.top > 54 ? bounds.top - 48 : bounds.bottom + 4,
                    });
                  }}
                >
                  <Ellipsis size={16} />
                </button>
              </>
            )}
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
    isComposing: boolean;
    keyCode: number;
  },
  touchKeyboard: boolean,
): "send" | "steer" | null {
  if (inputMethodKey(event)) return null;
  if (event.key !== "Enter" || event.shiftKey) return null;
  if (event.metaKey || event.ctrlKey) return "steer";
  return touchKeyboard ? null : "send";
}
