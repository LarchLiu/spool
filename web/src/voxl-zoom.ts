import type { Translator } from "./i18n";

type Point = { x: number; y: number };
const MIN_ZOOM = 1;
const MAX_ZOOM = 5;
const ZOOM_STEP = 1.25;
const clamp = (value: number, limit: number) =>
  Math.max(-limit, Math.min(limit, value));

// The native client sizes #surface to fit the viewport and maps input through
// getBoundingClientRect(). Transform the whole surface, including all video
// stripes, so its existing input mapping also accounts for zoom and translation.
export class DesktopViewport {
  zoom = MIN_ZOOM;
  x = 0;
  y = 0;
  width = 0;
  height = 0;
  surfaceWidth = 0;
  surfaceHeight = 0;

  resize(
    width: number,
    height: number,
    surfaceWidth: number,
    surfaceHeight: number,
  ) {
    if (this.surfaceWidth) this.x *= surfaceWidth / this.surfaceWidth;
    if (this.surfaceHeight) this.y *= surfaceHeight / this.surfaceHeight;
    Object.assign(this, { width, height, surfaceWidth, surfaceHeight });
    this.constrain();
  }

  zoomAt(value: number, from: Point, to = from) {
    const next = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, value));
    const ratio = next / this.zoom;
    this.x = to.x - this.width / 2 - (from.x - this.width / 2 - this.x) * ratio;
    this.y =
      to.y - this.height / 2 - (from.y - this.height / 2 - this.y) * ratio;
    this.zoom = next;
    this.constrain();
  }

  pan(dx: number, dy: number) {
    this.x += dx;
    this.y += dy;
    this.constrain();
  }

  reset() {
    this.zoom = MIN_ZOOM;
    this.x = this.y = 0;
  }

  private constrain() {
    this.x = clamp(
      this.x,
      Math.max(0, (this.surfaceWidth * this.zoom - this.width) / 2),
    );
    this.y = clamp(
      this.y,
      Math.max(0, (this.surfaceHeight * this.zoom - this.height) / 2),
    );
  }
}

export function setupVoxlZoom(doc: Document, t: Translator) {
  const frameWindow = doc.defaultView;
  const desktop = doc.getElementById("surface");
  const tools = doc.getElementById("tools");
  if (!frameWindow || !desktop || !tools) return () => {};
  const view = frameWindow as Window & typeof globalThis;
  const surface = desktop;

  const state = new DesktopViewport();
  const group = doc.createElement("div");
  group.id = "spool-voxl-zoom";
  group.setAttribute("role", "group");
  group.setAttribute("aria-label", t("Desktop zoom"));
  group.title = t(
    "Enable pan mode to zoom and pan. Use Ctrl/Command + wheel or pinch to zoom, and drag to pan. Turn it off to keep the view and restore remote controls, including two-finger scrolling.",
  );
  const style = doc.createElement("style");
  style.textContent = `
    #surface { flex-shrink: 0; transform-origin: center center; }
    #surface.spool-pan { cursor: grab !important; }
    #surface.spool-panning { cursor: grabbing !important; }
    #tools { max-width: calc(100vw - 24px); flex-wrap: wrap; justify-content: flex-end; }
    #spool-voxl-zoom { display: flex; align-items: center; gap: 2px; }
    #spool-voxl-zoom .ib { color: var(--icon-title, #e0e0e0); }
    #spool-voxl-zoom .ib:disabled { opacity: .35; cursor: default; }
    #spool-voxl-zoom .ib[aria-pressed="true"] { background: var(--state-pressed, #ffffff29); }
    #spool-voxl-zoom .zoom-level { min-width: 48px; width: auto; padding: 0 4px; font-size: 11px; font-variant-numeric: tabular-nums; }
    #tools.touch #spool-voxl-zoom .zoom-level { min-width: 54px; }
  `;
  doc.head.append(style);

  function button(label: string, paths?: string[]) {
    const element = doc.createElement("button");
    element.type = "button";
    element.className = "ib";
    element.title = label;
    element.setAttribute("aria-label", label);
    if (paths) {
      const svg = doc.createElementNS("http://www.w3.org/2000/svg", "svg");
      svg.setAttribute("class", "i");
      svg.setAttribute("viewBox", "0 0 24 24");
      svg.setAttribute("fill", "none");
      svg.setAttribute("stroke", "currentColor");
      svg.setAttribute("stroke-width", "1.5");
      svg.setAttribute("stroke-linecap", "round");
      svg.setAttribute("stroke-linejoin", "round");
      svg.setAttribute("aria-hidden", "true");
      for (const d of paths) {
        const path = doc.createElementNS(svg.namespaceURI, "path");
        path.setAttribute("d", d);
        svg.append(path);
      }
      element.append(svg);
    }
    group.append(element);
    return element;
  }
  const minus = button(t("Zoom out"), ["M5 12h14"]);
  const fit = button(t("Fit desktop to window"));
  fit.classList.add("zoom-level");
  const plus = button(t("Zoom in"), ["M5 12h14", "M12 5v14"]);
  const pan = button(t("Pan desktop"), [
    "M12 3v18M3 12h18M8 7l4-4 4 4M8 17l4 4 4-4M7 8l-4 4 4 4M17 8l4 4-4 4",
  ]);
  tools.prepend(group);

  let panMode = false;
  let mousePan: { id: number; x: number; y: number } | undefined;
  const touches = new Map<number, Point>();
  const forwardedTouches = new Set<number>();
  const cancellations = new WeakSet<Event>();
  let localTouch = false;
  let pinch: { mid: Point; distance: number } | undefined;
  let disposed = false;
  let animation: number | undefined;
  const previousTransform = surface.style.transform;

  function render() {
    surface.style.transform = `translate(${state.x}px, ${state.y}px) scale(${state.zoom})`;
    surface.classList.toggle(
      "spool-panning",
      panMode && (!!mousePan || localTouch),
    );
    fit.textContent = `${Math.round(state.zoom * 100)}%`;
    minus.disabled = !panMode || state.zoom <= MIN_ZOOM;
    plus.disabled = !panMode || state.zoom >= MAX_ZOOM || !surface.offsetWidth;
    fit.disabled = !panMode;
    pan.setAttribute("aria-pressed", String(panMode));
    surface.classList.toggle("spool-pan", panMode);
  }
  function resize() {
    state.resize(
      view.innerWidth,
      view.innerHeight,
      surface.offsetWidth,
      surface.offsetHeight,
    );
    render();
  }
  function scheduleResize() {
    if (animation !== undefined) return;
    animation = view.requestAnimationFrame(() => {
      animation = undefined;
      if (!disposed) resize();
    });
  }
  const center = () => ({ x: state.width / 2, y: state.height / 2 });
  const changeZoom = (factor: number) => {
    if (!panMode) return;
    state.zoomAt(state.zoom * factor, center());
    render();
  };
  minus.addEventListener("click", () => changeZoom(1 / ZOOM_STEP));
  plus.addEventListener("click", () => changeZoom(ZOOM_STEP));
  fit.addEventListener("click", () => {
    if (!panMode) return;
    state.reset();
    render();
  });
  pan.addEventListener("click", () => {
    panMode = !panMode;
    if (panMode) {
      // Switching modes can happen while a native touch is still held down.
      // Release its remote drag/long press before taking over locally.
      for (const id of forwardedTouches) cancelRemoteTouch(id);
      forwardedTouches.clear();
      localTouch = touches.size > 0;
      pinch = pinchState();
    }
    render();
  });
  group.addEventListener("pointerdown", (event) => event.stopPropagation());

  const inside = (event: Event) => surface.contains(event.target as Node);
  function consume(event: Event) {
    event.preventDefault();
    event.stopImmediatePropagation();
  }
  function capture(id: number) {
    try {
      surface.setPointerCapture(id);
    } catch {
      /* Synthetic tests / canceled contacts. */
    }
  }
  function cancelRemoteTouch(id: number) {
    // The first finger may have started a native drag or long-press timer.
    // Cancel it through voxl's own handler before taking over the gesture.
    const event = new view.PointerEvent("pointercancel", {
      bubbles: true,
      pointerId: id,
      pointerType: "touch",
    });
    cancellations.add(event);
    surface.dispatchEvent(event);
  }
  function pinchState() {
    const [a, b] = [...touches.values()];
    return a && b
      ? {
          mid: { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 },
          distance: Math.max(1, Math.hypot(a.x - b.x, a.y - b.y)),
        }
      : undefined;
  }
  function down(event: PointerEvent) {
    if (!inside(event)) return;
    if (event.pointerType === "touch") {
      touches.set(event.pointerId, { x: event.clientX, y: event.clientY });
      if (localTouch || panMode) {
        localTouch = true;
        for (const id of forwardedTouches) cancelRemoteTouch(id);
        forwardedTouches.clear();
        pinch = pinchState();
        capture(event.pointerId);
        consume(event);
        render();
      } else forwardedTouches.add(event.pointerId);
    } else if (panMode && (event.button === 1 || event.button === 0)) {
      mousePan = { id: event.pointerId, x: event.clientX, y: event.clientY };
      capture(event.pointerId);
      consume(event);
      render();
    }
  }
  function move(event: PointerEvent) {
    if (event.pointerType === "touch") {
      const previous = touches.get(event.pointerId);
      if (!previous) return;
      touches.set(event.pointerId, { x: event.clientX, y: event.clientY });
      if (!localTouch) return;
      consume(event);
      const next = pinchState();
      if (panMode && next && pinch)
        state.zoomAt(
          (state.zoom * next.distance) / pinch.distance,
          pinch.mid,
          next.mid,
        );
      else if (panMode && touches.size === 1)
        state.pan(event.clientX - previous.x, event.clientY - previous.y);
      pinch = next;
      render();
    } else if (mousePan?.id === event.pointerId) {
      consume(event);
      if (panMode)
        state.pan(event.clientX - mousePan.x, event.clientY - mousePan.y);
      mousePan = { id: event.pointerId, x: event.clientX, y: event.clientY };
      render();
    }
  }
  function up(event: PointerEvent) {
    if (cancellations.has(event)) return;
    if (event.pointerType === "touch") {
      if (!touches.delete(event.pointerId)) return;
      forwardedTouches.delete(event.pointerId);
      if (localTouch) {
        consume(event);
        pinch = pinchState();
        if (!touches.size) localTouch = false;
        render();
      }
    } else if (mousePan?.id === event.pointerId) {
      consume(event);
      mousePan = undefined;
      render();
    }
  }
  function wheel(event: WheelEvent) {
    if (!panMode || !inside(event) || (!event.ctrlKey && !event.metaKey))
      return;
    consume(event);
    const delta =
      event.deltaY *
      (event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? state.height : 1);
    state.zoomAt(state.zoom * Math.exp(clamp(-delta * 0.01, 0.5)), {
      x: event.clientX,
      y: event.clientY,
    });
    render();
  }
  function blur() {
    forwardedTouches.clear();
    touches.clear();
    localTouch = false;
    mousePan = pinch = undefined;
    render();
  }
  // Capture before the native surface handlers; unmodified input is left intact.
  doc.addEventListener("pointerdown", down, true);
  doc.addEventListener("pointermove", move, true);
  doc.addEventListener("pointerup", up, true);
  doc.addEventListener("pointercancel", up, true);
  doc.addEventListener("wheel", wheel, { capture: true, passive: false });
  view.addEventListener("blur", blur);
  view.addEventListener("resize", scheduleResize);
  doc.addEventListener("fullscreenchange", scheduleResize);
  const observer = new view.ResizeObserver(scheduleResize);
  observer.observe(surface);
  resize();

  return () => {
    if (disposed) return;
    disposed = true;
    blur();
    observer.disconnect();
    if (animation !== undefined) view.cancelAnimationFrame(animation);
    doc.removeEventListener("pointerdown", down, true);
    doc.removeEventListener("pointermove", move, true);
    doc.removeEventListener("pointerup", up, true);
    doc.removeEventListener("pointercancel", up, true);
    doc.removeEventListener("wheel", wheel, true);
    view.removeEventListener("blur", blur);
    view.removeEventListener("resize", scheduleResize);
    doc.removeEventListener("fullscreenchange", scheduleResize);
    surface.style.transform = previousTransform;
    surface.classList.remove("spool-pan", "spool-panning");
    group.remove();
    style.remove();
  };
}
