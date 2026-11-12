import { execFileSync } from "node:child_process";
import {
  appendFile,
  mkdir,
  readFile,
  readdir,
  writeFile,
} from "node:fs/promises";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { releaseVersion } from "./prepare-release.mjs";

const architectures = ["amd64", "arm64"];
const imageIDs = ["relay", "caddy"];

export function relayImageConfig(repository, tag) {
  if (!/^[\w.-]+\/[\w.-]+$/.test(repository ?? "")) {
    throw new Error("GITHUB_REPOSITORY must be owner/repository");
  }
  const version = releaseVersion(tag);
  if (version.length > 128)
    throw new Error("Release version exceeds the Docker tag limit");
  const prefix = `ghcr.io/${repository.toLowerCase()}-relay`;
  return { version, images: { relay: prefix, caddy: `${prefix}-caddy` } };
}

export function relayImageRecord(config, id, arch, digest) {
  if (!imageIDs.includes(id) || !architectures.includes(arch)) {
    throw new Error("Unsupported Relay image or architecture");
  }
  if (!/^sha256:[a-f0-9]{64}$/.test(digest ?? ""))
    throw new Error("Invalid image digest");
  return { id, arch, image: config.images[id], digest };
}

export function relayManifestCommands(
  records,
  { repository, tag, makeLatest = false },
) {
  const config = relayImageConfig(repository, tag);
  const expected = new Map(
    imageIDs.flatMap((id) =>
      architectures.map((arch) => [`${id}-${arch}`, null]),
    ),
  );
  for (const record of records) {
    const checked = relayImageRecord(
      config,
      record.id,
      record.arch,
      record.digest,
    );
    const key = `${checked.id}-${checked.arch}`;
    if (record.image !== checked.image || expected.get(key) !== null) {
      throw new Error("Unexpected or duplicate Relay image record");
    }
    expected.set(key, checked);
  }
  if ([...expected.values()].some((record) => !record))
    throw new Error("Missing Relay image architecture");
  // Older stable releases and prereleases must never move the stable channel.
  const latest = makeLatest && !config.version.includes("-");
  return imageIDs.map((id) => {
    const image = config.images[id];
    return [
      "buildx",
      "imagetools",
      "create",
      "--tag",
      `${image}:${config.version}`,
      ...(latest ? ["--tag", `${image}:latest`] : []),
      ...architectures.map(
        (arch) => `${image}@${expected.get(`${id}-${arch}`).digest}`,
      ),
    ];
  });
}

async function main() {
  const [command, ...args] = process.argv.slice(2);
  const repository = process.env.GITHUB_REPOSITORY;
  const tag = process.env.RELEASE_TAG;
  const config = relayImageConfig(repository, tag);
  if (command === "prepare" && args.length === 1) {
    const [id] = args;
    if (!imageIDs.includes(id)) throw new Error("Unsupported Relay image");
    const metadata = JSON.parse(await readFile("relay/version.json", "utf8"));
    if (metadata.version !== config.version)
      throw new Error("Relay version does not match RELEASE_TAG");
    if (!process.env.GITHUB_OUTPUT)
      throw new Error("GITHUB_OUTPUT is required");
    await appendFile(
      process.env.GITHUB_OUTPUT,
      `image=${config.images[id]}\nversion=${config.version}\n`,
    );
  } else if (command === "record" && args.length === 3) {
    const [id, arch, directory] = args;
    const record = relayImageRecord(config, id, arch, process.env.IMAGE_DIGEST);
    await mkdir(directory, { recursive: true });
    await writeFile(
      join(directory, `${id}-${arch}.json`),
      JSON.stringify(record) + "\n",
    );
  } else if (command === "publish" && args.length === 1) {
    if (!["true", "false"].includes(process.env.MAKE_LATEST))
      throw new Error("MAKE_LATEST must be true or false");
    const [directory] = args;
    const names = await readdir(directory);
    const records = await Promise.all(
      names.map(async (name) =>
        JSON.parse(await readFile(join(directory, name), "utf8")),
      ),
    );
    // Validate the complete set before publishing either manifest.
    for (const command of relayManifestCommands(records, {
      repository,
      tag,
      makeLatest: process.env.MAKE_LATEST === "true",
    })) {
      execFileSync("docker", command, { stdio: "inherit" });
    }
  } else {
    throw new Error(
      "Usage: bun tools/relay-images.mjs prepare <relay|caddy> | record <id> <arch> <directory> | publish <directory>",
    );
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href)
  await main();
