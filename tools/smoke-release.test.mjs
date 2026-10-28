import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import { extractSmokeArchive, smokeRelease, verifySmokeReport } from "./smoke-release.mjs";

test("Windows smoke extraction keeps drive letters out of tar's archive argument", async (t) => {
  for (const [target, drive] of [["windows-amd64", "D"], ["windows-arm64", "C"]]) {
    await t.test(target, () => {
      const directory = `${drive}:\\a\\spool\\spool\\smoke assets`;
      const filename = `spool-1.2.3-${target}.tar.gz`;
      const destination = "C:\\Users\\Runner User\\AppData\\Local\\Temp\\spool-smoke-package";
      const calls = [];
      extractSmokeArchive(directory, filename, destination, (...args) => calls.push(args));
      assert.deepEqual(calls, [["tar", ["-xzf", filename, "-C", resolve(destination)], { cwd: resolve(directory) }]]);
    });
  }
});

test("smoke extraction unpacks real archives from paths with spaces and colons", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "spool-smoke archive-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const directory = join(root, process.platform === "win32" ? "smoke assets" : "D: smoke assets");
  const payload = join(root, "payload");
  await mkdir(directory);
  await mkdir(join(payload, "resources"), { recursive: true });
  await writeFile(join(payload, "Spool.exe"), "packaged executable");
  await writeFile(join(payload, "resources", "asset.txt"), "packaged resource");
  for (const target of ["windows-amd64", "windows-arm64"]) {
    await t.test(target, async () => {
      const filename = `spool-1.2.3-${target}.tar.gz`;
      execFileSync("tar", ["-czf", filename, "-C", payload, "Spool.exe", "resources"], { cwd: directory });
      const destination = join(root, `extracted ${target}`);
      await mkdir(destination);
      extractSmokeArchive(directory, filename, destination);
      assert.equal(await readFile(join(destination, "Spool.exe"), "utf8"), "packaged executable");
      assert.equal(await readFile(join(destination, "resources", "asset.txt"), "utf8"), "packaged resource");
    });
  }
});

test("release smoke accepts only the expected version, target, native loop and Web probes", () => {
  const valid = { version: "1.2.3", target: "windows-arm64", native: true, web: true };
  assert.doesNotThrow(() => verifySmokeReport(valid, valid.version, valid.target));
  for (const field of Object.keys(valid)) {
    const wrong = { ...valid, [field]: typeof valid[field] === "boolean" ? false : "different" };
    assert.throws(() => verifySmokeReport(wrong, valid.version, valid.target));
  }
  assert.throws(() => verifySmokeReport({}, valid.version, valid.target));
});

test("cross-compiled or invalid-version smoke attempts fail before extracting or running anything", async () => {
  await assert.rejects(smokeRelease("missing", "not-native"), /target OS and architecture/);
  const target = `${process.platform === "win32" ? "windows" : process.platform}-${process.arch === "x64" ? "amd64" : process.arch}`;
  await assert.rejects(smokeRelease("missing", target, { RELEASE_TAG: "bad" }), /Release tag/);
});
