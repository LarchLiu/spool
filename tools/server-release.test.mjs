import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { smokeServer } from "./smoke-server.mjs";

test("desktop repackaging excludes an existing server archive and detached signature", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "spool-server-packaging-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const directory = join(root, "build/linux-amd64");
  await mkdir(directory, { recursive: true });
  for (const name of ["spool", "spool-server-1.2.3-linux-amd64.tar.gz", "spool-server-1.2.3-linux-amd64.tar.gz.sig"]) {
    await writeFile(join(directory, name), name);
  }
  const script = fileURLToPath(new URL("./package-release.mjs", import.meta.url));
  const result = spawnSync(process.execPath, [script, "linux-amd64"], {
    cwd: root, env: { ...process.env, RELEASE_TAG: "v1.2.3" }, encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stdout + result.stderr);
  const entries = execFileSync("tar", ["-tzf", join(directory, "spool-1.2.3-linux-amd64.tar.gz")], { encoding: "utf8" });
  assert.equal(entries.trim(), "spool");
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
