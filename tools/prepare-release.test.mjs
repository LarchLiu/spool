import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { releaseConfig, releaseNotes, updaterReleaseNotes } from "./prepare-release.mjs";

const key = generateKeyPairSync("ed25519");
const seed = key.privateKey.export({ format: "der", type: "pkcs8" }).subarray(-32);
const publicKey = key.publicKey.export({ format: "der", type: "spki" }).subarray(-32);
const env = {
  RELEASE_TAG: "v1.2.3",
  GITHUB_REPOSITORY: "LarchLiu/spool",
  MYGO_UPDATER_PUBLIC_KEY: publicKey.toString("base64"),
  MYGO_UPDATER_PRIVATE_KEY: Buffer.concat([seed, publicKey]).toString("base64"),
};

test("release tag is the version and only the public key enters the config", () => {
  const config = releaseConfig({ name: "Spool", version: "0.1.0", buildCommand: "npm --prefix web run build", linux: { webview: false } }, env);
  assert.equal(config.version, "1.2.3");
  assert.equal(config.buildCommand, "");
  assert.equal(config.updates.github, env.GITHUB_REPOSITORY);
  assert.equal(config.updates.publicKey, env.MYGO_UPDATER_PUBLIC_KEY);
  assert.deepEqual(config.linux, { webview: false });
  assert.ok(!JSON.stringify(config).includes(env.MYGO_UPDATER_PRIVATE_KEY));
});

test("missing and mismatched signing keys fail closed", () => {
  assert.throws(() => releaseConfig({}, { ...env, MYGO_UPDATER_PRIVATE_KEY: "" }), /Configure/);
  assert.throws(() => releaseConfig({}, { ...env, MYGO_UPDATER_PUBLIC_KEY: Buffer.alloc(32).toString("base64") }), /do not match/);
});

test("rejects invalid tags and repository names", () => {
  for (const tag of [undefined, "", "main", "1.2.3", "v1.2", "v01.2.3", "v1.2.3;echo oops", "v1.2.3\n", "v1.2.3-beta.01"]) {
    assert.throws(() => releaseConfig({}, { ...env, RELEASE_TAG: tag }), /Release tag/);
  }
  assert.throws(() => releaseConfig({}, { ...env, GITHUB_REPOSITORY: "owner/repo/extra" }), /GITHUB_REPOSITORY/);
  assert.equal(releaseConfig({}, { ...env, RELEASE_TAG: "v1.2.3-beta.1" }).version, "1.2.3-beta.1");
});

test("OS certificate signing is disabled without disabling update verification", () => {
  const config = releaseConfig({
    macos: {
      signingIdentity: "Developer ID Application: Test",
      notarize: { keychainProfile: "notary" },
      provisioningProfile: "/tmp/test.provisionprofile",
      minimumSystemVersion: "12.0",
    },
    windows: { certificate: "/tmp/test.pfx", signCommand: "sign %1" },
  }, {
    ...env,
    MACOS_SIGNING_IDENTITY: "Developer ID Application: Test",
    NOTARY_KEYCHAIN: "/tmp/test.keychain",
    WINDOWS_CERTIFICATE_FILE: "/tmp/test.pfx",
  });
  assert.equal(config.macos.signingIdentity, "-");
  assert.equal(config.macos.notarize, undefined);
  assert.equal(config.macos.provisioningProfile, undefined);
  assert.equal(config.macos.minimumSystemVersion, "12.0");
  assert.equal(config.windows.certificate, undefined);
  assert.equal(config.windows.signCommand, undefined);
  assert.equal(config.updates.publicKey, env.MYGO_UPDATER_PUBLIC_KEY);
});

test("explicit release notes take precedence over event notes and cannot be missing or empty", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "spool-notes-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const notesPath = join(directory, "notes.md");
  const eventPath = join(directory, "event.json");
  await writeFile(notesPath, "\n### Features\n\n- Generated changes\n");
  await writeFile(eventPath, JSON.stringify({ release: { body: "Manually written notes" } }));
  assert.equal(await releaseNotes({ RELEASE_NOTES_FILE: notesPath, GITHUB_EVENT_PATH: eventPath }),
    "### Features\n\n- Generated changes");
  assert.equal(await releaseNotes({ GITHUB_EVENT_PATH: eventPath }), "Manually written notes");
  await writeFile(notesPath, " \n");
  await assert.rejects(releaseNotes({ RELEASE_NOTES_FILE: notesPath }), /must not be empty/);
  await assert.rejects(releaseNotes({ RELEASE_NOTES_FILE: join(directory, "missing") }), /ENOENT/);
});

test("MyGo notes preserve draft sections instead of truncating at level-two headings", () => {
  assert.equal(updaterReleaseNotes("## Features\r\n\r\n- First\r\n\r\n## Fixes\r\n\r\n- Second\r\n"),
    "### Features\n\n- First\n\n### Fixes\n\n- Second");
});

test("CLI writes the same changelog into MyGo configuration without leaking the signing key", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "spool-prepare-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await writeFile(join(directory, "mygo.json"), JSON.stringify({ name: "Spool", version: "0.1.0" }));
  await writeFile(join(directory, "RELEASE_NOTES.md"), "## Features\n\n- Signed updates\n");
  const result = spawnSync(process.execPath, [fileURLToPath(new URL("./prepare-release.mjs", import.meta.url))], {
    cwd: directory,
    env: { ...env, RELEASE_NOTES_FILE: "RELEASE_NOTES.md" },
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  const config = JSON.parse(await readFile(join(directory, "mygo.json"), "utf8"));
  assert.equal(config.version, "1.2.3");
  assert.equal(config.updates.deltas, 0);
  assert.equal(config.updates.changelog, "build/release-notes.md");
  assert.equal(await readFile(join(directory, config.updates.changelog), "utf8"),
    "## 1.2.3\n\n### Features\n\n- Signed updates\n");
  assert.ok(!JSON.stringify(config).includes(env.MYGO_UPDATER_PRIVATE_KEY));
});
