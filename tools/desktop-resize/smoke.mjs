// Run the actual desktop's native probes with a disposable bundle and data.
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { requireRunTemp } from "../test-scratch.mjs";

requireRunTemp();
assert.equal(process.platform, "darwin");
const app = join(process.env.SPOOL_RUN_ROOT, "Spool Resize Smoke.app");
const executable = "Spool Resize Smoke";
const binary = join(app, "Contents/MacOS", executable);
const artifacts = resolve("build/artifacts/desktop-resize-smoke");
const report = join(artifacts, "report.json");
await mkdir(join(app, "Contents/MacOS"), { recursive: true });
await mkdir(artifacts, { recursive: true });
await writeFile(join(app, "Contents/Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.cloudgeek.spool.resize-smoke</string>
<key>CFBundleExecutable</key><string>${executable}</string>
<key>CFBundleName</key><string>${executable}</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>`);
execFileSync("go", ["build", ...(process.argv.includes("--race") ? ["-race"] : []), "-ldflags", "-X github.com/egoist/mygo.production=1 -X github.com/egoist/mygo.packageIdentifier=com.cloudgeek.spool.resize-smoke", "-o", binary, "./cmd/desktop"], { stdio: "inherit" });
const child = spawn(binary, ["--release-smoke", report, "--smoke-probe", "notifications"], { stdio: "inherit" });
const code = await new Promise((resolve, reject) => {
  child.once("error", reject);
  child.once("exit", code => resolve(code));
});
assert.equal(code, 0, "actual desktop native probes must pass");
const result = JSON.parse(await readFile(report, "utf8"));
assert.equal(result.native, true);
assert.equal(result.scope, "notifications");
assert.equal(result.notification_ui_responsive, true);
assert.equal(result.notification_event_driven, true);
console.log("PASS: actual desktop native probes, notification dispatch and shutdown");
