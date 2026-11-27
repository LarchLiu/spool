export type CaptureRect = {
  x: number;
  y: number;
  width: number;
  height: number;
};
export type CapturePoint = { x: number; y: number };
export type CaptureWindow = { id: number; name: string; bounds: CaptureRect };
export type ResizeHandle = "n" | "ne" | "e" | "se" | "s" | "sw" | "w" | "nw";
export const resizeHandles: ResizeHandle[] = [
  "nw",
  "n",
  "ne",
  "e",
  "se",
  "s",
  "sw",
  "w",
];

export function containsPoint(rect: CaptureRect, point: CapturePoint): boolean {
  return (
    point.x >= rect.x &&
    point.y >= rect.y &&
    point.x < rect.x + rect.width &&
    point.y < rect.y + rect.height
  );
}
export function windowAt(
  windows: CaptureWindow[],
  point: CapturePoint,
): CaptureWindow | undefined {
  return windows.find((window) => containsPoint(window.bounds, point));
}
const clamp = (value: number, min: number, max: number) =>
  Math.max(min, Math.min(max, Math.round(value)));
export function selectionRect(
  start: CapturePoint,
  end: CapturePoint,
  width: number,
  height: number,
): CaptureRect {
  const a = { x: clamp(start.x, 0, width), y: clamp(start.y, 0, height) };
  const b = { x: clamp(end.x, 0, width), y: clamp(end.y, 0, height) };
  return {
    x: Math.min(a.x, b.x),
    y: Math.min(a.y, b.y),
    width: Math.abs(a.x - b.x),
    height: Math.abs(a.y - b.y),
  };
}
export function adjustSelection(
  rect: CaptureRect,
  handle: ResizeHandle | "move",
  delta: CapturePoint,
  width: number,
  height: number,
): CaptureRect {
  if (handle === "move")
    return {
      ...rect,
      x: clamp(rect.x + delta.x, 0, width - rect.width),
      y: clamp(rect.y + delta.y, 0, height - rect.height),
    };
  const left = handle.includes("w")
    ? clamp(rect.x + delta.x, 0, rect.x + rect.width - 1)
    : rect.x;
  const top = handle.includes("n")
    ? clamp(rect.y + delta.y, 0, rect.y + rect.height - 1)
    : rect.y;
  const right = handle.includes("e")
    ? clamp(rect.x + rect.width + delta.x, left + 1, width)
    : rect.x + rect.width;
  const bottom = handle.includes("s")
    ? clamp(rect.y + rect.height + delta.y, top + 1, height)
    : rect.y + rect.height;
  return { x: left, y: top, width: right - left, height: bottom - top };
}
export function localWindowRect(
  window: CaptureWindow,
  bounds: CaptureRect,
): CaptureRect {
  const x = Math.max(0, window.bounds.x - bounds.x),
    y = Math.max(0, window.bounds.y - bounds.y);
  const right = Math.min(
    bounds.width,
    window.bounds.x + window.bounds.width - bounds.x,
  );
  const bottom = Math.min(
    bounds.height,
    window.bounds.y + window.bounds.height - bounds.y,
  );
  return {
    x,
    y,
    width: Math.max(0, right - x),
    height: Math.max(0, bottom - y),
  };
}
