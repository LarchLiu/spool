import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import type { ReactNode, RefObject } from "react";
import { createPortal } from "react-dom";

/** Shared sidebar and project-file menu surface, focus and dismissal behavior. */
export function ActionMenu({
  id,
  label,
  anchor,
  point,
  onClose,
  children,
}: {
  id?: string;
  label: string;
  anchor: RefObject<HTMLElement | null>;
  point?: { left?: number; top?: number };
  onClose: (restoreFocus?: boolean) => void;
  children: ReactNode;
}) {
  const generatedID = useId();
  const menu = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<{ left: number; top: number }>();
  useLayoutEffect(() => {
    if (!menu.current) return;
    const origin = anchor.current?.getBoundingClientRect();
    const panel = menu.current.getBoundingClientRect();
    setPosition({
      left: Math.max(
        8,
        Math.min(
          point?.left ?? origin?.left ?? 8,
          innerWidth - panel.width - 8,
        ),
      ),
      top: Math.max(
        8,
        Math.min(
          point?.top ?? (origin ? origin.bottom + 6 : 8),
          innerHeight - panel.height - 8,
        ),
      ),
    });
  }, [anchor, point]);
  useLayoutEffect(() => {
    if (!position) return;
    const first = menu.current?.querySelector<HTMLButtonElement>(
      "button:not(:disabled)",
    );
    (first ?? menu.current)?.focus();
  }, [position]);
  useEffect(() => {
    const escape = (event: KeyboardEvent) => {
      if (event.isComposing || event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      onClose(true);
    };
    const dismiss = () => onClose();
    document.addEventListener("keydown", escape, true);
    window.addEventListener("resize", dismiss);
    document.addEventListener("scroll", dismiss, true);
    return () => {
      document.removeEventListener("keydown", escape, true);
      window.removeEventListener("resize", dismiss);
      document.removeEventListener("scroll", dismiss, true);
    };
  }, [onClose]);
  return createPortal(
    <>
      <div
        className="chat-menu-backdrop"
        aria-hidden="true"
        onPointerDown={(event) => {
          event.preventDefault();
          event.stopPropagation();
        }}
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          onClose();
        }}
        onTouchEnd={(event) => {
          // WebKit can suppress click after a canceled pointerdown.
          // Cancel the compatibility click before removing its target.
          event.preventDefault();
          event.stopPropagation();
          onClose();
        }}
        onContextMenu={(event) => {
          event.preventDefault();
          event.stopPropagation();
          onClose();
        }}
      />
      <div
        id={id ?? generatedID}
        ref={menu}
        className="chat-title-menu"
        role="menu"
        tabIndex={-1}
        aria-label={label}
        style={{ ...position, visibility: position ? "visible" : "hidden" }}
        onBlur={(event) => {
          // WebKit can blur a button before its click opens a dialog.
          if (
            event.relatedTarget &&
            !event.currentTarget.contains(event.relatedTarget as Node)
          )
            onClose();
        }}
        onKeyDown={(event) => {
          if (event.key === "Tab") {
            onClose(true);
            return;
          }
          if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key))
            return;
          event.preventDefault();
          const items = Array.from(
            menu.current?.querySelectorAll<HTMLButtonElement>(
              "button:not(:disabled)",
            ) || [],
          );
          if (!items.length) return;
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
        }}
      >
        {children}
      </div>
    </>,
    document.body,
  );
}
