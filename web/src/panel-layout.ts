export type PanelSide = "left" | "right";
export type PanelLayout = {
  leftOpen: boolean;
  rightOpen: boolean;
  leftWidth: number;
  rightWidth: number;
};
export const defaultPanelLayout: PanelLayout = {
  leftOpen: true,
  rightOpen: true,
  leftWidth: 298,
  rightWidth: 360,
};
export const panelLimits = {
  left: { min: 220, max: 480 },
  right: { min: 280, max: 720 },
};
export const panelStorageKey = "spool.panel-layout.v1";
export const previewSplitStorageKey = "spool.file-preview-split.v1";
export const defaultFileTreeFraction = 0.32;
export const previewSplitHandleHeight = 8;

export function previewSplitLimits(height: number) {
  const available = height - previewSplitHandleHeight;
  if (!Number.isFinite(available) || available <= 0)
    return { min: 0.15, max: 0.8 };
  return {
    min: Math.max(0.15, Math.min(120, available / 2) / available),
    max: Math.min(0.8, 1 - Math.min(160, available / 2) / available),
  };
}

export function previewTreeFraction(value: number, height = 0) {
  const { min, max } = previewSplitLimits(height);
  return Math.max(
    min,
    Math.min(max, Number.isFinite(value) ? value : defaultFileTreeFraction),
  );
}

export function parsePreviewSplit(raw: string | null) {
  try {
    const value: unknown = JSON.parse(raw || "null");
    return previewTreeFraction(
      typeof value === "number" ? value : defaultFileTreeFraction,
    );
  } catch {
    return defaultFileTreeFraction;
  }
}

export function panelWidth(side: PanelSide, value: number) {
  const { min, max } = panelLimits[side];
  return Math.max(
    min,
    Math.min(
      max,
      Number.isFinite(value) ? value : defaultPanelLayout[`${side}Width`],
    ),
  );
}

export function parsePanelLayout(raw: string | null): PanelLayout {
  try {
    const value = JSON.parse(raw || "null");
    if (!value || typeof value !== "object") return { ...defaultPanelLayout };
    return {
      leftOpen: typeof value.leftOpen === "boolean" ? value.leftOpen : true,
      rightOpen: typeof value.rightOpen === "boolean" ? value.rightOpen : true,
      leftWidth: panelWidth("left", value.leftWidth),
      rightWidth: panelWidth("right", value.rightWidth),
    };
  } catch {
    return { ...defaultPanelLayout };
  }
}
