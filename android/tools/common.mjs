import { spawnSync } from "node:child_process";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import { homedir } from "node:os";
import { parseArgs } from "node:util";
export { requireRunTemp as requireRunTmp } from "../../tools/test-scratch.mjs";
export const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
export const SDK =
  process.env.ANDROID_HOME ||
  process.env.ANDROID_SDK_ROOT ||
  join(homedir(), "Library/Android/sdk");
export const sleep = (seconds) => Bun.sleepSync(seconds * 1000);
export const now = () => performance.now() / 1000;
export const json = (path) => JSON.parse(readFileSync(path, "utf8"));
export function writeJson(path, value) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, JSON.stringify(value, null, 2) + "\n");
}
export function command(
  executable,
  args = [],
  { check = true, text = true, timeout = 60000, ...options } = {},
) {
  const result = spawnSync(String(executable), args.map(String), {
    encoding: text ? "utf8" : undefined,
    timeout,
    maxBuffer: 256 * 1024 * 1024,
    ...options,
  });
  if (result.error || (check && result.status !== 0))
    throw new Error(
      result.error?.message ||
        String(result.stderr || result.stdout).trim() ||
        `${executable} exited ${result.status}`,
    );
  return {
    ...result,
    stdout: result.stdout ?? (text ? "" : Buffer.alloc(0)),
    stderr: result.stderr ?? (text ? "" : Buffer.alloc(0)),
  };
}
export function cli(options, { positionals = false, required = [] } = {}) {
  const result = parseArgs({
    options: { help: { type: "boolean", short: "h" }, ...options },
    allowPositionals: positionals,
  });
  if (result.values.help) {
    console.log(
      `Usage: bun ${process.argv[1]} ${Object.keys(options)
        .map((key) => "--" + key)
        .join(" ")}`,
    );
    process.exit(0);
  }
  for (const key of required)
    if (!result.values[key]) throw new Error("--" + key + " is required");
  return result;
}
export function serialArgs() {
  const { values } = cli(
    { serial: { type: "string" } },
    { required: ["serial"] },
  );
  if (!/^[A-Za-z0-9_.:-]+$/.test(values.serial))
    throw new Error("Invalid adb serial");
  return values;
}
export const quote = (value) =>
  "'" + String(value).replaceAll("'", "'\\''") + "'";
