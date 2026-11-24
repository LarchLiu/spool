import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { appendFileSync, existsSync } from "node:fs";

const packages = [
  "libgtk-3-0t64", "libwebkit2gtk-4.1-0", "libgl1", "libegl1",
  "libgl1-mesa-dri", "libxkbcommon0", "xvfb", "xauth", "dbus-x11",
];
const options = [
  "-o", "Acquire::Retries=2",
  "-o", "Acquire::http::Timeout=20",
  "-o", "Acquire::https::Timeout=20",
  "-o", "Acquire::Languages=none",
];
const archives = "/var/cache/apt/archives";
const mirrorFile = process.env.SPOOL_APT_MIRRORS_FILE || "/etc/apt/apt-mirrors.txt";
const officialMirrors = [
  "https://archive.ubuntu.com/ubuntu/",
  "https://security.ubuntu.com/ubuntu/",
];
const digest = (value) => createHash("sha256").update(value).digest("hex");

function run(command, args, capture = false, input) {
  const result = spawnSync(command, args, {
    env: { ...process.env, LC_ALL: "C", DEBIAN_FRONTEND: "noninteractive" },
    encoding: "utf8",
    input,
    stdio: [input === undefined ? "ignore" : "pipe", capture ? "pipe" : "inherit", "inherit"],
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} exited with ${result.status}`);
  return result.stdout?.trim();
}

function apt(args, capture = false, seconds) {
  // Run timeout as root so it can stop APT and its download workers before a
  // retry. Only network phases are bounded; dpkg is never interrupted this way.
  const timeout = seconds ? ["timeout", "--signal=TERM", "--kill-after=5s", `${seconds}s`] : [];
  return run("sudo", [...timeout, "env", "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive", "apt-get", ...options, ...args], capture);
}

function networkApt(args, arch, seconds) {
  // Hosted amd64 runners use this mirror list for every Ubuntu suite. Keep its
  // stable URI (and existing package indexes), but remove the slow cloud mirror.
  // arm64 runners use Ubuntu Ports directly and keep their source configuration.
  const mirrors = arch === "amd64" && existsSync(mirrorFile) ? officialMirrors : [];
  for (let attempt = 0; attempt < Math.max(1, mirrors.length); attempt++) {
    if (mirrors.length) {
      const order = [...mirrors.slice(attempt), ...mirrors.slice(0, attempt)];
      run("sudo", ["tee", mirrorFile], true, order.map((url, index) => `${url}\tpriority:${index + 1}\n`).join(""));
      console.log(`APT network attempt ${attempt + 1}: ${order[0]} (${seconds}s budget)`);
    }
    try {
      return apt(args, false, seconds);
    } catch (error) {
      if (attempt + 1 >= mirrors.length) throw error;
      console.warn(`APT network attempt failed or exceeded ${seconds}s; switching official mirror and reusing downloaded archives.`);
    }
  }
}

switch (process.argv[2]) {
  case "prepare": {
    if (!process.env.GITHUB_OUTPUT) throw new Error("GITHUB_OUTPUT is required");
    const arch = run("dpkg", ["--print-architecture"], true);
    networkApt(["-o", "APT::Update::Error-Mode=any", "update"], arch, 45);
    const plan = apt(["--print-uris", "--download-only", "--yes", "--no-install-recommends", "install", ...packages], true);
    // Ignore mirror URLs and progress text; filenames include resolved versions
    // and architectures. Size and checksum also identify the expected archives.
    const files = plan.split("\n")
      .filter((line) => line.startsWith("'"))
      .map((line) => line.replace(/^'[^']*'\s+/, ""))
      .sort();
    const distribution = run("lsb_release", ["-is"], true).toLowerCase();
    const version = run("lsb_release", ["-rs"], true);
    const prefix = `linux-native-v1-${distribution}-${version}-${arch}-${digest(packages.join("\n"))}-`;
    // Cache only readable .deb files, excluding APT's lock and private partial/.
    // The cache action restores as the runner user; APT still installs as root.
    run("sudo", ["chown", `${process.getuid()}:${process.getgid()}`, archives]);
    appendFileSync(process.env.GITHUB_OUTPUT, [
      `cache-path=${archives}/*.deb`,
      `cache-prefix=${prefix}`,
      `cache-key=${prefix}${digest(files.join("\n"))}`,
      `cache-needed=${files.length > 0}`,
      "",
    ].join("\n"));
    break;
  }
  case "install": {
    const arch = run("dpkg", ["--print-architecture"], true);
    const install = ["-o", "APT::Keep-Downloaded-Packages=true", "install", "--yes", "--no-install-recommends", ...packages];
    // A trickle of data defeats HTTP inactivity timeouts. Bound total download
    // time, then retry another mirror without discarding completed archives.
    networkApt(["--download-only", ...install], arch, 70);
    // A fresh VM still needs dpkg setup on cache hits. Start it only once all
    // archives are present, and forbid any downloads during package installation.
    apt(["--no-download", ...install]);
    break;
  }
  default:
    throw new Error("Usage: bun tools/linux-smoke-runtime.mjs prepare|install");
}
