package macapp_test

import (
	"os"
	"path/filepath"
	"testing"

	"spool/internal/voxl/macapp"
)

func TestInstalledRequiresSafeHelperVersion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		format    string
		installed bool
		upgrade   bool
	}{
		{"missing", "", false, false},
		{"original", "1\n", false, true},
		{"startup-cleanup-fix", "2\n", true, true},
		{"current", "3\n", true, false},
		{"newer", "99\n", true, true},
		{"invalid", "damaged\n", false, true},
		{"zero", "0\n", false, true},
		{"negative", "-1\n", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			app := macapp.Bundle(root)
			for _, path := range []string{
				macapp.Executable(root),
				filepath.Join(app, "Contents", "MacOS", "voxl-helper"),
				filepath.Join(app, "Contents", "Info.plist"),
				filepath.Join(app, "Contents", "Resources", "installation-format"),
			} {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				data := []byte("fixture")
				if filepath.Base(path) == "installation-format" {
					data = []byte(tc.format)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := macapp.Installed(root); got != tc.installed {
				t.Fatalf("Installed = %v, want %v", got, tc.installed)
			}
			if got := macapp.NeedsUpgrade(root); got != tc.upgrade {
				t.Fatalf("NeedsUpgrade = %v, want %v", got, tc.upgrade)
			}
		})
	}
}
