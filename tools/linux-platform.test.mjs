import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import {
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

const helper = resolve("tools/linux-dependencies.sh");
const bun = process.execPath;

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "linux-platform-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const osRelease = join(root, "os-release");
  const run = (body, env = {}) =>
    spawnSync("/bin/sh", ["-s"], {
      input: `set -eu\n. "$HELPER"\n${body}\n`,
      env: {
        PATH: `${join(root, "bin")}:/usr/bin:/bin`,
        HELPER: helper,
        OS_RELEASE: osRelease,
        ...env,
      },
      encoding: "utf8",
    });
  await mkdir(join(root, "bin"));
  await writeFile(join(root, "bin/bun"), `#!/bin/sh\nexec '${bun}' "$@"\n`, {
    mode: 0o755,
  });
  return { root, osRelease, run };
}

test("os-release identifies distribution versions and derivatives without choosing by installed tools", async (t) => {
  const f = await fixture(t);
  for (const [data, expected] of [
    ['ID=ubuntu\nVERSION_ID="22.04"', "apt-get:ubuntu 22.04"],
    ['ID=ubuntu\nVERSION_ID="24.04"', "apt-get:ubuntu 24.04"],
    ['ID=debian\nVERSION_ID="12"', "apt-get:debian 12"],
    ['ID=debian\nVERSION_ID="13"', "apt-get:debian 13"],
    [
      'ID=linuxmint\nID_LIKE="ubuntu debian"\nPRETTY_NAME="Linux Mint 22"',
      "apt-get:Linux Mint 22",
    ],
    [
      'ID=rocky\nID_LIKE="rhel centos fedora"\nVERSION_ID="9.5"',
      "dnf:rocky 9.5",
    ],
    ["ID=fedora\nVERSION_ID=43", "dnf:fedora 43"],
    ["ID=arch", "pacman:arch"],
    ["ID=endeavouros\nID_LIKE=arch", "pacman:endeavouros"],
    ["ID=opensuse-leap\nVERSION_ID=15.6", "zypper:opensuse-leap 15.6"],
    ["ID=opensuse-tumbleweed", "zypper:opensuse-tumbleweed"],
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

test(
  "Bun FFI probes actual shared libraries and rejects missing libraries",
  { skip: process.platform === "win32" },
  async (t) => {
    const f = await fixture(t);
    const library =
      process.platform === "darwin"
        ? "/usr/lib/libSystem.B.dylib"
        : "libc.so.6";
    const present = f.run('spool_load_libraries "$LIBRARY"', {
      LIBRARY: library,
    });
    assert.equal(present.status, 0, present.stderr);
    const missing = f.run('spool_load_libraries "$LIBRARY" "$MISSING"', {
      LIBRARY: library,
      MISSING: join(f.root, "missing-library.so"),
    });
    assert.notEqual(missing.status, 0);
  },
);

test("clang probing forwards its mode and searches custom library files and symlinks", async (t) => {
  const f = await fixture(t);
  const loader = join(f.root, "loader.mjs");
  const attempts = join(f.root, "attempts");
  // Keep the actual Bun CLI, argument parsing, and filesystem search. Only the
  // loader outcomes are controlled so installed host libclang cannot mask a miss.
  await writeFile(
    loader,
    `import {appendFileSync} from 'node:fs';
export const ptr = value => value;
export const dlopen = () => ({
    symbols: {
      dlopen(bytes) {
        const name = bytes.toString('utf8', 0, bytes.length - 1);
        appendFileSync(process.env.ATTEMPTS, name + '\\n');
        return name === process.env.ACCEPTED_LIBRARY ? 1 : null;
      },
      dlclose() {},
    },
    close() {},
  });
`,
  );
  const shim = join(f.root, "bun-shim.mjs");
  await writeFile(
    shim,
    `import {spawnSync} from 'node:child_process';
const args = process.argv.slice(2);
if (args[0] === '-e') args[1] = args[1].replaceAll('"bun:ffi"', ${JSON.stringify(JSON.stringify(loader))});
const result = spawnSync(process.execPath, args, {stdio:'inherit'});
process.exit(result.status ?? 1);
`,
  );
  await writeFile(
    join(f.root, "bin/bun"),
    `#!/bin/sh\nexec '${bun}' '${shim}' "$@" 2>"$ATTEMPTS.stderr"\n`,
    { mode: 0o755 },
  );
  const library = join(f.root, "library.so");
  const directory = join(f.root, "libraries");
  const link = join(directory, "libclang.so.1");
  await writeFile(library, "synthetic shared library");
  await mkdir(directory);
  await symlink(library, link);
  for (const [custom, accepted, status] of [
    ["", "", 1],
    [join(f.root, "missing"), "", 1],
    [library, "", 1],
    [library, library, 0],
    [directory, link, 0],
  ]) {
    await writeFile(attempts, "");
    const result = f.run("spool_load_libraries --clang", {
      LIBCLANG_PATH: custom,
      ACCEPTED_LIBRARY: accepted,
      ATTEMPTS: attempts,
    });
    const diagnostics =
      result.stderr + (await readFile(attempts + ".stderr", "utf8"));
    assert.equal(result.status, status, diagnostics);
    const searched = (await readFile(attempts, "utf8")).trim().split("\n");
    if (status === 0)
      assert.ok(searched.includes(accepted), JSON.stringify(searched));
    else
      assert.ok(
        searched.includes("libclang.so"),
        "a failed probe must search libraries before returning: " +
          JSON.stringify(searched) +
          diagnostics,
      );
  }
});

test("library probing accepts either complete WebKit stack and either tray implementation", async (t) => {
  const f = await fixture(t);
  // Isolate loader outcomes while exercising every shell stack fallback.
  const base = "libgtk-3.so.0 libGL.so.1 libEGL.so.1 libxkbcommon.so.0";
  const modern =
    "libwebkit2gtk-4.1.so.0 libjavascriptcoregtk-4.1.so.0 libsoup-3.0.so.0";
  const legacy =
    "libwebkit2gtk-4.0.so.37 libjavascriptcoregtk-4.0.so.18 libsoup-2.4.so.1";
  for (const stack of [modern, legacy]) {
    for (const tray of [
      "libayatana-appindicator3.so.1",
      "libappindicator3.so.1",
    ]) {
      const result = f.run(
        `spool_load_libraries() { for name do case " $LIBRARIES " in *" $name "*) ;; *) return 1 ;; esac; done; }
spool_runtime_ready`,
        { LIBRARIES: `${base} ${stack} ${tray}` },
      );
      assert.equal(result.status, 0, result.stderr);
    }
  }
  for (const missing of [
    "libgtk-3.so.0",
    "libsoup-3.0.so.0",
    "libayatana-appindicator3.so.1",
  ]) {
    const libraries = `${base} ${modern} libayatana-appindicator3.so.1`
      .split(" ")
      .filter((name) => name !== missing)
      .join(" ");
    const result = f.run(
      `spool_load_libraries() { for name do case " $LIBRARIES " in *" $name "*) ;; *) return 1 ;; esac; done; }
spool_runtime_ready`,
      { LIBRARIES: libraries },
    );
    assert.notEqual(result.status, 0, missing);
  }
});
