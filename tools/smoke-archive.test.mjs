import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdir, readFile, realpath, symlink, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import test from "node:test";
import { extractSmokeArchive, smokeTarCommand } from "./smoke-archive.mjs";
import { createScratch, removeScratch } from "./test-scratch.mjs";

test("Windows smoke uses native tar regardless of Git Bash's PATH", () => {
  for (const env of [
    { SystemRoot: "D:\\Windows", PATH: "C:\\Program Files\\Git\\usr\\bin" },
    { SYSTEMROOT: "D:\\Windows" },
    { windir: "D:\\Windows" },
  ]) {
    assert.equal(smokeTarCommand("win32", env), "D:\\Windows\\System32\\tar.exe");
  }
  assert.equal(smokeTarCommand("win32", {}), "C:\\Windows\\System32\\tar.exe");
  for (const platform of ["darwin", "linux"]) {
    assert.equal(smokeTarCommand(platform, { SystemRoot: "C:\\Windows" }), "tar");
  }
});

test("smoke extraction keeps drive letters out of tar's archive argument", () => {
  for (const [target, drive] of [["windows-amd64", "D"], ["windows-arm64", "C"]]) {
    const directory = `${drive}:\\a\\spool\\spool\\smoke assets`;
    const filename = `spool-1.2.3-${target}.tar.gz`;
    const destination = "C:\\Users\\Runner User\\AppData\\Local\\Temp\\spool-smoke-package";
    const calls = [];
    extractSmokeArchive(directory, filename, destination, (...args) => calls.push(args));
    assert.deepEqual(calls, [[smokeTarCommand(), ["-xzf", filename, "-C", resolve(destination)], { cwd: resolve(directory) }]]);
  }
});

test("smoke extraction unpacks desktop and server archives into realpath scratch with spaces and Unicode", async (t) => {
  const root = await createScratch("smoke-archive-test");
  t.after(() => removeScratch(root));
  const directory = join(root, process.platform === "win32" ? "smoke assets 中文" : "D: smoke assets 中文");
  const payload = join(root, "payload");
  await mkdir(directory);
  await mkdir(join(payload, "resources"), { recursive: true });
  await writeFile(join(payload, "Spool.exe"), "packaged desktop");
  await writeFile(join(payload, "spool-server.exe"), "packaged server");
  await writeFile(join(payload, "resources", "asset.txt"), "packaged resource");
  if (process.platform !== "win32") await symlink("asset.txt", join(payload, "resources", "asset-link"));
  for (const [name, executable] of [["spool", "Spool.exe"], ["spool-server", "spool-server.exe"]]) {
    const filename = `${name}-1.2.3-windows-${process.arch === "arm64" ? "arm64" : "amd64"}.tar.gz`;
    execFileSync(smokeTarCommand(), ["-czf", join(directory, filename), "--", executable, "resources"], {
      cwd: payload,
      env: { ...process.env, COPYFILE_DISABLE: "1", COPY_EXTENDED_ATTRIBUTES_DISABLE: "1" },
    });
    const destination = join(root, `extracted ${name} 中文`);
    await mkdir(destination);
    extractSmokeArchive(directory, filename, await realpath(destination));
    assert.match(await readFile(join(destination, executable), "utf8"), /packaged (desktop|server)/);
    assert.equal(await readFile(join(destination, "resources", "asset.txt"), "utf8"), "packaged resource");
    if (process.platform !== "win32") {
      assert.equal(await readFile(join(destination, "resources", "asset-link"), "utf8"), "packaged resource");
    }
  }
});
