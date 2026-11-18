package voxl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"spool/internal/testutil/voxlfixture"
	"spool/internal/voxl/macapp"
)

func TestMacOSMigratesExistingBinaryWithoutStartingOrRebuilding(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	voxlfixture.Install(t, source)
	root := filepath.Join(t.TempDir(), "voxl")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(root, "bin", "voxl")
	if err := os.Link(filepath.Join(source, "bin", "voxl"), legacy); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(legacy)
	m := New(root, nil)
	m.SetEnabled(t.Context(), true)
	if m.Status().Installed || m.Status().UpgradeAvailable || m.Available() {
		t.Fatal("legacy binary bypassed local helper installation")
	}
	// No Cargo, Rust or Homebrew on PATH: migration must only use the existing
	// binary and the system compiler/signing tools.
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := m.Install(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !m.Available() || m.Status().Running || m.Status().UpgradeAvailable || m.Status().Application != macapp.Bundle(root) {
		t.Fatal("migration did not finish without launching", m.Status())
	}
	retained, _ := os.ReadFile(legacy)
	if sha256.Sum256(original) != sha256.Sum256(retained) {
		t.Fatal("migration modified the old binary")
	}
	_, _, _, release, err := m.Acquire(t.Context(), "/ws")
	if err != nil {
		t.Fatal("migrated application cannot start", err)
	}
	t.Cleanup(m.Stop)
	release()
}

func TestMacOSCanceledPackagingPublishesNothing(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	voxlfixture.Install(t, source)
	root := filepath.Join(t.TempDir(), "voxl")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := macapp.Install(ctx, root, filepath.Join(source, "bin", "voxl"))
	if !errors.Is(err, context.Canceled) || macapp.Installed(root) {
		t.Fatal("canceled installation published a bundle", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "bin" {
		t.Fatal("canceled installation left scratch data", entries, err)
	}
}

func TestMacOSSupportedOlderHelperCanConnectWithoutUpgrading(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voxl")
	voxlfixture.Install(t, root)
	marker := filepath.Join(macapp.Bundle(root), "Contents", "Resources", "installation-format")
	if err := os.WriteFile(marker, []byte("2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", macapp.Bundle(root)).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	m := New(root, nil)
	m.latestRelease = func(context.Context) (string, error) {
		t.Fatal("connecting with an older helper must not check upstream")
		return "", nil
	}
	m.SetEnabled(t.Context(), true)
	t.Cleanup(m.Stop)
	if status := m.Status(); !status.Installed || !status.UpgradeAvailable || status.Running || !m.Available() {
		t.Fatal("supported older helper must remain usable with an optional upgrade", status)
	}
	for range 2 {
		_, _, _, release, err := m.Acquire(t.Context(), "/ws")
		if err != nil {
			t.Fatal("older helper cannot connect without upgrading", err)
		}
		if !m.Status().Running || !m.Status().UpgradeAvailable {
			t.Fatal("older helper lost connection or optional upgrade state", m.Status())
		}
		release()
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "2\n" {
		t.Fatal("connecting replaced the older helper", err)
	}
}

func TestMacOSOriginalHelperRequiresUpgradeBeforeConnecting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voxl")
	voxlfixture.Install(t, root)
	// Compile the actual original helper, rather than relabeling a helper that
	// already has the startup cleanup fix. It must stay blocked until replaced.
	app := macapp.Bundle(root)
	launcher := filepath.Join(app, "Contents", "MacOS", "voxl-helper")
	source, err := filepath.Abs("testdata/helper-format1.m")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "/usr/bin/xcrun", "--sdk", "macosx", "clang", "-O2", "-fobjc-arc", "-Wall", "-Wextra", "-Werror", "-mmacosx-version-min=12.0", "-framework", "AppKit", "-framework", "Foundation", source, "-o", launcher)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal("compile original helper:", err, string(output))
	}
	marker := filepath.Join(app, "Contents", "Resources", "installation-format")
	if err := os.WriteFile(marker, []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(app, "Contents", "Info.plist")
	info, err := os.ReadFile("testdata/helper-format1.plist")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plist, info, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.CommandContext(t.Context(), "/usr/bin/codesign", "--force", "--sign", "-", app).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	before, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := os.ReadFile(macapp.Executable(root))
	if err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "capture-started")
	t.Setenv("SPOOL_TEST_VOXL_STARTED", capture)
	t.Setenv("PATH", "/usr/bin:/bin")
	m := New(root, nil)
	m.SetEnabled(t.Context(), true)
	t.Cleanup(m.Stop)
	checks := 0
	m.latestRelease = func(context.Context) (string, error) {
		checks++
		return "", errors.New("offline")
	}
	assertBlocked := func() {
		t.Helper()
		if status := m.Status(); status.Installed || !status.UpgradeAvailable || status.Running || status.Installing || m.Available() {
			t.Fatal("original helper must require a local upgrade before connecting", status)
		}
		for _, resource := range []string{"/", "/ws", "/files/download"} {
			if _, _, _, release, err := m.Acquire(t.Context(), resource); err == nil {
				release()
				t.Fatal("unsafe original helper accepted a connection", resource)
			}
		}
		if _, err := os.Stat(capture); !os.IsNotExist(err) {
			t.Fatal("blocked helper started the engine", err)
		}
	}
	assertBlocked()
	if checks != 0 {
		t.Fatal("checking availability or connecting checked upstream", checks)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.Install(ctx); err == nil {
		t.Fatal("canceled original helper upgrade succeeded")
	}
	assertBlocked()
	if err := m.Install(t.Context()); err == nil {
		t.Fatal("offline original helper upgrade succeeded")
	}
	assertBlocked()
	if data, err := os.ReadFile(launcher); err != nil || !bytes.Equal(data, before) {
		t.Fatal("failed upgrade changed the original launcher", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "1\n" {
		t.Fatal("failed upgrade changed the original installation format", err)
	}
	m.latestRelease = func(context.Context) (string, error) { checks++; return Version, nil }
	if err := m.Install(t.Context()); err != nil {
		t.Fatal("original helper could not upgrade using its bundled engine", err)
	}
	if status := m.Status(); !status.Installed || status.UpgradeAvailable || status.Running || status.Error != "" || !m.Available() {
		t.Fatal("upgraded helper is not available", status)
	}
	if data, err := os.ReadFile(launcher); err != nil || bytes.Equal(data, before) {
		t.Fatal("upgrade did not replace the unsafe launcher", err)
	}
	if data, err := os.ReadFile(macapp.Executable(root)); err != nil || !bytes.Equal(data, engine) {
		t.Fatal("helper-only upgrade changed the bundled engine", err)
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatal("upgrade started the engine", err)
	}
	if err := m.Install(t.Context()); err != nil || checks != 2 {
		t.Fatal("current helper repeated an upstream check", checks, err)
	}
	_, _, _, release, err := m.Acquire(t.Context(), "/ws")
	if err != nil {
		t.Fatal("upgraded original helper cannot connect", err)
	}
	release()
}

func TestMacOSHelperUpgradeReusesEngineWhenNoNewerReleaseExists(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voxl")
	voxlfixture.Install(t, root)
	m := New(root, nil)
	checks := 0
	m.latestRelease = func(context.Context) (string, error) {
		checks++
		return "0.3.3", nil
	}
	m.SetEnabled(t.Context(), true)
	t.Cleanup(m.Stop)
	if !m.Status().Installed || m.Status().UpgradeAvailable {
		t.Fatal("current helper needs no upgrade", m.Status())
	}
	// Cargo installations have no legacy bin/voxl outside the application.
	if err := os.Remove(filepath.Join(root, "bin", "voxl")); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(macapp.Bundle(root), "Contents", "Resources", "installation-format")
	for _, version := range []string{"99\n", "2\n"} {
		if err := os.WriteFile(marker, []byte(version), 0600); err != nil {
			t.Fatal(err)
		}
		if !m.Status().Installed || !m.Status().UpgradeAvailable || !m.Available() {
			t.Fatal("installed helper must stay usable with an optional upgrade", version, m.Status())
		}
	}
	if output, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", macapp.Bundle(root)).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	engine, err := os.ReadFile(macapp.Executable(root))
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(root, "shared", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(shared), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	// A failed candidate lookup must fail locally, never reach a package manager.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	fallback := filepath.Join(home, ".cargo", "bin", "voxl")
	if err := os.MkdirAll(filepath.Dir(fallback), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallback, []byte("must use the bundled engine"), 0700); err != nil {
		t.Fatal(err)
	}
	if !m.Status().Installed || !m.Available() || !macapp.NeedsUpgrade(root) {
		t.Fatal("original helper must stay available before an optional upgrade")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.Install(ctx); err == nil {
		t.Fatal("canceled upgrade succeeded")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "2\n" {
		t.Fatal("failed upgrade changed the old app", err)
	}
	if !m.Status().UpgradeAvailable || !m.Available() {
		t.Fatal("canceled upgrade must preserve availability and allow retry", m.Status())
	}
	if err := m.Install(t.Context()); err != nil {
		t.Fatal("upgrade without Cargo or a legacy binary failed", err)
	}
	if !m.Available() || m.Status().Running || m.Status().UpgradeAvailable || macapp.NeedsUpgrade(root) {
		t.Fatal("upgrade did not publish the fixed helper without launching", m.Status())
	}
	if data, err := os.ReadFile(macapp.Executable(root)); err != nil || sha256.Sum256(data) != sha256.Sum256(engine) {
		t.Fatal("upgrade changed the bundled engine", err)
	}
	if data, err := os.ReadFile(shared); err != nil || string(data) != "unchanged" {
		t.Fatal("upgrade changed shared data", err)
	}
	if checks != 1 {
		t.Fatal("helper upgrade did not check upstream exactly once", checks)
	}
	if err := m.Install(t.Context()); err != nil || checks != 1 {
		t.Fatal("current helper repeated an upstream check or upgrade", checks, err)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", macapp.Bundle(root)).CombinedOutput(); err != nil {
		t.Fatal("upgraded bundle has an invalid signature", err, string(output))
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 2 {
		t.Fatal("upgrade left staging data", entries, err)
	}
	_, _, _, release, err := m.Acquire(t.Context(), "/ws")
	if err != nil {
		t.Fatal("upgraded application cannot start", err)
	}
	release()
}
