#!/usr/bin/env bun
// Own a command's temporary files and process group until both are cleaned up.
import { spawn } from "node:child_process";
import { mkdirSync, rmSync } from "node:fs";
import { constants } from "node:os";
import { join } from "node:path";
import { createScratch } from "./test-scratch.mjs";

const [task, command, ...args] = process.argv.slice(2);
if (!task || !/^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(task) || !command) {
  console.error("Usage: sh tools/run.sh <task-name> <command> [args...]");
  process.exit(2);
}
const run = await createScratch(task);
let child, interrupted, killTimer;
const signal = (name) => {
  try {
    process.kill(-child.pid, name);
  } catch (error) {
    if (!["ESRCH", "EPERM"].includes(error.code)) throw error;
  }
};
const alive = () => {
  try {
    process.kill(-child.pid, 0);
    return true;
  } catch (error) {
    if (error.code === "ESRCH") return false;
    // macOS can report EPERM while an esbuild/browser descendant is exiting.
    // Keep waiting for the group instead of failing before cleanup can finish.
    if (error.code === "EPERM") return true;
    throw error;
  }
};
for (const name of ["SIGINT", "SIGTERM"])
  process.on(name, () => {
    interrupted = name;
    if (child?.pid) {
      signal(name);
      killTimer ??= setTimeout(() => {
        if (alive()) signal("SIGKILL");
      }, 5000);
    }
  });
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
let code = 1;
try {
  for (const name of ["tmp", "go"]) mkdirSync(join(run, name));
  const env = {
    ...process.env,
    SPOOL_RUN_ROOT: run,
    GOTMPDIR: join(run, "go"),
  };
  for (const name of ["TMPDIR", "TMP", "TEMP"]) env[name] = join(run, "tmp");
  child = spawn(command, args, { env, stdio: "inherit", detached: true });
  const completed = new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (status, sig) =>
      resolve(status ?? 128 + constants.signals[sig]),
    );
  });
  code = await completed;
} catch (error) {
  console.error(error.message);
} finally {
  clearTimeout(killTimer);
  if (child?.pid && alive()) {
    signal("SIGTERM");
    let deadline = Date.now() + 5000;
    while (alive() && Date.now() < deadline) await sleep(50);
    if (alive()) {
      signal("SIGKILL");
      deadline = Date.now() + 1000;
      while (alive() && Date.now() < deadline) await sleep(50);
    }
    if (alive())
      throw new Error(
        "Owned process group did not stop; scratch retained at " + run,
      );
  }
  rmSync(run, { recursive: true, force: true });
}
process.exitCode = interrupted ? 128 + constants.signals[interrupted] : code;
