import type { CapturePoint, CaptureRect } from "./screenshot-selection";

export type AnnotationTool = "rect" | "line" | "mosaic";
export type ScreenshotTool = AnnotationTool | "move";
export type ScreenshotAnnotation = {
  tool: AnnotationTool;
  start: CapturePoint;
  end: CapturePoint;
};

export function pointInSelection(
  point: CapturePoint,
  rect: CaptureRect,
): CapturePoint {
  return {
    x: Math.max(rect.x, Math.min(rect.x + rect.width, point.x)),
    y: Math.max(rect.y, Math.min(rect.y + rect.height, point.y)),
  };
}

export function hasAnnotationSize(annotation: ScreenshotAnnotation): boolean {
  const dx = Math.abs(annotation.end.x - annotation.start.x);
  const dy = Math.abs(annotation.end.y - annotation.start.y);
  return annotation.tool === "line" ? dx + dy >= 3 : dx >= 3 && dy >= 3;
}

// Preview and export share the same renderer at the source display's resolution.
// Coordinates stay in display points; the canvas retains Retina source pixels.
export function renderScreenshot(
  canvas: HTMLCanvasElement,
  image: HTMLImageElement,
  bounds: CaptureRect,
  crop: CaptureRect,
  annotations: readonly ScreenshotAnnotation[],
  color: string,
): void {
  const sx = image.naturalWidth / bounds.width;
  const sy = image.naturalHeight / bounds.height;
  const left = Math.round(crop.x * sx);
  const top = Math.round(crop.y * sy);
  const width = Math.max(1, Math.round((crop.x + crop.width) * sx) - left);
  const height = Math.max(1, Math.round((crop.y + crop.height) * sy) - top);
  if (width * height > 100_000_000) throw new Error("Screenshot is too large");
  canvas.width = width;
  canvas.height = height;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("Could not render screenshot");
  ctx.drawImage(image, left, top, width, height, 0, 0, width, height);
  ctx.strokeStyle = color;
  ctx.lineWidth = 3 * Math.max(sx, sy);
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  for (const annotation of annotations) {
    const start = {
      x: annotation.start.x * sx - left,
      y: annotation.start.y * sy - top,
    };
    const end = {
      x: annotation.end.x * sx - left,
      y: annotation.end.y * sy - top,
    };
    const x = Math.min(start.x, end.x),
      y = Math.min(start.y, end.y);
    const w = Math.abs(end.x - start.x),
      h = Math.abs(end.y - start.y);
    if (annotation.tool === "rect") {
      ctx.strokeRect(x, y, w, h);
    } else if (annotation.tool === "line") {
      ctx.beginPath();
      ctx.moveTo(start.x, start.y);
      ctx.lineTo(end.x, end.y);
      ctx.stroke();
    } else {
      // Clip before sampling so resizing/moving a selection cannot smear pixels
      // from outside the exported crop into the mosaic.
      const x1 = Math.max(0, Math.round(x)),
        y1 = Math.max(0, Math.round(y));
      const x2 = Math.min(width, Math.round(x + w)),
        y2 = Math.min(height, Math.round(y + h));
      if (x2 <= x1 || y2 <= y1) continue;
      const block = Math.max(8 * Math.max(sx, sy), Math.max(w, h) / 24);
      const small = document.createElement("canvas");
      small.width = Math.max(1, Math.ceil((x2 - x1) / block));
      small.height = Math.max(1, Math.ceil((y2 - y1) / block));
      const smallCtx = small.getContext("2d");
      if (!smallCtx) throw new Error("Could not render screenshot");
      smallCtx.drawImage(
        canvas,
        x1,
        y1,
        x2 - x1,
        y2 - y1,
        0,
        0,
        small.width,
        small.height,
      );
      ctx.imageSmoothingEnabled = false;
      ctx.drawImage(small, x1, y1, x2 - x1, y2 - y1);
      ctx.imageSmoothingEnabled = true;
    }
  }
}

export async function screenshotPNG(
  canvas: HTMLCanvasElement,
): Promise<string> {
  const blob = await new Promise<Blob | null>((resolve) =>
    canvas.toBlob(resolve, "image/png"),
  );
  if (!blob) throw new Error("Could not render screenshot");
  if (blob.size > 4 * 1024 * 1024)
    throw new Error("Each attachment must be nonempty and at most 4 MB");
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",")[1]);
    reader.onerror = () => reject(new Error("Could not render screenshot"));
    reader.readAsDataURL(blob);
  });
}
