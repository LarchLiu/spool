import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

const helper = resolve("tools/linux-dependencies.sh");
const python = execFileSync("sh", ["-c", "command -v python3"], { encoding: "utf8" }).trim();

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "linux-platform-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const osRelease = join(root, "os-release");
  const run = (body, env = {}) => spawnSync("/bin/sh", ["-s"], {
    input: `set -eu\n. "$HELPER"\n${body}\n`,
    env: { PATH: `${join(root, "bin")}:/usr/bin:/bin`, HELPER: helper, OS_RELEASE: osRelease, ...env },
    encoding: "utf8",
  });
  await mkdir(join(root, "bin"));
  await writeFile(join(root, "bin/python3"), `#!/bin/sh\nexec '${python}' "$@"\n`, { mode: 0o755 });
  return { root, osRelease, run };
}

test("os-release identifies distribution versions and derivatives without choosing by installed tools", async (t) => {
  const f = await fixture(t);
  for (const [data, expected] of [
    ['ID=ubuntu\nVERSION_ID="22.04"', "apt-get:ubuntu 22.04"],
    ['ID=ubuntu\nVERSION_ID="24.04"', "apt-get:ubuntu 24.04"],
    ['ID=debian\nVERSION_ID="12"', "apt-get:debian 12"],
    ['ID=debian\nVERSION_ID="13"', "apt-get:debian 13"],
    ['ID=linuxmint\nID_LIKE="ubuntu debian"\nPRETTY_NAME="Linux Mint 22"', "apt-get:Linux Mint 22"],
    ['ID=rocky\nID_LIKE="rhel centos fedora"\nVERSION_ID="9.5"', "dnf:rocky 9.5"],
    ['ID=fedora\nVERSION_ID=43', "dnf:fedora 43"],
    ['ID=arch', "pacman:arch"],
    ['ID=endeavouros\nID_LIKE=arch', "pacman:endeavouros"],
    ['ID=opensuse-leap\nVERSION_ID=15.6', "zypper:opensuse-leap 15.6"],
    ['ID=opensuse-tumbleweed', "zypper:opensuse-tumbleweed"],
    ['ID=custom\nID_LIKE="suse opensuse"', "zypper:custom"],
  ]) {
    await writeFile(f.osRelease, data);
    const result = f.run('spool_distribution "$OS_RELEASE"');
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout.trim(), expected);
  }
});

test("os-release parsing never evaluates command substitutions", async (t) => {
  const f = await fixture(t);
  await writeFile(f.osRelease, `ID=ubuntu\nPRETTY_NAME="$(exit 79) Ubuntu"\n`);
  const result = f.run('spool_distribution "$OS_RELEASE"');
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "apt-get:$(exit 79) Ubuntu");
});

test("library probing accepts either complete WebKit stack and either tray implementation", async (t) => {
  const f = await fixture(t);
  // Exercise the actual Python loader and shell fallbacks with a fake CDLL.
  // Each Python process has independent library state, just like real probes.
  await writeFile(join(f.root, "ctypes.py"), `import os\ndef CDLL(name):\n    if name not in os.environ['LIBRARIES'].split():\n        raise OSError(name)\n`);
  const base = "libgtk-3.so.0 libGL.so.1 libEGL.so.1 libxkbcommon.so.0";
  const modern = "libwebkit2gtk-4.1.so.0 libjavascriptcoregtk-4.1.so.0 libsoup-3.0.so.0";
  const legacy = "libwebkit2gtk-4.0.so.37 libjavascriptcoregtk-4.0.so.18 libsoup-2.4.so.1";
  for (const stack of [modern, legacy]) {
    for (const tray of ["libayatana-appindicator3.so.1", "libappindicator3.so.1"]) {
      const result = f.run("spool_runtime_ready", { PYTHONPATH: f.root, PYTHONDONTWRITEBYTECODE: "1", LIBRARIES: `${base} ${stack} ${tray}` });
      assert.equal(result.status, 0, result.stderr);
    }
  }
  for (const missing of ["libgtk-3.so.0", "libsoup-3.0.so.0", "libayatana-appindicator3.so.1"]) {
    const libraries = `${base} ${modern} libayatana-appindicator3.so.1`.split(" ").filter((name) => name !== missing).join(" ");
    const result = f.run("spool_runtime_ready", { PYTHONPATH: f.root, PYTHONDONTWRITEBYTECODE: "1", LIBRARIES: libraries });
    assert.notEqual(result.status, 0, missing);
  }
});
