import assert from "node:assert/strict";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { installerName, releaseTargets, verifyReleaseAssets } from "./release-assets.mjs";

const key = generateKeyPairSync("ed25519");
const options = {
  tag: "v1.2.3",
  repository: "owner/spool",
  publicKey: key.publicKey.export({ format: "der", type: "spki" }).subarray(-32).toString("base64"),
  notes: "### Features\n\n- Signed updates",
};

async function fixture(t, { tag = options.tag, targets = releaseTargets } = {}) {
  const directory = await mkdtemp(join(tmpdir(), "spool-assets-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const version = tag.slice(1);
  for (const target of targets) {
    const archive = `spool-${version}-${target}.tar.gz`;
    const content = Buffer.from(`archive for ${target}`);
    const manifest = {
      schema: "spool-update-v1", repository: options.repository, target,
      version,
      notes: options.notes,
      url: `https://github.com/${options.repository}/releases/download/${tag}/${archive}`,
      size: content.length,
      signature: sign(null, createHash("sha256").update(content).digest(), key.privateKey).toString("base64"),
    };
    await writeFile(join(directory, archive), content);
    const data = Buffer.from(JSON.stringify(manifest));
    await writeFile(join(directory, `update-${target}.json`), data);
    await writeFile(join(directory, `update-${target}.json.sig`), sign(null, data, key.privateKey));
    await writeFile(join(directory, installerName(version, target)), "installer");
    if (target === "linux-amd64") await writeFile(join(directory, "install.sh"), "#!/bin/sh\n");
  }
  return directory;
}

test("accepts exactly six signed targets, installers and the Linux installation script", async (t) => {
  const directory = await fixture(t);
  assert.equal((await verifyReleaseAssets(directory, options)).length, 25);
});

test("per-target checks work and prerelease Debian names follow MyGo's version conversion", async (t) => {
  const tag = "v1.2.3-beta-test.1";
  const targets = ["linux-arm64"];
  const directory = await fixture(t, { tag, targets });
  assert.equal(installerName(tag.slice(1), targets[0]), "spool_1.2.3~beta.test.1_arm64.deb");
  assert.equal((await verifyReleaseAssets(directory, { ...options, tag, targets })).length, 4);
});

test("rejects missing installers, empty assets and unexpected files", async (t) => {
  for (const mutation of ["missing", "empty", "extra"]) {
    await t.test(mutation, async (t) => {
      const directory = await fixture(t);
      const installer = join(directory, installerName("1.2.3", "darwin-arm64"));
      if (mutation === "missing") await rm(installer);
      if (mutation === "empty") await writeFile(installer, "");
      if (mutation === "extra") await writeFile(join(directory, "private-key"), "not a release asset");
      await assert.rejects(verifyReleaseAssets(directory, options), /asset/i);
    });
  }
});

test("archive tampering fails both size and signature validation", async (t) => {
  const directory = await fixture(t);
  const path = join(directory, "spool-1.2.3-darwin-arm64.tar.gz");
  const content = await readFile(path);
  content[0] ^= 1;
  await writeFile(path, content);
  await assert.rejects(verifyReleaseAssets(directory, options), /invalid update archive signature/);
  await writeFile(path, "truncated");
  await assert.rejects(verifyReleaseAssets(directory, options), /archive size/);
});

test("rejects manifests for a different release, repository, signing key or changelog", async (t) => {
  const changes = [
    { version: "9.9.9" }, { url: "https://example.test/archive.tar.gz" },
    { signature: Buffer.alloc(64).toString("base64") }, { notes: "stale notes" }, { deltas: [{}] },
  ];
  for (const change of changes) {
    await t.test(Object.keys(change)[0], async (t) => {
      const directory = await fixture(t);
      const path = join(directory, "update-darwin-arm64.json");
      const manifest = JSON.parse(await readFile(path, "utf8"));
      await writeFile(path, JSON.stringify({ ...manifest, ...change }));
      await assert.rejects(verifyReleaseAssets(directory, options));
    });
  }
  const directory = await fixture(t);
  await assert.rejects(verifyReleaseAssets(directory, { ...options, publicKey: "" }), /public key/);
  await assert.rejects(verifyReleaseAssets(directory, { ...options, publicKey: Buffer.alloc(32).toString("base64") }), /signature/);
  await assert.rejects(verifyReleaseAssets(directory, { ...options, targets: ["unknown"] }), /Unsupported/);
});
