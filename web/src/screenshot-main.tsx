import React, { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { Check, Minus, Move, RotateCcw, Square, Undo2, X } from "lucide-react";
import { translator } from "./i18n";
import { resolveLanguage } from "./preferences";
import {
  adjustSelection,
  containsPoint,
  localWindowRect,
  resizeHandles,
  selectionRect,
  windowAt,
  type CapturePoint,
  type CaptureRect,
  type CaptureWindow,
  type ResizeHandle,
} from "./screenshot-selection";
import {
  hasAnnotationSize,
  pointInSelection,
  renderScreenshot,
  screenshotPNG,
  type ScreenshotAnnotation,
  type ScreenshotTool,
} from "./screenshot-editor";
import "./screenshot.css";

type CaptureSurface = {
  token: string;
  image: string;
  bounds: CaptureRect;
  windows: CaptureWindow[];
  cursor: CapturePoint;
  locale: string;
  theme: "light" | "dark";
  selected?: boolean;
  selection?: CaptureRect;
};
declare global {
  interface Window {
    spoolScreenshot?: CaptureSurface;
  }
}
type Gesture = {
  start: CapturePoint;
  rect?: CaptureRect;
  handle?: ResizeHandle | "move";
  annotation?: ScreenshotAnnotation;
  moved: boolean;
};
const screenshotBridge = () =>
  (
    window as Window & {
      mygo?: { call: <T>(method: string, ...args: unknown[]) => Promise<T> };
    }
  ).mygo;

function MosaicIcon({ size = 18 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M3 3h4v4H3zM13 8h4v4h-4zM8 13h4v4H8zM18 18h4v4h-4z" />
      <path
        opacity=".6"
        d="M8 3h4v4H8zM18 3h4v4h-4zM3 8h4v4H3zM18 13h4v4h-4zM3 18h4v4H3zM13 18h4v4h-4z"
      />
      <path
        opacity=".3"
        d="M13 3h4v4h-4zM8 8h4v4H8zM18 8h4v4h-4zM3 13h4v4H3zM13 13h4v4h-4zM8 18h4v4H8z"
      />
    </svg>
  );
}

function CaptureToolButton({
  title,
  children,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { title: string }) {
  return (
    <span className="capture-tool">
      <button type="button" title={title} {...props}>
        {children}
      </button>
      <span className="capture-tooltip" role="tooltip">
        {title}
      </span>
    </span>
  );
}

function ScreenshotSelection({ surface }: { surface: CaptureSurface }) {
  const t = translator(resolveLanguage("system", [surface.locale]));
  const [point, setPoint] = useState(surface.cursor);
  const [selection, setSelection] = useState<CaptureRect | undefined>(
    surface.selection ??
      (surface.selected
        ? {
            x: 0,
            y: 0,
            width: surface.bounds.width,
            height: surface.bounds.height,
          }
        : undefined),
  );
  const [ready, setReady] = useState(false);
  const [working, setWorking] = useState(false);
  const [tool, setTool] = useState<ScreenshotTool>();
  const [annotations, setAnnotations] = useState<ScreenshotAnnotation[]>([]);
  const [draft, setDraft] = useState<ScreenshotAnnotation>();
  const [error, setError] = useState("");
  const drag = useRef<Gesture>(undefined);
  const busy = useRef(false);
  const root = useRef<HTMLDivElement>(null);
  const image = useRef<HTMLImageElement>(null);
  const preview = useRef<HTMLCanvasElement>(null);
  const bounds = surface.bounds;
  const hover =
    ready &&
    containsPoint(
      { x: 0, y: 0, width: bounds.width, height: bounds.height },
      point,
    )
      ? windowAt(surface.windows, {
          x: point.x + bounds.x,
          y: point.y + bounds.y,
        })
      : undefined;
  const active = selection || (hover && localWindowRect(hover, bounds));
  const toolbarTop = selection
    ? selection.y + selection.height + 60 < bounds.height
      ? selection.y + selection.height + 12
      : Math.max(12, selection.y - 60)
    : 0;
  const navigate = (action: string) => {
    location.assign(
      "mygo://app/__screenshot?" +
        new URLSearchParams({ token: surface.token, action }),
    );
  };
  const color = () =>
    getComputedStyle(root.current!).getPropertyValue("--color-primary").trim();
  const confirm = async () => {
    if (
      !selection ||
      !selection.width ||
      !selection.height ||
      busy.current ||
      drag.current
    )
      return;
    busy.current = true;
    setWorking(true);
    setError("");
    try {
      const canvas = document.createElement("canvas");
      renderScreenshot(
        canvas,
        image.current!,
        bounds,
        selection,
        annotations,
        color(),
      );
      const data = await screenshotPNG(canvas);
      const bridge = screenshotBridge();
      if (!bridge) throw new Error("Screenshot editor is unavailable");
      // Stage through IPC, then close by navigation after the IPC reply reaches
      // this page. Closing during the call would cancel its own acknowledgement.
      await bridge.call("DesktopScreenshot.Stage", surface.token, data);
      navigate("complete");
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : "";
      setError(
        t(
          message.includes("4 MB")
            ? "Each attachment must be nonempty and at most 4 MB"
            : "Could not save screenshot. Try again.",
        ),
      );
      busy.current = false;
      setWorking(false);
    }
  };
  const reset = () => {
    setTool(undefined);
    setSelection(undefined);
    setAnnotations([]);
    setDraft(undefined);
    setError("");
  };
  useEffect(() => {
    if (!ready) return;
    // Keep initial keyboard focus on the surface instead of letting WKWebView
    // choose a toolbar button. Tab can still reach the tools after selection.
    root.current?.focus({ preventScroll: true });
    // Hidden WKWebViews suspend animation frames. Flush the committed image and
    // controls' layout before native reveals the panel, without awaiting rAF.
    root.current?.getBoundingClientRect();
    navigate("ready");
  }, [ready]);
  useEffect(() => {
    if (!selection || !preview.current || !image.current) return;
    try {
      renderScreenshot(
        preview.current,
        image.current,
        bounds,
        selection,
        draft ? [...annotations, draft] : annotations,
        color(),
      );
    } catch {
      setError(t("Could not save screenshot. Try again."));
    }
  }, [selection, annotations, draft]);
  useEffect(() => {
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        navigate("cancel");
      } else if (
        (event.metaKey || event.ctrlKey) &&
        event.key.toLowerCase() === "z"
      ) {
        event.preventDefault();
        if (!busy.current && !drag.current)
          setAnnotations((items) => items.slice(0, -1));
      } else if (
        event.key === "Enter" &&
        !(event.target instanceof HTMLButtonElement)
      ) {
        event.preventDefault();
        void confirm();
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  });
  const at = (event: React.PointerEvent): CapturePoint => {
    const rect = event.currentTarget.getBoundingClientRect();
    return {
      x: Math.round(event.clientX - rect.x),
      y: Math.round(event.clientY - rect.y),
    };
  };
  return (
    <div
      ref={root}
      className={`capture-surface tool-${tool || "none"}`}
      tabIndex={-1}
      style={{ width: bounds.width, height: bounds.height }}
      aria-label={t("Select screenshot")}
      onContextMenu={(event) => event.preventDefault()}
      onPointerDown={(event) => {
        if (
          !ready ||
          busy.current ||
          event.button !== 0 ||
          (event.target as Element).closest(".capture-toolbar")
        )
          return;
        root.current?.focus({ preventScroll: true });
        const start = at(event);
        const handle = (event.target as HTMLElement).dataset.handle as
          ResizeHandle | undefined;
        const inside = selection && containsPoint(selection, start);
        const annotation: ScreenshotAnnotation | undefined =
          inside && !handle && tool && tool !== "move"
            ? { tool, start, end: start }
            : undefined;
        drag.current = {
          start,
          moved: false,
          rect: selection,
          annotation,
          handle:
            handle ||
            (inside && (!tool || tool === "move") ? "move" : undefined),
        };
        setError("");
        if (!handle && !inside) reset();
        event.currentTarget.setPointerCapture(event.pointerId);
        event.preventDefault();
      }}
      onPointerMove={(event) => {
        const next = at(event);
        setPoint(next);
        const gesture = drag.current;
        if (!gesture) return;
        const delta = {
          x: next.x - gesture.start.x,
          y: next.y - gesture.start.y,
        };
        if (Math.abs(delta.x) + Math.abs(delta.y) >= 4) gesture.moved = true;
        if (!gesture.moved) return;
        if (gesture.annotation && gesture.rect) {
          gesture.annotation = {
            ...gesture.annotation,
            end: pointInSelection(next, gesture.rect),
          };
          setDraft(gesture.annotation);
        } else {
          setSelection(
            gesture.handle && gesture.rect
              ? adjustSelection(
                  gesture.rect,
                  gesture.handle,
                  delta,
                  bounds.width,
                  bounds.height,
                )
              : selectionRect(gesture.start, next, bounds.width, bounds.height),
          );
        }
      }}
      onPointerUp={(event) => {
        const gesture = drag.current;
        drag.current = undefined;
        if (event.currentTarget.hasPointerCapture(event.pointerId))
          event.currentTarget.releasePointerCapture(event.pointerId);
        setDraft(undefined);
        if (!gesture) return;
        if (gesture.annotation) {
          if (gesture.moved && hasAnnotationSize(gesture.annotation))
            setAnnotations((items) => [...items, gesture.annotation!]);
        } else if (!gesture.moved && !gesture.handle) {
          const target = windowAt(surface.windows, {
            x: gesture.start.x + bounds.x,
            y: gesture.start.y + bounds.y,
          });
          if (target) setSelection(localWindowRect(target, bounds));
        }
      }}
      onPointerCancel={() => {
        drag.current = undefined;
        setDraft(undefined);
      }}
    >
      <img
        ref={image}
        className="capture-background"
        src={surface.image}
        alt=""
        draggable={false}
        onLoad={() => setReady(true)}
        onError={() => navigate("cancel")}
      />
      <div className="capture-shade" />
      {active && active.width > 0 && active.height > 0 && (
        <div
          className={
            selection ? "capture-selection editable" : "capture-selection"
          }
          style={{
            left: active.x,
            top: active.y,
            width: active.width,
            height: active.height,
            backgroundImage: `url(${surface.image})`,
            backgroundSize: `${bounds.width}px ${bounds.height}px`,
            backgroundPosition: `-${active.x}px -${active.y}px`,
          }}
        >
          {selection && <canvas ref={preview} className="capture-preview" />}
          <span className="capture-size">
            {!selection && hover?.name && `${hover.name} · `}
            {`${Math.round(active.width)} × ${Math.round(active.height)}`}
          </span>
          {selection &&
            (["n", "e", "s", "w"] as const).map((edge) => (
              <span
                key={edge}
                data-handle={edge}
                className={`capture-edge edge-${edge}`}
              />
            ))}
          {selection &&
            resizeHandles.map((handle) => (
              <span
                key={handle}
                data-handle={handle}
                className={`capture-handle handle-${handle}`}
                aria-label={t("Resize selection")}
              />
            ))}
        </div>
      )}
      {ready && selection && (
        <div
          className="capture-toolbar"
          data-tooltip-side={toolbarTop < 48 ? "below" : "above"}
          onMouseDown={(event) => {
            // Pointer clicks should act without leaving a keyboard focus ring.
            // Keyboard navigation still focuses each button normally.
            event.preventDefault();
            root.current?.focus({ preventScroll: true });
          }}
          style={{
            left: Math.max(
              12,
              Math.min(
                bounds.width - Math.min(302, bounds.width - 24) - 12,
                selection.x,
              ),
            ),
            top: toolbarTop,
          }}
        >
          <div className="capture-toolbar-actions">
            {selection && (
              <>
                {(
                  [
                    ["move", "Move selection", Move],
                    ["rect", "Rectangle", Square],
                    ["line", "Line", Minus],
                    ["mosaic", "Mosaic", MosaicIcon],
                  ] as const
                ).map(([value, label, Icon]) => (
                  <CaptureToolButton
                    key={value}
                    data-tool={value}
                    type="button"
                    aria-label={t(label)}
                    title={t(label)}
                    aria-pressed={tool === value}
                    disabled={working}
                    onClick={() =>
                      setTool((current) =>
                        current === value ? undefined : value,
                      )
                    }
                  >
                    <Icon size={18} />
                  </CaptureToolButton>
                ))}
                <CaptureToolButton
                  type="button"
                  data-action="undo"
                  aria-label={t("Undo")}
                  title={t("Undo") + " (⌘/Ctrl+Z)"}
                  disabled={working || !annotations.length}
                  onClick={() => setAnnotations((items) => items.slice(0, -1))}
                >
                  <Undo2 size={18} />
                </CaptureToolButton>
                <CaptureToolButton
                  type="button"
                  aria-label={t("Select again")}
                  title={t("Select again")}
                  disabled={working}
                  onClick={reset}
                >
                  <RotateCcw size={18} />
                </CaptureToolButton>
              </>
            )}
            <CaptureToolButton
              type="button"
              aria-label={t("Cancel")}
              title={t("Cancel")}
              onClick={() => navigate("cancel")}
            >
              <X size={18} />
            </CaptureToolButton>
            {selection && (
              <CaptureToolButton
                type="button"
                className="capture-confirm"
                aria-label={t("Capture")}
                title={t("Capture")}
                disabled={working || !selection.width || !selection.height}
                onClick={() => void confirm()}
              >
                <Check size={18} />
              </CaptureToolButton>
            )}
          </div>
          {error && (
            <span className="capture-error" role="alert">
              {error}
            </span>
          )}
        </div>
      )}
    </div>
  );
}

async function startScreenshot() {
  const surface = window.spoolScreenshot;
  if (!surface) return;
  try {
    if (!surface.image) {
      const bridge = screenshotBridge();
      if (!bridge) throw new Error("Screenshot editor is unavailable");
      const content = await bridge.call<
        Pick<CaptureSurface, "image" | "windows">
      >("DesktopScreenshot.Load", surface.token);
      Object.assign(surface, content);
    }
    document.documentElement.dataset.theme = surface.theme;
    createRoot(document.getElementById("root")!).render(
      <ScreenshotSelection surface={surface} />,
    );
  } catch {
    location.assign(
      "mygo://app/__screenshot?" +
        new URLSearchParams({ token: surface.token, action: "cancel" }),
    );
  }
}
void startScreenshot();
