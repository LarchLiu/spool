import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { smokeRelease, verifySmokeReport } from "./smoke-release.mjs";

test("desktop extraction failures retain diagnostics and discard a previous success report", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "spool-smoke-failure-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const directory = join(root, "assets");
  const results = join(root, "smoke-results");
  await Promise.all([mkdir(directory), mkdir(results)]);
  await writeFile(join(results, "report.json"), '{"native":true}');
  const target = `${process.platform === "win32" ? "windows" : process.platform}-${process.arch === "x64" ? "amd64" : process.arch}`;
  const archive = `spool-1.2.3-${target}.tar.gz`;
  await writeFile(join(directory, archive), "broken archive");
  const result = spawnSync(process.execPath, [fileURLToPath(new URL("./smoke-release.mjs", import.meta.url)), directory, target], {
    cwd: root, env: { ...process.env, RELEASE_TAG: "v1.2.3" }, encoding: "utf8", timeout: 10_000,
  });
  assert.equal(result.error, undefined);
  assert.notEqual(result.status, 0, result.stdout + result.stderr);
  const log = await readFile(join(results, "startup.log"), "utf8");
  assert.match(log, /Command failed:/);
  assert.ok(log.includes(archive), log);
  await assert.rejects(readFile(join(results, "report.json")), { code: "ENOENT" });
});

test("release smoke accepts only the expected version, target, native loop and Web probes", () => {
  const valid = { version: "1.2.3", target: "windows-arm64", native: true, web: true, chat_webview: true, single_window: true, full_window_chat: true, shared_presentation: true, settings_webview: true, management_bridge: true };
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
