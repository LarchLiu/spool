package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDataDir(t *testing.T) {
	t.Run("home", func(t *testing.T) {
		t.Setenv("SPOOL_HOME", "")
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		if got, want := DefaultDataDir(), filepath.Join(home, ".spool"); got != want {
			t.Fatalf("DefaultDataDir() = %q, want %q", got, want)
		}
	})
	t.Run("override", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("SPOOL_HOME", "  "+root+"  ")
		if got := DefaultDataDir(); got != root {
			t.Fatalf("DefaultDataDir() = %q, want %q", got, root)
		}
	})
}
