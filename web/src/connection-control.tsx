import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";

/** Footer connection entry with the same title tooltip as Settings. */
export function ConnectionControl({
  label,
  icon,
  status,
  statusLabel,
  active = true,
  children,
}: {
  label: string;
  icon: ReactNode;
  status?: "connected" | "pending";
  statusLabel?: string;
  active?: boolean;
  children: (close: () => void) => ReactNode;
}) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<{ left: number; bottom: number }>();
  const close = () => setOpen(false);
  useEffect(() => {
    if (!active) setOpen(false);
  }, [active]);
  useLayoutEffect(() => {
    if (!open || !trigger.current || !panel.current) return;
    const anchor = trigger.current.getBoundingClientRect();
    const bounds = panel.current.getBoundingClientRect();
    setPosition({
      left: Math.max(
        8,
        Math.min(anchor.left, window.innerWidth - bounds.width - 8),
      ),
      bottom: Math.max(8, window.innerHeight - anchor.top + 8),
    });
  }, [open]);
  useLayoutEffect(() => {
    if (!open || !position) return;
    const target = panel.current?.querySelector<HTMLButtonElement>(
      "button:not(:disabled)",
    );
    (target ?? panel.current)?.focus({ preventScroll: true });
  }, [open, position]);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      if (
        !trigger.current?.contains(event.target as Node) &&
        !panel.current?.contains(event.target as Node)
      )
        close();
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.isComposing) return;
      event.preventDefault();
      event.stopPropagation();
      close();
      trigger.current?.focus({ preventScroll: true });
    };
    const scroll = (event: Event) => {
      if (!panel.current?.contains(event.target as Node)) close();
    };
    document.addEventListener("pointerdown", outside, true);
    document.addEventListener("keydown", escape, true);
    document.addEventListener("scroll", scroll, true);
    window.addEventListener("resize", close);
    return () => {
      document.removeEventListener("pointerdown", outside, true);
      document.removeEventListener("keydown", escape, true);
      document.removeEventListener("scroll", scroll, true);
      window.removeEventListener("resize", close);
    };
  }, [open]);
  return (
    <>
      <button
        ref={trigger}
        type="button"
        className="icon connection-control"
        title={label}
        aria-label={label}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        onClick={() => {
          if (open) close();
          else {
            setPosition(undefined);
            setOpen(true);
          }
        }}
      >
        {icon}
        {status && (
          <span
            className="connection-status-dot"
            data-state={status}
            role="img"
            aria-label={statusLabel}
          />
        )}
      </button>
      {open &&
        createPortal(
          <div
            ref={panel}
            id={id}
            role="dialog"
            aria-label={label}
            tabIndex={-1}
            className="connection-popover"
            style={{ ...position, visibility: position ? "visible" : "hidden" }}
            onBlur={(event) => {
              if (
                event.relatedTarget &&
                !event.currentTarget.contains(event.relatedTarget as Node) &&
                !trigger.current?.contains(event.relatedTarget as Node)
              )
                close();
            }}
          >
            {children(close)}
          </div>,
          document.body,
        )}
    </>
  );
}
