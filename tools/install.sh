#!/bin/sh
# Installs a signed Spool release for the current Linux user, without sudo.
# Requires Python 3, OpenSSL 3+ and curl (or wget). User data is never removed.
set -eu
repository='@REPOSITORY@'
public_key_der='@PUBLIC_KEY_DER@'
app_dir="$HOME/.local/spool.app"
desktop_file="${XDG_DATA_HOME:-$HOME/.local/share}/applications/spool.desktop"
fail() { echo "install.sh: $*" >&2; exit 1; }
case "${1:-}" in
  --help|-h) echo 'Usage: sh install.sh [ARCHIVE | --uninstall]'; exit 0 ;;
  --uninstall)
    rm -rf "$app_dir"
    if [ -f "$desktop_file" ] && grep -qF "$app_dir/" "$desktop_file"; then rm -f "$desktop_file"; fi
    echo 'Uninstalled Spool; settings and chat data are unchanged.'
    exit 0 ;;
  -*) fail 'unknown option' ;;
esac
[ "$#" -le 1 ] || fail 'pass at most one archive'
[ "$(uname -s)" = Linux ] || fail 'this installer is for Linux'
[ "$(id -u)" != 0 ] || fail 'run as your regular user, not root'
for tool in python3 openssl; do command -v "$tool" >/dev/null 2>&1 || fail "install $tool first"; done
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
archive="${1:-}"
if [ -n "$archive" ]; then
  cp "$(dirname "$archive")/update-$target.json" "$work/manifest.json"
  cp "$(dirname "$archive")/update-$target.json.sig" "$work/manifest.sig"
else
  base="https://github.com/$repository/releases/latest/download/update-$target.json"
  fetch "$base" "$work/manifest.json"
  fetch "$base.sig" "$work/manifest.sig"
fi
openssl pkeyutl -verify -pubin -keyform DER -inkey "$work/key.der" -rawin -in "$work/manifest.json" -sigfile "$work/manifest.sig" >/dev/null 2>&1 || fail 'invalid manifest signature; nothing was installed'
python3 - "$work" "$repository" "$target" <<'PY'
import base64, json, pathlib, re, sys
work, repo, target = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3]
raw = (work / 'manifest.json').read_bytes()
if len(raw) > 1048576:
    raise SystemExit('manifest exceeds size limit')
m = json.loads(raw)
version = m.get('version', '')
if not re.fullmatch(r'(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?', version):
    raise SystemExit('invalid release version')
url = f'https://github.com/{repo}/releases/download/v{version}/spool-{version}-{target}.tar.gz'
if m.get('schema') != 'spool-update-v1' or m.get('repository') != repo or m.get('target') != target or m.get('url') != url:
    raise SystemExit('manifest identity does not match this installer')
if type(m.get('size')) is not int or not 0 < m['size'] <= 1073741824:
    raise SystemExit('invalid archive size')
signature = base64.b64decode(m['signature'], validate=True)
if len(signature) != 64:
    raise SystemExit('invalid archive signature')
(work / 'archive.sig').write_bytes(signature)
(work / 'url').write_text(url)
print(f"Verified Spool {version} for {target}")
PY
if [ -z "$archive" ]; then archive="$work/app.tar.gz"; fetch "$(cat "$work/url")" "$archive"; fi
python3 - "$work" "$archive" <<'PY'
import hashlib, json, pathlib, sys
work, archive = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
if archive.stat().st_size != json.loads((work / 'manifest.json').read_text())['size']:
    raise SystemExit('archive size mismatch')
digest = hashlib.sha256()
with archive.open('rb') as f:
    for chunk in iter(lambda: f.read(1048576), b''):
        digest.update(chunk)
(work / 'digest').write_bytes(digest.digest())
PY
openssl pkeyutl -verify -pubin -keyform DER -inkey "$work/key.der" -rawin -in "$work/digest" -sigfile "$work/archive.sig" >/dev/null 2>&1 || fail 'invalid archive signature; nothing was installed'
# Extract only ordinary files/directories into a fresh private staging folder.
# Reject links, traversal, special files and excessive expanded size.
python3 - "$archive" "$staging" <<'PY'
import os, pathlib, shutil, sys, tarfile
root = pathlib.Path(sys.argv[2])
total = 0
with tarfile.open(sys.argv[1], 'r:gz') as archive:
    for member in archive:
        name = pathlib.PurePosixPath(member.name)
        if name.is_absolute() or '..' in name.parts or not (member.isfile() or member.isdir()):
            raise SystemExit('unsafe entry in archive')
        total += member.size
        if total > 1073741824:
            raise SystemExit('expanded archive exceeds size limit')
        dest = root.joinpath(*name.parts)
        if member.isdir():
            dest.mkdir(parents=True, exist_ok=True)
        else:
            dest.parent.mkdir(parents=True, exist_ok=True)
            with archive.extractfile(member) as src, dest.open('xb') as out:
                shutil.copyfileobj(src, out)
            os.chmod(dest, member.mode & 0o755)
PY
[ -x "$staging/spool" ] || fail 'archive does not contain the Spool executable'
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
echo "Installed Spool in $app_dir. Open it from your applications menu."
