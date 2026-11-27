import test from "node:test";
import assert from "node:assert/strict";
import {
  adjustSelection,
  selectionRect,
  windowAt,
  localWindowRect,
} from "../.test-build/screenshot-selection.js";

test("frontmost window wins at a desktop point and clips only its preview", () => {
  const windows = [
    { id: 1, bounds: { x: -120, y: 80, width: 200, height: 300 } },
    { id: 2, bounds: { x: -300, y: 0, width: 500, height: 500 } },
  ];
  assert.equal(windowAt(windows, { x: 0, y: 100 }).id, 1);
  assert.equal(windowAt(windows, { x: -200, y: 100 }).id, 2);
  assert.equal(windowAt(windows, { x: -800, y: 100 }), undefined);
  assert.deepEqual(
    localWindowRect(windows[0], { x: 0, y: 0, width: 400, height: 400 }),
    { x: 0, y: 80, width: 80, height: 300 },
  );
});
test("region selection works in all drag directions and stops at display boundaries", () => {
  assert.deepEqual(
    selectionRect({ x: 200, y: 100 }, { x: 20, y: 30 }, 1000, 700),
    { x: 20, y: 30, width: 180, height: 70 },
  );
  assert.deepEqual(
    selectionRect({ x: 200, y: 100 }, { x: -50, y: 900 }, 1000, 700),
    { x: 0, y: 100, width: 200, height: 600 },
  );
});
test("resizing never inverts the selected region and moving preserves dimensions", () => {
  const rect = { x: 100, y: 100, width: 300, height: 200 };
  assert.deepEqual(
    adjustSelection(rect, "nw", { x: 1000, y: 1000 }, 1000, 700),
    { x: 399, y: 299, width: 1, height: 1 },
  );
  assert.deepEqual(
    adjustSelection(rect, "se", { x: 1000, y: 1000 }, 1000, 700),
    { x: 100, y: 100, width: 900, height: 600 },
  );
  assert.deepEqual(
    adjustSelection(rect, "move", { x: -1000, y: 1000 }, 1000, 700),
    { x: 0, y: 500, width: 300, height: 200 },
  );
});
