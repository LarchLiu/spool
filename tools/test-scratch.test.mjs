import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { existsSync } from "node:fs";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import test from "node:test";
import { createScratch, removeScratch } from "./test-scratch.mjs";

test("scratch cleanup removes nested files and tolerates an already removed run", async (t) => {
  const run = await createScratch("cleanup-test");
  t.after(() => removeScratch(run));
  await mkdir(join(run, "app"));
  await writeFile(join(run, "app", "binary"), "disposable");
  await removeScratch(run);
  assert.equal(existsSync(run), false);
  await removeScratch(run);
});

test("scratch cleanup retries transient Windows locks and partially removed directories", async (t) => {
  const run = await createScratch("cleanup-retry-test");
  t.after(() => removeScratch(run));
  await writeFile(join(run, "binary"), "locked executable");
  await writeFile(join(run, "removed-on-first-attempt"), "disposable");
  const errors = ["EPERM", "EBUSY", "ENOTEMPTY"];
  const waits = [];
  await removeScratch(run, {
    async remove(path, options) {
      if (errors.length) {
        await rm(join(run, "removed-on-first-attempt"), { force: true });
        assert.equal(await readFile(join(run, "binary"), "utf8"), "locked executable");
        throw Object.assign(new Error("temporarily locked"), { code: errors.shift() });
      }
      await rm(path, options);
    },
    wait: async (ms) => { waits.push(ms); },
  });
  assert.equal(existsSync(run), false);
  assert.equal(waits.length, 3);
  assert.ok(waits.every((ms) => ms > 0));
  assert.ok(waits[2] > waits[0]);
});

test("scratch cleanup stops retrying a persistent lock and preserves the error", async () => {
  const error = Object.assign(new Error("still locked"), { code: "EPERM", path: "locked-run" });
  let attempts = 0;
  let elapsed = 0;
  await assert.rejects(removeScratch(error.path, {
    remove: async () => { attempts++; throw error; },
    wait: async (ms) => { elapsed += ms; },
  }), (actual) => actual === error);
  assert.ok(attempts > 1 && attempts <= 11);
  assert.ok(elapsed > 0 && elapsed <= 6000);
});

test("scratch cleanup immediately propagates errors unrelated to transient locks", async () => {
  const error = Object.assign(new Error("I/O failure"), { code: "EIO" });
  let attempts = 0;
  await assert.rejects(removeScratch("failed-run", {
    remove: async () => { attempts++; throw error; },
    wait: async () => { assert.fail("unexpected retry"); },
  }), (actual) => actual === error);
  assert.equal(attempts, 1);
});

test("Windows scratch cleanup waits for another process to release its working directory", {
  skip: process.platform !== "win32", timeout: 10_000,
}, async (t) => {
  const run = await createScratch("cleanup-windows-test");
  const child = spawn(process.execPath, ["-e", `
process.stdin.once('data', () => process.exit(0));
console.log('ready');
`], { cwd: run, stdio: ["pipe", "pipe", "pipe"] });
  const closed = once(child, "close");
  let timer;
  t.after(async () => {
    clearTimeout(timer);
    if (child.exitCode === null) child.kill("SIGKILL");
    await closed;
    await removeScratch(run);
  });
  await once(child.stdout, "data");
  await assert.rejects(rm(run, { recursive: true, force: true }),
    (error) => ["EPERM", "EBUSY", "ENOTEMPTY"].includes(error.code));
  timer = setTimeout(() => child.stdin.end("release"), 250);
  await removeScratch(run);
  await closed;
  assert.equal(existsSync(run), false);
});
