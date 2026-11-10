#!/bin/sh
# Disposable-container check. Package-manager state stays in the container;
# scratch, compiler output and retained diagnostics follow repository paths.
set -eu
mode="${1:-native}"
case "$mode" in native|desktop|voxl) ;; *) echo 'Usage: sh tools/linux-compat-smoke.sh [native|desktop|voxl]' >&2; exit 1 ;; esac
[ "$(id -u)" = 0 ] && { [ -e /.dockerenv ] || [ -e /run/.containerenv ]; } || {
  echo 'Run this check as root inside a disposable Docker/Podman container, never on the host.' >&2
  exit 1
}
REPO_ROOT="$(pwd)"
[ -f "$REPO_ROOT/tools/linux-dependencies.sh" ] || { echo 'Run from the repository root.' >&2; exit 1; }
mkdir -p "$REPO_ROOT/build/tmp"
RUN_ROOT="$(mktemp -d "$REPO_ROOT/build/tmp/linux-compat.XXXXXX")"
cleanup() { rm -rf -- "$RUN_ROOT"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -p "$RUN_ROOT/tmp" "$RUN_ROOT/go"
export TMPDIR="$RUN_ROOT/tmp" TMP="$RUN_ROOT/tmp" TEMP="$RUN_ROOT/tmp" GOTMPDIR="$RUN_ROOT/go"
export GOCACHE="$REPO_ROOT/build/cache/go-build"
export DEBIAN_FRONTEND=noninteractive
export HOME="$RUN_ROOT/home"
mkdir -p "$HOME"
image_id="$(sed -n 's/^ID=//p' /etc/os-release | tr -d '\"')"
case "$image_id" in
  ubuntu|debian)
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends python3 ca-certificates curl openssl
    ;;
  fedora) dnf install -y --setopt=install_weak_deps=False python3 ca-certificates curl-minimal openssl ;;
  arch) pacman -Syu --needed --noconfirm python ca-certificates curl openssl ;;
  opensuse*) zypper --non-interactive refresh; zypper --non-interactive install --no-recommends python3 ca-certificates curl openssl ;;
  *) echo 'Add bootstrap packages for this test image first.' >&2; exit 1 ;;
esac
. "$REPO_ROOT/tools/linux-dependencies.sh"
fail() { echo "linux-compat-smoke: $*" >&2; exit 1; }
# Only this container test replaces the production sudo/polkit boundary.
spool_system_command() { "$@"; }
fetch() { curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --retry 2 "$1" -o "$2"; }
work="$RUN_ROOT"
spool_need_runtime=yes
spool_need_native=yes
[ "$mode" != desktop ] || spool_need_native=no
spool_install_packages
spool_runtime_ready || fail 'desktop runtime load check failed'
if [ "$mode" != desktop ]; then
  spool_voxl_native_ready || fail 'voxl native dependency/link check failed'
fi
if [ "$mode" = voxl ]; then
  # Reuse toolchain/module caches; only the installed test binary and target
  # directory are disposable. No user's provider/app data is touched.
  export CARGO_HOME="$REPO_ROOT/build/cache/cargo" RUSTUP_HOME="$REPO_ROOT/build/cache/rustup"
  skip_dependencies=no
  skip_voxl_dependencies=no
  spool_prepare_dependencies
  cargo install voxl-server --version =0.3.3 --locked --registry crates-io --root "$RUN_ROOT/voxl" --target-dir "$RUN_ROOT/target"
  "$RUN_ROOT/voxl/bin/voxl-server" --help >/dev/null
fi
platform="$(spool_distribution)"
printf 'PASS %s: %s dependencies and runtime load/link probes\n' "${platform#*:}" "$mode"
