// Validate content-free dumpsys snapshots before measuring one focused chat.
import { readFileSync } from "node:fs";
import { sleep } from "./common.mjs";
export function validateTarget(snapshot, minParts = 0, renderer) {
  if ((snapshot.parts || 0) < minParts)
    throw new Error(
      `The foreground chat has ${snapshot.parts || 0} parts; at least ${minParts} are required; no gestures were issued`,
    );
  if (renderer != null && snapshot.renderer !== renderer)
    throw new Error(
      `Expected renderer ${renderer}, got ${snapshot.renderer || "unknown"}; no gestures were issued`,
    );
}
export function validateInterval(snapshot, expected, position) {
  if (
    ["parts", "turns", "virtualRows", "renderer"].some(
      (key) => snapshot[key] !== expected[key],
    )
  )
    throw new Error(
      "Transcript or disclosure row count changed; fixed-interval measurement aborted",
    );
  if (
    position != null &&
    (snapshot.viewport?.firstVisible !== position ||
      snapshot.viewport?.firstTop !== 0)
  )
    throw new Error(
      "The requested starting row did not settle at the viewport top; measurement aborted",
    );
}
export function reachedEdge(snapshot, direction) {
  const value =
    snapshot.viewport?.[direction === "up" ? "canScrollUp" : "canScrollDown"];
  if (typeof value !== "boolean")
    throw new Error("Use a debug APK that reports native scroll boundaries");
  return !value;
}
export function validateVisibleContent(before, after) {
  const left = before.visibleContent || {},
    right = after.visibleContent || {};
  if (
    !left.rows ||
    !/^[a-f0-9]{64}$/.test(left.sha256 || "") ||
    Object.keys(left).length !== Object.keys(right).length ||
    Object.keys(left).some(
      (key) => JSON.stringify(left[key]) !== JSON.stringify(right[key]),
    )
  )
    throw new Error(
      "Visible content or geometry changed after scrolling back to the verification row",
    );
}
export function waitFinishedArtTrace(
  copyTrace,
  path,
  attempts = 40,
  interval = 0.25,
) {
  let previous;
  for (let attempt = 0; attempt < attempts; attempt++) {
    copyTrace();
    const data = readFileSync(path),
      end = data.indexOf("*end\n"),
      binary = end < 0 ? Buffer.alloc(0) : data.subarray(end + 5);
    let complete = false;
    if (
      binary.length >= 18 &&
      data.subarray(0, end + 5).includes(Buffer.from("clock=dual\n"))
    ) {
      const offset = binary.readUInt16LE(6),
        record = binary.readUInt16LE(16);
      complete =
        binary.toString("ascii", 0, 4) === "SLOW" &&
        binary.readUInt16LE(4) === 3 &&
        offset >= 18 &&
        offset <= binary.length &&
        record >= 14 &&
        (binary.length - offset) % record === 0;
    }
    if (complete && previous?.equals(data)) return;
    previous = complete ? data : undefined;
    if (attempt + 1 < attempts && interval) sleep(interval);
  }
  throw new Error(
    "ART trace did not finish writing after profile stop; incomplete sampling data was not used",
  );
}
export function foregroundSnapshot(
  output,
  expected,
  requireFling = false,
  allowHydrating = false,
  requireHistory = true,
) {
  const snapshots = [
    ...output.matchAll(/^SPOOL_PERF (\{[^\r\n]+\})\s*$/gm),
  ].map((match) => {
    let value;
    try {
      value = JSON.parse(match[1]);
    } catch {
      throw new Error("Invalid debug probe JSON");
    }
    if (!value || Array.isArray(value) || typeof value !== "object")
      throw new Error("Invalid debug probe snapshot");
    return value;
  });
  if (!snapshots.length)
    throw new Error(
      "No responsive Activity probe was returned; check the foreground chat and main-thread responsiveness",
    );
  if (snapshots.some((value) => value.probeVersion !== 2))
    throw new Error(
      "Use the current debug APK; a version-2 performance probe is required",
    );
  const focused = snapshots.filter(
    (value) => value.foreground === true && value.focused === true,
  );
  if (focused.length !== 1)
    throw new Error(
      "Exactly one foreground, focused Activity is required; no background page was selected",
    );
  const value = focused[0];
  if (requireHistory && (value.historyVisible !== true || !value.turns))
    throw new Error(
      "Open a chat in the foreground; its history must be visible",
    );
  if (!/^[a-f0-9-]{36}$/.test(String(value.probeToken || "")))
    throw new Error("Missing per-Activity probe token");
  if (!Number.isInteger(value.selectionEpoch) || value.selectionEpoch < 0)
    throw new Error("Invalid selected-page epoch");
  if (
    expected &&
    ["probeToken", "selectionEpoch"].some((key) => value[key] !== expected[key])
  )
    throw new Error(
      "The Activity or selected chat changed; measurement aborted",
    );
  if (value.historyHasMore !== false && !allowHydrating)
    throw new Error(
      "History is still hydrating; wait for it to finish or explicitly allow hydration measurements",
    );
  if (value.renderPending && !allowHydrating)
    throw new Error(
      "Markdown parsing is still pending; wait for the renderer to settle",
    );
  if (requireFling && value.flingAccepted !== true)
    throw new Error(
      "The focused page rejected the targeted gesture; measurement aborted",
    );
  return value;
}
