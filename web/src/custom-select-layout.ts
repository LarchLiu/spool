type Rectangle = {
  left: number;
  top: number;
  right: number;
  bottom: number;
  width: number;
};
type Viewport = { x: number; y: number; width: number; height: number };

export function selectPopupLayout(
  anchor: Rectangle,
  viewport: Viewport,
  count: number,
) {
  const margin = 8,
    gap = 4;
  const width = Math.min(
    Math.max(180, anchor.width),
    Math.max(0, viewport.width - margin * 2),
  );
  const below = Math.max(
    0,
    viewport.y + viewport.height - margin - anchor.bottom - gap,
  );
  const above = Math.max(0, anchor.top - gap - viewport.y - margin);
  const desired = Math.min(280, count * 36 + 10);
  const side =
    below >= Math.min(desired, 180) || below >= above ? "bottom" : "top";
  return {
    left: Math.max(
      viewport.x + margin,
      Math.min(anchor.left, viewport.x + viewport.width - margin - width),
    ),
    top: side === "bottom" ? anchor.bottom + gap : anchor.top - gap,
    width,
    maxHeight: Math.min(desired, side === "bottom" ? below : above),
    side,
  } as const;
}
