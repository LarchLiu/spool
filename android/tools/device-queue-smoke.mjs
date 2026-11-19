// Verify real touch dispatch with synthetic queues in the network-free verification app.
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT, SDK, command, serialArgs, requireRunTmp, now, sleep, writeJson } from "./common.mjs";

const args = serialArgs();
requireRunTmp();
const pkg = "com.cloudgeek.spool.android.nativeverify";
const output = join(ROOT, "build/artifacts/android-native/device-queue");
const apk = join(ROOT, "build/android/gradle/app/outputs/apk/nativeVerify/app-nativeVerify.apk");
const device = (values, options) => command(join(SDK, "platform-tools/adb"), ["-s", args.serial, ...values], options);
const shell = (...values) => device(["shell", ...values]).stdout.trim();
const check = (value, message) => { if (!value) throw new Error(message); };
function focus(timeout = 0) {
  const deadline = now() + timeout;
  do {
    if (shell("dumpsys", "window").split("\n").some(line => line.includes("mCurrentFocus=") && line.includes(pkg))) return;
    if (now() >= deadline) break;
    sleep(.1);
  } while (true);
  throw new Error("Verification app must be foreground before touch input or capture");
}
function report(predicate = () => true, timeout = 8) {
  const deadline = now() + timeout;
  while (now() < deadline) {
    const raw = device(["exec-out", "run-as", pkg, "cat", "files/probe.json"], { check: false });
    let data;
    try { data = JSON.parse(raw.stdout); } catch {}
    if (data && predicate(data)) return data;
    sleep(.05);
  }
  throw new Error("Queue probe did not reach the expected state");
}
function touch(type, x, y) {
  focus(); shell("input", "motionevent", type, Math.round(x), Math.round(y));
}
function capture(name) {
  focus();
  const image = device(["exec-out", "screencap", "-p"], { text: false }).stdout;
  check(image.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10])), "Screenshot must be PNG");
  writeFileSync(join(output, name + ".png"), image);
}
function contained(data, id) {
  const queue = data.boxes.queue, row = data.queueRows.find(row => row.id === id);
  check(row.elevation > 0, "The actual row must be raised during drag");
  check(row.bounds.y >= queue.y && row.bounds.y + row.bounds.height <= queue.y + queue.height + 1,
    "Dragged row escaped the queue vertically");
  check(row.bounds.x >= queue.x && row.bounds.x + row.bounds.width <= queue.x + queue.width,
    "Dragged row escaped the queue horizontally");
  check(data.queueMoves.length === 0, "Touch movement must preview without submitting");
}
function settled(data) { return data.queueRows.every(row => row.elevation === 0 && Math.abs(row.translationY) < .5); }
function start(dark, count) {
  shell("am", "force-stop", pkg); shell("run-as", pkg, "rm", "-f", "files/probe.json");
  shell("am", "start", "-W", "-n", pkg + "/com.cloudgeek.spool.android.NativeVisualProbeActivity",
    "--es", "screen", "queue", "--es", "locale", "zh-CN", "--ez", "dark", String(dark), "--ei", "queueCount", count);
  focus(3);
  return report(data => data.queueRows?.length === count && data.boxes.queue.width > 0 && settled(data));
}

check(device(["get-state"]).stdout.trim() === "device", "Device is not ready");
check(!shell("pm", "list", "packages", pkg), "Refusing to overwrite a preexisting verification package");
check(existsSync(apk), "Build :app:assembleNativeVerify first");
const badging = command(join(SDK, "build-tools/36.1.0/aapt2"), ["dump", "badging", apk]).stdout;
check(badging.includes("name='" + pkg + "'") && !/^uses-permission: name='android\.permission\.(INTERNET|ACCESS_NETWORK_STATE)'/m.test(badging),
  "Verification APK must have its own identity and no network permissions");
mkdirSync(output, { recursive: true });
const summary = { model: shell("getprop", "ro.product.model"), api: shell("getprop", "ro.build.version.sdk"), scenes: [], removed: false };
let installed = false;
try {
  check(device(["install", "--no-incremental", apk]).stdout.includes("Success"), "Installation was not confirmed");
  installed = true;
  for (const dark of [false, true]) {
    const name = dark ? "dark" : "light";
    let data = start(dark, 3);
    const box = data.boxes.queue, grip = data.queueRows[2].grip;
    const x = grip.x + grip.width / 2, y = grip.y + grip.height / 2;
    touch("DOWN", x, y);
    try {
      touch("MOVE", box.x - 15, box.y - 80);
      data = report(data => data.queueRows[2].translationY < -10 && data.queueRows[0].translationY > 10);
      contained(data, "queued-2"); capture("contained-top-" + name);
      touch("MOVE", box.x + box.width + 15, box.y + box.height + 80);
      data = report(data => Math.abs(data.queueRows[2].translationY) < .5);
      contained(data, "queued-2"); capture("contained-bottom-" + name);
      touch("MOVE", x, box.y - 80);
      data = report(data => data.queueRows[2].translationY < -10);
      contained(data, "queued-2");
    } finally { touch("UP", x, box.y - 80); }
    data = report(data => data.queueMoves.length === 1 && settled(data));
    check(data.queueMoves[0].id === "queued-2" && data.queueMoves[0].before === "queued-0", "Drop submitted the wrong move");
    check(data.queueRows[0].id === "queued-2", "Drop did not immediately show the new order");
    capture("reordered-" + name);
    summary.scenes.push({ name, moves: data.queueMoves, order: data.queueRows.map(row => row.id) });

    data = start(dark, 20);
    const edge = data.boxes.queue, first = data.queueRows[0].grip;
    const dragX = first.x + first.width / 2, dragY = first.y + first.height / 2;
    touch("DOWN", dragX, dragY);
    try {
      touch("MOVE", dragX, edge.y + edge.height + 60);
      data = report(data => data.queueScroll > 0);
      contained(data, "queued-0");
      data = report(data => !data.queueCanScrollDown);
      contained(data, "queued-0"); capture("scrolled-drag-" + name);
    } finally { touch("UP", dragX, edge.y + edge.height + 60); }
    data = report(data => data.queueMoves.length === 1 && settled(data));
    check(data.queueMoves[0].id === "queued-0" && data.queueMoves[0].before === "", "Scrolled drop did not select the last slot");
    check(data.queueRows[19].id === "queued-0", "Scrolled drop did not show the last slot");
    capture("scrolled-reordered-" + name);
    summary.scenes.push({ name: "long-" + name, moves: data.queueMoves, order: data.queueRows.map(row => row.id) });
    console.log("Verified contained queue drag and edge scrolling:", name);
  }
} catch (error) {
  summary.error = error.message; throw error;
} finally {
  if (installed) {
    device(["shell", "am", "force-stop", pkg], { check: false });
    const removed = device(["uninstall", pkg], { check: false });
    summary.removed = removed.status === 0 && removed.stdout.includes("Success");
  }
  writeJson(join(output, "summary.json"), summary);
}
check(summary.removed, "Owned verification package needs cleanup");
console.log("Verified queue touch sorting; verification package removed.");
