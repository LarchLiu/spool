import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import {
  relayImageConfig,
  relayImageRecord,
  relayManifestCommands,
} from "./relay-images.mjs";

const repository = "LarchLiu/spool";
const tag = "v1.2.3";
const config = relayImageConfig(repository, tag);
const records = ["relay", "caddy"].flatMap((id, i) =>
  ["amd64", "arm64"].map((arch, j) =>
    relayImageRecord(
      config,
      id,
      arch,
      "sha256:" + String(i * 2 + j + 1).repeat(64),
    ),
  ),
);

test("Relay image names use the source repository and a validated release version", () => {
  assert.deepEqual(config, {
    version: "1.2.3",
    images: {
      relay: "ghcr.io/larchliu/spool-relay",
      caddy: "ghcr.io/larchliu/spool-relay-caddy",
    },
  });
  assert.equal(
    relayImageConfig("Another/Project", tag).images.relay,
    "ghcr.io/another/project-relay",
  );
  for (const invalid of [
    "main",
    "v1.2",
    "v1.2.3\n",
    "v1.2.3;echo oops",
    "v1.2.3-" + "x".repeat(128),
  ])
    assert.throws(() => relayImageConfig(repository, invalid));
  assert.throws(() => relayImageConfig("owner/repo/extra", tag));
  assert.throws(() =>
    relayImageRecord(config, "unknown", "amd64", records[0].digest),
  );
  assert.throws(() =>
    relayImageRecord(config, "relay", "arm/v7", records[0].digest),
  );
});

test("image preparation rejects a mismatched embedded version before publishing build outputs", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "spool-relay-images-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await mkdir(join(directory, "relay"));
  const metadata = join(directory, "relay/version.json");
  const output = join(directory, "output");
  const run = () =>
    spawnSync(
      process.execPath,
      [
        fileURLToPath(new URL("./relay-images.mjs", import.meta.url)),
        "prepare",
        "relay",
      ],
      {
        cwd: directory,
        env: {
          ...process.env,
          GITHUB_REPOSITORY: repository,
          RELEASE_TAG: tag,
          GITHUB_OUTPUT: output,
        },
        encoding: "utf8",
      },
    );
  await writeFile(metadata, '{"version":"1.2.2"}\n');
  let result = run();
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Relay version does not match RELEASE_TAG/);
  await assert.rejects(readFile(output), { code: "ENOENT" });
  await writeFile(metadata, '{"version":"1.2.3"}\n');
  result = run();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(
    await readFile(output, "utf8"),
    "image=ghcr.io/larchliu/spool-relay\nversion=1.2.3\n",
  );
});

test("multi-platform tags reference both tested architecture digests for each image", () => {
  const commands = relayManifestCommands(records, {
    repository,
    tag,
    makeLatest: true,
  });
  assert.equal(commands.length, 2);
  for (const [i, id] of ["relay", "caddy"].entries()) {
    const image = config.images[id];
    assert.deepEqual(commands[i], [
      "buildx",
      "imagetools",
      "create",
      "--tag",
      `${image}:1.2.3`,
      "--tag",
      `${image}:latest`,
      ...records
        .filter((record) => record.id === id)
        .map((record) => `${image}@${record.digest}`),
    ]);
  }
});

test("older releases and prereleases cannot replace the stable latest image", () => {
  for (const [release, makeLatest] of [
    [tag, false],
    ["v1.2.3-beta.1", true],
    ["v1.2.3-beta.1", false],
  ]) {
    const commands = relayManifestCommands(records, {
      repository,
      tag: release,
      makeLatest,
    });
    assert.equal(
      commands.some((command) =>
        command.some((argument) => argument.endsWith(":latest")),
      ),
      false,
    );
    for (const [i, id] of ["relay", "caddy"].entries())
      assert.ok(
        commands[i].includes(`${config.images[id]}:${release.slice(1)}`),
      );
  }
});

test("manifest publication rejects incomplete, duplicate, mismatched and malformed digests", () => {
  for (const input of [
    records.slice(1),
    [...records, records[0]],
    [{ ...records[0], digest: "latest" }, ...records.slice(1)],
    [
      { ...records[0], image: "ghcr.io/another/project-relay" },
      ...records.slice(1),
    ],
    [{ ...records[0], arch: "arm/v7" }, ...records.slice(1)],
  ]) {
    assert.throws(() =>
      relayManifestCommands(input, { repository, tag, makeLatest: true }),
    );
  }
});

test("release workflow tests native Relay images and publishes manifests only in serialized publication", async () => {
  const workflow = Bun.YAML.parse(
    await readFile(
      new URL("../.github/workflows/release.yml", import.meta.url),
      "utf8",
    ),
  );
  const build = workflow.jobs.relay;
  assert.equal(build.needs, "prepare");
  assert.deepEqual(build.permissions, { contents: "read", packages: "write" });
  const matrix = build.strategy.matrix.include;
  assert.equal(matrix.length, 4);
  for (const { id, arch } of records) {
    const entry = matrix.find(
      (entry) => entry.image === id && entry.arch === arch,
    );
    assert.ok(entry);
    assert.equal(
      entry.os,
      arch === "arm64" ? "ubuntu-24.04-arm" : "ubuntu-24.04",
    );
    assert.equal(
      entry.context,
      id === "relay" ? "relay" : "relay/deploy/caddy",
    );
  }
  const smoke = build.steps.findIndex((step) =>
    step.run?.includes("relay/test/image-smoke.mjs"),
  );
  const load = build.steps.findIndex((step) => step.with?.load === true);
  const push = build.steps.findIndex((step) => step.id === "push");
  assert.ok(load >= 0 && load < smoke && smoke < push);
  assert.match(build.steps[push].with.outputs, /push-by-digest=true/);
  assert.equal(
    build.steps[push].with.tags,
    undefined,
    "Build jobs cannot move release tags",
  );
  assert.ok(
    build.steps.some(
      (step) => step.with?.ref === "${{ needs.prepare.outputs.commit }}",
    ),
  );
  const publish = workflow.jobs.publish;
  assert.ok(publish.needs.includes("relay"));
  assert.equal(publish.permissions.packages, "write");
  assert.equal(publish.concurrency.group, "release-publish");
  const publishImages = publish.steps.findIndex((step) =>
    step.run?.includes("relay-images.mjs publish"),
  );
  const metadata = publish.steps.findIndex((step) => step.id === "metadata");
  const release = publish.steps.findIndex((step) =>
    step.run?.includes("gh release create"),
  );
  assert.ok(metadata < publishImages && publishImages < release);
  assert.equal(
    publish.steps[publishImages].env.MAKE_LATEST,
    "${{ steps.metadata.outputs.make_latest }}",
  );
});
