import { useRef } from "react";
import type { Translator } from "./i18n";
import {
  defaultPanelLayout,
  panelLimits,
  panelWidth,
  type PanelSide,
} from "./panel-layout";

export function PanelResizeHandle({
  side,
  width,
  onResize,
  t,
}: {
  side: PanelSide;
  width: number;
  onResize: (width: number) => void;
  t: Translator;
}) {
  const start = useRef({ x: 0, width, moved: false });
  const label = t(
    side === "left" ? "Resize left sidebar" : "Resize right sidebar",
  );
  return (
    <div
      className={`panel-resize-handle ${side}`}
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuemin={panelLimits[side].min}
      aria-valuemax={panelLimits[side].max}
      aria-valuenow={Math.round(width)}
      tabIndex={0}
      title={label}
      onPointerDown={(event) => {
        if (event.button !== 0) return;
        event.preventDefault();
        start.current = {
          x: event.clientX,
          width:
            event.currentTarget.parentElement?.getBoundingClientRect().width ||
            width,
          moved: false,
        };
        event.currentTarget.setPointerCapture(event.pointerId);
        event.currentTarget.classList.add("resizing");
      }}
      onPointerMove={(event) => {
        if (
          event.currentTarget.hasPointerCapture(event.pointerId) &&
          (event.clientX !== start.current.x || start.current.moved)
        ) {
          start.current.moved = true;
          onResize(
            panelWidth(
              side,
              start.current.width +
                (event.clientX - start.current.x) * (side === "left" ? 1 : -1),
            ),
          );
        }
      }}
      onPointerUp={(event) => {
        if (event.currentTarget.hasPointerCapture(event.pointerId))
          event.currentTarget.releasePointerCapture(event.pointerId);
        event.currentTarget.classList.remove("resizing");
      }}
      onPointerCancel={(event) =>
        event.currentTarget.classList.remove("resizing")
      }
      onLostPointerCapture={(event) =>
        event.currentTarget.classList.remove("resizing")
      }
      onDoubleClick={() => onResize(defaultPanelLayout[`${side}Width`])}
      onKeyDown={(event) => {
        if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
          event.preventDefault();
          onResize(
            panelWidth(
              side,
              width +
                (event.key === "ArrowRight" ? 10 : -10) *
                  (side === "left" ? 1 : -1),
            ),
          );
        } else if (event.key === "Home" || event.key === "End") {
          event.preventDefault();
          onResize(panelLimits[side][event.key === "Home" ? "min" : "max"]);
        }
      }}
    />
  );
}
