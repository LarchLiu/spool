package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDataDir(t *testing.T) {
	for _, development := range []bool{false, true} {
		name := ".spool"
		if development {
			name = ".spool-dev"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("SPOOL_HOME", "")
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			if got, want := DefaultDataDir(development), filepath.Join(home, name); got != want {
				t.Fatalf("DefaultDataDir(%v) = %q, want %q", development, got, want)
			}
			root := t.TempDir()
			t.Setenv("SPOOL_HOME", "  "+root+"  ")
			if got := DefaultDataDir(development); got != root {
				t.Fatalf("DefaultDataDir(%v) = %q, want %q", development, got, root)
			}
		})
	}
}
