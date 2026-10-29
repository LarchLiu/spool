import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { resizeTextarea } from "../.test-build/autosize-textarea.js";

function textareaFixture(t, measuredHeight) {
  const previous = globalThis.getComputedStyle;
  globalThis.getComputedStyle = () => ({
    paddingTop: "17px",
    paddingBottom: "8px",
    borderTopWidth: "0px",
    borderBottomWidth: "0px",
    lineHeight: "22.4px",
  });
  t.after(() => {
    if (previous) globalThis.getComputedStyle = previous;
    else delete globalThis.getComputedStyle;
  });
  const attributes = new Map([
    ["id", "chat-message"],
    ["name", "message"],
    ["autofocus", ""],
  ]);
  const writes = [];
  let attached = false;
  const measure = {
    style: {},
    removeAttribute: (name) => attributes.delete(name),
    setAttribute: (name, value) => attributes.set(name, value),
    remove() {
      attached = false;
    },
    get scrollHeight() {
      assert.equal(attached, true);
      assert.equal(measure.style.position, "absolute");
      if (measuredHeight instanceof Error) throw measuredHeight;
      return measuredHeight;
    },
  };
  const textarea = {
    value: "Draft text",
    style: new Proxy(
      { height: "48px" },
      {
        set(target, property, value) {
          writes.push([property, value]);
          target[property] = value;
          return true;
        },
      },
    ),
    cloneNode: () => measure,
    getBoundingClientRect: () => ({ width: 640 }),
    after(node) {
      assert.equal(node, measure);
      attached = true;
    },
  };
  return { textarea, measure, attributes, writes, attached: () => attached };
}

test("sizing measures off-layout without collapsing or focusing the live input", (t) => {
  const f = textareaFixture(t, 48);
  resizeTextarea(f.textarea);
  assert.deepEqual(f.writes, [
    ["height", "48px"],
    ["overflowY", "hidden"],
  ]);
  assert.equal(f.measure.style.width, "640px");
  assert.equal(f.measure.style.visibility, "hidden");
  assert.equal(f.measure.value, f.textarea.value);
  assert.equal(f.measure.tabIndex, -1);
  assert.deepEqual([...f.attributes], [["aria-hidden", "true"]]);
  assert.equal(f.attached(), false);
});

test("sizing keeps a one-line minimum, grows, caps at six lines and shrinks again", async (t) => {
  for (const [measured, height, overflow] of [
    [25, "48px", "hidden"],
    [93, "93px", "hidden"],
    [500, "160px", "auto"],
    [48, "48px", "hidden"],
  ]) {
    await t.test(`${measured}px content`, (t) => {
      const f = textareaFixture(t, measured);
      resizeTextarea(f.textarea);
      assert.equal(f.textarea.style.height, height);
      assert.equal(f.textarea.style.overflowY, overflow);
      assert.ok(
        f.writes.every(([key, value]) => key !== "height" || value !== "0px"),
      );
    });
  }
});

test("failed measurement removes the temporary element without changing live geometry", (t) => {
  const f = textareaFixture(t, new Error("measurement failed"));
  assert.throws(() => resizeTextarea(f.textarea), /measurement failed/);
  assert.equal(f.attached(), false);
  assert.deepEqual(f.writes, []);
});

test("compact floating windows can cap draft height without losing scrollable text", (t) => {
  const f = textareaFixture(t, 500);
  resizeTextarea(f.textarea, 3);
  assert.equal(f.textarea.style.height, "93px");
  assert.equal(f.textarea.style.overflowY, "auto");
  resizeTextarea(f.textarea, 1);
  assert.equal(f.textarea.style.height, "48px");
});

test("following history observes viewport changes as well as message growth", async () => {
  const main = await readFile(
    new URL("../src/main.tsx", import.meta.url),
    "utf8",
  );
  assert.match(
    main,
    /if \(follow\.current && history\.current\)\s*history\.current\.scrollTop = history\.current\.scrollHeight/,
  );
  assert.match(
    main,
    /resize\.observe\(element\);\s*if \(history\.current\) resize\.observe\(history\.current\)/,
  );
});
