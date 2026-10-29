export type FloatingWindowBounds = {
  x: number;
  y: number;
  width: number;
  height: number;
};

export type FloatingWindowViewport = FloatingWindowBounds;
export const floatingWindowEdges = [
  "n",
  "e",
  "s",
  "w",
  "ne",
  "se",
  "sw",
  "nw",
] as const;
export type FloatingWindowEdge = (typeof floatingWindowEdges)[number];

const inset = 12;
const minimum = { width: 300, height: 360 };
const preferred = { width: 600, height: 520 };
const clamp = (value: number, min: number, max: number) =>
  Math.max(min, Math.min(max, value));

function limits(viewport: FloatingWindowViewport) {
  const gap = Math.min(inset, viewport.width / 4, viewport.height / 4);
  return {
    left: viewport.x + gap,
    top: viewport.y + gap,
    right: viewport.x + viewport.width - gap,
    bottom: viewport.y + viewport.height - gap,
  };
}

export function fitFloatingWindow(
  bounds: FloatingWindowBounds | null | undefined,
  viewport: FloatingWindowViewport,
): FloatingWindowBounds {
  const { left, top, right, bottom } = limits(viewport);
  const width = clamp(
    bounds?.width ?? preferred.width,
    Math.min(minimum.width, right - left),
    right - left,
  );
  const height = clamp(
    bounds?.height ?? preferred.height,
    Math.min(minimum.height, bottom - top),
    bottom - top,
  );
  return {
    x: clamp(bounds?.x ?? right - width, left, right - width),
    y: clamp(bounds?.y ?? bottom - height, top, bottom - height),
    width,
    height,
  };
}

export function moveFloatingWindow(
  bounds: FloatingWindowBounds,
  dx: number,
  dy: number,
  viewport: FloatingWindowViewport,
) {
  return fitFloatingWindow(
    { ...bounds, x: bounds.x + dx, y: bounds.y + dy },
    viewport,
  );
}

export function resizeFloatingWindow(
  bounds: FloatingWindowBounds,
  dx: number,
  dy: number,
  viewport: FloatingWindowViewport,
  edge: FloatingWindowEdge = "se",
): FloatingWindowBounds {
  const fitted = fitFloatingWindow(bounds, viewport);
  const { left, top, right, bottom } = limits(viewport);
  const minWidth = Math.min(minimum.width, right - left);
  const minHeight = Math.min(minimum.height, bottom - top);
  const result = { ...fitted };
  if (edge.includes("e"))
    result.width = clamp(fitted.width + dx, minWidth, right - fitted.x);
  if (edge.includes("s"))
    result.height = clamp(fitted.height + dy, minHeight, bottom - fitted.y);
  if (edge.includes("w")) {
    const anchor = fitted.x + fitted.width;
    result.x = clamp(fitted.x + dx, left, anchor - minWidth);
    result.width = anchor - result.x;
  }
  if (edge.includes("n")) {
    const anchor = fitted.y + fitted.height;
    result.y = clamp(fitted.y + dy, top, anchor - minHeight);
    result.height = anchor - result.y;
  }
  return result;
}
