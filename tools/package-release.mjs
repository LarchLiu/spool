import { execFileSync } from "node:child_process";
import { readdir } from "node:fs/promises";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { releaseVersion } from "./prepare-release.mjs";
import { releaseTargets, installerName } from "./release-assets.mjs";

export async function packageRelease(target, env = process.env) {
  if (!releaseTargets.includes(target)) throw new Error(`Unsupported release target: ${target}`);
  const version = releaseVersion(env.RELEASE_TAG);
  const directory = join("build", target);
  const archive = `spool-${version}-${target}.tar.gz`;
  const excluded = new Set([archive, installerName(version, target), "install.sh"]);
  const entries = target.startsWith("darwin-") ? ["Spool.app"] :
    (await readdir(directory)).filter((name) => !excluded.has(name) && !name.startsWith("update-") && !name.endsWith(".delta"));
  if (!entries.includes(target.startsWith("darwin-") ? "Spool.app" : target.startsWith("windows-") ? "Spool.exe" : "spool")) {
    throw new Error(`Missing application payload for ${target}`);
  }
  // MyGo omits non-Linux archives without a private key. Package the same
  // installed payload here, leaving all long-lived signing keys out of builds.
  execFileSync("tar", ["-czf", archive, "--", ...entries], {
    cwd: directory, stdio: "inherit",
    env: { ...process.env, COPYFILE_DISABLE: "1", COPY_EXTENDED_ATTRIBUTES_DISABLE: "1" },
  });
  console.log(`Packaged unsigned ${join(directory, archive)}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await packageRelease(process.argv[2]);
}
