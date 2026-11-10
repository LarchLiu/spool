import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

const helper = resolve("tools/linux-dependencies.sh");
const python = execFileSync("sh", ["-c", "command -v python3"], { encoding: "utf8" }).trim();

async function fixture(t, { manager = "apt-get", privilege = "sudo", rustup = true, oldCargo = false, distribution } = {}) {
  const root = await mkdtemp(join(tmpdir(), "linux-dependencies-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const bin = join(root, "bin");
  const work = join(root, "work");
  await mkdir(bin);
  await mkdir(work);
  const osRelease = join(root, "os-release");
  const distributions = { "apt-get": 'ID=ubuntu\nVERSION_ID="24.04"', dnf: 'ID=fedora\nVERSION_ID=43', pacman: "ID=arch", zypper: "ID=opensuse-tumbleweed" };
  await writeFile(osRelease, distribution ?? distributions[manager]);
  // No host package manager, sudo, Rust, or user profile can leak into a test.
  await symlink(python, join(bin, "python3"));
  await symlink("/bin/sh", join(bin, "sh"));
  for (const name of ["cp", "chmod"]) {
    const path = execFileSync("sh", ["-c", `command -v ${name}`], { encoding: "utf8" }).trim();
    await symlink(path, join(bin, name));
  }
  const tool = (name, body) => writeFile(join(bin, name), `#!/bin/sh\nset -eu\n${body}\n`, { mode: 0o755 });
  await tool(manager, `case "$*" in
  '-q info '*|'-Si '*|'--non-interactive --no-refresh info '*)
    for package do :; done
    case " \${MISSING_PACKAGES:-} " in *" $package "*) exit 1 ;; esac
    echo "Package: $package"; exit 0 ;;
esac
printf '%s\\n' '${manager}'\" $*\" >> \"$FIXTURE_ROOT/actions\"
[ "\${PACKAGE_FAILURE:-no}" != yes ] || exit 1
case "$*" in
  install*|-S*|'--non-interactive install '*) : > "$FIXTURE_ROOT/runtime-ready"; : > "$FIXTURE_ROOT/native-ready" ;;
esac`);
  await tool("apt-cache", `case " \${MISSING_PACKAGES:-} " in
  *" $2 "*) echo '  Candidate: (none)' ;;
  *) echo '  Candidate: 1.0' ;;
esac`);
  if (privilege) {
    await tool(privilege, `printf '%s\\n' '${privilege}'\" $*\" >> \"$FIXTURE_ROOT/actions\"
exec "$@"`);
  }
  await tool("cargo", oldCargo
    ? `if [ -f "$FIXTURE_ROOT/cargo-ready" ]; then echo 'cargo 1.85.0'; else echo 'cargo 1.75.0'; fi`
    : "echo 'cargo 1.85.0'");
  await tool("rustc", `if [ -f "$FIXTURE_ROOT/rust-ready" ]; then echo 'rustc 1.85.0'; else echo 'rustc 1.75.0'; fi`);
  const rustupBody = `printf 'rustup %s\\n' "$*" >> "$FIXTURE_ROOT/actions"
if [ "$*" = 'default stable' ]; then : > "$FIXTURE_ROOT/rust-ready"; : > "$FIXTURE_ROOT/cargo-ready"; fi`;
  await writeFile(join(root, "rustup-template"), `#!/bin/sh\nset -eu\n${rustupBody}\n`, { mode: 0o755 });
  if (rustup) await tool("rustup", rustupBody);
  const run = (body = "", env = {}) => spawnSync("/bin/sh", ["-s"], {
    input: `set -eu
. "$HELPER"
fail() { echo "$*" >&2; exit 1; }
fetch() {
  [ "$1" = https://sh.rustup.rs ] || exit 1
  printf '%s\\n' 'fetch rustup' >> "$FIXTURE_ROOT/actions"
  printf '%s\\n' '#!/bin/sh' 'printf "bootstrap %s\\n" "$*" >> "$FIXTURE_ROOT/actions"' 'cp "$FIXTURE_ROOT/rustup-template" "$MOCK_BIN/rustup"' 'chmod 755 "$MOCK_BIN/rustup"' > "$2"
}
spool_runtime_ready() { [ -f "$FIXTURE_ROOT/runtime-ready" ]; }
spool_voxl_native_ready() { [ -f "$FIXTURE_ROOT/native-ready" ]; }
work="$WORK"
spool_os_release_file="$OS_RELEASE"
skip_dependencies=no
skip_voxl_dependencies=no
${body}
spool_prepare_dependencies
`,
    env: { PATH: bin, HOME: join(root, "home"), FIXTURE_ROOT: root, MOCK_BIN: bin, HELPER: helper, WORK: work, OS_RELEASE: osRelease, ...env },
    encoding: "utf8",
  });
  const actions = async () => (await readFile(join(root, "actions"), "utf8").catch(() => "")).trim().split("\n").filter(Boolean);
  const ready = (...names) => Promise.all(names.map((name) => writeFile(join(root, `${name}-ready`), "")));
  return { root, run, actions, ready };
}

test("ready Linux dependencies do not run a package manager or change Rust defaults", async (t) => {
  const f = await fixture(t);
  await f.ready("runtime", "native", "rust");
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(await f.actions(), []);
});

test("missing dependencies install system packages before preparing minimal stable Rust", async (t) => {
  const f = await fixture(t);
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  const actions = await f.actions();
  assert.equal(actions[0], "sudo apt-get update");
  const packages = actions.find((line) => line.startsWith("apt-get install "));
  for (const name of ["libwebkit2gtk-4.1-0", "libayatana-appindicator3-1", "libgl1-mesa-dri", "build-essential", "clang", "libclang-dev", "pkg-config", "esbuild", "libx264-dev", "libyuv-dev", "libopus-dev", "libpulse-dev", "libxcb1-dev"]) {
    assert.ok(packages.split(" ").includes(name), `${name}: ${packages}`);
  }
  assert.deepEqual(actions.slice(-2), ["rustup toolchain install stable --profile minimal", "rustup default stable"]);
  assert.match(result.stdout, /voxl build dependencies are ready/);
});

test("rustup bootstrap runs as the current user without modifying shell profiles", async (t) => {
  const f = await fixture(t, { rustup: false });
  await f.ready("runtime", "native");
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(await f.actions(), [
    "fetch rustup",
    "bootstrap -y --profile minimal --default-toolchain none --no-modify-path",
    "rustup toolchain install stable --profile minimal",
    "rustup default stable",
  ]);
});

test("an old Cargo is upgraded even when rustc is already compatible", async (t) => {
  const f = await fixture(t, { oldCargo: true });
  await f.ready("runtime", "native", "rust");
  const result = f.run();
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(await f.actions(), [
    "rustup toolchain install stable --profile minimal",
    "rustup default stable",
  ]);
});

test("desktop-only mode omits voxl libraries and Rust", async (t) => {
  const f = await fixture(t);
  const result = f.run("skip_voxl_dependencies=yes");
  assert.equal(result.status, 0, result.stderr);
  const actions = await f.actions();
  assert.ok(actions.some((line) => line.includes("libwebkit2gtk-4.1-0")));
  assert.ok(actions.every((line) => !/rustup|clang|libx264|libpulse|libxcb/.test(line)));
});

test("skip-dependencies performs no installation or runtime probing", async (t) => {
  const f = await fixture(t);
  const result = f.run("skip_dependencies=yes\nspool_runtime_ready() { exit 77; }");
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(await f.actions(), []);
});

test("package failure stops before Rust bootstrap and does not report success", async (t) => {
  const f = await fixture(t);
  const result = f.run("", { PACKAGE_FAILURE: "yes" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /existing Spool app was not replaced/);
  assert.ok((await f.actions()).every((line) => !line.includes("rustup")));
  assert.doesNotMatch(result.stdout, /dependencies are ready/);
});

test("supported package managers use native package names with authorization", async (t) => {
  for (const [manager, names] of [
    ["dnf", ["webkit2gtk4.1", "clang-devel", "pulseaudio-libs-devel", "libxcb-devel"]],
    ["pacman", ["webkit2gtk-4.1", "base-devel", "libpulse", "libxcb"]],
    ["zypper", ["libwebkit2gtk-4_1-0", "libclang-devel", "libpulse-devel", "libxcb-devel"]],
  ]) {
    await t.test(manager, async (t) => {
      const f = await fixture(t, { manager, privilege: "pkexec" });
      await f.ready("rust");
      const result = f.run();
      assert.equal(result.status, 0, result.stderr);
      const actions = await f.actions();
      const install = actions.find((line) => line.startsWith(`pkexec ${manager} `) && / install | -S /.test(line));
      assert.ok(install, actions.join("\n"));
      for (const name of names) assert.ok(install.split(" ").includes(name), install);
    });
  }
});

test("Debian/Ubuntu candidate selection supports pre-t64 GTK and legacy WebKit/tray packages", async (t) => {
  const f = await fixture(t, { distribution: 'ID=ubuntu\nVERSION_ID="22.04"' });
  const result = f.run("", { MISSING_PACKAGES: "libgtk-3-0t64 libwebkit2gtk-4.1-0 libayatana-appindicator3-1" });
  assert.equal(result.status, 0, result.stderr);
  const install = (await f.actions()).find((line) => line.startsWith("apt-get install "));
  for (const name of ["libgtk-3-0", "libwebkit2gtk-4.0-37", "libappindicator3-1"]) assert.ok(install.split(" ").includes(name), install);
});

test("missing repository packages are listed together before any package installation", async (t) => {
  for (const manager of ["apt-get", "dnf", "pacman", "zypper"]) {
    const f = await fixture(t, { manager });
    const names = manager === "apt-get" ? "libx264-dev esbuild" : manager === "dnf" ? "x264-devel esbuild" : manager === "pacman" ? "x264 esbuild" : "libx264-devel esbuild";
    const result = f.run("", { MISSING_PACKAGES: names });
    assert.notEqual(result.status, 0, manager);
    for (const name of names.split(" ")) assert.ok(result.stderr.includes(name), result.stderr);
    assert.match(result.stderr, /no dependency packages were installed/);
    assert.ok((await f.actions()).every((line) => !/ install | -S |rustup/.test(line)), manager);
  }
});

test("unsupported and immutable distributions do not run a host package manager", async (t) => {
  for (const distribution of ["ID=alpine", 'ID=nixos\nID_LIKE="debian"', 'ID=fedora\nVARIANT_ID=silverblue', 'ID=opensuse-microos\nID_LIKE="suse opensuse"', 'ID=steamos\nID_LIKE=arch', "ID=unknown", "ID=ubuntu\nVERSION_ID=20.04", "ID=debian\nVERSION_ID=11", "ID=rocky\nVERSION_ID=8.10"]) {
    const f = await fixture(t, { distribution });
    const result = f.run();
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /not supported/);
    assert.deepEqual(await f.actions(), []);
  }
});

test("lack of an authorization helper fails without running a package manager", async (t) => {
  const f = await fixture(t, { privilege: null });
  const result = f.run();
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /sudo or polkit/);
  assert.deepEqual(await f.actions(), []);
});

test("failed dependency recheck does not claim installation succeeded", async (t) => {
  const f = await fixture(t);
  const result = f.run("spool_runtime_ready() { return 1; }");
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /desktop libraries still cannot be loaded/);
  assert.doesNotMatch(result.stdout, /dependencies are ready/);
});
