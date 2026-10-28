import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";
import { releaseVersion } from "./prepare-release.mjs";

export function verifySmokeReport(result, version, target) {
  assert.deepEqual(result, { version, target, native: true, web: true });
}

export function extractSmokeArchive(directory, filename, destination, run = execFileSync) {
  // GNU tar treats a Windows drive letter in the archive path as a remote host.
  run("tar", ["-xzf", filename, "-C", resolve(destination)], { cwd: resolve(directory) });
}

export async function smokeRelease(directory, target, env = process.env) {
  const native = `${process.platform === "win32" ? "windows" : process.platform}-${process.arch === "x64" ? "amd64" : process.arch}`;
  assert.equal(target, native, "smoke tests must run on the target OS and architecture");
  const version = releaseVersion(env.RELEASE_TAG);
  const results = resolve("smoke-results");
  await mkdir(results, { recursive: true });
  const app = await mkdtemp(join(tmpdir(), "spool-smoke-package-"));
  const report = join(results, "report.json");
  await rm(report, { force: true });
  let output = "";
  let timer;
  try {
    extractSmokeArchive(directory, `spool-${version}-${target}.tar.gz`, app);
    const executable = join(app, target.startsWith("darwin-") ? "Spool.app/Contents/MacOS/Spool" : target.startsWith("windows-") ? "Spool.exe" : "spool");
    if (!target.startsWith("windows-")) await chmod(executable, 0o755);
    if (target.startsWith("darwin-")) execFileSync("codesign", ["--verify", "--deep", "--strict", join(app, "Spool.app")]);
    const child = spawn(executable, ["--release-smoke", report], { env, windowsHide: false, stdio: ["ignore", "pipe", "pipe"] });
    child.stdout.on("data", (data) => { output = (output + data).slice(-256 * 1024); });
    child.stderr.on("data", (data) => { output = (output + data).slice(-256 * 1024); });
    let timedOut = false;
    timer = setTimeout(() => { timedOut = true; child.kill("SIGKILL"); }, 90_000);
    const code = await new Promise((done, reject) => {
      child.once("error", reject);
      child.once("close", done);
    });
    assert.ok(!timedOut, "packaged app timed out");
    assert.equal(code, 0, `packaged app failed: ${output}`);
    const result = JSON.parse(await readFile(report, "utf8"));
    verifySmokeReport(result, version, target);
    console.log(`PASS ${target}: native window, SQLite, Web health, embedded PWA and clean exit (${version})`);
  } finally {
    clearTimeout(timer);
    await writeFile(join(results, "startup.log"), output);
    await rm(app, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await smokeRelease(process.argv[2], process.argv[3]);
}
