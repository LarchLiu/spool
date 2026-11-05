import assert from "node:assert/strict";
import test from "node:test";
import { createTestViteServer } from "./vite-fixture.mjs";

const server = await createTestViteServer(test, {
  root: new URL("..", import.meta.url).pathname,
  configFile: false,
  logLevel: "silent",
  appType: "custom",
  server: { middlewareMode: true, watch: null, hmr: false },
});
const { DesktopViewport } = await server.ssrLoadModule("/src/voxl-zoom.ts");

// These are the coordinates voxl's pointer mapper sends to a 2560x1440 host.
function desktopPoint(view, point) {
  const width = view.surfaceWidth * view.zoom;
  const height = view.surfaceHeight * view.zoom;
  const left = (view.width - width) / 2 + view.x;
  const top = (view.height - height) / 2 + view.y;
  return {
    x: ((point.x - left) / width) * 2560,
    y: ((point.y - top) / height) * 1440,
  };
}
function close(a, b) {
  assert.ok(Math.abs(a - b) < 1e-8, `${a} != ${b}`);
}
function fitted() {
  const view = new DesktopViewport();
  view.resize(1000, 800, 1000, 562.5);
  return view;
}

test("zoom keeps the desktop point under the mouse fixed and retains its input coordinates", () => {
  const view = fitted();
  const mouse = { x: 640, y: 430 };
  const before = desktopPoint(view, mouse);
  view.zoomAt(2, mouse);
  const after = desktopPoint(view, mouse);
  close(before.x, after.x);
  close(before.y, after.y);
  assert.equal(view.zoom, 2);
});

test("pinch follows its moving midpoint without moving the remote desktop point", () => {
  const view = fitted();
  view.zoomAt(2, { x: 500, y: 400 });
  const from = { x: 510, y: 390 },
    to = { x: 580, y: 440 };
  const before = desktopPoint(view, from);
  view.zoomAt(3, from, to);
  const after = desktopPoint(view, to);
  close(before.x, after.x);
  close(before.y, after.y);
});

test("zoom and pan stop at their limits without exposing empty space", () => {
  const view = fitted();
  view.zoomAt(100, { x: 500, y: 400 });
  assert.equal(view.zoom, 5);
  view.pan(10000, -10000);
  assert.equal(view.x, (view.surfaceWidth * 5 - view.width) / 2);
  assert.equal(view.y, -(view.surfaceHeight * 5 - view.height) / 2);
  view.zoomAt(0.1, { x: 0, y: 0 });
  assert.equal(view.zoom, 1);
  assert.equal(Math.abs(view.x), 0);
  assert.equal(Math.abs(view.y), 0);
  // At fit, black letterboxing cannot itself be panned.
  view.pan(100, 200);
  assert.equal(Math.abs(view.x), 0);
  assert.equal(Math.abs(view.y), 0);
});

test("window and fullscreen resizing preserve the viewed center within new boundaries", () => {
  const view = fitted();
  view.zoomAt(3, { x: 500, y: 400 });
  view.pan(-130, 75);
  const before = desktopPoint(view, { x: 500, y: 400 });
  view.resize(1600, 1000, 1600, 900);
  const after = desktopPoint(view, { x: 800, y: 500 });
  close(before.x, after.x);
  close(before.y, after.y);
  view.resize(320, 900, 320, 180);
  assert.equal(Math.abs(view.y), 0);
  assert.equal(view.zoom, 3);
});

test("fit resets both zoom and translation after panning", () => {
  const view = fitted();
  view.zoomAt(4, { x: 250, y: 300 });
  view.pan(-200, 70);
  view.reset();
  assert.equal(view.zoom, 1);
  assert.equal(view.x, 0);
  assert.equal(view.y, 0);
});
