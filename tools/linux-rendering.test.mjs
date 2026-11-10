import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

const helper = resolve("tools/linux-dependencies.sh");
const python = execFileSync("sh", ["-c", "command -v python3"], { encoding: "utf8" }).trim();

async function fixture(t, { glxinfo = false } = {}) {
  const root = await mkdtemp(join(tmpdir(), "linux-rendering-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const bin = join(root, "bin");
  const devices = join(root, "dev");
  await mkdir(bin);
  await mkdir(join(devices, "dri"), { recursive: true });
  await symlink(python, join(bin, "python3"));
  if (glxinfo) await writeFile(join(bin, "glxinfo"), `#!/bin/sh
[ "$*" = '-B' ] || exit 99
printf '%s\n' "$GLX_OUTPUT"
exit "\${GLX_STATUS:-0}"
`, { mode: 0o755 });
  const run = (body, env = {}) => spawnSync("/bin/sh", ["-s"], {
    input: `set -eu
. "$HELPER"
app_dir="$HOME/.local/spool.app"
rendering=auto
${body}
`,
    env: { PATH: bin, HOME: join(root, "home with spaces"), HELPER: helper, DEVICE_ROOT: devices, ...env },
    encoding: "utf8",
  });
  const gpu = (env) => run('if spool_gpu_available "$DEVICE_ROOT"; then echo hardware; else echo software; fi', env);
  // /dev/null is an accessible character device on both Linux and macOS.
  // Symlinks let tests model GPU device nodes without privileged mknod calls.
  const device = (name) => symlink("/dev/null", join(devices, name));
  return { run, gpu, device, devices };
}

test("GPU detection requires an accessible render node, not a framebuffer or ordinary file", async (t) => {
  const f = await fixture(t);
  await f.device("dri/card0");
  await writeFile(join(f.devices, "dri/renderD128"), "not a device");
  let result = f.gpu();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "software");
  await rm(join(f.devices, "dri/renderD128"));
  await f.device("dri/renderD128");
  result = f.gpu();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "hardware");
});

test("NVIDIA device fallback requires both a GPU and its control device", async (t) => {
  const f = await fixture(t);
  await f.device("nvidia0");
  assert.equal(f.gpu().stdout.trim(), "software");
  await f.device("nvidiactl");
  assert.equal(f.gpu().stdout.trim(), "hardware");
});

test("current display software renderers override the presence of GPU devices", async (t) => {
  const f = await fixture(t, { glxinfo: true });
  await f.device("dri/renderD128");
  for (const renderer of ["llvmpipe (LLVM 18.1.8, 256 bits)", "softpipe", "Software Rasterizer", "SwiftShader Device", "lavapipe"]) {
    const result = f.gpu({ DISPLAY: ":1", GLX_OUTPUT: `OpenGL renderer string: ${renderer}` });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout.trim(), "software", renderer);
  }
});

test("current display accelerated renderer is accepted without local render nodes", async (t) => {
  const f = await fixture(t, { glxinfo: true });
  const result = f.gpu({ DISPLAY: ":0", GLX_OUTPUT: "    Accelerated: yes\nOpenGL renderer string: Mesa Intel(R) Graphics" });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), "hardware");
});

test("failed or inconclusive OpenGL probes select software rendering", async (t) => {
  const f = await fixture(t, { glxinfo: true });
  await f.device("dri/renderD128");
  for (const env of [
    { GLX_STATUS: "1", GLX_OUTPUT: "OpenGL renderer string: NVIDIA" },
    { GLX_OUTPUT: "unable to open display" },
    { GLX_OUTPUT: "Accelerated: no\nOpenGL renderer string: Virtual GPU" },
  ]) {
    const result = f.gpu({ DISPLAY: ":1", ...env });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout.trim(), "software");
  }
});

test("native Wayland uses device access rather than an unrelated XWayland renderer", async (t) => {
  const f = await fixture(t, { glxinfo: true });
  const env = { DISPLAY: ":0", WAYLAND_DISPLAY: "wayland-0", XDG_SESSION_TYPE: "wayland", GLX_OUTPUT: "OpenGL renderer string: llvmpipe" };
  assert.equal(f.gpu(env).stdout.trim(), "software");
  await f.device("dri/renderD128");
  assert.equal(f.gpu(env).stdout.trim(), "hardware");
});

test("auto rendering writes all WebKit software flags only when no usable GPU is detected", async (t) => {
  const f = await fixture(t);
  for (const available of [true, false]) {
    const result = f.run(`spool_gpu_available() { return ${available ? 0 : 1}; }
spool_configure_rendering
printf 'EXEC=%s\n' "$desktop_exec"`);
    assert.equal(result.status, 0, result.stderr);
    const exec = result.stdout.trim().split("\n").at(-1);
    assert.match(exec, /".*home with spaces\/\.local\/spool\.app\/spool"$/);
    if (available) assert.doesNotMatch(exec, /LIBGL|WEBKIT|\/usr\/bin\/env/);
    else assert.match(exec, /\/usr\/bin\/env LIBGL_ALWAYS_SOFTWARE=1 WEBKIT_DISABLE_COMPOSITING_MODE=1 WEBKIT_SKIA_ENABLE_CPU_RENDERING=1 /);
  }
});

test("explicit rendering modes bypass GPU probing", async (t) => {
  const f = await fixture(t);
  for (const rendering of ["software", "hardware"]) {
    const result = f.run(`rendering=${rendering}
spool_gpu_available() { exit 77; }
spool_configure_rendering
printf 'EXEC=%s\n' "$desktop_exec"`);
    assert.equal(result.status, 0, result.stderr);
    if (rendering === "software") assert.match(result.stdout, /LIBGL_ALWAYS_SOFTWARE=1/);
    else assert.doesNotMatch(result.stdout, /LIBGL|WEBKIT/);
  }
});
