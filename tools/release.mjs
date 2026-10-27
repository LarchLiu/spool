import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { readFile, realpath, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { releaseVersion } from "./prepare-release.mjs";
import { compareTags } from "./release-metadata.mjs";

const repositoryRoot = fileURLToPath(new URL("../", import.meta.url));
const versionFiles = ["mygo.json", "web/package.json", "web/package-lock.json"];
const usage = `Usage: node tools/release.mjs <version|patch|minor|major> [--dry-run] [--offline]

Examples:
  node tools/release.mjs 0.2.0
  node tools/release.mjs 0.3.0-beta.1
  node tools/release.mjs patch --dry-run

Synchronizes version files, commits them, and creates an annotated local tag.
Checks origin tags unless --offline is set. Never pushes or publishes.
`;

export function parseReleaseArgs(args) {
  const options = { dryRun: false, offline: false };
  for (const arg of args) {
    if (arg === "--help" || arg === "-h") return { help: true };
    if (arg === "--dry-run") options.dryRun = true;
    else if (arg === "--offline") options.offline = true;
    else if (arg.startsWith("-")) throw new Error(`Unknown option: ${arg}`);
    else if (options.input !== undefined) throw new Error("Pass exactly one version or bump type");
    else options.input = arg;
  }
  if (!options.input) throw new Error("Pass a version or bump type (patch, minor, major)");
  return options;
}

export function nextReleaseVersion(input, currentVersion, tags) {
  const currentTag = `v${releaseVersion(`v${currentVersion}`)}`;
  const validTags = tags.filter((tag) => {
    try {
      releaseVersion(tag);
      return true;
    } catch {
      return false;
    }
  });
  const latestTag = validTags.sort(compareTags).at(-1);
  const baseline = latestTag && compareTags(latestTag, currentTag) > 0 ? latestTag : currentTag;
  let version;
  if (["patch", "minor", "major"].includes(input)) {
    const [core, prerelease] = releaseVersion(baseline).split(/-(.*)/s);
    let [major, minor, patch] = core.split(".").map(BigInt);
    if (input === "major") {
      if (!prerelease || minor !== 0n || patch !== 0n) major++;
      minor = patch = 0n;
    } else if (input === "minor") {
      if (!prerelease || patch !== 0n) minor++;
      patch = 0n;
    } else if (!prerelease) patch++;
    version = `${major}.${minor}.${patch}`;
  } else {
    version = releaseVersion(input.startsWith("v") ? input : `v${input}`);
  }
  const tag = `v${version}`;
  if (validTags.includes(tag)) throw new Error(`Tag ${tag} already exists locally or on origin`);
  if (compareTags(tag, currentTag) < 0) throw new Error(`${tag} is older than the current version ${currentTag}`);
  if (latestTag && compareTags(tag, latestTag) <= 0) throw new Error(`${tag} must be newer than the latest known tag ${latestTag}`);
  return { version, tag, latestTag: latestTag ?? "" };
}

function versionFile(name, text, version) {
  const data = JSON.parse(text);
  if (typeof data.version !== "string") throw new Error(`${name} has no version field`);
  data.version = version;
  if (name === "web/package-lock.json") {
    if (!data.packages?.[""]) throw new Error(`${name} must have a packages[""] root entry (lockfile v2+)`);
    data.packages[""].version = version;
  }
  // Keep the compact MyGo config and npm's existing indentation/newline style.
  const indent = text.match(/\n([\t ]+)"/)?.[1];
  const newline = text.includes("\r\n") ? "\r\n" : "\n";
  const content = JSON.stringify(data, null, indent).replaceAll("\n", newline);
  return content + (text.endsWith("\n") ? newline : "");
}

export async function prepareLocalRelease(input, {
  root = repositoryRoot, dryRun = false, offline = false, env = process.env,
} = {}) {
  root = await realpath(root);
  const git = (args) => {
    try {
      return execFileSync("git", args, { cwd: root, env, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
    } catch (error) {
      throw new Error(`git ${args[0]} failed: ${error.stderr?.toString().trim() || error.message}`, { cause: error });
    }
  };
  if (await realpath(git(["rev-parse", "--show-toplevel"])) !== root) throw new Error("Run from the repository root");
  const branch = git(["branch", "--show-current"]);
  if (!branch) throw new Error("A release requires a named branch, not detached HEAD");
  for (const operation of ["MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"]) {
    if (existsSync(resolve(root, git(["rev-parse", "--git-path", operation])))) {
      throw new Error("Finish the current merge, rebase, cherry-pick or revert before preparing a release");
    }
  }
  const head = git(["rev-parse", "HEAD"]);
  const dirty = Boolean(git(["status", "--porcelain=v1", "--untracked-files=all"]));
  if (dirty && !dryRun) throw new Error("Working tree has uncommitted changes; commit or stash them first");
  git(["ls-files", "--error-unmatch", "--", ...versionFiles]);
  const texts = await Promise.all(versionFiles.map((name) => readFile(join(root, name), "utf8")));
  const currentVersion = JSON.parse(texts[0]).version;
  // Validate user input before contacting origin; the second pass includes remote tags.
  nextReleaseVersion(input, currentVersion, []);
  const localTags = git(["tag", "--list", "v*"]).split("\n").filter(Boolean);
  const remoteTags = offline ? [] : git(["ls-remote", "--tags", "--refs", "origin", "v*"])
    .split("\n").map((line) => line.match(/^[0-9a-f]+\s+refs\/tags\/(v\S+)$/)?.[1]).filter(Boolean);
  const release = nextReleaseVersion(input, currentVersion, [...localTags, ...remoteTags]);
  const files = versionFiles.map((name, i) => ({ name, content: versionFile(name, texts[i], release.version) }));
  const result = { ...release, branch, currentVersion, files: [...versionFiles], dirty, offline, dryRun };
  if (dryRun) return result;

  // Fail before writing anything if Git cannot identify the release author.
  git(["var", "GIT_AUTHOR_IDENT"]);
  git(["var", "GIT_COMMITTER_IDENT"]);
  if (git(["rev-parse", "HEAD"]) !== head || git(["branch", "--show-current"]) !== branch ||
      git(["status", "--porcelain=v1", "--untracked-files=all"])) {
    throw new Error("The checkout changed during release preparation; nothing was written, please retry");
  }
  for (const file of files) await writeFile(join(root, file.name), file.content);
  let committed = false;
  try {
    git(["add", "--", ...versionFiles]);
    // The first release may use the already checked-in development version.
    git(["commit", "--allow-empty", "-m", `chore: release ${release.tag}`]);
    committed = true;
    git(["tag", "-a", release.tag, "-m", `Spool ${release.tag}`]);
  } catch (error) {
    const state = committed ? "The release commit exists, but tagging failed." : "Version files may be modified or staged; no release tag was created.";
    throw new Error(`${error.message}\n${state} Inspect git status and git log; nothing was pushed.`, { cause: error });
  }
  return { ...result, commit: git(["rev-parse", "HEAD"]) };
}

async function main() {
  const { input, help, ...options } = parseReleaseArgs(process.argv.slice(2));
  if (help) return console.log(usage);
  const result = await prepareLocalRelease(input, options);
  console.log(`Current version: ${result.currentVersion}`);
  console.log(`Latest known tag: ${result.latestTag || "<none>"}`);
  console.log(`Release: ${result.tag} on ${result.branch}`);
  console.log(`Version files: ${result.files.join(", ")}`);
  if (result.offline) console.log("Warning: origin tags were not checked (--offline).");
  if (result.dryRun) {
    if (result.dirty) console.log("Warning: commit or stash existing changes before preparing the release.");
    return console.log("Dry run: no files, commits or tags changed.");
  }
  console.log(`Created commit ${result.commit.slice(0, 12)} and annotated tag ${result.tag}.`);
  console.log(`Review: git show --stat ${result.tag}`);
  console.log(`Publish when ready: git push --atomic origin HEAD ${result.tag}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(`${error.message}\n\n${usage}`);
    process.exitCode = 1;
  });
}
