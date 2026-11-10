import { execFileSync } from "node:child_process";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { releaseVersion } from "./prepare-release.mjs";
import { releaseTargets, serverArchiveName } from "./release-assets.mjs";
import { createScratch } from "./test-scratch.mjs";

export async function packageServer(target, env = process.env) {
  if (!releaseTargets.includes(target)) throw new Error(`Unsupported server target: ${target}`);
  const version = releaseVersion(env.RELEASE_TAG);
  const metadata = JSON.parse(await readFile("mygo.json", "utf8"));
  if (metadata.version !== version) throw new Error("Server version does not match the packaging metadata");
  const root = resolve(".");
  const output = join(root, "build", target);
  await mkdir(output, { recursive: true });
  const run = await createScratch("package-server");
  try {
    const payload = join(run, "payload");
    await Promise.all(["tmp", "go", "payload"].map((name) => mkdir(join(run, name))));
    const [platform, arch] = target.split("-");
    const binary = platform === "windows" ? "spool-server.exe" : "spool-server";
    const buildEnv = {
      ...env, GOOS: platform, GOARCH: arch, CGO_ENABLED: "0",
      GOTMPDIR: join(run, "go"), TMPDIR: join(run, "tmp"), TMP: join(run, "tmp"), TEMP: join(run, "tmp"),
    };
    execFileSync("go", ["build", "-trimpath", "-ldflags", "-s -w -X main.production=1", "-o", join(payload, binary), "./cmd/server"], { env: buildEnv, stdio: "inherit" });
    await writeFile(join(payload, "README.txt"), `Spool Server ${version} (${target})\n\nRun ./${binary} --help to see all commands.\nRun ./${binary} help project member set for command-specific options.\nNo GUI, GTK, WebKit or voxl is required. Web/PWA assets are embedded.\nInstall and authenticate Codex/Claude as the same OS user running the service.\nAdministrative commands require the service to be stopped.\nProduction data defaults to ~/.spool; use --data-dir or SPOOL_HOME to change it.\n\nLinux: see spool-server.service for a systemd user service example.\nFull instructions: docs/server-cli.md in the source repository.\n`);
    await writeFile(join(payload, "server-cli.md"), await readFile("docs/server-cli.md"));
    const entries = [binary, "README.txt", "server-cli.md"];
    if (platform === "linux") {
      await writeFile(join(payload, "spool-server.service"), await readFile("deploy/spool-server.service"));
      entries.push("spool-server.service");
    }
    execFileSync("tar", ["-czf", join(output, serverArchiveName(version, target)), "--", ...entries], {
      cwd: payload, stdio: "inherit", env: { ...buildEnv, COPYFILE_DISABLE: "1", COPY_EXTENDED_ATTRIBUTES_DISABLE: "1" },
    });
  } finally {
    await rm(run, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  if (process.argv.length !== 3) throw new Error("Usage: bun tools/package-server.mjs <os-arch>");
  await packageServer(process.argv[2]);
}
