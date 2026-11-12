# Inlined into the release installer. Existing runtimes stay untouched; a
# downloaded Bun belongs to runtime_work and is removed by the installer trap.
spool_js() {
  "${spool_js_runtime:-bun}" --input-type=module "$@"
}

spool_runtime_usable() {
  "$1" --input-type=module -e '
import { createHash } from "node:crypto";
if (!process.versions.bun && Number(process.versions.node.split(".")[0]) < 18) process.exit(1);
createHash("sha256");
' >/dev/null 2>&1
}

spool_prepare_runtime() {
  for spool_candidate in bun node; do
    if command -v "$spool_candidate" >/dev/null 2>&1 && spool_runtime_usable "$spool_candidate"; then
      spool_js_runtime="$(command -v "$spool_candidate")"
      return 0
    fi
  done
  command -v unzip >/dev/null 2>&1 || fail 'install unzip to bootstrap Bun, or install Bun or Node.js 18+ and retry'
  spool_bun_version='@BUN_VERSION@'
  case "$target" in
    linux-amd64) spool_bun_archive=bun-linux-x64-baseline ;;
    linux-arm64) spool_bun_archive=bun-linux-aarch64 ;;
    *) fail 'unsupported Bun bootstrap architecture' ;;
  esac
  echo "No usable Bun or Node.js found; downloading temporary Bun ${spool_bun_version}..."
  # Execute beside the app so noexec-mounted system temporary storage works.
  runtime_work="$(mktemp -d "$HOME/.local/.spool.runtime.XXXXXX")"
  # Bun can create its install/transpiler caches even for built-in-only scripts.
  # A bootstrapped runtime must keep those files inside the same cleanup scope.
  BUN_INSTALL_CACHE_DIR="$runtime_work/install-cache"
  BUN_RUNTIME_TRANSPILER_CACHE_PATH="$runtime_work/transpiler-cache"
  export BUN_INSTALL_CACHE_DIR BUN_RUNTIME_TRANSPILER_CACHE_PATH
  spool_bun_base="https://github.com/oven-sh/bun/releases/download/bun-v$spool_bun_version"
  fetch "$spool_bun_base/$spool_bun_archive.zip" "$runtime_work/bun.zip"
  fetch "$spool_bun_base/SHASUMS256.txt" "$runtime_work/checksums"
  spool_expected=''
  while read -r spool_sum spool_name; do
    if [ "$spool_name" = "$spool_bun_archive.zip" ]; then spool_expected="$spool_sum"; fi
  done <"$runtime_work/checksums"
  case "$spool_expected" in ''|*[!a-f0-9]*) fail 'invalid Bun download checksum' ;; esac
  [ "${#spool_expected}" = 64 ] || fail 'invalid Bun download checksum'
  spool_actual="$(openssl dgst -sha256 -r "$runtime_work/bun.zip")"
  [ "${spool_actual%% *}" = "$spool_expected" ] || fail 'Bun download checksum mismatch'
  unzip -p "$runtime_work/bun.zip" "$spool_bun_archive/bun" >"$runtime_work/bun" || fail 'could not unpack Bun'
  chmod 700 "$runtime_work/bun"
  [ "$("$runtime_work/bun" --version)" = "$spool_bun_version" ] && spool_runtime_usable "$runtime_work/bun" || fail 'downloaded Bun could not run'
  spool_js_runtime="$runtime_work/bun"
}
