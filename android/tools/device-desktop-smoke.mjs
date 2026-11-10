#!/usr/bin/env bun
// Verify native desktop decoding/input in an owned network-free Android package.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { openSync, closeSync, mkdirSync, writeFileSync } from "node:fs";
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
const args = serialArgs(),
  tmp = requireRunTmp(),
  pkg = "com.cloudgeek.spool.android.nativeverify",
  output = join(ROOT, "build/artifacts/android-desktop");
const adbPath = join(SDK, "platform-tools/adb"),
  reports = {};
const run = (values, options) =>
    command(adbPath, ["-s", args.serial, ...values], options),
  shell = (...values) => run(["shell", ...values]).stdout.trim();
function read(name, predicate) {
  const deadline = now() + 25;
  let latest;
  while (now() < deadline) {
    try {
      latest = JSON.parse(
        run(["exec-out", "run-as", pkg, "cat", "files/desktop-probe.json"], {
          check: false,
        }).stdout,
      );
    } catch {
      sleep(0.15);
      continue;
    }
    if (latest.case === name) {
      if (latest.error) throw new Error(latest.error);
      if (predicate(latest)) return latest;
    }
    sleep(0.15);
  }
  throw new Error(
    `Desktop probe timed out (${name}): ${JSON.stringify(latest)}`,
  );
}
function listening() {
  return (
    command(
      process.execPath,
      [
        "-e",
        'const socket = await Bun.connect({hostname:"127.0.0.1",port:5037,socket:{data(){},error(){},open(socket){socket.end();}}}); socket.end();',
      ],
      { check: false, timeout: 2000 },
    ).status === 0
  );
}
let server,
  log,
  installed = false;
try {
  if (!listening()) {
    log = openSync(join(tmp, "adb-server.log"), "w");
    server = spawn(
      adbPath,
      ["-L", "tcp:localhost:5037", "server", "nodaemon"],
      { stdio: ["ignore", log, log] },
    );
    const exited = new Promise((resolve) => {
      server.once("error", resolve);
      server.once("exit", resolve);
    });
    server.exited = exited;
    for (let i = 0; !listening(); i++) {
      if (i >= 50) throw new Error("ADB server did not become ready");
      await Bun.sleep(100);
      if (server.exitCode !== null)
        throw new Error("Could not start owned ADB server");
    }
  }
  assert.ok(
    !shell("pm", "list", "packages", pkg),
    "nativeverify is already installed; refusing to overwrite an unowned package",
  );
  const apk = join(
      ROOT,
      "build/android/gradle/app/outputs/apk/nativeVerify/app-nativeVerify.apk",
    ),
    badging = command(join(SDK, "build-tools/36.1.0/aapt2"), [
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
    "Test package identity or network isolation failed",
  );
  mkdirSync(output, { recursive: true });
  assert.ok(
    run(["install", "--no-incremental", apk]).stdout.includes("Success"),
    "Test package installation not confirmed",
  );
  installed = true;
  for (const name of ["control", "readonly", "revoke"]) {
    shell(
      "am",
      "start",
      "-S",
      "-n",
      pkg + "/com.cloudgeek.spool.android.NativeDesktopProbeActivity",
      "--es",
      "case",
      name,
      "--ez",
      "readOnly",
      String(name === "readonly"),
      "--ez",
      "revoke",
      String(name === "revoke"),
    );
    const report = read(
      name,
      (value) =>
        value.acknowledged === 8 &&
        value.exercised &&
        value.pixelsMatch &&
        value.zoom === 1 &&
        (name !== "control" ||
          (value.fullscreen &&
            value.fullscreenHeight > value.windowedHeight &&
            value.systemBarsHidden)),
    );
    assert.ok(
      report.iconButtons === 17 && report.textButtons === 0,
      "Desktop controls must use labelled icon buttons",
    );
    const commands = report.commands;
    assert.deepEqual(
      commands.filter((value) => value.startsWith("a,")),
      Array.from({ length: 8 }, (_, i) => "a," + (i + 1)),
      "Frame acknowledgements were incomplete or out of order",
    );
    const inputs = commands.filter((value) => /^(m,|b,|k,)/.test(value));
    if (name === "readonly")
      assert.ok(
        !inputs.length && !report.controllable,
        "Read-only desktop sent remote input",
      );
    else
      assert.ok(
        ["m,320,180", "b,1,1", "b,1,0", "k,16797229,1", "k,16797229,0"].every(
          (value) => commands.includes(value),
        ),
        "Touch/Chinese IME commands did not match voxl",
      );
    assert.ok(
      !commands.some((value) => value.startsWith("sz,")),
      "Local zoom resized the host desktop",
    );
    if (name === "control") {
      const capture = (file) => {
        assert.ok(
          shell("dumpsys", "window")
            .match(/mCurrentFocus=([^\r\n]*)/)?.[1]
            .includes(pkg),
          "Synthetic viewer is not foreground; refusing screenshot",
        );
        writeFileSync(
          join(output, file),
          run(["exec-out", "screencap", "-p"], { text: false }).stdout,
        );
      };
      capture("device-fullscreen.png");
      shell("input", "tap", report.toolbarToggleX, report.toolbarToggleY);
      report.toolbarCollapsedState = read(
        name,
        (value) =>
          value.toolbarCollapsed &&
          value.toolbarWidth < value.width / 4 &&
          value.fullscreen &&
          value.showing &&
          !value.wireClosed,
      );
      capture("device-toolbar-collapsed.png");
      const collapsed = report.toolbarCollapsedState;
      shell("input", "tap", collapsed.toolbarToggleX, collapsed.toolbarToggleY);
      report.toolbarExpandedState = read(
        name,
        (value) =>
          !value.toolbarCollapsed &&
          value.toolbarWidth > value.width / 2 &&
          value.fullscreen &&
          value.showing &&
          !value.wireClosed,
      );
      shell("input", "tap", report.fullscreenButtonX, report.fullscreenButtonY);
      report.fullscreenExit = read(
        name,
        (value) =>
          !value.fullscreen &&
          value.showing &&
          !value.wireClosed &&
          value.height < value.fullscreenHeight &&
          !value.systemBarsHidden,
      );
      capture("device.png");
      shell(
        "am",
        "start",
        "-n",
        pkg + "/com.cloudgeek.spool.android.NativeVisualProbeActivity",
      );
      report.backgroundCleanup = read(
        name,
        (value) => value.wireClosed && value.disconnected,
      );
    }
    if (name === "revoke")
      report.revocation = read(
        name,
        (value) =>
          value.wireClosed && value.disconnected && !value.controllable,
      );
    reports[name] = report;
    writeJson(join(output, "device.json"), reports);
    console.log("PASS:", name);
  }
} finally {
  let cleanupFailed = false;
  if (installed) {
    const result = run(["uninstall", pkg], { check: false });
    cleanupFailed = result.status !== 0 || !result.stdout.includes("Success");
  }
  if (server) {
    server.kill("SIGTERM");
    const timer = setTimeout(() => server.kill("SIGKILL"), 10000);
    await server.exited;
    clearTimeout(timer);
  }
  if (log !== undefined) closeSync(log);
  if (cleanupFailed)
    throw new Error("Owned nativeverify package cleanup failed");
}
