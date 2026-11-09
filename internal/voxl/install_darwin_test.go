package voxl

import (
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
	if m.Status().Installed || m.Status().UpgradeRequired || m.Available() {
		t.Fatal("legacy binary bypassed local helper installation")
	}
	// No Cargo, Rust or Homebrew on PATH: migration must only use the existing
	// binary and the system compiler/signing tools.
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := m.Install(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !m.Available() || m.Status().Running || m.Status().UpgradeRequired || m.Status().Application != macapp.Bundle(root) {
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

func TestMacOSUpgradesOriginalHelperUsingItsBundledEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voxl")
	voxlfixture.Install(t, root)
	m := New(root, nil)
	m.SetEnabled(t.Context(), true)
	t.Cleanup(m.Stop)
	if !m.Status().Installed || m.Status().UpgradeRequired {
		t.Fatal("current helper needs no upgrade", m.Status())
	}
	// Cargo installations have no legacy bin/voxl outside the application.
	if err := os.Remove(filepath.Join(root, "bin", "voxl")); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(macapp.Bundle(root), "Contents", "Resources", "installation-format")
	for _, version := range []string{"3\n", "1\n"} {
		if err := os.WriteFile(marker, []byte(version), 0600); err != nil {
			t.Fatal(err)
		}
		if m.Status().Installed || !m.Status().UpgradeRequired || m.Available() {
			t.Fatal("incompatible helper must require an upgrade", version, m.Status())
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
	if m.Status().Installed || m.Available() || !macapp.NeedsUpgrade(root) {
		t.Fatal("original helper was accepted without the startup fix")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.Install(ctx); err == nil {
		t.Fatal("canceled upgrade succeeded")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "1\n" {
		t.Fatal("failed upgrade changed the old app", err)
	}
	if !m.Status().UpgradeRequired {
		t.Fatal("canceled upgrade must remain available to retry", m.Status())
	}
	if err := m.Install(t.Context()); err != nil {
		t.Fatal("upgrade without Cargo or a legacy binary failed", err)
	}
	if !m.Available() || m.Status().Running || m.Status().UpgradeRequired || macapp.NeedsUpgrade(root) {
		t.Fatal("upgrade did not publish the fixed helper without launching", m.Status())
	}
	if data, err := os.ReadFile(macapp.Executable(root)); err != nil || sha256.Sum256(data) != sha256.Sum256(engine) {
		t.Fatal("upgrade changed the bundled engine", err)
	}
	if data, err := os.ReadFile(shared); err != nil || string(data) != "unchanged" {
		t.Fatal("upgrade changed shared data", err)
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
