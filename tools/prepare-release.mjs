import { createPrivateKey, createPublicKey } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

export function releaseVersion(tag) {
  if (typeof tag !== "string" || !/^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.test(tag)) {
    throw new Error("Release tag must be vMAJOR.MINOR.PATCH, optionally with a prerelease suffix");
  }
  const version = tag.slice(1);
  const prerelease = version.split(/-(.*)/s)[1];
  if (prerelease?.split(".").some((part) => /^0\d+$/.test(part))) {
    throw new Error("Release tag prerelease numbers must not have leading zeroes");
  }
  return version;
}

export function releaseConfig(config, env) {
  const version = releaseVersion(env.RELEASE_TAG);
  const repository = env.GITHUB_REPOSITORY ?? "";
  if (!/^[\w.-]+\/[\w.-]+$/.test(repository)) {
    throw new Error("GITHUB_REPOSITORY must be owner/repository");
  }
  const publicKey = (env.MYGO_UPDATER_PUBLIC_KEY ?? "").trim();
  const privateKey = Buffer.from((env.MYGO_UPDATER_PRIVATE_KEY ?? "").trim(), "base64");
  if (Buffer.from(publicKey, "base64").length !== 32 || privateKey.length !== 64) {
    throw new Error("Configure MYGO_UPDATER_PUBLIC_KEY (variable) and MYGO_UPDATER_PRIVATE_KEY (secret) using mygo keygen");
  }
  const key = createPrivateKey({
    key: Buffer.concat([Buffer.from("302e020100300506032b657004220420", "hex"), privateKey.subarray(0, 32)]),
    format: "der",
    type: "pkcs8",
  });
  const derived = createPublicKey(key).export({ format: "der", type: "spki" }).subarray(-32);
  if (derived.toString("base64") !== publicKey || !derived.equals(privateKey.subarray(32))) {
    throw new Error("The update signing keys do not match; keep the same key pair across releases");
  }
  // Release builds need no OS certificates. macOS still needs an ad-hoc
  // signature for executable integrity, not a Developer ID or notarization.
  const macos = { ...config.macos, signingIdentity: "-" };
  delete macos.notarize;
  delete macos.provisioningProfile;
  const windows = { ...config.windows };
  delete windows.certificate;
  delete windows.signCommand;
  return {
    ...config,
    version,
    // CI supplies the verified embedded Web assets. Leaving buildCommand set
    // makes MyGo infer WebView bindings and run the desktop app during packaging.
    buildCommand: "",
    updates: {
      github: repository,
      publicKey,
      changelog: "build/release-notes.md",
      // Keep releases self-contained: no dependency on an earlier release's
      // mutable Latest pointer or archive availability while building.
      deltas: 0,
    },
    macos,
    windows,
  };
}

export async function releaseNotes(env) {
  if (env.RELEASE_NOTES_FILE) {
    const notes = (await readFile(env.RELEASE_NOTES_FILE, "utf8")).trim();
    if (!notes) throw new Error("Release notes must not be empty");
    return notes;
  }
  let notes = "See the GitHub release for details.";
  if (env.GITHUB_EVENT_PATH) {
    const event = JSON.parse(await readFile(env.GITHUB_EVENT_PATH, "utf8"));
    notes = event.release?.body?.trim() || notes;
  }
  return notes;
}

export function updaterReleaseNotes(notes) {
  // MyGo treats every level-two heading as the start of another version.
  return notes.replaceAll("\r\n", "\n").replace(/^## /gm, "### ").trim();
}

async function main() {
  const args = process.argv.slice(2);
  if (args.length > 1 || (args.length === 1 && args[0] !== "--check")) {
    throw new Error("Usage: node tools/prepare-release.mjs [--check]");
  }
  const config = releaseConfig(JSON.parse(await readFile("mygo.json", "utf8")), process.env);
  const notes = await releaseNotes(process.env);
  // Verification jobs must retain the development config for metadata tests.
  // Only packaging jobs should replace it with the release configuration.
  if (args[0] === "--check") {
    console.log(`Validated Spool ${config.version} with signing keys and release notes; no files changed`);
    return;
  }
  await mkdir("build", { recursive: true });
  await writeFile("build/release-notes.md", `## ${config.version}\n\n${updaterReleaseNotes(notes)}\n`);
  await writeFile("mygo.json", `${JSON.stringify(config, null, 2)}\n`);
  console.log(`Prepared Spool ${config.version} with signed updates from ${config.updates.github}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await main();
}
