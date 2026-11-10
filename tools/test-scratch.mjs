import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { realpathSync } from "node:fs";
import { lstat, mkdtemp, mkdir, realpath, rm } from "node:fs/promises";
import { tmpdir, userInfo } from "node:os";
import { isAbsolute, join, relative, sep } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

export function requireRunTemp(env = process.env) {
  const run = env.SPOOL_RUN_ROOT;
  const temp = env.TMPDIR;
  assert.ok(
    run && temp && isAbsolute(run) && isAbsolute(temp),
    "Run through sh tools/run.sh <task> <command>",
  );
  const child = relative(realpathSync(run), realpathSync(temp));
  assert.ok(
    child &&
      child !== ".." &&
      !child.startsWith(".." + sep) &&
      !isAbsolute(child),
    "TMPDIR must be inside the owned SPOOL_RUN_ROOT",
  );
  return temp;
}

export async function createScratch(task) {
  assert.match(task, /^[a-zA-Z0-9][a-zA-Z0-9_-]*$/);
  // /tmp can be shared by multiple users. Keep the spool namespace inside a
  // private user directory rather than letting the first user own /tmp/spool.
  const uid = process.getuid?.();
  const identity =
    uid ??
    createHash("sha256").update(userInfo().username).digest("hex").slice(0, 16);
  const userTemp = join(tmpdir(), "spool-" + identity);
  await mkdir(userTemp, { recursive: true, mode: 0o700 });
  const owner = await lstat(userTemp);
  assert.ok(
    owner.isDirectory() &&
      !owner.isSymbolicLink() &&
      (uid === undefined || (owner.uid === uid && (owner.mode & 0o077) === 0)),
    "Temporary namespace must be a private directory owned by the current user: " +
      userTemp,
  );
  const base = join(userTemp, "spool");
  await mkdir(base, { recursive: true });
  return realpath(await mkdtemp(join(base, task + ".")));
}

export async function removeScratch(path, { remove = rm, wait = delay } = {}) {
  // Windows can briefly keep executable/file handles locked after child close.
  // Retry here so cleanup does not depend on the runtime's fs.rm retry support.
  for (let attempt = 0; ; attempt++) {
    try {
      await remove(path, { recursive: true, force: true });
      return;
    } catch (error) {
      if (attempt >= 10 || !["EPERM", "EBUSY", "ENOTEMPTY"].includes(error.code)) {
        throw error;
      }
      await wait(100 * (attempt + 1));
    }
  }
}
