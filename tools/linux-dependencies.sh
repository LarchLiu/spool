# Inlined into the signed release installer by sign-release.mjs. Keep this file
# sourceable so dependency installation can be tested with inert package tools.
spool_load_libraries() {
  bun -e '
import { dlopen } from "bun:ffi";
import { ptr } from "bun:ffi";
import { existsSync, statSync } from "node:fs";
// Bun requires a symbol table. Call the system loader to probe arbitrary libraries.
const loader = dlopen(process.platform === "darwin" ? "/usr/lib/libSystem.B.dylib" : "libdl.so.2", {
  dlopen: { args: ["ptr", "i32"], returns: "ptr" },
  dlclose: { args: ["ptr"], returns: "i32" },
});
const clang = process.argv[1] === "--clang";
let names = process.argv.slice(1);
if (clang) {
  const matches = (pattern) => {
    try { return [...new Bun.Glob(pattern).scanSync({ followSymlinks: true })]; }
    catch (error) { if (["ENOENT", "ENOTDIR"].includes(error.code)) return []; throw error; }
  };
  const custom = process.env.LIBCLANG_PATH || "";
  names = custom && existsSync(custom) && statSync(custom).isFile() ? [custom] : custom ? matches(custom + "/libclang*.so*") : [];
  names.push("libclang.so", ...["/usr/lib/llvm-*/lib/libclang.so*", "/usr/lib/*-linux-gnu/libclang-*.so*", "/usr/lib64/libclang*.so*", "/usr/lib/libclang*.so*"].flatMap(matches));
}
try {
  for (const name of names) {
    const encoded = Buffer.from(name + "\0"), handle = loader.symbols.dlopen(ptr(encoded), 2);
    if (handle) { loader.symbols.dlclose(handle); if (clang) process.exit(0); }
    else if (!clang) process.exit(1);
  }
  if (clang) process.exit(1);
} finally { loader.close(); }
' -- "$@" >/dev/null 2>&1
}

spool_runtime_ready() {
  spool_load_libraries libgtk-3.so.0 libGL.so.1 libEGL.so.1 libxkbcommon.so.0 || return 1
  # Keep the two WebKit/libsoup stacks in separate probe processes. Loading
  # libsoup 2 and 3 into the same process can itself abort the process.
  if ! spool_load_libraries libwebkit2gtk-4.1.so.0 libjavascriptcoregtk-4.1.so.0 libsoup-3.0.so.0; then
    spool_load_libraries libwebkit2gtk-4.0.so.37 libjavascriptcoregtk-4.0.so.18 libsoup-2.4.so.1 || return 1
  fi
  spool_load_libraries libayatana-appindicator3.so.1 || spool_load_libraries libappindicator3.so.1
}

spool_rust_ready() {
  # voxl's locked dependencies need Rust 1.85; Ubuntu 24.04 starts at 1.75.
  for spool_tool in cargo rustc; do
    command -v "$spool_tool" >/dev/null 2>&1 || return 1
    "$spool_tool" --version | bun -e '
const m = (await Bun.stdin.text()).match(/^(?:rustc|cargo) (\d+)\.(\d+)/);
process.exit(m && (+m[1] > 1 || +m[1] === 1 && +m[2] >= 85) ? 0 : 1);
' || return 1
  done
}

spool_voxl_native_ready() {
  for spool_tool in cc clang pkg-config esbuild; do
    command -v "$spool_tool" >/dev/null 2>&1 && "$spool_tool" --version >/dev/null 2>&1 || return 1
  done
  pkg-config --exists x264 opus libpulse-simple xcb || return 1
  # Check libclang for bindgen, then link libyuv without running the result:
  # Debian/Ubuntu's libyuv package has no pkg-config file.
  spool_load_libraries --clang || return 1
  printf '%s\n' 'int main(void) { return 0; }' | clang -x c - -o "$work/yuv-probe" -lyuv >/dev/null 2>&1
}

spool_distribution() {
  # os-release uses shell quoting, but parsing it must not execute shell code.
  bun - "${1:-${spool_os_release_file:-/etc/os-release}}" <<'JS'
import { existsSync, readFileSync } from 'node:fs';
let path = process.argv[2]; if (!existsSync(path) && path === '/etc/os-release') path = '/usr/lib/os-release';
const data = {};
// Tokenize quoted os-release values without interpreting substitutions or escapes as code.
function word(input) {
  let value = '', quote = '', active = false, done = false;
  for (let i = 0; i < input.length; i++) {
    const c = input[i];
    if (!quote && c === '#') break;
    if (!quote && /\s/.test(c)) { if (active) done = true; continue; }
    if (done) return undefined;
    active = true;
    if (c === quote) { quote = ''; continue; }
    if (!quote && (c === '"' || c === "'")) { quote = c; continue; }
    if (c === '\\' && quote !== "'") {
      if (++i >= input.length) return undefined;
      if (quote === '"' && !['$', String.fromCharCode(96), '"', '\\'].includes(input[i])) value += '\\';
      value += input[i];
    } else value += c;
  }
  return !quote && active ? value : undefined;
}
try { for (const line of readFileSync(path, 'utf8').split(/\r?\n/)) {
  const at = line.indexOf('='), key = line.slice(0, at); if (at < 0 || !['ID', 'ID_LIKE', 'VERSION_ID', 'PRETTY_NAME', 'VARIANT_ID'].includes(key)) continue;
  const value = word(line.slice(at + 1)); if (value !== undefined) data[key] = value;
} } catch {}
const identity = (data.ID || 'unknown').toLowerCase(), family = [identity, ...(data.ID_LIKE || '').toLowerCase().split(/\s+/)];
let manager = 'unsupported';
if (!['alpine', 'nixos', 'steamos', 'opensuse-microos', 'opensuse-aeon', 'opensuse-kalpa'].includes(identity) && !['silverblue', 'kinoite', 'coreos', 'sericea', 'onyx'].includes(data.VARIANT_ID)) {
  const families = { 'apt-get': ['ubuntu', 'debian', 'linuxmint', 'pop', 'neon', 'zorin'], dnf: ['fedora', 'rhel', 'centos', 'rocky', 'almalinux', 'ol'], pacman: ['arch', 'manjaro', 'endeavouros'], zypper: ['suse', 'opensuse', 'opensuse-leap', 'opensuse-tumbleweed', 'sles'] };
  outer: for (const item of family) for (const [tool, values] of Object.entries(families)) if (values.includes(item)) { manager = tool; break outer; }
}
const minimum = { ubuntu: [22, 4], debian: [12], rhel: [9], centos: [9], rocky: [9], almalinux: [9], ol: [9] }[identity];
if (minimum && /^\d+(?:\.\d+)*$/.test(data.VERSION_ID || '')) {
  const current = data.VERSION_ID.split('.').map(Number); let comparison = 0;
  for (let i = 0; i < Math.max(current.length, minimum.length); i++) { const delta = (current[i] || 0) - (minimum[i] || 0); if (delta) { comparison = delta; break; } }
  if (comparison < 0) manager = 'unsupported';
}
const label = (data.PRETTY_NAME || (identity + ' ' + (data.VERSION_ID || '')).trim()).replace(/[\x00-\x1f\x7f]/g, ' ').slice(0, 160);
console.log(manager + ':' + label);
JS
}

spool_package_available() {
  case "$spool_manager" in
    apt-get)
      LC_ALL=C apt-cache policy "$1" 2>/dev/null | bun -e '
const match = (await Bun.stdin.text()).match(/^\s*Candidate:\s*(\S+)/m); process.exit(match && match[1] !== "(none)" ? 0 : 1);
'
      ;;
    dnf) LC_ALL=C dnf -q info "$1" >/dev/null 2>&1 ;;
    pacman) LC_ALL=C pacman -Si "$1" >/dev/null 2>&1 ;;
    zypper) LC_ALL=C zypper --non-interactive --no-refresh info --type package "$1" >/dev/null 2>&1 ;;
  esac
}

spool_add_package() {
  spool_package_label="$1"
  shift
  for spool_candidate do
    if spool_package_available "$spool_candidate"; then
      spool_packages="$spool_packages $spool_candidate"
      return 0
    fi
  done
  spool_missing_packages="$spool_missing_packages
  $spool_package_label (tried: $*)"
}

spool_system_command() {
  printf 'Installing system dependencies:'
  printf ' %s' "$@"
  printf '\n'
  if command -v sudo >/dev/null 2>&1; then
    # sudo prompts on the terminal, including when install.sh is piped to sh.
    sudo "$@" || fail 'system dependency installation failed; the existing Spool app was not replaced'
  elif command -v pkexec >/dev/null 2>&1; then
    pkexec "$@" || fail 'system dependency authorization or installation failed; the existing Spool app was not replaced'
  else
    fail 'install sudo or polkit to authorize system dependencies, or install the listed packages as administrator and retry'
  fi
}

spool_install_packages() {
  spool_platform="$(spool_distribution)"
  spool_manager="${spool_platform%%:*}"
  spool_platform="${spool_platform#*:}"
  echo "Detected Linux distribution: $spool_platform"
  [ "$spool_manager" != unsupported ] || fail "automatic system package installation is not supported on $spool_platform; provision libraries using the Spool README and retry, or use --skip-dependencies"
  if [ -e /run/ostree-booted ] || command -v transactional-update >/dev/null 2>&1; then
    fail 'immutable/OSTree systems require administrator-managed dependencies; use --skip-dependencies after provisioning them'
  fi
  command -v "$spool_manager" >/dev/null 2>&1 || fail "$spool_platform requires $spool_manager, but it is not available; install dependencies manually"
  # Refresh metadata before selecting candidates. Do not run pacman -Sy:
  # Arch requires a full system upgrade, which this app installer must not do.
  case "$spool_manager" in
    apt-get) spool_system_command apt-get update ;;
    dnf) spool_system_command dnf makecache -y ;;
    zypper) spool_system_command zypper --non-interactive refresh ;;
  esac
  spool_packages=''
  spool_missing_packages=''
  case "$spool_manager" in
    apt-get)
      if [ "$spool_need_runtime" = yes ]; then
        spool_add_package 'GTK 3' libgtk-3-0t64 libgtk-3-0
        spool_add_package WebKitGTK libwebkit2gtk-4.1-0 libwebkit2gtk-4.0-37
        for spool_package in libgl1 libegl1 libgl1-mesa-dri libxkbcommon0; do spool_add_package "$spool_package" "$spool_package"; done
        spool_add_package 'tray indicator' libayatana-appindicator3-1 libappindicator3-1
      fi
      if [ "$spool_need_native" = yes ]; then
        for spool_package in build-essential clang libclang-dev pkg-config esbuild libx264-dev libyuv-dev libopus-dev libpulse-dev libxcb1-dev; do spool_add_package "$spool_package" "$spool_package"; done
      fi
      ;;
    dnf)
      if [ "$spool_need_runtime" = yes ]; then
        spool_add_package 'GTK 3' gtk3
        spool_add_package WebKitGTK webkit2gtk4.1 webkit2gtk4.0 webkit2gtk3
        for spool_package in mesa-libGL mesa-libEGL mesa-dri-drivers libxkbcommon; do spool_add_package "$spool_package" "$spool_package"; done
        spool_add_package 'tray indicator' libayatana-appindicator-gtk3 libappindicator-gtk3
      fi
      if [ "$spool_need_native" = yes ]; then
        for spool_package in gcc clang clang-devel pkgconf-pkg-config esbuild x264-devel libyuv-devel opus-devel pulseaudio-libs-devel libxcb-devel; do spool_add_package "$spool_package" "$spool_package"; done
      fi
      ;;
    pacman)
      if [ "$spool_need_runtime" = yes ]; then
        spool_add_package 'GTK 3' gtk3
        spool_add_package WebKitGTK webkit2gtk-4.1 webkit2gtk
        spool_add_package 'OpenGL dispatch' libglvnd
        for spool_package in mesa libxkbcommon; do spool_add_package "$spool_package" "$spool_package"; done
        spool_add_package 'tray indicator' libayatana-appindicator libappindicator-gtk3
      fi
      if [ "$spool_need_native" = yes ]; then
        for spool_package in base-devel clang pkgconf esbuild x264 libyuv opus libpulse libxcb; do spool_add_package "$spool_package" "$spool_package"; done
      fi
      ;;
    zypper)
      if [ "$spool_need_runtime" = yes ]; then
        spool_add_package 'GTK 3' libgtk-3-0
        spool_add_package WebKitGTK libwebkit2gtk-4_1-0 libwebkit2gtk-4_0-37
        spool_add_package OpenGL Mesa-libGL1 libGL1
        spool_add_package EGL libEGL1 Mesa-libEGL1
        for spool_package in Mesa-dri libxkbcommon0; do spool_add_package "$spool_package" "$spool_package"; done
        spool_add_package 'tray indicator' libayatana-appindicator3-1 libappindicator3-1
      fi
      if [ "$spool_need_native" = yes ]; then
        for spool_package in gcc clang esbuild libx264-devel libyuv-devel libopus-devel libpulse-devel libxcb-devel; do spool_add_package "$spool_package" "$spool_package"; done
        spool_add_package pkg-config pkgconf-pkg-config pkg-config
        spool_add_package libclang libclang-devel clang-devel
      fi
      ;;
  esac
  if [ -n "$spool_missing_packages" ]; then
    printf 'Packages unavailable in the configured repositories for %s:%s\n' "$spool_platform" "$spool_missing_packages" >&2
    echo 'Check enabled repositories (Ubuntu: universe; Fedora/RHEL: codec/development repositories; openSUSE: codec repositories). Arch users should update with pacman -Syu themselves before retrying.' >&2
    fail 'no dependency packages were installed and the existing Spool app was not replaced; configure repositories manually, or use --skip-voxl-dependencies for desktop-only installation'
  fi
  # Package names above are fixed identifiers, never shell code or user input.
  set -- $spool_packages
  case "$spool_manager" in
    apt-get) spool_system_command apt-get install -y --no-install-recommends "$@" ;;
    dnf) spool_system_command dnf install -y --setopt=install_weak_deps=False "$@" ;;
    pacman) spool_system_command pacman -S --needed --noconfirm "$@" ;;
    zypper) spool_system_command zypper --non-interactive install --no-recommends "$@" ;;
  esac
}

spool_prepare_dependencies() {
  [ "$skip_dependencies" = no ] || return 0
  # Match the desktop installer: it also searches these user tool locations.
  PATH="${CARGO_HOME:-$HOME/.cargo}/bin:${BUN_INSTALL:-$HOME/.bun}/bin:$PATH"
  export PATH
  spool_need_runtime=no
  spool_need_native=no
  spool_runtime_ready || spool_need_runtime=yes
  if [ "$skip_voxl_dependencies" = no ]; then
    spool_voxl_native_ready || spool_need_native=yes
  fi
  if [ "$spool_need_runtime" = yes ] || [ "$spool_need_native" = yes ]; then
    spool_install_packages
  fi
  spool_runtime_ready || fail 'desktop libraries still cannot be loaded after installation; check WebKitGTK 4.1/4.0 and the system packages'
  if [ "$skip_voxl_dependencies" = no ]; then
    spool_voxl_native_ready || fail 'voxl native build dependencies are still unavailable; check clang, libclang, esbuild, pkg-config, x264, libyuv, Opus, PulseAudio and XCB'
    if ! spool_rust_ready; then
      echo 'Preparing the minimal stable Rust toolchain for voxl…'
      if ! command -v rustup >/dev/null 2>&1; then
        fetch https://sh.rustup.rs "$work/rustup-init.sh"
        # Install as the regular user and leave shell profiles unchanged.
        sh "$work/rustup-init.sh" -y --profile minimal --default-toolchain none --no-modify-path || fail 'could not install rustup'
      fi
      rustup toolchain install stable --profile minimal || fail 'could not download the stable Rust toolchain'
      rustup default stable || fail 'could not select the stable Rust toolchain'
      # Discard a cached /usr/bin/cargo lookup after creating ~/.cargo/bin.
      hash -r
      spool_rust_ready || fail 'Rust 1.85 or later and Cargo are still unavailable after rustup setup'
    fi
    echo 'voxl build dependencies are ready. Use Install voxl in Spool to build the helper.'
  fi
  echo 'Desktop runtime dependencies are ready.'
}
