// sh tools/run.sh desktop-resize bun tools/desktop-resize/run.mjs [--content direct|native] [--automatic]
import { execFileSync, spawn } from "node:child_process";
import assert from "node:assert/strict";
import { mkdir, open, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { requireRunTemp } from "../test-scratch.mjs";
import { startFrontendFixture } from "./frontend.mjs";

requireRunTemp();
if (process.platform !== "darwin") throw new Error("This diagnostic requires macOS");
const app = join(process.env.SPOOL_RUN_ROOT, "Spool Resize Diagnostic.app");
const binary = join(app, "Contents/MacOS/Spool Resize Diagnostic");
const artifacts = resolve("build/artifacts/desktop-resize");
const args = process.argv.slice(2);
const spoolUI = args.includes("--spool-ui");
if (spoolUI) args.splice(args.indexOf("--spool-ui"), 1);
await mkdir(join(app, "Contents/MacOS"), { recursive: true });
await mkdir(artifacts, { recursive: true });
await writeFile(join(app, "Contents/Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.cloudgeek.spool.resize-diagnostic</string>
<key>CFBundleExecutable</key><string>Spool Resize Diagnostic</string><key>CFBundleName</key><string>Spool Resize Diagnostic</string>
<key>CFBundlePackageType</key><string>APPL</string><key>NSHighResolutionCapable</key><true/></dict></plist>`);
execFileSync("go", ["build", "-o", binary, "./tools/desktop-resize"], { stdio: "inherit" });
const pollIndex = args.indexOf("--cookie-poll");
const recording = pollIndex < 0 ? "events.jsonl" : `events-cookies-${args[pollIndex + 1]}.jsonl`;
const eventPath = join(artifacts, recording);
const output = await open(eventPath, "w", 0o600);
let frontend;
try {
  if (spoolUI) {
    frontend = startFrontendFixture();
    args.push("--url", frontend.url);
  }
  console.log("Isolated diagnostic: " + app);
  console.log("Resize events: " + eventPath);
  const child = spawn(binary, args, { stdio: ["ignore", output.fd, "inherit"] });
  process.exitCode = await new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", code => resolve(code ?? 1));
  });
} finally {
  await frontend?.close();
  await output.close();
}
if (process.exitCode === 0 && args.includes("--automatic")) {
  const records = (await readFile(eventPath, "utf8")).trim().split("\n").map(line => JSON.parse(line));
  const sizes = records.filter(record => record.event === "automatic");
  assert.equal(sizes.length, 120, "all requested sizes must be checked");
  for (const record of sizes) {
    assert.equal(record.bounds.Width, record.target.Width, "native window width follows the requested width");
    assert.equal(record.bounds.Height, record.target.Height, "native window height follows the requested height");
  }
  assert.equal(records.at(-1).event, "quit", "native diagnostics shut down cleanly");
  console.log(`PASS: 120 horizontal/vertical size changes; longest dispatch ${Math.max(...sizes.map(record => record.delay_ms || 0)).toFixed(1)}ms`);
}
