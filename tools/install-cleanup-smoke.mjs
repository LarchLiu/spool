// Prepare a disposable Linux smoke fixture locally, then copy its files to a
// test host and run check.sh. No release keys or actual app data are needed.
import { generateKeyPairSync } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { signRelease } from "./sign-release.mjs";
import { releaseTargets, serverArchiveName, installerName } from "./release-assets.mjs";

const directory = resolve(process.argv[2]);
await mkdir(directory, { recursive: true });
const assets = join(directory, "assets");
const payload = join(directory, "payload");
await mkdir(assets);
await mkdir(payload);
await writeFile(join(payload, "spool"), "#!/bin/sh\necho cleanup-smoke-app\n", { mode: 0o755 });
const notes = join(directory, "notes.md");
await writeFile(notes, "## Test\n\n- Disposable cleanup smoke test\n");
for (const target of releaseTargets) {
  await writeFile(join(assets, serverArchiveName("1.2.3", target)), "unused fixture");
  await writeFile(join(assets, installerName("1.2.3", target)), "unused fixture");
  await writeFile(join(assets, `spool-1.2.3-${target}.tar.gz`), "unused fixture");
}
execFileSync("tar", ["-czf", join(assets, "spool-1.2.3-linux-amd64.tar.gz"), "-C", payload, "spool"], { env: { ...process.env, COPYFILE_DISABLE: "1" } });
const keys = generateKeyPairSync("ed25519");
const pub = keys.publicKey.export({ format: "der", type: "spki" }).subarray(-32);
const seed = keys.privateKey.export({ format: "der", type: "pkcs8" }).subarray(-32);
await signRelease(assets, {
  RELEASE_TAG: "v1.2.3", GITHUB_REPOSITORY: "owner/spool", RELEASE_NOTES_FILE: notes,
  MYGO_UPDATER_PUBLIC_KEY: pub.toString("base64"),
  MYGO_UPDATER_PRIVATE_KEY: Buffer.concat([seed, pub]).toString("base64"),
});
await writeFile(join(directory, "check.sh"), `#!/bin/sh
set -eu
cd "$(dirname "$0")"
fixture="$PWD"
for scenario in success failure canceled; do
  root="$fixture/$scenario"
  mkdir -p "$root/home" "$root/bin" "$root/tmp" "$root/assets"
  for tool in sh uname id openssl mkdir rmdir rm mktemp tr dirname cp cat mv ln chmod unzip curl; do
    ln -s "$(command -v "$tool")" "$root/bin/$tool"
  done
  cp "$fixture/assets/"* "$root/assets/"
  if [ "$scenario" = failure ]; then printf x | dd of="$root/assets/spool-1.2.3-linux-amd64.tar.gz" bs=1 count=1 conv=notrunc status=none; fi
  if [ "$scenario" = canceled ]; then
    openssl_path="$(command -v openssl)"
    rm "$root/bin/openssl"
    printf '#!/bin/sh\n"%s" "$@" || exit $?\nprevious=\nfor argument do\n  if [ "$previous" = -in ]; then case "$argument" in */digest) kill -TERM "$PPID" ;; esac; fi\n  previous="$argument"\ndone\n' "$openssl_path" > "$root/bin/openssl"
    chmod 700 "$root/bin/openssl"
  fi
  status=0
  HOME="$root/home" BUN_INSTALL="$root/home/.bun" XDG_DATA_HOME="$root/home/.local/share" PATH="$root/bin" TMPDIR="$root/tmp" \\
    /bin/sh "$root/assets/install.sh" --skip-dependencies "$root/assets/spool-1.2.3-linux-amd64.tar.gz" >"$root/install.log" 2>&1 || status=$?
  cat "$root/install.log"
  grep -q 'downloading temporary Bun' "$root/install.log"
  if [ "$scenario" = success ]; then
    [ "$status" = 0 ]
    [ "$("$root/home/.local/spool.app/spool")" = cleanup-smoke-app ]
  elif [ "$scenario" = failure ]; then
    [ "$status" != 0 ]
    grep -q 'invalid archive signature' "$root/install.log"
  else
    [ "$status" != 0 ]
    [ ! -e "$root/home/.local/spool.app" ]
  fi
  [ -z "$(find "$root/home/.local" -maxdepth 1 -name '.spool*' -print)" ]
  [ -z "$(find "$root/tmp" -mindepth 1 -print)" ]
  [ ! -e "$root/home/.bun" ]
  echo "BUN_CLEANUP_OK $scenario: private runtime and scratch removed; HOME/.bun absent"
done
`, { mode: 0o755 });
console.log(`Installer cleanup fixture prepared: ${directory}`);
