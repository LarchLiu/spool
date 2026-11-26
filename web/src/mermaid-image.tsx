import { useEffect, useId, useRef } from "react";
import { Minus, Plus, Scan } from "lucide-react";
import type { MermaidAction } from "./mermaid-diagram.js";

type Vector = {
  url: string;
  width: number;
  height: number;
  title?: string;
  description?: string;
};

/** Static SVG image with local pan/zoom; no Mermaid runtime in the viewer. */
export function MermaidImage({
  vector,
  expanded,
  t,
  onError,
}: {
  vector: Vector;
  expanded: boolean;
  t: (key: MermaidAction) => string;
  onError: () => void;
}) {
  const viewport = useRef<HTMLDivElement>(null);
  const descriptionID = useId();
  const image = useRef<HTMLImageElement>(null);
  const action = useRef<(name: string) => void>(() => {});
  useEffect(() => {
    const host = viewport.current,
      picture = image.current;
    if (!expanded || !host || !picture) return;
    let scale = 1,
      fitScale = 1,
      x = 0,
      y = 0;
    const pointers = new Map<number, { x: number; y: number }>();
    const paint = () => {
      picture.style.transform = `translate(${x}px, ${y}px) scale(${scale})`;
    };
    const resize = (
      value: number,
      cx = host.clientWidth / 2,
      cy = host.clientHeight / 2,
    ) => {
      value = Math.max(fitScale * 0.2, Math.min(fitScale * 16, value));
      x = cx - ((cx - x) * value) / scale;
      y = cy - ((cy - y) * value) / scale;
      scale = value;
      paint();
    };
    const fit = () => {
      fitScale = Math.min(
        Math.max(1, host.clientWidth - 32) / vector.width,
        Math.max(1, host.clientHeight - 28) / vector.height,
      );
      scale = fitScale;
      x = (host.clientWidth - vector.width * scale) / 2;
      y = (host.clientHeight - vector.height * scale) / 2;
      paint();
    };
    action.current = (name) => {
      if (name === "fit") fit();
      else resize(scale * (name === "in" ? 1.25 : 0.8));
    };
    const point = (event: PointerEvent) => ({
      x: event.clientX,
      y: event.clientY,
    });
    const down = (event: PointerEvent) => {
      if (event.pointerType === "mouse" && event.button !== 0) return;
      pointers.set(event.pointerId, point(event));
      host.setPointerCapture(event.pointerId);
    };
    const move = (event: PointerEvent) => {
      const previous = pointers.get(event.pointerId);
      if (!previous) return;
      const other = [...pointers.entries()].find(
        ([id]) => id !== event.pointerId,
      )?.[1];
      if (other) {
        const before = Math.hypot(previous.x - other.x, previous.y - other.y);
        const after = Math.hypot(
          event.clientX - other.x,
          event.clientY - other.y,
        );
        const bounds = host.getBoundingClientRect();
        if (before > 0)
          resize(
            (scale * after) / before,
            (event.clientX + other.x) / 2 - bounds.left,
            (event.clientY + other.y) / 2 - bounds.top,
          );
      } else {
        x += event.clientX - previous.x;
        y += event.clientY - previous.y;
        paint();
      }
      pointers.set(event.pointerId, point(event));
    };
    const up = (event: PointerEvent) => {
      pointers.delete(event.pointerId);
    };
    const wheel = (event: WheelEvent) => {
      if (!(event.ctrlKey || event.metaKey)) return;
      event.preventDefault();
      const bounds = host.getBoundingClientRect();
      resize(
        scale * Math.exp(-event.deltaY * 0.01),
        event.clientX - bounds.left,
        event.clientY - bounds.top,
      );
    };
    host.addEventListener("pointerdown", down);
    host.addEventListener("pointermove", move);
    for (const name of [
      "pointerup",
      "pointercancel",
      "lostpointercapture",
    ] as const)
      host.addEventListener(name, up);
    host.addEventListener("wheel", wheel, { passive: false });
    host.addEventListener("dblclick", fit);
    const observer = new ResizeObserver(fit);
    observer.observe(host);
    fit();
    return () => {
      observer.disconnect();
      action.current = () => {};
      host.removeEventListener("pointerdown", down);
      host.removeEventListener("pointermove", move);
      for (const name of [
        "pointerup",
        "pointercancel",
        "lostpointercapture",
      ] as const)
        host.removeEventListener(name, up);
      host.removeEventListener("wheel", wheel);
      host.removeEventListener("dblclick", fit);
    };
  }, [vector, expanded]);
  return (
    <>
      <div
        ref={viewport}
        className="markdown-mermaid-viewport"
        data-expanded={expanded}
      >
        <img
          ref={image}
          className="markdown-mermaid-image"
          src={vector.url}
          width={vector.width}
          height={vector.height}
          alt={vector.title || "Mermaid"}
          aria-describedby={vector.description ? descriptionID : undefined}
          draggable={false}
          onError={onError}
        />
        {vector.description && (
          <span id={descriptionID} hidden>
            {vector.description}
          </span>
        )}
      </div>
      {expanded && (
        <div className="mermaid-zoom-toolbar">
          <button
            type="button"
            className="icon"
            aria-label={t("Zoom out")}
            title={t("Zoom out")}
            onClick={() => action.current("out")}
          >
            <Minus size={18} />
          </button>
          <button
            type="button"
            className="icon"
            aria-label={t("Fit diagram")}
            title={t("Fit diagram")}
            onClick={() => action.current("fit")}
          >
            <Scan size={18} />
          </button>
          <button
            type="button"
            className="icon"
            aria-label={t("Zoom in")}
            title={t("Zoom in")}
            onClick={() => action.current("in")}
          >
            <Plus size={18} />
          </button>
        </div>
      )}
    </>
  );
}
