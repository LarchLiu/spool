#!/usr/bin/env bun
// Verify native desktop decoding/input in an owned network-free Android package.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { openSync, closeSync, mkdirSync, writeFileSync, readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { join } from "node:path";
import {
  ROOT,
  SDK,
  command,
  cli,
  quote,
  requireRunTmp,
  now,
  sleep,
  writeJson,
} from "./common.mjs";
const { values: args } = cli({
  serial: { type: "string" },
  cases: { type: "string", default: "control,readonly,revoke" },
  "resume-owned": { type: "boolean", default: false },
}, { required: ["serial"] });
assert.match(args.serial, /^[A-Za-z0-9_.:-]+$/, "Invalid adb serial");
const cases = args.cases.split(",");
assert.ok(cases.length && cases.every((name) => ["control", "readonly", "revoke"].includes(name)), "Invalid desktop cases");
const tmp = requireRunTmp(),
  pkg = "com.cloudgeek.spool.android.nativeverify",
  output = join(ROOT, "build/artifacts/android-desktop");
const adbPath = join(SDK, "platform-tools/adb"),
  reports = {};
const run = (values, options) =>
    command(adbPath, ["-s", args.serial, ...values], options),
  shell = (...values) => run(["shell", ...values]).stdout.trim();
async function startCase(values) {
  let failure;
  for (let attempt = 0; attempt < 20; attempt++) {
    // Recreate only the owned task; keep its process alive between fixture scenes.
    try { run(["shell", "am", "start", "-f", "0x10008000", ...values], { timeout: 10000 }); return; }
    catch (error) { failure = error; await Bun.sleep(400); }
  }
  throw failure;
}
function capture(file) {
  assert.ok(
    shell("dumpsys", "window").match(/mCurrentFocus=([^\r\n]*)/)?.[1].includes(pkg),
    "Synthetic viewer is not foreground; refusing screenshot",
  );
  writeFileSync(join(output, file), run(["exec-out", "screencap", "-p"], { text: false }).stdout);
}
function read(name, predicate, step = "desktop") {
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
  writeJson(join(output, "failure.json"), { case: name, step, latest });
  try { capture("device-failure.png"); } catch {}
  throw new Error(
    `Desktop probe timed out (${name}/${step}): ${JSON.stringify(latest)}`,
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
  installed = false,
  failure;
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
  const present = Boolean(shell("pm", "list", "packages", pkg));
  assert.ok(!present || args["resume-owned"], "nativeverify is already installed; refusing to overwrite an unowned package");
  assert.ok(!args["resume-owned"] || present, "No owned verification package to resume");
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
  if (args["resume-owned"]) {
    const installedPath = shell("pm", "path", pkg);
    assert.match(installedPath, /^package:\/data\/app\/[^\r\n]+\/base\.apk$/, "Unexpected verification APK path");
    const installedHash = shell("sha256sum", quote(installedPath.slice("package:".length))).split(/\s/)[0];
    const expectedHash = createHash("sha256").update(readFileSync(apk)).digest("hex");
    assert.equal(installedHash, expectedHash, "Owned verification package differs from this checkout's APK");
    try { Object.assign(reports, JSON.parse(readFileSync(join(output, "device.json"), "utf8"))); } catch {}
  } else {
    writeJson(join(output, "device.json"), reports);
    assert.ok(
      run(["install", "--no-incremental", apk]).stdout.includes("Success"),
      "Test package installation not confirmed",
    );
  }
  installed = true;
  for (const name of cases) {
    await startCase([
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
    ]);
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
    if (name !== "revoke") {
      shell("input", "tap", report.folderButtonX, report.folderButtonY);
      const folder = read(name, (value) => value.sharedFolderOpen && value.sharedFolderEntries === 2 && value.fileRowX > 0, "shared folder open");
      assert.equal(folder.fileUploadVisible, name !== "readonly", "Read-only shared folders must suppress uploads");
      assert.ok(folder.showing && !folder.wireClosed, "Opening shared folders disconnected the desktop");
      if (name === "control") capture("device-shared-folder.png");
      shell("input", "tap", folder.fileRowX, folder.fileRowY);
      const child = read(name, (value) => value.sharedFolderPath === "Documents" && value.sharedFolderEntries === 1, "enter directory");
      shell("input", "tap", child.fileUpX, child.fileUpY);
      const parent = read(name, (value) => value.sharedFolderPath === "" && value.sharedFolderEntries === 2, "parent directory");
      shell("input", "tap", parent.fileRefreshX, parent.fileRefreshY);
      const refreshed = read(name, (value) => value.listedFolders > parent.listedFolders, "refresh directory");
      console.log("PASS:", name, "shared-folder browse/refresh");
      if (name === "control") {
        shell("input", "tap", refreshed.fileUploadX, refreshed.fileUploadY);
        report.documentPicker = read(name, (value) => value.documentPickerWaiting && !value.videoReady, "document picker open");
        assert.ok(report.documentPicker.showing && !report.documentPicker.wireClosed, "Document picker disconnected the desktop");
        shell("input", "keyevent", "KEYCODE_BACK");
        report.documentPickerReturn = read(name, (value) => !value.documentPickerWaiting && value.videoReady && value.pixelsMatch && value.windowFocused, "document picker return");
        assert.ok(report.documentPickerReturn.showing && !report.documentPickerReturn.wireClosed, "Returning from document picker closed the desktop");
        // Input dispatch can remain on the closing DocumentsUI window during its animation.
        await Bun.sleep(350);
      }
      const closePosition = report.documentPickerReturn || refreshed;
      shell("input", "tap", closePosition.fileCloseX, closePosition.fileCloseY);
      report.sharedFolder = read(name, (value) => !value.sharedFolderOpen && value.showing && !value.wireClosed, "shared folder close");
    }
    if (name === "control") {
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
} catch (error) {
  failure = error;
  throw error;
} finally {
  let cleanupFailed = false;
  if (installed) {
    try {
      const result = run(["uninstall", pkg], { check: false });
      if (result.status !== 0 || !result.stdout.includes("Success")) {
        // Wireless adb can drop the uninstall reply before PackageManager finishes.
        cleanupFailed = true;
        for (let attempt = 0; attempt < 10; attempt++) {
          const remaining = run(["shell", "pm", "list", "packages", pkg], { check: false, timeout: 2000 });
          if (remaining.status === 0 && !remaining.stdout.trim()) { cleanupFailed = false; break; }
          await Bun.sleep(200);
        }
      }
    } catch {
      cleanupFailed = true;
    }
  }
  if (server) {
    server.kill("SIGTERM");
    const timer = setTimeout(() => server.kill("SIGKILL"), 10000);
    await server.exited;
    clearTimeout(timer);
  }
  if (log !== undefined) closeSync(log);
  if (cleanupFailed) {
    const message = "Owned nativeverify package cleanup failed";
    if (failure) console.error(message);
    else throw new Error(message);
  }
}
