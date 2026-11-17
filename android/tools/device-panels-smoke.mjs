// Physical-device verification of synthetic panels, without production app data.
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT, SDK, command, serialArgs, requireRunTmp, now, sleep, writeJson } from "./common.mjs";

const args = serialArgs();
requireRunTmp();
const pkg = "com.cloudgeek.spool.android.nativeverify";
const output = join(ROOT, "build/artifacts/android-native/device-panels");
const apk = join(ROOT, "build/android/gradle/app/outputs/apk/nativeVerify/app-nativeVerify.apk");
const device = (values, options) => command(join(SDK, "platform-tools/adb"), ["-s", args.serial, ...values], options);
const shell = (...values) => device(["shell", ...values]).stdout.trim();
const check = (condition, message) => { if (!condition) throw new Error(message); };
function report(predicate = () => true, timeout = 8) {
  const deadline = now() + timeout;
  while (now() < deadline) {
    try {
      const value = JSON.parse(device(["exec-out", "run-as", pkg, "cat", "files/probe.json"], { check: false }).stdout);
      if (predicate(value)) return value;
    } catch {}
    sleep(.15);
  }
  throw new Error("Panel probe did not reach the expected state");
}
function focus(timeout = 0) {
  const deadline = now() + timeout;
  do {
    if (shell("dumpsys", "window").split("\n").some(line => line.includes("mCurrentFocus=") && line.includes(pkg))) return;
    if (now() >= deadline) break;
    sleep(.1);
  } while (true);
  throw new Error("Verification app must be foreground before input or screenshots");
}
function tap(box) {
  focus(); shell("input", "tap", Math.round(box.x + box.width / 2), Math.round(box.y + box.height / 2));
}
function capture(name) {
  focus();
  const image = device(["exec-out", "screencap", "-p"], { text: false }).stdout;
  check(image.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10])), "Screenshot must be PNG");
  writeFileSync(join(output, name + ".png"), image);
}
check(device(["get-state"]).stdout.trim() === "device", "Device is not ready");
check(!shell("pm", "list", "packages", pkg), "Refusing to overwrite a preexisting verification package");
check(existsSync(apk), "Build :app:assembleNativeVerify first");
const badging = command(join(SDK, "build-tools/36.1.0/aapt2"), ["dump", "badging", apk]).stdout;
check(badging.includes("name='" + pkg + "'") && !/^uses-permission: name='android\.permission\.(INTERNET|ACCESS_NETWORK_STATE)'/m.test(badging), "Verification APK must have its own identity and no network permissions");
mkdirSync(output, { recursive: true });
const summary = { model: shell("getprop", "ro.product.model"), api: shell("getprop", "ro.build.version.sdk"), scenes: [], sidebar: [], removed: false };
let installed = false;
try {
  check(device(["install", "--no-incremental", apk]).stdout.includes("Success"), "Installation was not confirmed");
  installed = true;
  for (const dark of [false, true]) for (const locale of ["zh-CN", "en"]) {
    const name = (dark ? "dark" : "light") + "-" + locale;
    shell("am", "force-stop", pkg); shell("run-as", pkg, "rm", "-f", "files/probe.json");
    shell("am", "start", "-W", "-n", pkg + "/com.cloudgeek.spool.android.NativeVisualProbeActivity", "--es", "screen", "panels", "--es", "locale", locale, "--ez", "dark", String(dark));
    focus(3);
    let data = report(value => value.panelOpen && value.boxes.gitTab?.width > 0);
    tap(data.boxes.projectTab); capture("project-" + name);
    tap(data.boxes.gitTab); data = report(value => value.boxes.historyTab?.width > 0);
    capture("changes-" + name);
    tap(data.boxes.historyTab); capture("history-top-" + name);
    for (let gesture = 0; gesture < 8; gesture++) {
      data = report(); if (data.historyPages > 0 || data.historyPending) break;
      const box = data.boxes.gitScroll; focus();
      shell("input", "swipe", Math.round(box.x + box.width / 2), Math.round(box.y + box.height * .85), Math.round(box.x + box.width / 2), Math.round(box.y + box.height * .2), 1200);
    }
    data = report(value => !!value.pageAfter);
    writeJson(join(output, name + ".json"), data);
    check(data.pageBefore.anchor === data.pageAfter.anchor, "Pagination changed the visible anchor");
    check(data.pageBefore.scrollY === data.pageAfter.scrollY && data.pageBefore.anchorY === data.pageAfter.anchorY, "Pagination shifted the viewport");
    capture("history-" + name);
    tap(data.boxes.closeFiles); data = report(value => !value.panelOpen);
    check(data.titleVisibleCharacters >= (locale === "zh-CN" ? 8 : 18), "Header title remains prematurely truncated");
    capture("header-" + name);
    tap(data.boxes.titleAction); capture("header-focused-" + name);
    tap(data.boxes.openFiles); data = report(value => value.panelOpen);
    tap(data.boxes.closeFiles); data = report(value => !value.panelOpen);
    capture("header-after-close-" + name);
    writeJson(join(output, name + ".json"), data);
    summary.scenes.push({ name, before: data.pageBefore, after: data.pageAfter, titleVisibleCharacters: data.titleVisibleCharacters });
    console.log("Verified physical-device panels:", name);
  }
  for (const dark of [false, true]) {
    const name = dark ? "dark" : "light";
    shell("am", "force-stop", pkg); shell("run-as", pkg, "rm", "-f", "files/probe.json");
    shell("am", "start", "-W", "-n", pkg + "/com.cloudgeek.spool.android.NativeVisualProbeActivity", "--es", "screen", "sidebar", "--es", "locale", "zh-CN", "--ez", "dark", String(dark));
    focus(3);
    let data = report(value => value.boxes.brandWhitespace?.width > 0);
    const blank = data.boxes.brandWhitespace, x = Math.round(blank.x + blank.width * .75), y = Math.round(blank.y + blank.height / 2);
    focus(); shell("input", "motionevent", "DOWN", x, y);
    try {
      data = report(value => value.sidebarBackgroundPressed);
      check(data.projectsPressed.length === 2 && data.projectsPressed.every(value => !value), "Blank header touch highlighted project rows");
      capture("sidebar-blank-pressed-" + name);
    } finally { focus(); shell("input", "motionevent", "UP", x, y); }
    data = report(value => !value.sidebarBackgroundPressed);
    check(data.projectsPressed.every(value => !value), "Project highlight remained after releasing the blank header");
    summary.sidebar.push({ name, projectsPressed: data.projectsPressed });
    console.log("Verified blank sidebar touch:", name);
  }
} catch (error) {
  summary.error = error.message; throw error;
} finally {
  if (installed) {
    device(["shell", "am", "force-stop", pkg], { check: false });
    const removed = device(["uninstall", pkg], { check: false }); summary.removed = removed.status === 0 && removed.stdout.includes("Success");
  }
  writeJson(join(output, "summary.json"), summary);
}
check(summary.removed, "Owned verification package needs cleanup");
console.log("Verified pagination anchors, long titles and panel transitions; verification package removed.");
