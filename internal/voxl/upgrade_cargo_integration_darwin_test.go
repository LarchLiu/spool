package voxl

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"spool/internal/testutil/voxlfixture"
	"spool/internal/voxl/macapp"
)

// Opt in on a Mac with Rust and the native build dependencies installed. This
// builds the released crate with real Cargo, then verifies the published helper
// without launching capture. Ordinary tests keep using private tool fixtures.
func TestHelperUpgradeWithRealCargo(t *testing.T) {
	if os.Getenv("SPOOL_TEST_VOXL_REAL_CARGO") != "1" {
		t.Skip("set SPOOL_TEST_VOXL_REAL_CARGO=1 to build the real voxl 0.3.5 release")
	}
	root := filepath.Join(t.TempDir(), "voxl")
	voxlfixture.Install(t, root)
	requireHelperUpgrade(t, root)
	shared := filepath.Join(root, "shared", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(shared), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	m := New(root, nil)
	m.latestRelease = func(context.Context) (string, error) { return "0.3.5", nil }
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	if err := m.Install(ctx); err != nil {
		t.Fatal("real Cargo build and helper upgrade failed:", err)
	}
	if status := m.Status(); !status.Installed || status.UpgradeAvailable || status.Installing || status.Running || status.Error != "" {
		t.Fatal("real upgrade did not publish a current, idle helper", status)
	}
	version, err := engineVersion(ctx, m.executable())
	if err != nil || version != "v0.3.5" {
		t.Fatalf("published engine version = %q, err = %v", version, err)
	}
	if output, err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", macapp.Bundle(root)).CombinedOutput(); err != nil {
		t.Fatal("real upgraded bundle has an invalid signature:", err, string(output))
	}
	if data, err := os.ReadFile(shared); err != nil || string(data) != "unchanged" {
		t.Fatal("real upgrade changed shared data:", err)
	}
	for _, name := range []string{"voxl", "voxl-helper"} {
		if _, err := os.Stat(filepath.Join(macapp.Bundle(root), "Contents", "MacOS", name)); err != nil {
			t.Fatal("real upgrade did not publish", name, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 2 || entries[0].Name() != "bin" || entries[1].Name() != "shared" {
		t.Fatal("real upgrade left staging data:", entries, err)
	}
	t.Log("real voxl 0.3.5 built, packaged, signed and verified; old staging removed and shared files preserved")
}
