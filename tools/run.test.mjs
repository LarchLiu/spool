import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { once } from "node:events";
import { existsSync } from "node:fs";
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  realpath,
  rm,
  stat,
  symlink,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { requireRunTemp } from "./test-scratch.mjs";

const runner = fileURLToPath(new URL("./run.sh", import.meta.url));
const bun = process.execPath;
const android = fileURLToPath(
  new URL("../android/tools/run.sh", import.meta.url),
);
const posix = { skip: process.platform === "win32" };

test(
  "run wrapper cleans successful and failed runs while preserving shared cache settings",
  posix,
  () => {
    for (const status of [0, 23]) {
      const result = spawnSync(
        "sh",
        [
          runner,
          "lifecycle-test",
          "bun",
          "-e",
          `
import {writeFileSync} from 'node:fs';
writeFileSync(process.env.TMPDIR + '/fixture', 'disposable');
console.log(JSON.stringify(Object.fromEntries(['SPOOL_RUN_ROOT','TMPDIR','TMP','TEMP','GOTMPDIR','GOCACHE','GRADLE_USER_HOME'].map(key => [key, process.env[key]]))));
process.exit(${status});
`,
        ],
        {
          encoding: "utf8",
          env: {
            ...process.env,
            GOCACHE: "caller-go-cache",
            GRADLE_USER_HOME: "caller-gradle-cache",
          },
        },
      );
      assert.equal(result.status, status, result.stderr);
      const state = JSON.parse(result.stdout);
      assert.equal(state.TMPDIR, join(state.SPOOL_RUN_ROOT, "tmp"));
      assert.equal(state.TMP, state.TMPDIR);
      assert.equal(state.TEMP, state.TMPDIR);
      assert.equal(state.GOTMPDIR, join(state.SPOOL_RUN_ROOT, "go"));
      assert.equal(state.GOCACHE, "caller-go-cache");
      assert.equal(state.GRADLE_USER_HOME, "caller-gradle-cache");
      assert.ok(!state.SPOOL_RUN_ROOT.includes("/build/tmp/"));
      assert.equal(existsSync(state.SPOOL_RUN_ROOT), false);
    }
  },
);

test(
  "Android entry point uses the same system-temp owner and cleans failed commands",
  posix,
  () => {
    const result = spawnSync(
      "sh",
      [android, "sh", "-c", 'printf "%s\\n" "$SPOOL_RUN_ROOT"; exit 19'],
      { encoding: "utf8" },
    );
    assert.equal(result.status, 19, result.stderr);
    const run = result.stdout.trim();
    assert.match(run, /[/\\]spool[/\\]android\./);
    assert.equal(existsSync(run), false);
  },
);

test(
  "wrapper and standalone scratch avoid a shared spool directory owned by another user",
  posix,
  async (t) => {
    const root = await realpath(
      await mkdtemp(join(tmpdir(), "spool-users-test-")),
    );
    const shared = join(root, "spool");
    await mkdir(shared);
    await writeFile(join(shared, "unowned"), "keep");
    await chmod(shared, 0o555);
    t.after(async () => {
      await chmod(shared, 0o755);
      await rm(root, { recursive: true, force: true });
    });
    const helper = fileURLToPath(
      new URL("./test-scratch.mjs", import.meta.url),
    );
    const commands = [
      [
        "sh",
        [
          runner,
          "users-test",
          bun,
          "-e",
          "console.log(process.env.SPOOL_RUN_ROOT)",
        ],
      ],
      [
        bun,
        [
          "-e",
          `import {createScratch} from ${JSON.stringify(helper)};
import {rm} from 'node:fs/promises';
const run = await createScratch('users-test');
try { console.log(run); } finally { await rm(run, {recursive:true,force:true}); }`,
        ],
      ],
    ];
    for (const [command, args] of commands) {
      const result = spawnSync(command, args, {
        env: { ...process.env, TMPDIR: root, TMP: root, TEMP: root },
        encoding: "utf8",
      });
      assert.equal(result.status, 0, result.stderr);
      const run = result.stdout.trim();
      assert.ok(run.startsWith(join(root, "spool-")));
      assert.match(run, /[/\\]spool[/\\]users-test\./);
      const owner = await stat(join(run, "../.."));
      assert.equal(owner.uid, process.getuid());
      assert.equal(owner.mode & 0o777, 0o700);
      assert.equal(existsSync(run), false);
    }
    assert.equal(await readFile(join(shared, "unowned"), "utf8"), "keep");
    assert.equal((await stat(shared)).mode & 0o777, 0o555);
  },
);

test(
  "wrapper refuses a symlink in place of its private user directory",
  posix,
  async (t) => {
    const root = await mkdtemp(join(tmpdir(), "spool-owner-test-"));
    t.after(() => rm(root, { recursive: true, force: true }));
    const unowned = join(root, "unowned");
    await mkdir(unowned);
    await writeFile(join(unowned, "keep"), "unowned");
    await symlink(unowned, join(root, "spool-" + process.getuid()));
    const result = spawnSync(
      "sh",
      [runner, "owner-test", bun, "-e", "process.exit(0)"],
      {
        env: { ...process.env, TMPDIR: root, TMP: root, TEMP: root },
        encoding: "utf8",
      },
    );
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /private directory owned by the current user/);
    assert.equal(await readFile(join(unowned, "keep"), "utf8"), "unowned");
  },
);

test(
  "wrapper waits out transient EPERM during descendant exit before cleaning scratch",
  posix,
  async (t) => {
    const root = await mkdtemp(join(tmpdir(), "spool-exit-test-"));
    t.after(() => rm(root, { recursive: true, force: true }));
    const preload = join(root, "exiting-process.mjs");
    const marker = join(root, "signals.json");
    await writeFile(
      preload,
      `import {writeFileSync} from 'node:fs';
const kill = process.kill, calls = [];
process.kill = function(pid, signal) {
  if (pid < 0 && calls.length < 2) {
    calls.push(signal);
    writeFileSync(${JSON.stringify(marker)}, JSON.stringify(calls));
    throw Object.assign(new Error('process is exiting'), {code:'EPERM'});
  }
  return kill.call(process, pid, signal);
};
`,
    );
    const result = spawnSync(
      bun,
      [
        "--preload",
        preload,
        fileURLToPath(new URL("./run.mjs", import.meta.url)),
        "exit-test",
        bun,
        "-e",
        "console.log(process.env.SPOOL_RUN_ROOT)",
      ],
      { encoding: "utf8" },
    );
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(JSON.parse(await readFile(marker, "utf8")), [
      0,
      "SIGTERM",
    ]);
    assert.equal(existsSync(result.stdout.trim()), false);
  },
);

for (const [interrupt, expectedCode] of [
  ["SIGINT", 130],
  ["SIGTERM", 143],
]) {
  test(
    `${interrupt} reaches owned children before their scratch directory is removed`,
    { ...posix, timeout: 15000 },
    async (t) => {
      const root = await mkdtemp(join(tmpdir(), "spool-run-signal-test-"));
      t.after(() => rm(root, { recursive: true, force: true }));
      const marker = join(root, "stopped.json");
      const child = spawn(
        "sh",
        [
          runner,
          "signal-test",
          "bun",
          "-e",
          `
import {spawn} from 'node:child_process';
import {existsSync, writeFileSync} from 'node:fs';
const grandchild = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)']);
const exited = new Promise(resolve => grandchild.once('exit', resolve));
for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, async () => {
  await exited;
  writeFileSync(process.argv[1], JSON.stringify({scratch_existed: existsSync(process.env.TMPDIR), grandchild: grandchild.pid}));
  process.exit(0);
});
console.log(JSON.stringify({run: process.env.SPOOL_RUN_ROOT, pid: process.pid}));
setInterval(() => {}, 1000);
`,
          marker,
        ],
        { stdio: ["ignore", "pipe", "pipe"] },
      );
      t.after(() => {
        if (child.exitCode === null) child.kill("SIGKILL");
      });
      let stderr = "";
      child.stderr.on("data", (data) => {
        stderr += data;
      });
      const closed = once(child, "close");
      const lines = createInterface({ input: child.stdout });
      const [line] = await once(lines, "line");
      const state = JSON.parse(line);
      child.kill(interrupt);
      const [code] = await closed;
      lines.close();
      assert.equal(code, expectedCode, stderr);
      const stopped = JSON.parse(await readFile(marker, "utf8"));
      assert.equal(stopped.scratch_existed, true);
      assert.equal(existsSync(state.run), false);
      for (const pid of [state.pid, stopped.grandchild]) {
        assert.throws(() => process.kill(pid, 0), { code: "ESRCH" });
      }
    },
  );
}

test(
  "browser scratch guard accepts owned system temp and rejects sibling and symlink escapes",
  posix,
  async (t) => {
    const root = await mkdtemp(join(tmpdir(), "spool-run-guard-test-"));
    t.after(() => rm(root, { recursive: true, force: true }));
    const run = join(root, "run"),
      temp = join(run, "tmp"),
      sibling = join(root, "run-other");
    await mkdir(temp, { recursive: true });
    await mkdir(sibling);
    assert.equal(requireRunTemp({ SPOOL_RUN_ROOT: run, TMPDIR: temp }), temp);
    assert.throws(() =>
      requireRunTemp({ SPOOL_RUN_ROOT: run, TMPDIR: sibling }),
    );
    assert.throws(() => requireRunTemp({ SPOOL_RUN_ROOT: run, TMPDIR: run }));
    await symlink(sibling, join(run, "escape"));
    assert.throws(() =>
      requireRunTemp({ SPOOL_RUN_ROOT: run, TMPDIR: join(run, "escape") }),
    );
  },
);
