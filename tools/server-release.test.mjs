import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { smokeServer } from "./smoke-server.mjs";

test("desktop repackaging excludes server packages, installers and generated installation scripts", async (t) => {
  for (const target of ["linux-amd64", "windows-amd64"]) await t.test(target, async (t) => {
    const root = await mkdtemp(join(tmpdir(), "spool-server-packaging-"));
    t.after(() => rm(root, { recursive: true, force: true }));
    const directory = join(root, "build", target);
    await mkdir(directory, { recursive: true });
    const executable = target.startsWith("windows-") ? "Spool.exe" : "spool";
    const installer = target.startsWith("windows-") ? "spool-setup-1.2.3-amd64.exe" : "spool_1.2.3_amd64.deb";
    for (const name of [executable, installer, `spool-server-1.2.3-${target}.tar.gz`, `spool-server-1.2.3-${target}.tar.gz.sig`, "install.sh", "install.ps1", `update-${target}.json`, "old.delta", "spool-1.2.3-amd64.rpm", "PKGBUILD"]) {
      await writeFile(join(directory, name), name);
    }
    const script = fileURLToPath(new URL("./package-release.mjs", import.meta.url));
    const result = spawnSync(process.execPath, [script, target], {
      cwd: root, env: { ...process.env, RELEASE_TAG: "v1.2.3" }, encoding: "utf8",
    });
    assert.equal(result.status, 0, result.stdout + result.stderr);
    const entries = execFileSync("tar", ["-tzf", join(directory, `spool-1.2.3-${target}.tar.gz`)], { encoding: "utf8" });
    assert.equal(entries.trim(), executable);
  });
});

test("server packaging rejects a mismatched embedded version before building", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "spool-server-version-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await writeFile(join(root, "mygo.json"), '{"version":"1.2.2"}');
  const script = fileURLToPath(new URL("./package-server.mjs", import.meta.url));
  const result = spawnSync(process.execPath, [script, "linux-amd64"], {
    cwd: root, env: { ...process.env, RELEASE_TAG: "v1.2.3" }, encoding: "utf8",
  });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Server version does not match/);
  assert.equal(await readFile(join(root, "mygo.json"), "utf8"), '{"version":"1.2.2"}');
});

test("server smoke rejects non-native targets before touching scratch or spawning", async () => {
  await assert.rejects(smokeServer("missing", "other-platform", {}), /target OS and architecture/);
});

test("server extraction failures retain diagnostics and discard a previous success report", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "spool-server-smoke-failure-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const directory = join(root, "assets");
  const results = join(root, "build/artifacts/server-smoke");
  await Promise.all([mkdir(directory), mkdir(results, { recursive: true })]);
  await writeFile(join(results, "report.json"), '{"web":true}');
  const target = `${process.platform === "win32" ? "windows" : process.platform}-${process.arch === "x64" ? "amd64" : process.arch}`;
  const archive = `spool-server-1.2.3-${target}.tar.gz`;
  await writeFile(join(directory, archive), "broken archive");
  const result = spawnSync(process.execPath, [fileURLToPath(new URL("./smoke-server.mjs", import.meta.url)), directory, target], {
    cwd: root, env: { ...process.env, RELEASE_TAG: "v1.2.3" }, encoding: "utf8", timeout: 10_000,
  });
  assert.equal(result.error, undefined);
  assert.notEqual(result.status, 0, result.stdout + result.stderr);
  const log = await readFile(join(results, "startup.log"), "utf8");
  assert.match(log, /Command failed:/);
  assert.ok(log.includes(archive), log);
  await assert.rejects(readFile(join(results, "report.json")), { code: "ENOENT" });
});
