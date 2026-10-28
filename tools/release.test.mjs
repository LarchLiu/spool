import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { nextReleaseVersion, parseReleaseArgs, prepareLocalRelease } from "./release.mjs";

const versionFiles = ["mygo.json", "web/package.json", "web/package-lock.json"];

async function fixture(t, { remote = true } = {}) {
  const directory = await mkdtemp(join(tmpdir(), "spool-release-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const root = join(directory, "repo");
  await mkdir(join(root, "web"), { recursive: true });
  const env = Object.fromEntries(Object.entries(process.env).filter(([name]) => !name.startsWith("GIT_")));
  Object.assign(env, {
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_CONFIG_GLOBAL: join(directory, "no-global-config"),
    GIT_AUTHOR_NAME: "Spool Release Test",
    GIT_AUTHOR_EMAIL: "release@example.test",
    GIT_COMMITTER_NAME: "Spool Release Test",
    GIT_COMMITTER_EMAIL: "release@example.test",
  });
  const git = (...args) => execFileSync("git", args, { cwd: root, env, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  git("init", "--initial-branch=main");
  git("config", "commit.gpgsign", "false");
  git("config", "tag.gpgsign", "false");
  const hooks = join(root, ".git", "test-hooks");
  git("config", "core.hooksPath", hooks);
  await writeFile(join(root, "mygo.json"), '{"name":"Spool","version":"0.1.0","buildCommand":"npm --prefix web run build"}\n');
  await writeFile(join(root, "web/package.json"), `${JSON.stringify({ name: "spool-web", version: "0.1.0", dependencies: { example: "0.1.0" } }, null, 2)}\n`);
  await writeFile(join(root, "web/package-lock.json"), `${JSON.stringify({
    name: "spool-web", version: "0.1.0", lockfileVersion: 3,
    packages: { "": { name: "spool-web", version: "0.1.0", dependencies: { example: "0.1.0" } }, "node_modules/example": { version: "0.1.0" } },
  }, null, 2)}\n`);
  git("add", "--", ...versionFiles);
  git("commit", "-m", "Initial version");
  if (remote) {
    const origin = join(directory, "origin.git");
    git("init", "--bare", "--initial-branch=main", origin);
    git("remote", "add", "origin", origin);
    git("push", "origin", "HEAD");
  }
  const readVersions = async () => Promise.all(versionFiles.map((name) => readFile(join(root, name), "utf8")));
  return { root, directory, env, git, hooks, readVersions };
}

test("CLI accepts explicit versions and bump types, with opt-in preview and offline mode", () => {
  assert.deepEqual(parseReleaseArgs(["0.2.0"]), { input: "0.2.0", dryRun: false, offline: false });
  assert.deepEqual(parseReleaseArgs(["--dry-run", "minor", "--offline"]), { input: "minor", dryRun: true, offline: true });
  assert.deepEqual(parseReleaseArgs(["--help"]), { help: true });
  for (const args of [["--push", "patch"], ["patch", "minor"], ["", "patch"], ["patch", ""], ["", ""]]) {
    assert.throws(() => parseReleaseArgs(args));
  }
});

test("CLI defaults omitted or empty versions to patch, including with flags", () => {
  for (const args of [[], [""]]) {
    assert.deepEqual(parseReleaseArgs(args), { input: "patch", dryRun: false, offline: false });
  }
  assert.deepEqual(parseReleaseArgs(["--dry-run"]), { input: "patch", dryRun: true, offline: false });
  assert.deepEqual(parseReleaseArgs(["", "--offline"]), { input: "patch", dryRun: false, offline: true });
  assert.deepEqual(parseReleaseArgs(["--offline", "", "--dry-run"]), { input: "patch", dryRun: true, offline: true });
});

test("default CLI version prepares a patch release without pushing", async (t) => {
  const repo = await fixture(t);
  const remoteBefore = repo.git("ls-remote", "origin");
  const { input, ...options } = parseReleaseArgs([]);
  const result = await prepareLocalRelease(input, { ...repo, ...options });
  assert.equal(result.version, "0.1.1");
  assert.equal(repo.git("log", "-1", "--format=%s"), "chore: release v0.1.1");
  assert.equal(repo.git("cat-file", "-t", "v0.1.1"), "tag");
  assert.equal(repo.git("status", "--porcelain"), "");
  assert.equal(repo.git("ls-remote", "origin"), remoteBefore);
});

test("automatic versions use the highest known version, without floating point truncation", () => {
  assert.equal(nextReleaseVersion("patch", "0.1.0", []).version, "0.1.1");
  assert.equal(nextReleaseVersion("minor", "0.1.0", []).version, "0.2.0");
  assert.equal(nextReleaseVersion("major", "0.1.0", []).version, "1.0.0");
  assert.equal(nextReleaseVersion("patch", "0.1.0", ["v1.9.0", "v1.10.0", "vnot-a-version"]).version, "1.10.1");
  assert.equal(nextReleaseVersion("patch", "2.0.0", ["v1.0.0"]).version, "2.0.1");
  assert.equal(nextReleaseVersion("patch", "1.0.0", ["v1.9007199254740993.0"]).version, "1.9007199254740993.1");
});

test("bumping a prerelease follows SemVer promotion rules", () => {
  assert.equal(nextReleaseVersion("patch", "1.2.3-beta.1", []).version, "1.2.3");
  assert.equal(nextReleaseVersion("minor", "1.2.0-beta.1", []).version, "1.2.0");
  assert.equal(nextReleaseVersion("minor", "1.2.3-beta.1", []).version, "1.3.0");
  assert.equal(nextReleaseVersion("major", "2.0.0-rc.1", []).version, "2.0.0");
  assert.equal(nextReleaseVersion("major", "2.1.0-rc.1", []).version, "3.0.0");
});

test("explicit versions support prereleases and an optional v, but reject invalid or non-increasing tags", () => {
  assert.equal(nextReleaseVersion("0.2.0-beta.1", "0.1.0", []).tag, "v0.2.0-beta.1");
  assert.equal(nextReleaseVersion("v0.2.0", "0.1.0", []).version, "0.2.0");
  assert.equal(nextReleaseVersion("0.1.0", "0.1.0", []).version, "0.1.0");
  for (const version of ["0.2", "01.2.3", "1.2.3-beta.01", "1.2.3;echo oops", "1.2.3\n"]) {
    assert.throws(() => nextReleaseVersion(version, "0.1.0", []), /Release tag/);
  }
  assert.throws(() => nextReleaseVersion("0.2.0", "0.1.0", ["v0.2.0"]), /already exists/);
  assert.throws(() => nextReleaseVersion("0.2.0", "0.1.0", ["v0.3.0"]), /newer than/);
  assert.throws(() => nextReleaseVersion("0.1.0", "0.2.0", []), /older than/);
});

test("release updates only version metadata, commits it and creates an annotated tag without pushing", async (t) => {
  const repo = await fixture(t);
  const remoteBefore = repo.git("ls-remote", "origin");
  const result = await prepareLocalRelease("0.2.0", repo);
  const [mygoText, packageText, lockText] = await repo.readVersions();
  const mygo = JSON.parse(mygoText);
  const pkg = JSON.parse(packageText);
  const lock = JSON.parse(lockText);
  assert.equal(mygo.version, "0.2.0");
  assert.equal(mygo.buildCommand, "npm --prefix web run build");
  assert.equal(mygoText.split("\n").length, 2);
  assert.equal(pkg.version, "0.2.0");
  assert.equal(pkg.dependencies.example, "0.1.0");
  assert.equal(lock.version, "0.2.0");
  assert.equal(lock.packages[""].version, "0.2.0");
  assert.equal(lock.packages["node_modules/example"].version, "0.1.0");
  assert.equal(repo.git("log", "-1", "--format=%s"), "chore: release v0.2.0");
  assert.deepEqual(repo.git("diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD").split("\n").sort(), [...versionFiles].sort());
  assert.equal(repo.git("cat-file", "-t", "v0.2.0"), "tag");
  assert.equal(repo.git("rev-parse", "v0.2.0^{commit}"), result.commit);
  assert.equal(repo.git("status", "--porcelain"), "");
  assert.equal(repo.git("ls-remote", "origin"), remoteBefore);
});

test("the first release can use the current version and still gets a release commit", async (t) => {
  const repo = await fixture(t);
  const before = repo.git("rev-parse", "HEAD");
  const result = await prepareLocalRelease("0.1.0", repo);
  assert.notEqual(result.commit, before);
  assert.equal(repo.git("diff", "HEAD^", "HEAD"), "");
  assert.equal(repo.git("log", "-1", "--format=%s"), "chore: release v0.1.0");
  assert.equal(repo.git("cat-file", "-t", "v0.1.0"), "tag");
});

test("remote-only tags prevent duplicates and participate in automatic version selection", async (t) => {
  const repo = await fixture(t);
  repo.git("tag", "v0.4.0");
  repo.git("push", "origin", "v0.4.0");
  repo.git("tag", "--delete", "v0.4.0");
  const before = await repo.readVersions();
  const head = repo.git("rev-parse", "HEAD");
  await assert.rejects(prepareLocalRelease("0.4.0", repo), /already exists/);
  assert.deepEqual(await repo.readVersions(), before);
  assert.equal(repo.git("rev-parse", "HEAD"), head);
  const result = await prepareLocalRelease("patch", repo);
  assert.equal(result.version, "0.4.1");
  assert.equal(result.latestTag, "v0.4.0");
  assert.equal(repo.git("ls-remote", "--tags", "origin", "v0.4.1"), "");
});

test("dirty worktrees are rejected without touching staged, unstaged or untracked work", async (t) => {
  for (const state of ["staged", "unstaged", "untracked"]) {
    await t.test(state, async (t) => {
      const repo = await fixture(t);
      const path = join(repo.root, state === "untracked" ? "notes.txt" : "mygo.json");
      await writeFile(path, state === "untracked" ? "my release notes\n" : '{"version":"0.1.0","name":"Keep my changes"}\n');
      if (state === "staged") repo.git("add", "mygo.json");
      const files = await repo.readVersions();
      const status = repo.git("status", "--porcelain");
      const head = repo.git("rev-parse", "HEAD");
      await assert.rejects(prepareLocalRelease("patch", repo), /uncommitted changes/);
      assert.deepEqual(await repo.readVersions(), files);
      assert.equal(repo.git("status", "--porcelain"), status);
      assert.equal(repo.git("rev-parse", "HEAD"), head);
      assert.equal(repo.git("tag", "--list"), "");
    });
  }
});

test("dry-run previews dirty worktrees without changing any files, commits or tags", async (t) => {
  const repo = await fixture(t);
  await writeFile(join(repo.root, "notes.txt"), "uncommitted work\n");
  const files = await repo.readVersions();
  const head = repo.git("rev-parse", "HEAD");
  const result = await prepareLocalRelease("minor", { ...repo, dryRun: true });
  assert.equal(result.version, "0.2.0");
  assert.equal(result.dirty, true);
  assert.equal(result.commit, undefined);
  assert.deepEqual(await repo.readVersions(), files);
  assert.equal(repo.git("rev-parse", "HEAD"), head);
  assert.equal(repo.git("tag", "--list"), "");
  assert.equal(await readFile(join(repo.root, "notes.txt"), "utf8"), "uncommitted work\n");
});

test("missing or inaccessible origin fails closed, while offline mode is explicit", async (t) => {
  const repo = await fixture(t, { remote: false });
  const before = await repo.readVersions();
  await assert.rejects(prepareLocalRelease("patch", repo), /git ls-remote failed/);
  assert.deepEqual(await repo.readVersions(), before);
  repo.git("remote", "add", "origin", join(repo.directory, "missing.git"));
  await assert.rejects(prepareLocalRelease("patch", repo), /git ls-remote failed/);
  assert.deepEqual(await repo.readVersions(), before);
  const result = await prepareLocalRelease("patch", { ...repo, offline: true });
  assert.equal(result.version, "0.1.1");
  assert.equal(result.offline, true);
});

test("detached HEAD and malformed lockfiles are rejected before writing versions", async (t) => {
  const repo = await fixture(t);
  repo.git("checkout", "--detach");
  const before = await repo.readVersions();
  await assert.rejects(prepareLocalRelease("patch", repo), /named branch/);
  assert.deepEqual(await repo.readVersions(), before);
  repo.git("checkout", "main");
  await writeFile(join(repo.root, "web/package-lock.json"), '{"version":"0.1.0","lockfileVersion":1}\n');
  repo.git("add", "web/package-lock.json");
  repo.git("commit", "-m", "Old lockfile");
  const malformed = await repo.readVersions();
  await assert.rejects(prepareLocalRelease("patch", repo), /root entry/);
  assert.deepEqual(await repo.readVersions(), malformed);
  assert.equal(repo.git("status", "--porcelain"), "");
});

test("commit hook failures leave reviewable version changes but do not create a tag or push", async (t) => {
  const repo = await fixture(t);
  await mkdir(repo.hooks, { recursive: true });
  await writeFile(join(repo.hooks, "pre-commit"), "#!/bin/sh\nexit 1\n", { mode: 0o755 });
  const head = repo.git("rev-parse", "HEAD");
  const remoteBefore = repo.git("ls-remote", "origin");
  await assert.rejects(prepareLocalRelease("patch", repo), /Version files may be modified or staged; no release tag was created/);
  assert.equal(repo.git("rev-parse", "HEAD"), head);
  assert.equal(repo.git("tag", "--list"), "");
  assert.equal(JSON.parse((await repo.readVersions())[0]).version, "0.1.1");
  assert.equal(repo.git("ls-remote", "origin"), remoteBefore);
});

test("an in-progress merge is rejected even when its worktree is clean", async (t) => {
  const repo = await fixture(t);
  const before = await repo.readVersions();
  const head = repo.git("rev-parse", "HEAD");
  await writeFile(join(repo.root, ".git", "MERGE_HEAD"), `${head}\n`);
  assert.equal(repo.git("status", "--porcelain"), "");
  await assert.rejects(prepareLocalRelease("patch", repo), /Finish the current merge/);
  assert.deepEqual(await repo.readVersions(), before);
  assert.equal(repo.git("rev-parse", "HEAD"), head);
});

test("tag signing failures keep the release commit and explain recovery without resetting it", async (t) => {
  const repo = await fixture(t);
  repo.git("config", "tag.gpgsign", "true");
  repo.git("config", "gpg.program", join(repo.directory, "missing-gpg"));
  const head = repo.git("rev-parse", "HEAD");
  const remoteBefore = repo.git("ls-remote", "origin");
  await assert.rejects(prepareLocalRelease("patch", repo), /The release commit exists, but tagging failed/);
  assert.notEqual(repo.git("rev-parse", "HEAD"), head);
  assert.equal(repo.git("log", "-1", "--format=%s"), "chore: release v0.1.1");
  assert.equal(repo.git("tag", "--list"), "");
  assert.equal(repo.git("status", "--porcelain"), "");
  assert.equal(repo.git("ls-remote", "origin"), remoteBefore);
});
