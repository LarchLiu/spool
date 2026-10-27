import { createPrivateKey, createPublicKey } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

export function releaseConfig(config, env) {
  const tag = env.RELEASE_TAG ?? "";
  if (!/^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.test(tag)) {
    throw new Error("Release tag must be vMAJOR.MINOR.PATCH, optionally with a prerelease suffix");
  }
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
    version: tag.slice(1),
    updates: {
      github: repository,
      publicKey,
      changelog: "build/release-notes.md",
      // Published-release builds cannot use latest/download as their delta base:
      // it already points at this release, whose assets are not uploaded yet.
      deltas: 0,
    },
    macos,
    windows,
  };
}

async function main() {
  const config = releaseConfig(JSON.parse(await readFile("mygo.json", "utf8")), process.env);
  let notes = "See the GitHub release for details.";
  if (process.env.GITHUB_EVENT_PATH) {
    const event = JSON.parse(await readFile(process.env.GITHUB_EVENT_PATH, "utf8"));
    notes = event.release?.body?.trim() || notes;
  }
  await mkdir("build", { recursive: true });
  await writeFile("build/release-notes.md", `## ${config.version}\n\n${notes}\n`);
  await writeFile("mygo.json", `${JSON.stringify(config, null, 2)}\n`);
  console.log(`Prepared Spool ${config.version} with signed updates from ${config.updates.github}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await main();
}
