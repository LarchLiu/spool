#!/bin/sh
# Installs a signed Spool release for the current Linux user. Only system
# dependency installation uses sudo/polkit; the app and Rust stay user-owned.
# Requires Bun, OpenSSL 3+ and curl (or wget). User data is never removed.
set -eu
repository='@REPOSITORY@'
public_key_der='@PUBLIC_KEY_DER@'
app_dir="$HOME/.local/spool.app"
desktop_file="${XDG_DATA_HOME:-$HOME/.local/share}/applications/spool.desktop"
fail() { echo "install.sh: $*" >&2; exit 1; }
skip_dependencies=no
skip_voxl_dependencies=no
archive=''
for argument do
case "$argument" in
  --help|-h)
    echo 'Usage: sh install.sh [--skip-voxl-dependencies | --skip-dependencies] [ARCHIVE]'
    echo '       sh install.sh --uninstall'
    echo 'Installs WebKitGTK, desktop runtime libraries and voxl build dependencies by default.'
    echo 'System packages require administrator authorization; Rust is installed for the current user.'
    exit 0 ;;
  --uninstall)
    rm -rf "$app_dir"
    if [ -f "$desktop_file" ] && grep -qF "$app_dir/" "$desktop_file"; then rm -f "$desktop_file"; fi
    echo 'Uninstalled Spool; settings and chat data are unchanged.'
    exit 0 ;;
  --skip-voxl-dependencies) skip_voxl_dependencies=yes ;;
  --skip-dependencies) skip_dependencies=yes ;;
  -*) fail 'unknown option' ;;
  *) [ -z "$archive" ] || fail 'pass at most one archive'; archive="$argument" ;;
esac
done
[ "$(uname -s)" = Linux ] || fail 'this installer is for Linux'
[ "$(id -u)" != 0 ] || fail 'run as your regular user, not root'
PATH="${BUN_INSTALL:-$HOME/.bun}/bin:$PATH"
export PATH
for tool in bun openssl; do command -v "$tool" >/dev/null 2>&1 || fail "install $tool first"; done
case "$(openssl version)" in 'OpenSSL 3.'*|'OpenSSL 4.'*) ;; *) fail 'OpenSSL 3 or later is required for Ed25519 verification' ;; esac
case "$(uname -m)" in
  x86_64|amd64) target=linux-amd64 ;;
  aarch64|arm64) target=linux-arm64 ;;
  *) fail 'unsupported architecture' ;;
esac
# A .desktop Exec value cannot safely represent these path characters.
[ "$(printf '%s' "$app_dir" | tr -d '"$%\\\140\n')" = "$app_dir" ] || fail 'unsupported home-directory characters'
mkdir -p "$HOME/.local"
lock="$HOME/.local/.spool-install.lock"
mkdir "$lock" 2>/dev/null || fail "another installer is running, or a stale lock exists at $lock"
work=''; staging=''; backup=''
cleanup() {
  if [ -n "$backup" ] && [ -d "$backup/app" ] && [ ! -e "$app_dir" ]; then mv "$backup/app" "$app_dir"; fi
  [ -z "$work" ] || rm -rf "$work"
  [ -z "$staging" ] || rm -rf "$staging"
  # Keep the backup if an exceptional restore failure prevented recovery.
  if [ -n "$backup" ] && [ ! -e "$backup/app" ]; then rmdir "$backup" 2>/dev/null || true; fi
  rmdir "$lock" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
work="$(mktemp -d)"
staging="$(mktemp -d "$HOME/.local/.spool.install.XXXXXX")"
printf '%s' "$public_key_der" | openssl base64 -d -A >"$work/key.der"
fetch() {
  case "$1" in https://*) ;; *) fail 'downloads must use HTTPS' ;; esac
  if command -v curl >/dev/null 2>&1; then
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 20 --max-time 180 --retry 2 --retry-connrefused --retry-delay 2 --retry-max-time 600 --max-filesize 1073741824 "$1" -o "$2"
  else
    command -v wget >/dev/null 2>&1 || fail 'install curl or wget first'
    wget --https-only --timeout=60 --tries=3 --retry-connrefused --retry-on-http-error=408,429,500,502,503,504 -qO "$2" "$1"
  fi
}
@LINUX_DEPENDENCIES@
if [ -n "$archive" ]; then
  cp "$(dirname "$archive")/update-$target.json" "$work/manifest.json"
  cp "$(dirname "$archive")/update-$target.json.sig" "$work/manifest.sig"
else
  base="https://github.com/$repository/releases/latest/download/update-$target.json"
  fetch "$base" "$work/manifest.json"
  fetch "$base.sig" "$work/manifest.sig"
fi
openssl pkeyutl -verify -pubin -keyform DER -inkey "$work/key.der" -rawin -in "$work/manifest.json" -sigfile "$work/manifest.sig" >/dev/null 2>&1 || fail 'invalid manifest signature; nothing was installed'
spool_archive() {
  bun - "$@" <<'JS'
@INSTALLER_JS@
JS
}
spool_archive manifest "$work" "$repository" "$target"
# Hash, verify and extract the same private copy, never reopen an offline
# caller-owned path after verification (including replaceable symlinks).
if [ -n "$archive" ]; then
  cp "$archive" "$work/app.tar.gz"
else
  fetch "$(cat "$work/url")" "$work/app.tar.gz"
fi
archive="$work/app.tar.gz"
spool_archive digest "$work" "$archive"
openssl pkeyutl -verify -pubin -keyform DER -inkey "$work/key.der" -rawin -in "$work/digest" -sigfile "$work/archive.sig" >/dev/null 2>&1 || fail 'invalid archive signature; nothing was installed'
# Extract only ordinary files/directories into a fresh private staging folder.
# Reject links, traversal, special files and excessive expanded size.
spool_archive extract "$archive" "$staging"
[ -x "$staging/spool" ] || fail 'archive does not contain the Spool executable'
# Verify and stage the release before making any system changes. Failures leave
# the old application in place. Only copy the app after dependency rechecks pass.
spool_prepare_dependencies
backup="$(mktemp -d "$HOME/.local/.spool.backup.XXXXXX")"
if [ -e "$app_dir" ]; then mv "$app_dir" "$backup/app"; fi
mv "$staging" "$app_dir"
staging=''
rm -rf "$backup/app"
mkdir -p "$(dirname "$desktop_file")"
cat >"$desktop_file" <<EOF
[Desktop Entry]
Type=Application
Name=Spool
Exec="$app_dir/spool"
Icon=$app_dir/spool.png
Terminal=false
Categories=Development;
EOF
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database "$(dirname "$desktop_file")" || true
fi
echo "Installed Spool in $app_dir. Open Applications > Development > Spool."
echo 'If Spool is already running, fully quit and reopen it to load the installed libraries.'
