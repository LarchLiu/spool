// Run through tools/run.sh: download a pinned SDK into disposable scratch and
// compare its local listSessions output with the Go implementation.
import { join } from "node:path";

const runRoot = process.env.SPOOL_RUN_ROOT;
if (!runRoot) throw new Error("Run with sh tools/run.sh claude-list-parity bun tools/check-claude-session-list.mjs");

const version = "0.3.326";
// dist.integrity of this version in the npm registry. Update it with the version.
const integrity = "sha512-BUTSky/4YWfL/MxC+4kV0NDbJFjbJPoQMFEk/s9G6hV9LUfYPMbsWz94bFQ7ZIo2ubj2xwJ2c2NVxkNlQhVw+Q==";
const archive = join(runRoot, "claude-agent-sdk.tgz");
const response = await fetch(
  `https://registry.npmjs.org/@anthropic-ai/claude-agent-sdk/-/claude-agent-sdk-${version}.tgz`,
);
if (!response.ok) throw new Error(`SDK download failed: ${response.status}`);
const tarball = new Uint8Array(await response.arrayBuffer());
// The package is executed below, so never extract an unverified download.
const actual = "sha512-" + new Bun.CryptoHasher("sha512").update(tarball).digest("base64");
if (actual !== integrity) throw new Error(`SDK integrity mismatch: expected ${integrity}, got ${actual}`);
await Bun.write(archive, tarball);

async function run(argv, env = process.env) {
  const child = Bun.spawn(argv, { env, stdout: "inherit", stderr: "inherit" });
  const code = await child.exited;
  if (code !== 0) throw new Error(`${argv[0]} exited with status ${code}`);
}

await run(["tar", "-xzf", archive, "-C", runRoot]);
await run(["go", "test", "./internal/agent", "-run", "TestClaude(SessionList|Archive)", "-count=1"], {
  ...process.env,
  SPOOL_CLAUDE_SDK_MJS: join(runRoot, "package", "sdk.mjs"),
});
