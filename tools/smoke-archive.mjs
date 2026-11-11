import { execFileSync } from "node:child_process";
import { closeSync, openSync } from "node:fs";
import { resolve, win32 } from "node:path";

export function smokeTarCommand(platform = process.platform, env = process.env) {
  // Git Bash puts its MSYS tar on PATH. It cannot reliably consume the native
  // Windows paths returned by realpath(), so use Windows' own tar explicitly.
  return platform === "win32"
    ? win32.join(env.SystemRoot || env.SYSTEMROOT || env.windir || "C:\\Windows", "System32", "tar.exe")
    : "tar";
}

export function extractSmokeArchive(directory, filename, destination, run = execFileSync) {
  const archive = resolve(directory, filename);
  const input = openSync(archive, "r");
  try {
    // Windows tar can lose Unicode in command-line paths. Open the archive in
    // Bun and pass its handle; CreateProcess receives the working directory as
    // UTF-16, so neither the archive nor destination needs a tar path argument.
    run(smokeTarCommand(), ["-xzf", "-"], { cwd: resolve(destination), stdio: [input, "pipe", "pipe"] });
  } catch (error) {
    throw new Error(`Could not extract ${archive} into ${resolve(destination)}: ${error.message}`, { cause: error });
  } finally {
    closeSync(input);
  }
}
