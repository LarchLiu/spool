import { appendFile, readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { releaseVersion } from "./prepare-release.mjs";

export function buildConfig(config) {
  const version = releaseVersion(`v${config.version}`);
  const { updates, ...base } = config;
  const macos = { ...config.macos, signingIdentity: "-" };
  delete macos.notarize;
  delete macos.provisioningProfile;
  const windows = { ...config.windows };
  delete windows.certificate;
  delete windows.signCommand;
  // Test installers retain the source version but must never join the official
  // update channel or run the desktop to generate unused WebView bindings.
  return { ...base, version, buildCommand: "", macos, windows };
}

async function main() {
  const args = process.argv.slice(2);
  if (args.length > 1 || (args.length === 1 && args[0] !== "--check")) {
    throw new Error("Usage: node tools/prepare-build.mjs [--check]");
  }
  const config = buildConfig(JSON.parse(await readFile("mygo.json", "utf8")));
  if (args[0] === "--check") {
    if (process.env.GITHUB_OUTPUT) await appendFile(process.env.GITHUB_OUTPUT, `version=${config.version}\n`);
    console.log(`Validated Spool ${config.version} test build; no files changed`);
    return;
  }
  await writeFile("mygo.json", `${JSON.stringify(config, null, 2)}\n`);
  console.log(`Prepared Spool ${config.version} test build; automatic updates and certificate signing are disabled`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await main();
}
