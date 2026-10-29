import assert from "node:assert/strict";
import test from "node:test";
import {
  fitFloatingWindow,
  moveFloatingWindow,
  resizeFloatingWindow,
  floatingWindowEdges,
} from "../.test-build/components/web/src/floating-window.js";

const desktop = { x: 0, y: 0, width: 1440, height: 900 };
const mobile = { x: 0, y: 0, width: 390, height: 700 };

test("chat initially opens compactly at the bottom right", () => {
  assert.deepEqual(fitFloatingWindow(null, desktop), {
    x: 1008,
    y: 368,
    width: 420,
    height: 520,
  });
  assert.deepEqual(fitFloatingWindow(null, mobile), {
    x: 12,
    y: 168,
    width: 366,
    height: 520,
  });
});

test("drag keeps the full window and close control inside the viewport", () => {
  const bounds = fitFloatingWindow(null, desktop);
  assert.deepEqual(moveFloatingWindow(bounds, -80, -50, desktop), {
    ...bounds,
    x: 928,
    y: 318,
  });
  assert.deepEqual(moveFloatingWindow(bounds, -10000, -10000, desktop), {
    ...bounds,
    x: 12,
    y: 12,
  });
  assert.deepEqual(moveFloatingWindow(bounds, 10000, 10000, desktop), bounds);
});

test("resize keeps the top-left fixed, respects minimums and stops at the edge", () => {
  const bounds = { x: 100, y: 80, width: 420, height: 520 };
  assert.deepEqual(resizeFloatingWindow(bounds, 80, 40, desktop), {
    ...bounds,
    width: 500,
    height: 560,
  });
  assert.deepEqual(resizeFloatingWindow(bounds, -10000, -10000, desktop), {
    ...bounds,
    width: 300,
    height: 360,
  });
  assert.deepEqual(resizeFloatingWindow(bounds, 10000, 10000, desktop), {
    ...bounds,
    width: 1328,
    height: 808,
  });
});

test("viewport changes fit saved geometry, including narrow screens and keyboards", () => {
  const saved = { x: 1000, y: 500, width: 600, height: 600 };
  assert.deepEqual(fitFloatingWindow(saved, mobile), {
    x: 12,
    y: 88,
    width: 366,
    height: 600,
  });
  const keyboard = { x: 0, y: 220, width: 320, height: 280 };
  assert.deepEqual(fitFloatingWindow(saved, keyboard), {
    x: 12,
    y: 232,
    width: 296,
    height: 256,
  });
  const tiny = { x: 10, y: 30, width: 20, height: 40 };
  assert.deepEqual(fitFloatingWindow(saved, tiny), {
    x: 15,
    y: 35,
    width: 10,
    height: 30,
  });
});

test("all four edges and corners resize only their axes with opposite edges anchored", () => {
  const bounds = { x: 100, y: 100, width: 420, height: 520 };
  for (const edge of floatingWindowEdges) {
    const result = resizeFloatingWindow(bounds, 20, 30, desktop, edge);
    assert.equal(result.x, edge.includes("w") ? 120 : 100);
    assert.equal(result.y, edge.includes("n") ? 130 : 100);
    assert.equal(
      result.width,
      edge.includes("w") ? 400 : edge.includes("e") ? 440 : 420,
    );
    assert.equal(
      result.height,
      edge.includes("n") ? 490 : edge.includes("s") ? 550 : 520,
    );
    if (edge.includes("w"))
      assert.equal(result.x + result.width, bounds.x + bounds.width);
    if (edge.includes("n"))
      assert.equal(result.y + result.height, bounds.y + bounds.height);
  }
});

test("all move/resize combinations stay within mobile and offset visual viewports", () => {
  for (const viewport of [
    desktop,
    mobile,
    { x: 40, y: 120, width: 280, height: 240 },
  ]) {
    for (const dx of [-10000, -20, 0, 80, 10000]) {
      for (const dy of [-10000, -20, 0, 80, 10000]) {
        const original = fitFloatingWindow(null, viewport);
        for (const result of [
          moveFloatingWindow(original, dx, dy, viewport),
          ...floatingWindowEdges.map((edge) =>
            resizeFloatingWindow(original, dx, dy, viewport, edge),
          ),
        ]) {
          assert.ok(result.x >= viewport.x);
          assert.ok(result.y >= viewport.y);
          assert.ok(result.x + result.width <= viewport.x + viewport.width);
          assert.ok(result.y + result.height <= viewport.y + viewport.height);
          assert.ok(result.width > 0 && result.height > 0);
        }
      }
    }
  }
});
