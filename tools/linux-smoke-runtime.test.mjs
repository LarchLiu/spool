import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

const script = resolve("tools/linux-smoke-runtime.mjs");
const plan = [
  "Reading package lists...",
  "'http://mirror.example/ubuntu/libwebkit_1_amd64.deb' libwebkit_1_amd64.deb 123 SHA256:abc",
  "'http://mirror.example/ubuntu/libegl_2_amd64.deb' libegl_2_amd64.deb 456 SHA256:def",
].join("\n");

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "linux-smoke-runtime-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const bin = join(root, "bin"), output = join(root, "outputs"), calls = join(root, "calls");
  const mirrors = join(root, "apt-mirrors.txt");
  await mkdir(bin);
  const mock = join(root, "mock.mjs");
  await writeFile(mock, `import { appendFileSync, readFileSync, writeFileSync } from "node:fs";
import { basename } from "node:path";
let command = basename(process.argv[2]), args = process.argv.slice(3);
const env = { ...process.env };
let budget;
if (command === "sudo") {
  command = args.shift();
  if (command === "timeout") {
    if (args.shift() !== "--signal=TERM" || args.shift() !== "--kill-after=5s") throw new Error("Expected graceful process-group timeout");
    budget = args.shift();
    command = args.shift();
  }
  if (command === "env") {
    while (args[0]?.includes("=")) {
      const at = args[0].indexOf("=");
      env[args[0].slice(0, at)] = args.shift().slice(at + 1);
    }
    command = args.shift();
  }
}
const mirrors = readFileSync(env.SPOOL_APT_MIRRORS_FILE, "utf8");
appendFileSync(env.CALLS, JSON.stringify({command, args, budget, mirrors, frontend: env.DEBIAN_FRONTEND, locale: env.LC_ALL}) + "\\n");
if (command === "apt-get") {
  const phase = args.includes("--print-uris") ? "plan" : args.includes("update") ? "update" : args.includes("--download-only") ? "download" : "install";
  if (env.FAIL_PHASE === phase) process.exit(42);
  if (env.FAIL_PRIMARY_PHASE === phase && mirrors.startsWith("https://archive.ubuntu.com/")) process.exit(Number(env.FAIL_STATUS || 124));
  if (phase === "plan") console.log(env.PLAN);
} else if (command === "lsb_release") console.log(args[0] === "-is" ? "Ubuntu" : env.DISTRO_VERSION);
else if (command === "dpkg") console.log(env.ARCH);
else if (command === "tee") writeFileSync(args[0], readFileSync(0, "utf8"));
else if (command !== "chown") throw new Error("Unexpected host command: " + command);
`);
  for (const command of ["sudo", "lsb_release", "dpkg"]) {
    await writeFile(join(bin, command), `#!/bin/sh\nexec '${process.execPath.replaceAll("'", "'\\''")}' '${mock.replaceAll("'", "'\\''")}' "$0" "$@"\n`, { mode: 0o755 });
  }
  return {
    async run(phase, env = {}) {
      await writeFile(output, "");
      await writeFile(calls, "");
      await writeFile(mirrors, "http://us-east-2.ec2.archive.ubuntu.com/ubuntu/\tpriority:1\nhttps://archive.ubuntu.com/ubuntu/\tpriority:2\n");
      const result = spawnSync(process.execPath, [script, phase], {
        env: { ...process.env, PATH: bin, CALLS: calls, SPOOL_APT_MIRRORS_FILE: mirrors, GITHUB_OUTPUT: output, PLAN: plan, ARCH: "amd64", DISTRO_VERSION: "24.04", ...env },
        encoding: "utf8",
      });
      return {
        ...result,
        outputs: Object.fromEntries((await readFile(output, "utf8")).trim().split("\n").filter(Boolean).map((line) => line.split("="))),
        calls: (await readFile(calls, "utf8")).trim().split("\n").filter(Boolean).map((line) => JSON.parse(line)),
      };
    },
  };
}

test("Linux runtime cache keys follow resolved versions, independently of mirror and download order", async (t) => {
  const f = await fixture(t);
  const first = await f.run("prepare");
  assert.equal(first.status, 0, first.stderr);
  assert.equal(first.outputs["cache-path"], "/var/cache/apt/archives/*.deb");
  assert.equal(first.outputs["cache-needed"], "true");
  const reordered = await f.run("prepare", { PLAN: plan.replaceAll("http://mirror.example", "https://another.example").split("\n").reverse().join("\n") });
  assert.equal(reordered.status, 0, reordered.stderr);
  assert.equal(reordered.outputs["cache-key"], first.outputs["cache-key"]);
  const updated = await f.run("prepare", { PLAN: plan.replaceAll("libwebkit_1_", "libwebkit_3_") });
  assert.equal(updated.status, 0, updated.stderr);
  assert.notEqual(updated.outputs["cache-key"], first.outputs["cache-key"]);
  assert.equal(updated.outputs["cache-prefix"], first.outputs["cache-prefix"]);
  for (const env of [{ ARCH: "arm64" }, { DISTRO_VERSION: "26.04" }]) {
    const other = await f.run("prepare", env);
    assert.equal(other.status, 0, other.stderr);
    assert.notEqual(other.outputs["cache-prefix"], first.outputs["cache-prefix"]);
  }
  const apt = first.calls.filter((call) => call.command === "apt-get");
  assert.equal(apt.length, 2);
  assert.ok(apt[0].args.includes("update"));
  assert.ok(apt[1].args.includes("--print-uris"));
  assert.ok(apt[1].args.includes("--download-only"));
  assert.ok(apt.every((call) => call.frontend === "noninteractive" && call.locale === "C"));
});

test("cold Linux runtime installation uses official mirrors, downloads first and installs offline", async (t) => {
  const f = await fixture(t);
  const result = await f.run("install");
  assert.equal(result.status, 0, result.stderr);
  const apt = result.calls.filter((call) => call.command === "apt-get");
  assert.equal(apt.length, 2);
  assert.ok(apt[0].args.includes("--download-only"));
  assert.equal(apt[0].budget, "70s");
  assert.ok(apt[1].args.includes("--no-download"));
  assert.equal(apt[1].budget, undefined, "never interrupt dpkg with a network timeout");
  for (const { args, mirrors, frontend } of apt) {
    assert.equal(frontend, "noninteractive");
    assert.ok(args.includes("APT::Keep-Downloaded-Packages=true"));
    assert.ok(args.includes("install"));
    assert.ok(mirrors.startsWith("https://archive.ubuntu.com/ubuntu/\tpriority:1"));
    assert.ok(!mirrors.includes("ec2.archive"));
    for (const name of ["libgtk-3-0t64", "libwebkit2gtk-4.1-0", "libgl1", "libegl1", "libgl1-mesa-dri", "libxkbcommon0", "xvfb", "xauth", "dbus-x11"]) assert.ok(args.includes(name), name);
  }
});

test("slow or failing cold downloads switch official mirrors before starting dpkg", async (t) => {
  const f = await fixture(t);
  for (const status of [124, 100]) {
    const result = await f.run("install", { FAIL_PRIMARY_PHASE: "download", FAIL_STATUS: String(status) });
    assert.equal(result.status, 0, result.stderr);
    const apt = result.calls.filter((call) => call.command === "apt-get");
    assert.equal(apt.length, 3);
    assert.ok(apt[0].mirrors.startsWith("https://archive.ubuntu.com/"));
    assert.ok(apt[1].mirrors.startsWith("https://security.ubuntu.com/"));
    assert.ok(apt.slice(0, 2).every((call) => call.args.includes("--download-only") && call.budget === "70s"));
    assert.ok(apt[2].args.includes("--no-download"));
    assert.ok(result.calls.every((call) => !["rm", "clean", "autoclean"].includes(call.command)));
    assert.match(result.stderr, /switching official mirror/);
  }
  const failed = await f.run("install", { FAIL_PHASE: "download" });
  assert.notEqual(failed.status, 0);
  assert.ok(failed.calls.filter((call) => call.command === "apt-get").every((call) => call.args.includes("--download-only")));
});

test("index refresh has bounded mirror failover while ARM sources stay intact", async (t) => {
  const f = await fixture(t);
  const result = await f.run("prepare", { FAIL_PRIMARY_PHASE: "update" });
  assert.equal(result.status, 0, result.stderr);
  const updates = result.calls.filter((call) => call.command === "apt-get" && call.args.includes("update"));
  assert.equal(updates.length, 2);
  assert.ok(updates.every((call) => call.budget === "45s" && call.args.includes("APT::Update::Error-Mode=any")));
  assert.ok(updates[1].mirrors.startsWith("https://security.ubuntu.com/"));
  const arm = await f.run("prepare", { ARCH: "arm64" });
  assert.equal(arm.status, 0, arm.stderr);
  assert.ok(arm.calls.every((call) => call.command !== "tee"));
});

test("Linux runtime preparation skips empty archive caches and propagates APT failures", async (t) => {
  const f = await fixture(t);
  const empty = await f.run("prepare", { PLAN: "0 upgraded, 0 newly installed." });
  assert.equal(empty.status, 0, empty.stderr);
  assert.equal(empty.outputs["cache-needed"], "false");
  for (const phase of ["update", "plan"]) {
    const failed = await f.run("prepare", { FAIL_PHASE: phase });
    assert.notEqual(failed.status, 0);
    assert.deepEqual(failed.outputs, {});
    assert.ok(failed.calls.every((call) => call.command !== "chown"));
  }
  assert.notEqual((await f.run("install", { FAIL_PHASE: "install" })).status, 0);
});

test("build and release share native package caches and save before desktop startup", async () => {
  const pipelines = [];
  for (const name of ["build", "release"]) {
    const workflow = Bun.YAML.parse(await readFile(resolve(`.github/workflows/${name}.yml`), "utf8"));
    const steps = workflow.jobs.smoke.steps;
    const prepare = steps.find((step) => step.id === "linux-runtime");
    const restore = steps.find((step) => step.id === "linux-runtime-cache");
    const install = steps.find((step) => step.name === "Install Linux native runtime");
    const save = steps.find((step) => step.uses?.startsWith("actions/cache/save@"));
    const launch = steps.find((step) => step.name === "Launch packaged desktop and probe Web service");
    assert.ok(steps.indexOf(prepare) < steps.indexOf(restore));
    assert.ok(steps.indexOf(restore) < steps.indexOf(install));
    assert.ok(steps.indexOf(install) < steps.indexOf(save));
    assert.ok(steps.indexOf(save) < steps.indexOf(launch));
    assert.equal(install.if, "runner.os == 'Linux'");
    assert.equal(save.with.key, restore.with.key);
    assert.equal(save.with.path, restore.with.path);
    assert.ok(save.if.includes("steps.linux-runtime-cache.outputs.cache-hit != 'true'"));
    for (const action of [restore, save]) assert.match(action.uses, /@[a-f0-9]{40}$/);
    pipelines.push([prepare, restore, install, save]);
  }
  assert.deepEqual(pipelines[0], pipelines[1]);
});
