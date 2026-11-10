import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, rmSync, writeFileSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  foregroundSnapshot,
  reachedEdge,
  validateInterval,
  validateTarget,
  validateVisibleContent,
  waitFinishedArtTrace,
} from "./performance-probe.mjs";
const snapshot = (changes) => ({
  probeVersion: 2,
  probeToken: "12345678-1234-1234-1234-123456789abc",
  selectionEpoch: 3,
  foreground: true,
  focused: true,
  historyVisible: true,
  historyHasMore: false,
  turns: 64,
  flingAccepted: false,
  ...changes,
});
const dump = (...values) =>
  "TASK\n" +
  values.map((value) => "SPOOL_PERF " + JSON.stringify(value)).join("\n") +
  "\n";
test("selects the focused page instead of the first background result", () => {
  const focused = snapshot();
  assert.deepEqual(
    foregroundSnapshot(
      dump(snapshot({ foreground: false, focused: false }), focused),
    ),
    focused,
  );
});
test("never guesses when no or multiple pages have focus", () => {
  for (const values of [
    [snapshot({ focused: false })],
    [snapshot(), snapshot()],
  ])
    assert.throws(() => foregroundSnapshot(dump(...values)), /Exactly one/);
});
test("rejects legacy, missing, malformed probe data", () => {
  for (const value of ["", "SPOOL_PERF {invalid}\n", dump({ turns: 64 })])
    assert.throws(() => foregroundSnapshot(value));
});
test("an Activity timeout is not misreported as an old APK", () =>
  assert.throws(
    () =>
      foregroundSnapshot(
        "Failure while dumping the activity: java.io.IOException: Timeout",
      ),
    /responsiveness/,
  ));
test("rejects navigation and Activity changes", () => {
  for (const change of [
    { selectionEpoch: 4 },
    { probeToken: "abcdef12-1234-1234-1234-123456789abc" },
  ])
    assert.throws(
      () => foregroundSnapshot(dump(snapshot(change)), snapshot()),
      /changed/,
    );
});
test("requires a visible nonempty history", () => {
  for (const change of [{ historyVisible: false }, { turns: 0 }])
    assert.throws(() => foregroundSnapshot(dump(snapshot(change))), /visible/);
});
test("title navigation can wait for history but still requires focus", () => {
  const loading = snapshot({
    historyVisible: false,
    turns: 0,
    historyHasMore: true,
  });
  assert.deepEqual(
    foregroundSnapshot(dump(loading), undefined, false, true, false),
    loading,
  );
  assert.throws(
    () =>
      foregroundSnapshot(
        dump({ ...loading, focused: false }),
        undefined,
        false,
        true,
        false,
      ),
    /Exactly one/,
  );
});
test("hydration needs explicit opt-in", () => {
  const output = dump(snapshot({ historyHasMore: true }));
  assert.throws(() => foregroundSnapshot(output), /hydrating/);
  assert.equal(
    foregroundSnapshot(output, undefined, false, true).historyHasMore,
    true,
  );
});
test("Markdown must settle before a baseline", () => {
  const output = dump(snapshot({ renderPending: 3 }));
  assert.throws(() => foregroundSnapshot(output), /Markdown parsing/);
  assert.equal(
    foregroundSnapshot(output, undefined, false, true).renderPending,
    3,
  );
  assert.equal(
    foregroundSnapshot(dump(snapshot({ renderPending: 0 }))).renderPending,
    0,
  );
});
test("rejects short chats and unintended renderers before gestures", () => {
  assert.throws(
    () =>
      validateTarget(
        snapshot({ parts: 49, renderer: "markwon-recycler" }),
        1000,
        "markwon-recycler",
      ),
    /at least 1000.*no gestures/,
  );
  assert.throws(
    () =>
      validateTarget(
        snapshot({ parts: 2000, renderer: "native-views" }),
        1000,
        "markwon-recycler",
      ),
    /Expected renderer.*no gestures/,
  );
  validateTarget(
    snapshot({ parts: 2000, renderer: "markwon-recycler" }),
    1000,
    "markwon-recycler",
  );
});
test("rejected gestures cannot count as samples", () => {
  assert.throws(
    () => foregroundSnapshot(dump(snapshot()), undefined, true),
    /rejected/,
  );
  assert.equal(
    foregroundSnapshot(dump(snapshot({ flingAccepted: true })), undefined, true)
      .flingAccepted,
    true,
  );
});
test("requires valid tokens and page epochs", () => {
  for (const change of [
    { probeToken: "" },
    { selectionEpoch: true },
    { selectionEpoch: -1 },
    { selectionEpoch: "3" },
  ])
    assert.throws(() => foregroundSnapshot(dump(snapshot(change))));
});
test("fixed intervals require the same rows and exact start", () => {
  const expected = snapshot({
    parts: 3000,
    virtualRows: 400,
    renderer: "markwon-recycler",
    viewport: { firstVisible: 120, firstTop: 0 },
  });
  validateInterval(expected, expected, 120);
  for (const change of [
    { parts: 3001 },
    { turns: 65 },
    { virtualRows: 399 },
    { renderer: "native-views" },
  ])
    assert.throws(
      () => validateInterval({ ...expected, ...change }, expected),
      /row count changed/,
    );
  for (const viewport of [
    { firstVisible: 119, firstTop: 0 },
    { firstVisible: 120, firstTop: -1 },
    {},
  ])
    assert.throws(
      () => validateInterval({ ...expected, viewport }, expected, 120),
      /viewport top/,
    );
});
test("edges require native bounds rather than a last visible row", () => {
  const partial = snapshot({
    viewport: {
      firstVisible: 0,
      lastVisible: 10,
      canScrollUp: true,
      canScrollDown: true,
    },
  });
  assert.equal(reachedEdge(partial, "up"), false);
  assert.equal(reachedEdge(partial, "down"), false);
  assert.equal(
    reachedEdge(snapshot({ viewport: { canScrollUp: false } }), "up"),
    true,
  );
  assert.equal(
    reachedEdge(snapshot({ viewport: { canScrollDown: false } }), "down"),
    true,
  );
  assert.throws(
    () => reachedEdge(snapshot({ viewport: { lastVisible: 10 } }), "down"),
    /boundaries/,
  );
});
test("content verification rejects missing and changed digests", () => {
  const valid = snapshot({
    visibleContent: { rows: 25, sha256: "a".repeat(64) },
  });
  validateVisibleContent(valid, valid);
  for (const value of [
    {},
    { rows: 24, sha256: "a".repeat(64) },
    { rows: 25, sha256: "b".repeat(64) },
  ])
    assert.throws(
      () => validateVisibleContent(valid, snapshot({ visibleContent: value })),
      /content or geometry changed/,
    );
  assert.throws(
    () => validateVisibleContent(snapshot(), snapshot()),
    /content or geometry changed/,
  );
});
function trace(records = 1) {
  const binary = Buffer.alloc(18 + records * 14);
  binary.write("SLOW");
  binary.writeUInt16LE(3, 4);
  binary.writeUInt16LE(18, 6);
  binary.writeUInt16LE(14, 16);
  for (let i = 0; i < records; i++) {
    binary.writeUInt16LE(1, 18 + i * 14);
    binary.writeUInt32LE(4, 20 + i * 14);
  }
  return Buffer.concat([
    Buffer.from("*version\n3\nclock=dual\n*end\n"),
    binary,
  ]);
}
function wait(copies) {
  const root = mkdtempSync(join(tmpdir(), "trace-test-")),
    target = join(root, "trace");
  let index = 0;
  try {
    waitFinishedArtTrace(
      () => writeFileSync(target, copies[index++]),
      target,
      copies.length,
      0,
    );
    return readFileSync(target);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}
test("waits for stop to write a complete header and records", () => {
  const value = trace();
  assert.deepEqual(
    wait([Buffer.alloc(0), value.subarray(0, -1), value, value]),
    value,
  );
});
test("a complete header is insufficient while the trace grows", () =>
  assert.deepEqual(wait([trace(), trace(2), trace(3), trace(3)]), trace(3)));
test("incomplete sampling times out instead of becoming evidence", () =>
  assert.throws(
    () => wait(Array(4).fill(Buffer.from("unfinished"))),
    /incomplete sampling data was not used/,
  ));
