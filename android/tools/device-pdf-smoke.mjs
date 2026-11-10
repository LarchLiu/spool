#!/usr/bin/env bun
// Test the actual PDF renderer in an owned, network-free nativeVerify install.
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
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
const pkg = "com.cloudgeek.spool.android.nativeverify",
  output = join(ROOT, "build/artifacts/android-pdf"),
  apk = join(
    ROOT,
    "build/android/gradle/app/outputs/apk/nativeVerify/app-nativeVerify.apk",
  );
const device = (values, options) =>
  command(
    join(SDK, "platform-tools/adb"),
    ["-s", args.serial, ...values],
    options,
  );
const shell = (...values) => device(["shell", ...values]).stdout.trim();
mkdirSync(output, { recursive: true });
function report(predicate) {
  const deadline = now() + 12;
  let last;
  while (now() < deadline) {
    try {
      last = JSON.parse(
        device(["exec-out", "run-as", pkg, "cat", "files/probe.json"], {
          check: false,
        }).stdout,
      );
    } catch {
      sleep(0.15);
      continue;
    }
    if (predicate(last)) return last;
    sleep(0.15);
  }
  throw new Error("PDF state did not settle: " + JSON.stringify(last));
}
const focus = () =>
  assert.ok(
    shell("dumpsys window | grep mCurrentFocus").includes(pkg),
    "Verification app is not foreground; refusing input",
  );
function tap(snapshot, name) {
  focus();
  const box = snapshot.boxes[name];
  shell(
    "input",
    "tap",
    Math.trunc(box.x + box.width / 2),
    Math.trunc(box.y + box.height / 2),
  );
}
function swipe(snapshot) {
  focus();
  const box = snapshot.boxes.pdfViewport,
    x = Math.trunc(box.x + box.width / 2);
  shell(
    "input",
    "swipe",
    x,
    Math.trunc(box.y + box.height * 0.85),
    x,
    Math.trunc(box.y + box.height * 0.2),
    700,
  );
}
assert.ok(
  !shell("pm", "list", "packages", pkg),
  "nativeverify is already installed; refusing to overwrite an unowned installation",
);
const badging = command(join(SDK, "build-tools/36.1.0/aapt2"), [
  "dump",
  "badging",
  apk,
]).stdout;
const permissions = [
  ...badging.matchAll(/^uses-permission: name='([^']+)'/gm),
].map((match) => match[1]);
assert.ok(
  badging.includes(`name='${pkg}'`) &&
    permissions.every(
      (name) => name === pkg + ".DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION",
    ),
  "Verification APK identity or network isolation failed",
);
let installed = false,
  summary = {};
try {
  assert.ok(
    device(["install", "--no-incremental", apk]).stdout.includes("Success"),
    "Verification APK installation failed",
  );
  installed = true;
  const component =
    pkg + "/com.cloudgeek.spool.android.NativeVisualProbeActivity";
  shell(
    "am",
    "start",
    "-W",
    "-n",
    component,
    "--es",
    "screen",
    "pdf",
    "--es",
    "locale",
    "zh-CN",
  );
  let first = report((data) => data.pdfReady && data.pdfPosition === "1 / 2");
  assert.ok(first.pdfPageWidth === 320 && first.pdfPageHeight === 480);
  const capture = (name) => {
    focus();
    writeFileSync(
      join(output, name),
      device(["exec-out", "screencap", "-p"], { text: false }).stdout,
    );
  };
  capture("page-1.png");
  let current = first;
  for (let i = 0; i < 3; i++) {
    swipe(current);
    current = report((data) => data.pdfScroll > 0);
    if (current.pdfPosition === "2 / 2") break;
  }
  let second = report((data) => data.pdfReady && data.pdfPosition === "2 / 2");
  assert.ok(
    second.pdfPageWidth === 480 &&
      second.pdfPageHeight === 320 &&
      second.pdfCachedPages > 0 &&
      second.pdfCachedPages <= 3,
  );
  capture("page-2.png");
  tap(second, "pdfPrevious");
  first = report((data) => data.pdfReady && data.pdfPosition === "1 / 2");
  tap(first, "pdfNext");
  second = report((data) => data.pdfReady && data.pdfPosition === "2 / 2");
  tap(second, "pdfZoomIn");
  let zoomed = report((data) => data.pdfReady && data.pdfZoom > 1);
  for (let i = 0; i < 6 && zoomed.pdfZoom < 4; i++) {
    const zoom = zoomed.pdfZoom;
    tap(zoomed, "pdfZoomIn");
    zoomed = report((data) => data.pdfReady && data.pdfZoom > zoom);
  }
  assert.ok(
    zoomed.pdfZoom === 4 &&
      zoomed.pdfBitmapPixels > 0 &&
      zoomed.pdfBitmapPixels <= 4000000,
  );
  tap(zoomed, "pdfFit");
  const fitted = report((data) => data.pdfReady && data.pdfZoom === 1);
  tap(fitted, "pdfPrevious");
  const back = report((data) => data.pdfReady && data.pdfPosition === "1 / 2");
  tap(back, "pdfClose");
  report((data) => data.pdfOpen === false);
  assert.ok(!shell("run-as", pkg, "ls", "cache").includes("spool-pdf-"));
  shell("am", "force-stop", pkg);
  shell("run-as", pkg, "rm", "-f", "files/probe.json");
  shell(
    "am",
    "start",
    "-W",
    "-n",
    component,
    "--es",
    "screen",
    "pdf",
    "--es",
    "locale",
    "zh-CN",
    "--ez",
    "invalidPdf",
    "true",
  );
  report((data) => (data.pdfStatus || "").includes("无法预览"));
  summary = {
    nativePdfRendering: true,
    pages: 2,
    portraitAndLandscape: true,
    navigation: true,
    continuousScrolling: true,
    zoomAndFit: true,
    boundedPageBitmap: true,
    closeReleasesDocument: true,
    invalidPdfFallback: true,
    api: first.api,
    model: shell("getprop", "ro.product.model"),
    removed: false,
  };
} finally {
  if (installed) {
    device(["shell", "am", "force-stop", pkg], { check: false });
    const result = device(["uninstall", pkg], { check: false });
    summary.removed = result.status === 0 && result.stdout.includes("Success");
  }
  if (Object.keys(summary).length)
    writeJson(join(output, "device-report.json"), summary);
  if (installed && !summary.removed)
    throw new Error("Owned verification app could not be removed");
}
console.log(JSON.stringify(summary));
