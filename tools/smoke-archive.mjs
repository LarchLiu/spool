import { execFileSync } from "node:child_process";
import { resolve, win32 } from "node:path";

export function smokeTarCommand(platform = process.platform, env = process.env) {
  // Git Bash puts its MSYS tar on PATH. It cannot reliably consume the native
  // Windows paths returned by realpath(), so use Windows' own tar explicitly.
  return platform === "win32"
    ? win32.join(env.SystemRoot || env.SYSTEMROOT || env.windir || "C:\\Windows", "System32", "tar.exe")
    : "tar";
}

export function extractSmokeArchive(directory, filename, destination, run = execFileSync) {
  // Keep the archive name relative: GNU tar interprets drive letters as hosts.
  run(smokeTarCommand(), ["-xzf", filename, "-C", resolve(destination)], { cwd: resolve(directory) });
}
