package voxl

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
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
	if m.Status().Installed || m.Available() {
		t.Fatal("legacy binary bypassed local helper installation")
	}
	// No Cargo, Rust or Homebrew on PATH: migration must only use the existing
	// binary and the system compiler/signing tools.
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := m.Install(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !m.Available() || m.Status().Running || m.Status().Application != macapp.Bundle(root) {
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
