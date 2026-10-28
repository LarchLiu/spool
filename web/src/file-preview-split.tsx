import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import type { Translator } from "./i18n";
import {
  defaultFileTreeFraction,
  parsePreviewSplit,
  previewSplitHandleHeight,
  previewSplitLimits,
  previewSplitStorageKey,
  previewTreeFraction,
} from "./panel-layout.js";

export function FilePreviewSplit({
  tree,
  preview,
  hint,
  t,
}: {
  tree: ReactNode;
  preview: ReactNode;
  hint: ReactNode;
  t: Translator;
}) {
  const container = useRef<HTMLDivElement>(null);
  const previewID = useId();
  const [height, setHeight] = useState(0);
  const [preferredFraction, setPreferredFraction] = useState(() => {
    try {
      return parsePreviewSplit(localStorage.getItem(previewSplitStorageKey));
    } catch {
      return defaultFileTreeFraction;
    }
  });
  const fraction = previewTreeFraction(preferredFraction, height);
  const { min, max } = previewSplitLimits(height);
  const start = useRef({ y: 0, fraction, available: 1, height });
  const label = t("Resize file preview");

  useEffect(() => {
    const element = container.current;
    if (!element) return;
    const measure = () => setHeight(element.getBoundingClientRect().height);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    try {
      localStorage.setItem(
        previewSplitStorageKey,
        JSON.stringify(preferredFraction),
      );
    } catch {
      /* Storage can be disabled. */
    }
  }, [preferredFraction]);

  return (
    <div
      ref={container}
      className={`file-sidebar-content ${preview ? "has-preview" : ""}`}
      style={
        preview
          ? {
              gridTemplateRows: `minmax(0, ${fraction}fr) ${previewSplitHandleHeight}px minmax(0, ${1 - fraction}fr)`,
            }
          : undefined
      }
    >
      <div className="file-tree-pane">{tree}</div>
      {preview ? (
        <>
          <div
            className="file-preview-resize-handle"
            role="separator"
            aria-orientation="horizontal"
            aria-label={label}
            title={label}
            aria-controls={previewID}
            aria-valuemin={Math.round(min * 100)}
            aria-valuemax={Math.round(max * 100)}
            aria-valuenow={Math.round(fraction * 100)}
            tabIndex={0}
            onPointerDown={(event) => {
              if (event.button !== 0) return;
              event.preventDefault();
              const actualHeight =
                container.current?.getBoundingClientRect().height || height;
              start.current = {
                y: event.clientY,
                fraction: previewTreeFraction(preferredFraction, actualHeight),
                available: Math.max(1, actualHeight - previewSplitHandleHeight),
                height: actualHeight,
              };
              event.currentTarget.setPointerCapture(event.pointerId);
              event.currentTarget.classList.add("resizing");
            }}
            onPointerMove={(event) => {
              if (event.currentTarget.hasPointerCapture(event.pointerId))
                setPreferredFraction(
                  previewTreeFraction(
                    start.current.fraction +
                      (event.clientY - start.current.y) /
                        start.current.available,
                    start.current.height,
                  ),
                );
            }}
            onPointerUp={(event) => {
              if (event.currentTarget.hasPointerCapture(event.pointerId))
                event.currentTarget.releasePointerCapture(event.pointerId);
              event.currentTarget.classList.remove("resizing");
            }}
            onLostPointerCapture={(event) =>
              event.currentTarget.classList.remove("resizing")
            }
            onPointerCancel={(event) =>
              event.currentTarget.classList.remove("resizing")
            }
            onDoubleClick={() => setPreferredFraction(defaultFileTreeFraction)}
            onKeyDown={(event) => {
              if (event.key === "ArrowUp" || event.key === "ArrowDown") {
                event.preventDefault();
                const delta =
                  20 / Math.max(1, height - previewSplitHandleHeight);
                setPreferredFraction(
                  previewTreeFraction(
                    fraction + (event.key === "ArrowDown" ? delta : -delta),
                    height,
                  ),
                );
              } else if (event.key === "Home" || event.key === "End") {
                event.preventDefault();
                setPreferredFraction(event.key === "Home" ? min : max);
              }
            }}
          />
          <div className="file-preview-pane" id={previewID}>
            {preview}
          </div>
        </>
      ) : (
        hint
      )}
    </div>
  );
}
