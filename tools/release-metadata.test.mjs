import assert from "node:assert/strict";
import test from "node:test";
import { compareTags, fetchReleases, releaseMetadata } from "./release-metadata.mjs";

const published = (tag_name, extra = {}) => ({ tag_name, draft: false, prerelease: tag_name.includes("-"), ...extra });

test("tag comparison follows SemVer, including numeric prereleases and large version numbers", () => {
  const tags = [
    "v1.0.0-alpha", "v1.0.0-alpha.1", "v1.0.0-alpha.beta", "v1.0.0-beta", "v1.0.0-beta.2",
    "v1.0.0-beta.11", "v1.0.0-rc.1", "v1.0.0", "v1.1.0", "v1.10.0", "v2.0.0",
    "v9007199254740992.0.0", "v9007199254740993.0.0",
  ];
  assert.deepEqual([...tags].reverse().sort(compareTags), tags);
  for (const tag of tags) assert.equal(compareTags(tag, tag), 0);
});

test("changelog base is the highest older published release in the same tag channel", () => {
  const releases = [
    published("v1.8.0"), published("v1.9.0", { draft: true }), published("v1.11.0"),
    published("v1.10.0-beta.1"), published("other-tag"), published("v01.9.0"), published("v1.7.0"),
  ];
  const metadata = releaseMetadata(releases, "v1.10.0");
  assert.equal(metadata.previous_tag, "v1.8.0");
  assert.equal(metadata.prerelease, false);
  assert.equal(metadata.make_latest, false);
  assert.equal(metadata.draft_id, "");
});

test("beta, rc, numeric prereleases and stable releases stay in separate channels", () => {
  const releases = [
    published("v1.0.0"), published("v2.0.0-beta.2"), published("v2.0.0-beta.10"),
    published("v2.0.0-rc.1"), published("v2.0.0-stable"), published("v2.0.0-1"),
  ];
  assert.equal(releaseMetadata(releases, "v2.0.0-beta.11").previous_tag, "v2.0.0-beta.10");
  assert.equal(releaseMetadata(releases, "v2.0.0-rc.2").previous_tag, "v2.0.0-rc.1");
  assert.equal(releaseMetadata(releases, "v2.0.0-2").previous_tag, "v2.0.0-1");
  assert.equal(releaseMetadata(releases, "v2.0.0").previous_tag, "v1.0.0");
  assert.equal(releaseMetadata(releases, "v2.0.0-beta.11").make_latest, false);
  assert.equal(releaseMetadata(releases, "v2.0.0").make_latest, true);
});

test("first release has no base; drafts can be retried; published releases cannot be rebuilt", () => {
  assert.deepEqual(releaseMetadata([], "v1.0.0"), {
    previous_tag: "", prerelease: false, make_latest: true, draft_id: "",
  });
  const metadata = releaseMetadata([published("v1.0.0", { draft: true, id: 42 })], "v1.0.0");
  assert.equal(metadata.draft_id, 42);
  assert.throws(() => releaseMetadata([published("v1.0.0")], "v1.0.0"), /already published/);
});

test("release listing paginates past ten pages and sends authentication only in headers", async () => {
  const calls = [];
  const releases = await fetchReleases({
    repository: "owner/repo",
    token: "test-token",
    apiUrl: "https://api.example.test",
    fetchImpl: async (url, options) => {
      calls.push({ url, options });
      return { ok: true, json: async () => calls.length <= 11 ? Array.from({ length: 100 }, (_, id) => ({ id })) : [] };
    },
  });
  assert.equal(releases.length, 1100);
  assert.equal(calls.length, 12);
  assert.equal(calls.at(-1).url, "https://api.example.test/repos/owner/repo/releases?per_page=100&page=12");
  assert.equal(calls[0].options.headers.Authorization, "Bearer test-token");
  assert.ok(calls[0].options.signal instanceof AbortSignal);
});

test("API errors and invalid responses fail closed, rather than pretending this is the first release", async () => {
  await assert.rejects(fetchReleases({ repository: "owner/repo", fetchImpl: async () => ({ ok: false, status: 403 }) }), /HTTP 403/);
  await assert.rejects(fetchReleases({ repository: "owner/repo", fetchImpl: async () => ({ ok: true, json: async () => ({}) }) }), /must be an array/);
  await assert.rejects(fetchReleases({ repository: "owner/repo/extra" }), /GITHUB_REPOSITORY/);
});
