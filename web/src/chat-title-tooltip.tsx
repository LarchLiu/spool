import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type PointerEvent,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { AgentIcon } from "./agent-icon";
import { messageTime } from "./message-footer";
import type { Locale } from "./preferences";
import type { Backend } from "./types";

export type ChatTitleTooltipSession = {
  backend: Backend;
  updatedAt: number;
  locale: Locale;
};

export function useChatTitleTooltip(
  title: string,
  trigger: RefObject<HTMLButtonElement | null>,
  disabled: boolean,
  side: "below" | "right" = "below",
  session?: ChatTitleTooltipSession,
) {
  const id = useId();
  const popup = useRef<HTMLDivElement>(null);
  const popupHovered = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<{
    left: number;
    top: number;
    maxHeight?: number;
  }>();
  const visible = open && !disabled;
  const time =
    visible && session ? messageTime(session.updatedAt, session.locale) : null;

  function hide() {
    clearTimeout(timer.current);
    popupHovered.current = false;
    setOpen(false);
  }
  function show() {
    clearTimeout(timer.current);
    const label = trigger.current?.querySelector<HTMLElement>(
      ".nav-chat-title, strong",
    );
    if (
      !disabled &&
      label &&
      (side === "right" || label.scrollWidth > label.clientWidth)
    ) {
      setOpen(true);
    }
  }
  function leave() {
    clearTimeout(timer.current);
    // Allow moving into the portal to read or scroll a long title.
    timer.current = setTimeout(() => {
      if (!popupHovered.current) hide();
    }, 150);
  }
  function enter(event: PointerEvent) {
    if (event.pointerType === "touch") return;
    clearTimeout(timer.current);
    timer.current = setTimeout(show, 700);
  }

  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(hide, [disabled, title]);
  useLayoutEffect(() => {
    if (!visible || !trigger.current || !popup.current) return;
    const anchor = (
      (side === "right" && trigger.current.closest(".nav-chat")) ||
      trigger.current
    ).getBoundingClientRect();
    const panel = popup.current.getBoundingClientRect();
    const edge = 8;
    const below = anchor.bottom + 6;
    const top =
      side === "right"
        ? anchor.top
        : below + panel.height <= window.innerHeight - edge
          ? below
          : anchor.top - panel.height - 6;
    const sidebar = trigger.current
      .closest(".sidebar")
      ?.getBoundingClientRect();
    const left =
      side === "right" ? (sidebar?.right ?? anchor.right) + 8 : anchor.left;
    setPosition({
      left: Math.max(
        edge,
        Math.min(left, window.innerWidth - panel.width - edge),
      ),
      top:
        side === "right"
          ? Math.max(edge, top)
          : Math.max(
              edge,
              Math.min(top, window.innerHeight - panel.height - edge),
            ),
      maxHeight:
        side === "right"
          ? Math.min(480, window.innerHeight - Math.max(edge, top) - edge)
          : undefined,
    });
  }, [
    visible,
    title,
    trigger,
    side,
    session?.backend,
    session?.updatedAt,
    session?.locale,
  ]);
  useEffect(() => {
    if (!visible) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.isComposing) return;
      event.preventDefault();
      event.stopPropagation();
      hide();
    };
    const scroll = (event: Event) => {
      if (!popup.current?.contains(event.target as Node)) hide();
    };
    const outside = (event: Event) => {
      if (!popup.current?.contains(event.target as Node)) hide();
    };
    document.addEventListener("keydown", escape);
    document.addEventListener("pointerdown", outside);
    document.addEventListener("scroll", scroll, true);
    window.addEventListener("resize", hide);
    return () => {
      document.removeEventListener("keydown", escape);
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("scroll", scroll, true);
      window.removeEventListener("resize", hide);
    };
  }, [visible]);

  return {
    tooltipProps: {
      "aria-describedby": visible ? id : undefined,
      onPointerEnter: enter,
      onPointerLeave: leave,
      onFocus: () => {
        if (trigger.current?.matches(":focus-visible")) show();
      },
      onBlur: leave,
      onPointerDown: hide,
    },
    tooltip: visible
      ? createPortal(
          <div
            ref={popup}
            id={id}
            role="tooltip"
            className="chat-title-tooltip"
            data-side={side}
            style={{ ...position, visibility: position ? "visible" : "hidden" }}
            onPointerEnter={() => {
              popupHovered.current = true;
              clearTimeout(timer.current);
            }}
            onPointerLeave={() => {
              popupHovered.current = false;
              leave();
            }}
          >
            {session && (
              <div className="chat-title-tooltip-meta">
                <AgentIcon backend={session.backend} colored />
                {time && (
                  <time dateTime={time.dateTime} title={time.title}>
                    {time.label}
                  </time>
                )}
              </div>
            )}
            <div className="chat-title-tooltip-title">{title}</div>
          </div>,
          document.body,
        )
      : null,
  };
}
