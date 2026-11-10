import { createHash, sign } from "node:crypto";
import { createReadStream } from "node:fs";
import { readFile, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { releaseConfig, releaseSigningKey, updaterReleaseNotes } from "./prepare-release.mjs";
import { releaseTargets, serverArchiveName, verifyReleaseAssets } from "./release-assets.mjs";

export async function signRelease(directory, env = process.env) {
  const config = releaseConfig({}, env);
  const key = releaseSigningKey(env);
  const notes = updaterReleaseNotes(await readFile(env.RELEASE_NOTES_FILE, "utf8"));
  if (!notes) throw new Error("Release notes must not be empty");
  // Reject missing, stale or injected build output before signing anything.
  await verifyReleaseAssets(directory, {
    tag: env.RELEASE_TAG, repository: env.GITHUB_REPOSITORY,
    publicKey: config.updates.publicKey, notes, unsigned: true,
  });
  for (const target of releaseTargets) {
    const serverArchive = serverArchiveName(config.version, target);
    const serverHash = createHash("sha256");
    for await (const chunk of createReadStream(join(directory, serverArchive))) serverHash.update(chunk);
    await writeFile(join(directory, `${serverArchive}.sig`), sign(null, serverHash.digest(), key));
    const archive = `spool-${config.version}-${target}.tar.gz`;
    const path = join(directory, archive);
    const hash = createHash("sha256");
    for await (const chunk of createReadStream(path)) hash.update(chunk);
    const manifest = {
      schema: "spool-update-v1", repository: env.GITHUB_REPOSITORY, target,
      version: config.version, notes, date: new Date().toISOString(),
      url: `https://github.com/${env.GITHUB_REPOSITORY}/releases/download/${env.RELEASE_TAG}/${archive}`,
      size: (await stat(path)).size,
      // Preserve this legacy signature so 0.1.1 clients can install the fix.
      signature: sign(null, hash.digest(), key).toString("base64"),
    };
    const data = Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`);
    await writeFile(join(directory, `update-${target}.json`), data);
    await writeFile(join(directory, `update-${target}.json.sig`), sign(null, data, key));
  }
  const publicKeyDER = Buffer.concat([Buffer.from("302a300506032b6570032100", "hex"), Buffer.from(config.updates.publicKey, "base64")]);
  const template = await readFile(new URL("./install.sh", import.meta.url), "utf8");
  const installerBuild = await Bun.build({
    entrypoints: [fileURLToPath(new URL("./install-archive.mjs", import.meta.url))],
    target: "bun", format: "esm", write: false,
  });
  if (!installerBuild.success) throw new AggregateError(installerBuild.logs, "Could not bundle the standalone installer");
  const installer = await installerBuild.outputs[0].text();
  const dependencies = await readFile(new URL("./linux-dependencies.sh", import.meta.url), "utf8");
  await writeFile(join(directory, "install.sh"), template
    .replaceAll("@REPOSITORY@", env.GITHUB_REPOSITORY)
    .replaceAll("@PUBLIC_KEY_DER@", publicKeyDER.toString("base64"))
    .replace("@LINUX_DEPENDENCIES@", () => dependencies)
    .replace("@INSTALLER_JS@", () => installer), { mode: 0o755 });
  await verifyReleaseAssets(directory, {
    tag: env.RELEASE_TAG, repository: env.GITHUB_REPOSITORY,
    publicKey: config.updates.publicKey, notes,
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await signRelease(process.argv[2] ?? "release-assets");
}
