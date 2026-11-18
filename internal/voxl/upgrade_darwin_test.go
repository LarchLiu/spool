package voxl

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"spool/internal/testutil/voxlfixture"
	"spool/internal/voxl/macapp"
)

func requireHelperUpgrade(t *testing.T, root string) {
	t.Helper()
	marker := filepath.Join(macapp.Bundle(root), "Contents", "Resources", "installation-format")
	if err := os.WriteFile(marker, []byte("2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", macapp.Bundle(root)).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
}

func TestHelperUpgradeBuildsNewerEngineAndPublishesBothTogether(t *testing.T) {
	root, tools := filepath.Join(t.TempDir(), "voxl"), t.TempDir()
	voxlfixture.Install(t, root)
	requireHelperUpgrade(t, root)
	m := New(root, nil)
	m.SetEnabled(t.Context(), true)
	t.Cleanup(m.Stop)
	checks := 0
	m.latestRelease = func(context.Context) (string, error) {
		checks++
		return "0.4.0", nil
	}
	before, err := os.ReadFile(m.executable())
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(tools, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// All dependency and Cargo commands use these private scripts. No package
	// manager, real Rust toolchain, network or developer home is used.
	script := `#!/bin/sh
set -eu
tool=${0##*/}
if [ "${1-}" = --version ]; then
  printf '%s 1.85.0\n' "$tool"
  exit 0
fi
case "$tool" in
  brew) printf '%s\n' "$SPOOL_TEST_UPDATE_TOOLS" ;;
  xcrun)
    case "${1-}" in
      --show-sdk-path) printf '%s\n' "$SPOOL_TEST_UPDATE_TOOLS" ;;
      --find) printf '%s/bin/clang\n' "$SPOOL_TEST_UPDATE_TOOLS" ;;
      *) exit 1 ;;
    esac ;;
  pkg-config) exit 0 ;;
  cargo)
    [ "$1" = install ] || exit 1
    printf '%s\n' "$@" > "$SPOOL_TEST_UPDATE_TOOLS/cargo-args"
    [ "${SPOOL_TEST_UPDATE_BUILD_FAIL-}" != 1 ] || exit 1
    destination=
    while [ "$#" -gt 0 ]; do
      if [ "$1" = --root ]; then shift; destination=$1; fi
      shift
    done
    [ -n "$destination" ] || exit 1
    /bin/mkdir -p "$destination/bin"
    /bin/cp "$SPOOL_TEST_UPDATE_ENGINE" "$destination/bin/voxl-server"
    /bin/chmod 700 "$destination/bin/voxl-server" ;;
  *) exit 1 ;;
esac
`
	for _, name := range []string{"cargo", "rustc", "clang", "cc", "pkg-config", "esbuild", "xcrun", "brew"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	libclang := filepath.Join(tools, "libclang")
	if err := os.WriteFile(libclang, []byte("inert dependency probe"), 0600); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"HOME": tools, "CARGO_HOME": filepath.Join(tools, "cargo"), "RUSTUP_HOME": filepath.Join(tools, "rustup"),
		"BUN_INSTALL": tools, "PATH": bin, "LIBCLANG_PATH": libclang, "ESBUILD": filepath.Join(bin, "esbuild"),
		"SPOOL_TEST_UPDATE_TOOLS": tools, "SPOOL_TEST_UPDATE_ENGINE": binary,
	} {
		t.Setenv(key, value)
	}
	local := filepath.Join(tools, ".cargo", "bin", "voxl")
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("old local engine must not be reused"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPOOL_TEST_UPDATE_BUILD_FAIL", "1")
	if err := m.Install(t.Context()); err == nil {
		t.Fatal("failed Cargo build was accepted")
	}
	if after, err := os.ReadFile(m.executable()); err != nil || !bytes.Equal(before, after) {
		t.Fatal("build failure changed the previous engine", err)
	}
	if status := m.Status(); !status.UpgradeAvailable || !status.Installed || status.Installing || !m.Available() {
		t.Fatal("build failure must preserve the usable older helper and optional upgrade", status)
	}
	t.Setenv("SPOOL_TEST_UPDATE_BUILD_FAIL", "0")
	marker := filepath.Join(tools, "capture-started")
	t.Setenv("SPOOL_TEST_VOXL_STARTED", marker)
	if err := m.Install(t.Context()); err != nil {
		t.Fatal("latest release was not built and published:", err)
	}
	args, err := os.ReadFile(filepath.Join(tools, "cargo-args"))
	if err != nil {
		t.Fatal("update did not invoke Cargo:", err)
	}
	if !strings.HasPrefix(string(args), "install\nvoxl-server\n") || !strings.Contains(string(args), "--registry\ncrates-io\n") || !strings.Contains(string(args), "--version\n=0.4.0\n") {
		t.Fatalf("upgrade did not build the detected newer release: %s", args)
	}
	if status := m.Status(); !status.Installed || status.Running || status.Installing || status.Error != "" {
		t.Fatal("latest release did not leave a usable helper", status)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("update started capture", err)
	}
	if err := m.Install(t.Context()); err != nil || checks != 2 {
		t.Fatal("current helper repeated an engine check/build", checks, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "bin" {
		t.Fatal("latest-release update left staging data", entries, err)
	}
}

func TestHelperUpgradeCheckFailurePreservesOldBundleAndCanRetry(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voxl")
	voxlfixture.Install(t, root)
	requireHelperUpgrade(t, root)
	m := New(root, nil)
	m.SetEnabled(t.Context(), true)
	t.Cleanup(m.Stop)
	before, err := os.ReadFile(m.executable())
	if err != nil {
		t.Fatal(err)
	}
	m.latestRelease = func(context.Context) (string, error) { return "", errors.New("offline") }
	if err := m.Install(t.Context()); err == nil {
		t.Fatal("failed registry check was accepted")
	}
	if status := m.Status(); !status.UpgradeAvailable || !status.Installed || status.Installing || status.Error == "" || !m.Available() {
		t.Fatal("failed check lost upgrade/retry state", status)
	}
	if after, err := os.ReadFile(m.executable()); err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed registry check changed the old engine", err)
	}
	_, _, _, release, err := m.Acquire(t.Context(), "/ws")
	if err != nil {
		t.Fatal("older helper cannot connect after a failed upgrade", err)
	}
	release()
	// A newer local development build must not be replaced by an older stable
	// upstream release. The helper itself still upgrades after a successful check.
	t.Setenv("SPOOL_TEST_VOXL_VERSION", "voxl-server 0.5.0-dev")
	m.latestRelease = func(context.Context) (string, error) { return "0.4.0", nil }
	if err := m.Install(t.Context()); err != nil {
		t.Fatal("helper upgrade could not reuse a newer local engine", err)
	}
	if status := m.Status(); !status.Installed || status.UpgradeAvailable || status.Error != "" {
		t.Fatal("retry did not finish upgrading the helper", status)
	}
	if after, err := os.ReadFile(m.executable()); err != nil || !bytes.Equal(before, after) {
		t.Fatal("helper upgrade downgraded the local engine", err)
	}
}
