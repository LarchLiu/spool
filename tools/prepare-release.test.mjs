import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import test from "node:test";
import { releaseConfig } from "./prepare-release.mjs";

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
  const config = releaseConfig({ name: "Spool", version: "0.1.0", linux: { webview: false } }, env);
  assert.equal(config.version, "1.2.3");
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
  for (const tag of ["main", "1.2.3", "v1.2", "v01.2.3", "v1.2.3;echo oops", "v1.2.3\n"]) {
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
