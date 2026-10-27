import { appendFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { releaseVersion } from "./prepare-release.mjs";

function versionParts(tag) {
  const [version, prerelease = ""] = releaseVersion(tag).split(/-(.*)/s);
  return [...version.split("."), prerelease];
}

function compareIdentifier(left, right) {
  if (left === right) return 0;
  const numericLeft = /^\d+$/.test(left);
  const numericRight = /^\d+$/.test(right);
  if (numericLeft && numericRight) return BigInt(left) < BigInt(right) ? -1 : 1;
  if (numericLeft !== numericRight) return numericLeft ? -1 : 1;
  return left < right ? -1 : 1;
}

export function compareTags(left, right) {
  const a = versionParts(left);
  const b = versionParts(right);
  for (let i = 0; i < 3; i++) {
    const result = compareIdentifier(a[i], b[i]);
    if (result) return result;
  }
  if (!a[3] || !b[3]) return a[3] === b[3] ? 0 : a[3] ? -1 : 1;
  const aPre = a[3].split(".");
  const bPre = b[3].split(".");
  for (let i = 0; i < Math.max(aPre.length, bPre.length); i++) {
    if (aPre[i] === undefined) return -1;
    if (bPre[i] === undefined) return 1;
    const result = compareIdentifier(aPre[i], bPre[i]);
    if (result) return result;
  }
  return 0;
}

function channel(tag) {
  const prerelease = versionParts(tag)[3];
  if (!prerelease) return "stable";
  const first = prerelease.split(".")[0];
  return /^\d+$/.test(first) ? "prerelease" : `prerelease:${first}`;
}

export function releaseMetadata(releases, tag) {
  releaseVersion(tag);
  const current = releases.find((release) => release.tag_name === tag);
  if (current && !current.draft) {
    throw new Error(`${tag} is already published; use a new version instead of replacing distributed assets`);
  }
  const published = releases.filter((release) => {
    if (release.draft) return false;
    try {
      releaseVersion(release.tag_name);
      return true;
    } catch {
      return false;
    }
  });
  const previous = published
    .filter((release) => channel(release.tag_name) === channel(tag) && compareTags(release.tag_name, tag) < 0)
    .sort((a, b) => compareTags(a.tag_name, b.tag_name))
    .at(-1);
  const prerelease = channel(tag) !== "stable";
  return {
    previous_tag: previous?.tag_name ?? "",
    prerelease,
    make_latest: !prerelease && !published.some((release) =>
      !release.prerelease && channel(release.tag_name) === "stable" && compareTags(release.tag_name, tag) > 0),
    draft_id: current?.id ?? "",
  };
}

export async function fetchReleases({ repository, token, apiUrl = "https://api.github.com", fetchImpl = fetch }) {
  if (!/^[\w.-]+\/[\w.-]+$/.test(repository ?? "")) {
    throw new Error("GITHUB_REPOSITORY must be owner/repository");
  }
  const releases = [];
  for (let page = 1; ; page++) {
    const response = await fetchImpl(`${apiUrl}/repos/${repository}/releases?per_page=100&page=${page}`, {
      headers: {
        Accept: "application/vnd.github+json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      signal: AbortSignal.timeout(30_000),
    });
    if (!response.ok) throw new Error(`Failed to list GitHub releases: HTTP ${response.status}`);
    const items = await response.json();
    if (!Array.isArray(items)) throw new Error("GitHub releases response must be an array");
    releases.push(...items);
    if (items.length < 100) return releases;
  }
}

async function main() {
  releaseVersion(process.env.RELEASE_TAG);
  const releases = await fetchReleases({
    repository: process.env.GITHUB_REPOSITORY,
    token: process.env.GITHUB_TOKEN,
    apiUrl: process.env.GITHUB_API_URL,
  });
  const output = releaseMetadata(releases, process.env.RELEASE_TAG);
  if (process.env.GITHUB_OUTPUT) {
    await appendFile(process.env.GITHUB_OUTPUT, Object.entries(output).map(([key, value]) => `${key}=${value}\n`).join(""));
  }
  console.log(JSON.stringify(output));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await main();
}
