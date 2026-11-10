#!/usr/bin/env bun
// Launch the real Activity in an owned, shrunk, non-debuggable synthetic APK.
import { createHash, randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  ROOT,
  SDK,
  command,
  serialArgs,
  requireRunTmp,
  now,
  sleep,
  writeJson,
} from "./common.mjs";
const args = serialArgs();
requireRunTmp();
const apk = join(
    ROOT,
    "build/android/gradle/app/outputs/apk/releaseVerify/app-releaseVerify.apk",
  ),
  output = join(
    ROOT,
    "build/artifacts/android-markwon-release/device-smoke.json",
  );
const pkg = "com.cloudgeek.spool.android.releaseverify",
  component = pkg + "/com.cloudgeek.spool.android.MainActivity";
const adb = (values, options) =>
  command(join(SDK, "platform-tools/adb"), ["-s", args.serial, ...values], {
    timeout: 45000,
    ...options,
  });
if (adb(["shell", "pm", "list", "packages", pkg]).stdout.trim())
  throw new Error(
    "releaseverify is already installed; refusing to replace or remove an unowned installation",
  );
const badging = command(join(SDK, "build-tools/36.1.0/aapt2"), [
  "dump",
  "badging",
  apk,
]).stdout;
if (
  !badging.includes("name='" + pkg + "'") ||
  badging.includes("application-debuggable")
)
  throw new Error(
    "Verification APK must have its isolated package ID and be non-debuggable",
  );
if (badging.includes("android.permission.INTERNET"))
  throw new Error("Verification APK must have no Internet permission");
const token = randomUUID(),
  summary = {
    package: pkg,
    apkSha256: createHash("sha256").update(readFileSync(apk)).digest("hex"),
    api: adb(["shell", "getprop", "ro.build.version.sdk"]).stdout.trim(),
    model: adb(["shell", "getprop", "ro.product.model"]).stdout.trim(),
    internetPermission: false,
    removed: false,
  };
if (Number(summary.api) < 29)
  throw new Error(
    "The committed-frame device smoke requires Android API29 or later",
  );
let installed = false;
try {
  if (!adb(["install", "--no-incremental", apk]).stdout.includes("Success"))
    throw new Error("Verification installation was not confirmed");
  installed = true;
  adb([
    "shell",
    "am",
    "start",
    "-W",
    "-n",
    component,
    "--ez",
    "spool_markwon",
    "false",
    "--es",
    "release_probe_token",
    token,
  ]);
  const deadline = now() + 35;
  while (!summary.startup) {
    const pid = adb(["shell", "pidof", pkg], { check: false }).stdout.trim();
    if (/^\d+$/.test(pid))
      for (const line of adb([
        "logcat",
        "--pid=" + pid,
        "-d",
        "-v",
        "raw",
        "-s",
        "SpoolReleaseVerify:I",
      ]).stdout.split(/\r?\n/)) {
        let value;
        try {
          value = JSON.parse(line);
        } catch {
          continue;
        }
        if (value.token === token) {
          delete value.token;
          summary.startup = value;
        }
      }
    if (!summary.startup) {
      if (now() >= deadline)
        throw new Error("Release startup/rendering did not complete");
      sleep(0.4);
    }
  }
  const result = summary.startup;
  if (result.error) throw new Error(result.error);
  const expected = [
    "defaultMarkwon",
    "legacyPreferenceIgnored",
    "legacyIntentIgnored",
    "markdownRendered",
    "codeBlockRendered",
    "tableRendered",
    "tableCellsSelectable",
    "codeSyntaxHighlighted",
    "fileSourceSyntaxHighlighted",
    "frameCommitted",
  ];
  if (
    result.debuggable !== false ||
    expected.some((key) => result[key] !== true)
  )
    throw new Error("Release renderer assertions failed");
  if (
    adb([
      "shell",
      "dumpsys",
      "activity",
      component,
      "spool-performance",
    ]).stdout.includes("SPOOL_PERF ")
  )
    throw new Error(
      "The debug performance entry point was exposed by a release APK",
    );
  summary.performanceProbeBlocked = true;
  console.log(
    "Non-debuggable release rendered Markdown, tables and highlighted code, committed a frame, and blocked debug probes.",
  );
} catch (error) {
  summary.error = error.message;
  throw error;
} finally {
  if (installed) {
    adb(["shell", "am", "force-stop", pkg], { check: false });
    const removed = adb(["uninstall", pkg], { check: false });
    summary.removed =
      removed.status === 0 && removed.stdout.includes("Success");
  }
  writeJson(output, summary);
}
if (!summary.removed)
  throw new Error("Owned verification package cleanup requires attention");
console.log("Owned verification package removed; report:", output);
