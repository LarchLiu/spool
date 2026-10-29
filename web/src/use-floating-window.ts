import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent, PointerEvent } from "react";
import {
  fitFloatingWindow,
  moveFloatingWindow,
  resizeFloatingWindow,
  type FloatingWindowBounds,
  type FloatingWindowViewport,
  type FloatingWindowEdge,
} from "./floating-window.js";

type Operation = "move" | FloatingWindowEdge;

function viewport(): FloatingWindowViewport {
  if (typeof window === "undefined")
    return { x: 0, y: 0, width: 1024, height: 768 };
  const visible = window.visualViewport;
  return {
    x: visible?.offsetLeft ?? 0,
    y: visible?.offsetTop ?? 0,
    width: visible?.width ?? window.innerWidth,
    height: visible?.height ?? window.innerHeight,
  };
}

function transform(
  bounds: FloatingWindowBounds,
  dx: number,
  dy: number,
  operation: Operation,
) {
  return operation === "move"
    ? moveFloatingWindow(bounds, dx, dy, viewport())
    : resizeFloatingWindow(bounds, dx, dy, viewport(), operation);
}

export function useFloatingWindow(
  initialBounds?: FloatingWindowBounds | null,
  onBoundsChange?: (bounds: FloatingWindowBounds) => void,
) {
  const [bounds, setBounds] = useState(() =>
    fitFloatingWindow(initialBounds, viewport()),
  );
  const [operation, setOperation] = useState<Operation | null>(null);
  const gesture = useRef<{
    pointer: number;
    x: number;
    y: number;
    bounds: FloatingWindowBounds;
    operation: Operation;
  } | null>(null);

  useEffect(() => {
    onBoundsChange?.(bounds);
  }, [bounds, onBoundsChange]);

  useEffect(() => {
    const fit = () =>
      setBounds((current) => fitFloatingWindow(current, viewport()));
    window.addEventListener("resize", fit);
    window.visualViewport?.addEventListener("resize", fit);
    window.visualViewport?.addEventListener("scroll", fit);
    return () => {
      window.removeEventListener("resize", fit);
      window.visualViewport?.removeEventListener("resize", fit);
      window.visualViewport?.removeEventListener("scroll", fit);
    };
  }, []);

  const finish = (event: PointerEvent<HTMLElement>) => {
    if (gesture.current?.pointer !== event.pointerId) return;
    gesture.current = null;
    setOperation(null);
    if (event.currentTarget.hasPointerCapture(event.pointerId))
      event.currentTarget.releasePointerCapture(event.pointerId);
  };

  const handle = (kind: Operation) => ({
    onPointerDown(event: PointerEvent<HTMLElement>) {
      if (event.button !== 0 || !event.isPrimary || gesture.current) return;
      event.preventDefault();
      event.currentTarget.setPointerCapture(event.pointerId);
      gesture.current = {
        pointer: event.pointerId,
        x: event.clientX,
        y: event.clientY,
        bounds,
        operation: kind,
      };
      setOperation(kind);
    },
    onPointerMove(event: PointerEvent<HTMLElement>) {
      const start = gesture.current;
      if (!start || start.pointer !== event.pointerId) return;
      setBounds(
        transform(
          start.bounds,
          event.clientX - start.x,
          event.clientY - start.y,
          start.operation,
        ),
      );
    },
    onPointerUp: finish,
    onPointerCancel: finish,
    onLostPointerCapture: finish,
    onKeyDown(event: KeyboardEvent<HTMLElement>) {
      const step = event.shiftKey ? 40 : 10;
      const delta: Record<string, [number, number]> = {
        ArrowLeft: [-step, 0],
        ArrowRight: [step, 0],
        ArrowUp: [0, -step],
        ArrowDown: [0, step],
      };
      const movement = delta[event.key];
      if (!movement) return;
      event.preventDefault();
      event.stopPropagation();
      setBounds((current) => transform(current, ...movement, kind));
    },
  });

  return {
    bounds,
    operation,
    moveHandle: handle("move"),
    resizeHandle: (edge: FloatingWindowEdge) => handle(edge),
  };
}
