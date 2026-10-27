import { createHash, createPublicKey, verify } from "node:crypto";
import { createReadStream } from "node:fs";
import { readFile, readdir, stat } from "node:fs/promises";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { releaseVersion, updaterReleaseNotes } from "./prepare-release.mjs";

export const releaseTargets = [
  "darwin-arm64", "darwin-amd64", "windows-amd64", "windows-arm64", "linux-amd64", "linux-arm64",
];

export function installerName(version, target) {
  const [platform, arch] = target.split("-");
  if (platform === "darwin") return `Spool ${version} ${arch}.dmg`;
  if (platform === "windows") return `Spool Setup ${version} ${arch}.exe`;
  const debVersion = version.replace(/-(.*)/s, (_, prerelease) => `~${prerelease.replaceAll("-", ".")}`);
  return `spool_${debVersion}_${arch}.deb`;
}

async function digest(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest();
}

export async function verifyReleaseAssets(directory, { tag, repository, publicKey, notes, targets = releaseTargets }) {
  const version = releaseVersion(tag);
  const rawKey = Buffer.from(publicKey ?? "", "base64");
  if (rawKey.length !== 32) throw new Error("MYGO_UPDATER_PUBLIC_KEY must be a base64 Ed25519 public key");
  const key = createPublicKey({
    key: Buffer.concat([Buffer.from("302a300506032b6570032100", "hex"), rawKey]),
    format: "der",
    type: "spki",
  });
  const expected = [];
  for (const target of targets) {
    if (!releaseTargets.includes(target)) throw new Error(`Unsupported release target: ${target}`);
    const archive = `spool-${version}-${target}.tar.gz`;
    const manifestName = `update-${target}.json`;
    expected.push(archive, manifestName, installerName(version, target));
    if (target === "linux-amd64") expected.push("install.sh");
    const manifest = JSON.parse(await readFile(join(directory, manifestName), "utf8"));
    const url = `https://github.com/${repository}/releases/download/${tag}/${archive}`;
    if (manifest.version !== version || manifest.url !== url) {
      throw new Error(`${manifestName}: version or archive URL does not match ${tag}`);
    }
    if (typeof notes !== "string" || manifest.notes?.trim() !== updaterReleaseNotes(notes)) {
      throw new Error(`${manifestName}: notes do not match the release notes`);
    }
    if (manifest.deltas?.length) throw new Error(`${manifestName}: delta updates must be disabled`);
    const path = join(directory, archive);
    const size = (await stat(path)).size;
    if (size === 0 || manifest.size !== size) throw new Error(`${manifestName}: archive size does not match`);
    if (!verify(null, await digest(path), key, Buffer.from(manifest.signature ?? "", "base64"))) {
      throw new Error(`${manifestName}: invalid update archive signature`);
    }
  }
  const actual = (await readdir(directory)).sort();
  if (JSON.stringify(actual) !== JSON.stringify(expected.sort())) {
    throw new Error(`Release asset set does not match: expected ${expected.join(", ")}; found ${actual.join(", ")}`);
  }
  for (const name of expected) {
    const info = await stat(join(directory, name));
    if (!info.isFile() || info.size === 0) throw new Error(`Release asset must be a nonempty file: ${name}`);
  }
  return expected;
}

async function main() {
  const [directory = "release-assets", target] = process.argv.slice(2);
  const notes = await readFile(process.env.RELEASE_NOTES_FILE, "utf8");
  const assets = await verifyReleaseAssets(directory, {
    tag: process.env.RELEASE_TAG,
    repository: process.env.GITHUB_REPOSITORY,
    publicKey: process.env.MYGO_UPDATER_PUBLIC_KEY,
    notes,
    ...(target ? { targets: [target] } : {}),
  });
  console.log(`Verified ${assets.length} release assets, including archive signatures`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await main();
}
