# Desktop updates

[简体中文](desktop-updates.zh-CN.md)

Official Desktop releases check the stable GitHub release feed. Spool verifies
the exact manifest bytes using its Ed25519 key, then MyGo verifies the archive's
signature before extracting and installing it. Signature failures stop the update.
Development builds do not enable automatic updates. Installations must be writable;
package-managed installations should be updated using their package manager.

Automatic installation retains Spool's existing checks for active chats and its
update lease. Downloading, installing and restarting remain separate states.
On macOS, MyGo 0.10 stages the new bundle beside the installed app and replaces it
after the process exits. **Installed** means the update is ready; the running
process still uses its current version until restart. On Windows and Linux,
`.mygo-files` records the entries owned by the application. Later updates remove
obsolete owned entries and preserve unrelated files, including portable data and
installer uninstallers. Keep user data outside the macOS `.app` bundle.

## Compatibility between releases

A release carrying MyGo 0.10 can be installed by MyGo 0.6.4. The installed app then
uses the new updater for its next update. A separate transitional release is not
required, and clients may skip intermediate Spool releases while the following
contract remains compatible:

- Keep the existing update signing key and `spool-update-v1` manifest schema.
- Publish `update-OS-ARCH.json`, its detached `.json.sig`, and
  `spool-VERSION-OS-ARCH.tar.gz` for each supported target.
- Sign the final archive bytes and manifest bytes after packaging, using the same
  release version. Increment the **Spool** version; changing MyGo alone does not
  offer an update to clients already on that Spool version.
- Preserve the tar archive layout expected by both updaters. DMG and Windows
  installer filenames do not determine the automatic-update archive URL.

Spool now supplies MyGo's `Updater.SetSource` with an authenticated source. This
replaces the previous interception of the global HTTP client, which MyGo's newer
dedicated client bypasses. The source retains Spool's dynamic proxy configuration
and permits only archive URLs and sizes from verified manifests.

MyGo also supports prerelease selection and delta updates. Spool currently uses
stable full archives: enabling either feature needs corresponding authenticated
feed, manifest validation and release-publishing changes. Neither is required to
benefit from the new installation and relaunch behavior.

## Regression checks

Ordinary `internal/appupdate` tests build actual release-mode executables with
MyGo 0.6.4 and 0.10.0, then exercise old-to-new and new-to-new installation. They
also check interrupted/canceled downloads and preservation of user files. A
temporary alternate module file builds the legacy fixture without changing the
repository's dependency version or replacing reusable caches.

The native relaunch check additionally verifies single-instance lock acquisition,
quit cleanup and the version of the restarted process:

```sh
sh tools/run.sh updater-e2e sh -eu -c '
  SPOOL_UPDATER_E2E=1 go test -v -timeout=5m ./internal/appupdate \
    -run "^TestMyGoUpgradeReplacesApplicationAndPreservesUserData$"
'
```

Build and release CI run this check on each native OS/architecture target. Linux
requires a display, supplied by Xvfb and a D-Bus session in CI. These fixtures
test the actual updater and lifecycle APIs; they are not a full historical Spool
application or a published-release download test.
