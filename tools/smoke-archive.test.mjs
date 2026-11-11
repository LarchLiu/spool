import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { closeSync, fstatSync, openSync, readFileSync } from "node:fs";
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

test("smoke extraction passes archive bytes without path arguments and closes handles on success and failure", async (t) => {
  const root = await createScratch("smoke-archive-handles");
  t.after(() => removeScratch(root));
  const filename = "spool 测试.tar.gz";
  await writeFile(join(root, filename), "archive bytes");
  for (const fails of [false, true]) {
    let input;
    const run = (command, args, options) => {
      assert.equal(command, smokeTarCommand());
      assert.deepEqual(args, ["-xzf", "-"]);
      assert.equal(options.cwd, resolve(root));
      [input] = options.stdio;
      assert.equal(readFileSync(input, "utf8"), "archive bytes");
      if (fails) throw new Error("tar failure");
    };
    if (fails) {
      assert.throws(() => extractSmokeArchive(root, filename, root, run), /spool 测试.tar.gz.*tar failure/);
    } else {
      extractSmokeArchive(root, filename, root, run);
    }
    assert.throws(() => fstatSync(input), { code: "EBADF" });
  }
});

test("smoke extraction unpacks desktop and server archives into realpath scratch with spaces and Unicode", async (t) => {
  const root = await createScratch("smoke-archive-test");
  t.after(() => removeScratch(root));
  const directory = join(root, process.platform === "win32" ? "smoke assets 中文" : "D: smoke assets 中文");
  const payload = join(root, "payload 中文");
  await mkdir(directory);
  await mkdir(join(payload, "resources"), { recursive: true });
  await writeFile(join(payload, "Spool.exe"), "packaged desktop");
  await writeFile(join(payload, "spool-server.exe"), "packaged server");
  await writeFile(join(payload, "resources", "asset.txt"), "packaged resource");
  if (process.platform !== "win32") await symlink("asset.txt", join(payload, "resources", "asset-link"));
  for (const [name, executable] of [["spool", "Spool.exe"], ["spool-server", "spool-server.exe"]]) {
    const filename = `${name}-1.2.3-windows-${process.arch === "arm64" ? "arm64" : "amd64"}.tar.gz`;
    const output = openSync(join(directory, filename), "w");
    try {
      execFileSync(smokeTarCommand(), ["-czf", "-", "--", executable, "resources"], {
        cwd: payload, stdio: ["ignore", output, "pipe"],
        env: { ...process.env, COPYFILE_DISABLE: "1", COPY_EXTENDED_ATTRIBUTES_DISABLE: "1" },
      });
    } finally {
      closeSync(output);
    }
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
