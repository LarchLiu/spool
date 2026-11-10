#!/usr/bin/env bun
// Exercise an owned, network-free nativeVerify package, never the user's app.
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
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
  output = join(ROOT, "build/artifacts/android-native/device-ui"),
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
const focus = () => {
  if (!shell("dumpsys window | grep mCurrentFocus").includes(pkg))
    throw new Error(
      "Verification app is not foreground; no screenshot or input was performed",
    );
};
function report(predicate = () => true, file = "probe.json", timeout = 8) {
  const deadline = now() + timeout;
  while (now() < deadline) {
    let data;
    try {
      data = JSON.parse(
        device(["exec-out", "run-as", pkg, "cat", "files/" + file], {
          check: false,
        }).stdout,
      );
    } catch {
      sleep(0.15);
      continue;
    }
    if (predicate(data)) return data;
    sleep(0.15);
  }
  throw new Error("Synthetic layout report did not reach the expected state");
}
const port = process.env.ANDROID_ADB_SERVER_PORT || "5037";
if (
  command("lsof", ["-a", "-c", "adb", "-iTCP:" + port, "-sTCP:LISTEN", "-t"], {
    check: false,
  }).status
)
  throw new Error(
    "Run probe-devices.sh first; this helper only reuses an existing adb server",
  );
if (device(["get-state"]).stdout.trim() !== "device")
  throw new Error("Selected device is not ready");
if (shell("pm", "list", "packages", pkg))
  throw new Error(
    "nativeverify is already installed; refusing to overwrite or clean an unowned installation",
  );
if (!existsSync(apk)) throw new Error("Build :app:assembleNativeVerify first");
const badging = command(join(SDK, "build-tools/36.1.0/aapt2"), [
  "dump",
  "badging",
  apk,
]).stdout;
if (
  !badging.includes("name='" + pkg + "'") ||
  /^uses-permission/m.test(badging)
)
  throw new Error(
    "Verification APK identity/network-permission isolation failed",
  );
mkdirSync(output, { recursive: true });
const summary = {
  package: pkg,
  api: shell("getprop", "ro.build.version.sdk"),
  model: shell("getprop", "ro.product.model"),
  networkPermissions: [],
  scenes: [],
  removed: false,
};
let installed = false;
try {
  if (!device(["install", "--no-incremental", apk]).stdout.includes("Success"))
    throw new Error("Verification APK installation was not confirmed");
  installed = true;
  const scenes = [];
  for (const dark of [false, true]) {
    for (const locale of ["zh-CN", "en", "ja"])
      scenes.push(["login", dark, locale]);
    scenes.push(["workspace", dark, "en"]);
    for (const [locale, name] of [
      ["en", "tight-list"],
      ["en", "table-wrapped"],
      ["zh-CN", "chinese-blocks"],
      ["ja", "japanese-blocks"],
    ])
      scenes.push(["markdown", dark, locale, name]);
    scenes.push(["messages", dark, "en"], ["settings", dark, "zh-CN"]);
    for (const locale of ["zh-CN", "en", "ja"])
      scenes.push(["language", dark, locale]);
    scenes.push(["source", dark, "en"]);
  }
  function start(screen, dark, locale, name, ime = false) {
    shell("am", "force-stop", pkg);
    shell("run-as", pkg, "rm", "-f", "files/probe.json");
    const values = [
      "am",
      "start",
      "-W",
      "-n",
      pkg + "/com.cloudgeek.spool.android.NativeVisualProbeActivity",
      "--es",
      "screen",
      screen,
      "--es",
      "locale",
      locale,
      "--ez",
      "dark",
      String(dark),
    ];
    if (name) values.push("--es", "case", name);
    if (ime) values.push("--ez", "imeProbe", "true");
    shell(...values);
    const data = report(
      (value) =>
        value.screen === screen &&
        value.dark === dark &&
        value.locale === locale &&
        (!name || name in value.boxes) &&
        (!["settings", "language"].includes(screen) || value.settingsShowing) &&
        (screen !== "language" || value.languageMenuShowing) &&
        (screen !== "source" ||
          value.syntaxColorSpans > 0 ||
          value.syntaxError),
    );
    if (data.syntaxError) throw new Error(data.syntaxError);
    if (
      screen === "language" &&
      (data.languageOptions?.length !== 4 ||
        JSON.stringify(data.languageOptions.slice(1)) !==
          JSON.stringify(["简体中文", "English", "日本語"]))
    )
      throw new Error(
        "Physical-device language menu differs from the three-locale requirement",
      );
    focus();
    return data;
  }
  for (const [screen, dark, locale, name] of scenes) {
    const data = start(screen, dark, locale, name),
      filename = [screen, name, dark ? "dark" : "light", locale]
        .filter(Boolean)
        .join("-");
    const image = device(["exec-out", "screencap", "-p"], {
      text: false,
    }).stdout;
    if (
      !image
        .subarray(0, 8)
        .equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))
    )
      throw new Error("Device screenshot was not a PNG");
    writeFileSync(join(output, filename + ".png"), image);
    writeJson(join(output, filename + ".json"), data);
    summary.scenes.push(filename);
    console.log("Captured isolated native scene:", filename);
  }
  start("workspace", false, "en", undefined, true);
  const ime = report(() => true, "ime.json", 25);
  if (ime.error) throw new Error(ime.error);
  const { before, visible, grown, hidden } = ime;
  if (!visible.imeVisible || visible.paddingBottom <= before.paddingBottom)
    throw new Error("IME did not resize the shared window shell");
  const composer = visible.boxes.composer;
  if (
    composer.y + composer.height >
    visible.root.y + visible.root.height - visible.paddingBottom + 1
  )
    throw new Error("Composer overlaps the physical-device IME");
  if (
    !grown.imeVisible ||
    grown.boxes.input.height <= before.boxes.input.height ||
    hidden.imeVisible
  )
    throw new Error(
      "Native multiline growth or IME hide did not match the expected state",
    );
  writeJson(join(output, "ime-layout.json"), {
    before,
    visible,
    grown,
    hidden,
  });
  summary.ime =
    "app-owned focus/show, synthetic multiline growth, hide; composer stays above keyboard; no injected events or real gesture/composition claim";
} catch (error) {
  summary.error = error.message;
  throw error;
} finally {
  if (installed) {
    device(["shell", "am", "force-stop", pkg], { check: false });
    const result = device(["uninstall", pkg], { check: false });
    summary.removed = result.status === 0 && result.stdout.includes("Success");
  }
  writeJson(join(output, "summary.json"), summary);
}
if (!summary.removed)
  throw new Error("Owned verification package cleanup requires attention");
console.log(
  "Verified",
  summary.scenes.length,
  "isolated physical-device scenes and IME layout; owned package removed.",
);
