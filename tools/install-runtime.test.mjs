import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

const helper = resolve("tools/install-runtime.sh");
const node = spawnSync("sh", ["-c", "command -v node"], { encoding: "utf8" }).stdout?.trim();

async function fixture(t, { bun = true, nodeRuntime = false, brokenBun = false } = {}) {
  const root = await mkdtemp(join(tmpdir(), "installer-runtime-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const bin = join(root, "bin"), calls = join(root, "calls");
  await mkdir(bin);
  for (const [name, path] of [["bun", bun && process.execPath], ["node", nodeRuntime && node]]) {
    if (path) await writeFile(join(bin, name), `#!/bin/sh\nprintf '${name}\\n' >> "$CALLS"\n${name === "bun" && brokenBun ? "exit 1" : `exec '${path}' "$@"`}\n`, { mode: 0o755 });
  }
  const run = () => spawnSync("/bin/sh", ["-s"], {
    input: `set -eu
. "$HELPER"
fail() { echo "$*" >&2; exit 1; }
fetch() { echo 'unexpected download' >&2; exit 79; }
target=linux-amd64
spool_prepare_runtime
spool_js -e 'console.log(process.versions.bun ? "bun" : "node")'
`,
    env: { PATH: bin, HOME: join(root, "home"), CALLS: calls, HELPER: helper },
    encoding: "utf8",
  });
  return { run, calls: () => readFile(calls, "utf8") };
}

test("installer prefers usable Bun when both runtime commands exist", async (t) => {
  const f = await fixture(t, { nodeRuntime: true });
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "bun");
  assert.doesNotMatch(await f.calls(), /node/);
});

test("installer reuses Node without downloading Bun", { skip: !node }, async (t) => {
  const f = await fixture(t, { bun: false, nodeRuntime: true });
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "node");
});

test("an unusable Bun falls back to Node", { skip: !node }, async (t) => {
  const f = await fixture(t, { nodeRuntime: true, brokenBun: true });
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "node");
  assert.match(await f.calls(), /^bun\nnode\n/);
});

test("bootstrap explains the unzip prerequisite before downloading anything", async (t) => {
  const f = await fixture(t, { bun: false });
  const result = f.run();
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /install unzip to bootstrap Bun/);
  assert.doesNotMatch(result.stderr, /unexpected download/);
});
