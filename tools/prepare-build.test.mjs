import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { buildConfig } from "./prepare-build.mjs";
import { releaseTargets } from "./release-assets.mjs";

test("test builds preserve source metadata, disable updates and remove certificate requirements", () => {
  const source = {
    name: "Spool", version: "0.2.0-beta.1", buildCommand: "go run ./tools/branding.go",
    linux: { webview: false }, updates: { github: "owner/repo", privateKey: "not-a-real-key" },
    macos: { signingIdentity: "Developer ID", notarize: { keychainProfile: "test" }, provisioningProfile: "test", minimumSystemVersion: "12.0" },
    windows: { certificate: "test.pfx", signCommand: "sign %1" },
  };
  const original = structuredClone(source);
  const config = buildConfig(source);
  assert.equal(config.version, source.version);
  assert.equal(config.name, source.name);
  assert.equal(config.buildCommand, "");
  assert.ok(!Object.hasOwn(config, "updates"));
  assert.deepEqual(config.linux, source.linux);
  assert.deepEqual(config.macos, { signingIdentity: "-", minimumSystemVersion: "12.0" });
  assert.deepEqual(config.windows, {});
  assert.deepEqual(source, original);
  assert.ok(!JSON.stringify(config).includes("not-a-real-key"));
  assert.throws(() => buildConfig({ version: "invalid" }), /Release tag/);
});

test("build preparation needs no tag, repository, keys or notes and only edits the packaging config", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "spool-build-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const source = await readFile(new URL("../mygo.json", import.meta.url), "utf8");
  await writeFile(join(directory, "mygo.json"), source);
  const cli = fileURLToPath(new URL("./prepare-build.mjs", import.meta.url));
  const run = (...args) => spawnSync(process.execPath, [cli, ...args], { cwd: directory, env: {}, encoding: "utf8" });
  let result = run("--unknown");
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Usage:/);
  assert.equal(await readFile(join(directory, "mygo.json"), "utf8"), source);
  result = run("--check");
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /no files changed/);
  assert.equal(await readFile(join(directory, "mygo.json"), "utf8"), source);
  result = run();
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(JSON.parse(await readFile(join(directory, "mygo.json"), "utf8")), buildConfig(JSON.parse(source)));
  assert.deepEqual(await readdir(directory), ["mygo.json"]);
});

test("check mode exports the source version for packaging and smoke without changing metadata", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "spool-build-output-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const source = '{"name":"Spool","version":"0.3.0-beta.2","buildCommand":"go run ./tools/branding.go"}\n';
  await writeFile(join(directory, "mygo.json"), source);
  const output = join(directory, "outputs");
  const cli = fileURLToPath(new URL("./prepare-build.mjs", import.meta.url));
  const result = spawnSync(process.execPath, [cli, "--check"], {
    cwd: directory, env: { GITHUB_OUTPUT: output }, encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(await readFile(output, "utf8"), "version=0.3.0-beta.2\n");
  assert.equal(await readFile(join(directory, "mygo.json"), "utf8"), source);
});

test("manual build workflow resolves a branch or commit once and cannot publish or use signing keys", async () => {
  const workflow = await readFile(new URL("../.github/workflows/build.yml", import.meta.url), "utf8");
  for (const use of workflow.matchAll(/uses: (\S+)/g)) assert.match(use[1], /^actions\/[\w-]+@[a-f0-9]{40}$/);
  assert.match(workflow, /on:\n  workflow_dispatch:/);
  assert.doesNotMatch(workflow, /^  (push|pull_request|release|schedule):/m);
  assert.match(workflow, /permissions:\n  contents: read/);
  assert.doesNotMatch(workflow, /secrets\.|vars\.|contents: write|gh release|tools\/(sign-release|release-metadata|prepare-release|release)\.mjs|RELEASE_BUILD_ONLY|MYGO_LDFLAGS/);
  assert.match(workflow, /ref: \$\{\{ inputs\.ref \|\| github\.ref \}\}/);
  assert.match(workflow, /commit="\$\(git rev-parse HEAD\)"/);
  assert.match(workflow, /ref: \$\{\{ needs\.prepare\.outputs\.commit \}\}/);
  const [prepare, build] = workflow.split("\n  build:\n");
  assert.doesNotMatch(prepare, /^        run: node tools\/prepare-build\.mjs$/m);
  assert.match(prepare, /^        run: node tools\/prepare-build\.mjs --check$/m);
  assert.match(prepare, /npm --prefix web test/);
  assert.match(prepare, /go test -race -v -timeout=10m/);
  assert.match(prepare, /go vet \.\/\.\.\./);
  assert.match(build, /run: node tools\/prepare-build\.mjs/);
  assert.match(build, /-skip-build-command -sign - -skip-notarize/);
  assert.match(build, /name: test-packages-\$\{\{ matrix\.target \}\}/);
  assert.match(build, /node tools\/release-assets\.mjs build-assets "\$\{\{ matrix\.target \}\}" --unsigned/);
  assert.match(build, /RELEASE_TAG: v\$\{\{ needs\.prepare\.outputs\.version \}\}/);
  for (const target of releaseTargets) assert.match(build, new RegExp(`target: ${target} \\}`));
  const smoke = workflow.split("\n  smoke:\n")[1];
  assert.ok(smoke);
  assert.match(smoke, /needs: \[prepare, build\]/);
  assert.match(smoke, /node tools\/smoke-release\.mjs smoke-assets/);
  assert.match(smoke, /ref: \$\{\{ needs\.prepare\.outputs\.commit \}\}/);
  assert.match(smoke, /RELEASE_TAG: v\$\{\{ needs\.prepare\.outputs\.version \}\}/);
  for (const runner of ["macos-15-intel", "windows-11-arm", "ubuntu-24.04-arm"]) assert.ok(smoke.includes(runner));
  for (const target of releaseTargets) assert.match(smoke, new RegExp(`target: ${target} \\}`));
});
