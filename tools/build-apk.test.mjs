import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "spool-apk-make-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(join(root, "android/tools"), { recursive: true });
  await mkdir(join(root, "tools"));
  await mkdir(join(root, "bin"));
  await writeFile(join(root, "Makefile"), await readFile(new URL("../Makefile", import.meta.url)));
  await writeFile(join(root, "android/tools/build-apk.sh"), '#!/bin/sh\nprintf "%s\\n" "$@" >> "$APK_TEST_LOG"\n');
  await writeFile(join(root, "tools/release.mjs"), 'import { writeFileSync } from "node:fs"; writeFileSync(process.env.RELEASE_TEST_LOG, JSON.stringify(process.argv.slice(2)));\n');
  await symlink(process.execPath, join(root, "bin/bun"));
  const buildLog = join(root, "apk.log");
  const releaseLog = join(root, "release.log");
  const env = {
    ...process.env, PATH: `${join(root, "bin")}${delimiter}${process.env.PATH}`,
    APK_TEST_LOG: buildLog, RELEASE_TEST_LOG: releaseLog,
  };
  const make = (...args) => spawnSync("make", ["--no-print-directory", ...args], { cwd: root, env, encoding: "utf8" });
  return { root, buildLog, releaseLog, make };
}

test("APK make goals build once and release selects signing without preparing a version release", async (t) => {
  for (const [args, mode] of [
    [["build", "apk"], "debug"],
    [["build", "apk", "release"], "release"],
    [["-j4", "build", "apk", "release"], "release"],
    [["build", "apk", "mode=release"], "release"],
  ]) {
    await t.test(args.join(" "), async (t) => {
      const { buildLog, releaseLog, make } = await fixture(t);
      const result = make(...args);
      assert.equal(result.status, 0, result.stderr + result.stdout);
      assert.equal(await readFile(buildLog, "utf8"), `${mode}\n`);
      assert.equal(existsSync(releaseLog), false, "APK builds must never run the version/tag release command");
    });
  }
});

test("standalone make release still prepares the requested version without building APKs", async (t) => {
  const { buildLog, releaseLog, make } = await fixture(t);
  const result = make("release", "version=0.2.0");
  assert.equal(result.status, 0, result.stderr + result.stdout);
  assert.deepEqual(JSON.parse(await readFile(releaseLog, "utf8")), ["0.2.0"]);
  assert.equal(existsSync(buildLog), false);
});

test("APK CLI rejects invalid modes and missing signing configuration before building", async (t) => {
  const { root } = await fixture(t);
  const cli = fileURLToPath(new URL("../android/tools/build-apk.sh", import.meta.url));
  const invalid = spawnSync("sh", [cli, "production"], { encoding: "utf8" });
  assert.equal(invalid.status, 2);
  assert.match(invalid.stderr, /Usage:/);
  const missing = spawnSync("sh", [cli, "release"], {
    env: { ...process.env, SPOOL_ANDROID_SIGNING_ENV: join(root, "missing.env") }, encoding: "utf8",
  });
  assert.equal(missing.status, 1);
  assert.match(missing.stderr, /Missing Android signing configuration:/);
  assert.doesNotMatch(missing.stdout, /Building gomobile|Running Android build tasks/);
});
