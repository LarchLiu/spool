# Inlined into the signed release installer by sign-release.mjs. Keep this file
# sourceable so dependency installation can be tested with inert package tools.
spool_load_libraries() {
  python3 -c '
import ctypes, sys
try:
    for name in sys.argv[1:]:
        ctypes.CDLL(name)
except OSError:
    sys.exit(1)
' "$@" >/dev/null 2>&1
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
    "$spool_tool" --version | python3 -c '
import re, sys
m = re.match(r"(?:rustc|cargo) (\d+)\.(\d+)", sys.stdin.read())
sys.exit(0 if m and tuple(map(int, m.groups())) >= (1, 85) else 1)
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
  python3 -c '
import ctypes, ctypes.util, glob, os, sys
custom = os.environ.get("LIBCLANG_PATH", "")
names = [custom] if os.path.isfile(custom) else glob.glob(os.path.join(custom, "libclang*.so*")) if custom else []
names += [ctypes.util.find_library("clang")]
for pattern in ("/usr/lib/llvm-*/lib/libclang.so*", "/usr/lib/*-linux-gnu/libclang-*.so*", "/usr/lib64/libclang*.so*", "/usr/lib/libclang*.so*"):
    names += glob.glob(pattern)
for name in filter(None, names):
    try:
        ctypes.CDLL(name)
        sys.exit(0)
    except OSError:
        pass
sys.exit(1)
' >/dev/null 2>&1 || return 1
  printf '%s\n' 'int main(void) { return 0; }' | clang -x c - -o "$work/yuv-probe" -lyuv >/dev/null 2>&1
}

spool_distribution() {
  # os-release uses shell quoting, but parsing it must not execute shell code.
  python3 - "${1:-${spool_os_release_file:-/etc/os-release}}" <<'PY'
import pathlib, re, shlex, sys
path = pathlib.Path(sys.argv[1])
if not path.exists() and sys.argv[1] == "/etc/os-release":
    path = pathlib.Path("/usr/lib/os-release")
data = {}
try:
    for line in path.read_text().splitlines():
        key, sep, value = line.partition("=")
        if sep and key in ("ID", "ID_LIKE", "VERSION_ID", "PRETTY_NAME", "VARIANT_ID"):
            words = shlex.split(value, comments=True)
            if len(words) == 1:
                data[key] = words[0]
except (OSError, ValueError):
    pass
identity = data.get("ID", "unknown").lower()
family = [identity] + data.get("ID_LIKE", "").lower().split()
manager = "unsupported"
if identity not in ("alpine", "nixos", "steamos", "opensuse-microos", "opensuse-aeon", "opensuse-kalpa") and data.get("VARIANT_ID") not in ("silverblue", "kinoite", "coreos", "sericea", "onyx"):
    for item in family:
        if item in ("ubuntu", "debian", "linuxmint", "pop", "neon", "zorin"):
            manager = "apt-get"; break
        if item in ("fedora", "rhel", "centos", "rocky", "almalinux", "ol"):
            manager = "dnf"; break
        if item in ("arch", "manjaro", "endeavouros"):
            manager = "pacman"; break
        if item in ("suse", "opensuse", "opensuse-leap", "opensuse-tumbleweed", "sles"):
            manager = "zypper"; break
label = data.get("PRETTY_NAME") or (identity + " " + data.get("VERSION_ID", "")).strip()
label = re.sub(r"[\x00-\x1f\x7f]", " ", label)[:160]
# These older releases lack the expected system/toolchain baseline. Derivative
# VERSION_ID values belong to their own numbering schemes, so never compare
# Mint's version, for example, against Ubuntu's version.
minimums = {"ubuntu": (22, 4), "debian": (12,), "rhel": (9,), "centos": (9,), "rocky": (9,), "almalinux": (9,), "ol": (9,)}
version = data.get("VERSION_ID", "")
if identity in minimums and re.fullmatch(r"\d+(?:\.\d+)*", version):
    current = tuple(map(int, version.split(".")))
    if current < minimums[identity]:
        manager = "unsupported"
print(manager + ":" + label)
PY
}

spool_package_available() {
  case "$spool_manager" in
    apt-get)
      LC_ALL=C apt-cache policy "$1" 2>/dev/null | python3 -c '
import re, sys
m = re.search(r"^\s*Candidate:\s*(\S+)", sys.stdin.read(), re.M)
sys.exit(0 if m and m[1] != "(none)" else 1)
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
