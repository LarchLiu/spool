#!/usr/bin/env bun
// Measure targeted flings on the focused debug chat without capturing content.
import { randomUUID } from "node:crypto";
import { join } from "node:path";
import {
  ROOT,
  SDK,
  cli,
  command,
  requireRunTmp,
  now,
  sleep,
  writeJson,
  quote,
} from "./common.mjs";
import {
  foregroundSnapshot,
  reachedEdge,
  validateInterval,
  validateTarget,
  validateVisibleContent,
  waitFinishedArtTrace,
} from "./performance-probe.mjs";
import { summarize } from "./summarize-method-trace.mjs";
import { readFileSync } from "node:fs";
const strings = {
  serial: undefined,
  direction: "alternate",
  traversal: undefined,
  "chat-title": undefined,
  "activity-reuse": undefined,
  "expect-renderer": undefined,
  output: join(
    ROOT,
    "build/artifacts/android-native/device-performance-foreground.json",
  ),
};
const numbers = {
  rounds: 12,
  "start-position": undefined,
  "warmup-rounds": 0,
  "settle-seconds": 2.5,
  "method-sampling-us": 0,
  "min-parts": 0,
  "wait-history-seconds": 0,
  "pause-seconds": 1.25,
};
const options = Object.fromEntries(
  [...Object.entries(strings), ...Object.entries(numbers)].map(
    ([key, value]) => [
      key,
      { type: "string", ...(value == null ? {} : { default: String(value) }) },
    ],
  ),
);
Object.assign(options, {
  "verify-position": { type: "string", multiple: true },
  "expand-tools": { type: "boolean" },
  "allow-hydrating": { type: "boolean" },
});
const { values: args } = cli(options, { required: ["serial"] });
for (const key of Object.keys(numbers))
  if (args[key] !== undefined) {
    args[key] = Number(args[key]);
    if (!Number.isFinite(args[key])) throw new Error("Invalid --" + key);
  }
args["verify-position"] = (args["verify-position"] || []).map(Number);
const check = (condition, message) => {
  if (!condition) throw new Error(message);
};
check(/^[A-Za-z0-9_.:-]+$/.test(args.serial), "Invalid adb serial");
for (const key of [
  "rounds",
  "start-position",
  "warmup-rounds",
  "method-sampling-us",
  "min-parts",
  "wait-history-seconds",
])
  check(
    args[key] == null || Number.isInteger(args[key]),
    "--" + key + " must be an integer",
  );
check(
  ["alternate", "up", "down"].includes(args.direction),
  "Invalid direction",
);
check(
  !args.traversal ||
    ["bottom-to-top", "top-to-bottom", "round-trip"].includes(args.traversal),
  "Invalid traversal",
);
check(
  !args["activity-reuse"] || ["on", "off"].includes(args["activity-reuse"]),
  "Invalid activity reuse",
);
check(
  !args["expect-renderer"] ||
    ["native-views", "markwon-recycler"].includes(args["expect-renderer"]),
  "Invalid renderer",
);
check(
  !args["method-sampling-us"] ||
    (args["method-sampling-us"] >= 1000 &&
      args["method-sampling-us"] <= 100000),
  "--method-sampling-us must be between 1000 and 100000",
);
check(
  args["wait-history-seconds"] >= 0 && args["wait-history-seconds"] <= 300,
  "--wait-history-seconds must be between 0 and 300",
);
check(args["min-parts"] >= 0, "--min-parts must be nonnegative");
check(
  args["pause-seconds"] >= 0.25 && args["pause-seconds"] <= 10,
  "--pause-seconds must be between 0.25 and 10",
);
check(
  args["start-position"] == null || args["start-position"] >= 0,
  "--start-position must be nonnegative",
);
check(
  args["warmup-rounds"] >= 0 &&
    args["warmup-rounds"] <= 60 &&
    (!args["warmup-rounds"] ||
      args["start-position"] != null ||
      args["expand-tools"]),
  "--warmup-rounds requires --start-position and must be between 0 and 60",
);
check(
  args["settle-seconds"] >= 2 && args["settle-seconds"] <= 10,
  "--settle-seconds must be between 2 and 10",
);
check(
  !args.traversal ||
    (args["start-position"] == null &&
      !args["warmup-rounds"] &&
      !args["method-sampling-us"]),
  "--traversal uses edge positioning and requires no warmup or method sampler",
);
check(
  args["verify-position"].every(
    (value) => Number.isInteger(value) && value >= 0,
  ) &&
    (!args["verify-position"].length ||
      args.traversal ||
      args["start-position"] != null),
  "--verify-position requires --traversal or --start-position and nonnegative positions",
);
const tmp = requireRunTmp(),
  pkg = "com.cloudgeek.spool.android.dev",
  component = pkg + "/com.cloudgeek.spool.android.MainActivity";
const adbPath = Bun.which("adb") || join(SDK, "platform-tools/adb");
const adb = (...values) =>
  command(adbPath, ["-s", args.serial, ...values], { timeout: 30000 }).stdout;
function snapshot(
  expected,
  velocity,
  allowHydrating = args["allow-hydrating"],
  requireHistory = true,
) {
  const action =
    velocity == null
      ? []
      : ["fling", velocity, expected.probeToken, expected.selectionEpoch];
  return foregroundSnapshot(
    adb(
      "shell",
      "dumpsys",
      "activity",
      component,
      "spool-performance",
      ...action,
    ),
    expected,
    velocity != null,
    allowHydrating,
    requireHistory,
  );
}
function control(
  expected,
  name,
  value,
  allowHydrating = args["allow-hydrating"],
) {
  const result = foregroundSnapshot(
    adb(
      "shell",
      "dumpsys",
      "activity",
      component,
      "spool-performance",
      name,
      value,
      expected.probeToken,
      expected.selectionEpoch,
    ),
    expected,
    false,
    allowHydrating,
  );
  check(
    result.controlAccepted === true,
    "The focused page rejected the targeted debug control; measurement aborted",
  );
  return result;
}
function flings(expected, rounds) {
  const samples = [];
  for (let turn = 0; turn < rounds; turn++) {
    const started = now(),
      velocity =
        args.direction === "up" ||
        (args.direction === "alternate" && turn % 2 === 0)
          ? -7000
          : 7000,
      stats = snapshot(expected, velocity);
    if (args["start-position"] != null) validateInterval(stats, expected);
    stats.commandMs = Math.round((now() - started) * 1000000) / 1000;
    samples.push(stats);
    sleep(args["pause-seconds"]);
  }
  sleep(Math.max(0, args["settle-seconds"] - args["pause-seconds"]));
  return samples;
}
function position(expected) {
  control(expected, "position", args["start-position"]);
  sleep(args["settle-seconds"]);
  const settled = snapshot(expected);
  validateInterval(settled, expected, args["start-position"]);
  return settled;
}
function verifyPositions(expected) {
  const checks = new Map();
  for (const row of args["verify-position"]) {
    control(expected, "position", row);
    sleep(Math.max(5, args["settle-seconds"]));
    const settled = control(expected, "verify-content", "visible");
    validateInterval(settled, expected, row);
    checks.set(row, settled);
  }
  return checks;
}
function traverse(expected) {
  const directions =
      args.traversal === "top-to-bottom"
        ? ["down"]
        : args.traversal === "round-trip"
          ? ["up", "down"]
          : ["up"],
    samples = [],
    legs = [];
  let current = expected;
  for (const direction of directions) {
    let count = 0;
    while (!reachedEdge(current, direction)) {
      check(
        count < 160,
        "Full traversal did not reach the requested boundary after 160 flings",
      );
      const started = now();
      current = snapshot(expected, direction === "up" ? -7000 : 7000);
      validateInterval(current, expected);
      current.commandMs = Math.round((now() - started) * 1000000) / 1000;
      current.direction = direction;
      samples.push(current);
      count++;
      if (!reachedEdge(current, direction)) sleep(args["pause-seconds"]);
    }
    legs.push({
      direction,
      flings: count,
      reachedEdge: true,
      firstVisible: current.viewport.firstVisible,
      lastVisible: current.viewport.lastVisible,
    });
    console.error(`Reached ${direction} boundary after ${count} flings`);
  }
  sleep(args["settle-seconds"]);
  return [samples, legs];
}
let deviceTrace,
  profiling = false,
  restoreReuse,
  target,
  scenario,
  result;
const verificationErrors = [];
try {
  if (args["chat-title"]) {
    let initial = snapshot(undefined, undefined, true, false),
      opened;
    const activityToken = initial.probeToken;
    let deadline = now() + 60;
    while (true) {
      opened = foregroundSnapshot(
        adb(
          "shell",
          "dumpsys",
          "activity",
          component,
          "spool-performance",
          "open-title",
          quote(args["chat-title"]),
          initial.probeToken,
          initial.selectionEpoch,
        ),
        undefined,
        false,
        true,
        false,
      );
      check(
        opened.probeToken === activityToken,
        "The Activity changed while opening the named chat",
      );
      if (opened.controlAccepted === true) break;
      check(
        now() < deadline,
        "The exact chat title was not uniquely available in this focused Activity",
      );
      sleep(1);
      initial = snapshot(undefined, undefined, true, false);
      check(
        initial.probeToken === activityToken,
        "The Activity changed while waiting for the chat list",
      );
    }
    deadline = now() + Math.max(60, args["wait-history-seconds"]);
    let retries = 0;
    while (
      !opened.historyVisible ||
      !opened.turns ||
      opened.historyHasMore ||
      opened.renderPending
    ) {
      check(
        now() < deadline,
        "The selected chat did not finish loading before the history deadline",
      );
      if (opened.historyFailed && retries < 3) {
        opened = control(opened, "history-retry", "retry", true);
        retries++;
      }
      sleep(1);
      opened = snapshot(opened, undefined, true, false);
    }
  }
  let before = snapshot(
      undefined,
      undefined,
      args["allow-hydrating"] || args["wait-history-seconds"] > 0,
    ),
    deadline = now() + args["wait-history-seconds"];
  while (
    (before.historyHasMore || before.renderPending) &&
    !args["allow-hydrating"]
  ) {
    check(
      now() < deadline,
      "The same foreground chat is still hydrating; no gestures were issued",
    );
    sleep(Math.min(5, Math.max(0, deadline - now())));
    before = snapshot(before, undefined, true);
  }
  validateTarget(before, args["min-parts"], args["expect-renderer"]);
  target = before;
  if (args["expand-tools"]) {
    scenario = control(before, "expand-tools", "standard").scenario;
    if (args["start-position"] == null && !args.traversal)
      args["start-position"] = scenario.startPosition;
    sleep(args["settle-seconds"]);
    before = snapshot(before);
  }
  if (args["activity-reuse"] != null) {
    const enabled = before.viewport?.activityRowReuse;
    check(
      typeof enabled === "boolean",
      "Use a debug APK with activity reuse controls",
    );
    control(before, "activity-reuse", args["activity-reuse"]);
    restoreReuse = enabled ? "on" : "off";
  }
  const verificationBefore = verifyPositions(before);
  if (args.traversal) {
    const edge = args.traversal === "top-to-bottom" ? "up" : "down";
    control(before, "position", edge === "up" ? 0 : before.virtualRows - 1);
    sleep(args["settle-seconds"]);
    const settled = snapshot(before);
    validateInterval(settled, before);
    before = settled;
    check(
      reachedEdge(before, edge),
      "The full traversal did not start at the requested native boundary",
    );
    console.error(
      `Starting ${args.traversal}: ${before.virtualRows} rows, ${before.parts} parts`,
    );
  }
  if (args["start-position"] != null) before = position(before);
  if (args["warmup-rounds"]) {
    flings(before, args["warmup-rounds"]);
    before = position(before);
  }
  if (args["method-sampling-us"]) {
    deviceTrace =
      "/data/local/tmp/spool-scroll-" +
      randomUUID().replaceAll("-", "") +
      ".trace";
    const response = adb(
      "shell",
      "am",
      "profile",
      "start",
      "--sampling",
      args["method-sampling-us"],
      pkg,
      deviceTrace,
    );
    check(!response.includes("Error"), response.trim());
    profiling = true;
  }
  adb("shell", "dumpsys", "gfxinfo", pkg, "reset");
  const started = now(),
    [samples, legs] = args.traversal
      ? traverse(before)
      : [flings(before, Math.max(1, Math.min(60, args.rounds))), []];
  const raw = adb("shell", "dumpsys", "gfxinfo", pkg),
    frames = {};
  for (const match of raw.matchAll(
    /^\s*(Total frames rendered|Janky frames|\d+th percentile|Number Slow UI thread|Number Slow bitmap uploads|Number Slow issue draw commands):\s*(.+)/gm,
  ))
    frames[match[1]] = match[2];
  result = {
    scope:
      "One foreground, focused debug chat; gestures bind to its Activity token and selection epoch; no touch injection or backend writes",
    before,
    after: snapshot(before),
    flings: samples,
    frameStats: frames,
    allowHydrating: !!args["allow-hydrating"],
    direction: args.direction,
    pauseSeconds: args["pause-seconds"],
    methodSamplingUs: args["method-sampling-us"],
    startPosition: args["start-position"] ?? null,
    activityReuse: args["activity-reuse"] ?? null,
    warmupRounds: args["warmup-rounds"],
    settleSeconds: args["settle-seconds"],
    scenario: scenario ?? null,
    traversal: args.traversal ?? null,
    legs,
    elapsedSeconds: Math.round((now() - started) * 1000) / 1000,
    minParts: args["min-parts"],
    expectedRenderer: args["expect-renderer"] ?? null,
    limitations:
      "Memory and gfxinfo counters cover the process, not only the selected Activity. Not finger-to-photon latency or Web/native raster parity.",
  };
  if (args["start-position"] != null) validateInterval(result.after, before);
  if (args.traversal || verificationBefore.size) {
    validateInterval(result.after, before);
    const after = verifyPositions(before);
    result.contentVerification = [];
    for (const [row, initial] of verificationBefore) {
      const current = after.get(row);
      let matched = true;
      try {
        validateVisibleContent(initial, current);
      } catch (error) {
        matched = false;
        verificationErrors.push(error.message);
      }
      result.contentVerification.push({
        position: row,
        matched,
        before: initial.visibleContent,
        after: current.visibleContent,
      });
    }
  }
  if (profiling) {
    adb("shell", "am", "profile", "stop", pkg);
    profiling = false;
    const local = join(tmp, "scroll.trace");
    waitFinishedArtTrace(() => adb("pull", deviceTrace, local), local);
    result.methodTrace = summarize(readFileSync(local));
    delete result.methodTrace.trace;
  }
} finally {
  try {
    if (profiling) adb("shell", "am", "profile", "stop", pkg);
  } finally {
    try {
      if (deviceTrace) adb("shell", "rm", "-f", "--", deviceTrace);
    } finally {
      if (restoreReuse) control(target, "activity-reuse", restoreReuse);
    }
  }
}
writeJson(args.output, result);
console.log(JSON.stringify(result, null, 2));
if (verificationErrors.length) throw new Error(verificationErrors.join("; "));
