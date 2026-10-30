package spoolnet

import (
	"os"
	"path/filepath"
	"testing"

	"tailscale.com/logpolicy"
)

func TestLogStorageUsesPrivateDirectory(t *testing.T) {
	t.Setenv("TS_LOGS_DIR", "")
	dataDir := filepath.Join(t.TempDir(), "tsnet")
	if err := prepareLogStorage(dataDir); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataDir, "logs")
	if got := os.Getenv("TS_LOGS_DIR"); got != want {
		t.Fatalf("TS_LOGS_DIR = %q, want %q", got, want)
	}
	info, err := os.Stat(want)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("log directory must be private: %v, %v", info, err)
	}
	if got := logpolicy.LogsDir(func(string, ...any) {}); got != want {
		t.Fatalf("backend log directory = %q, want %q", got, want)
	}
	if err := prepareLogStorage(dataDir); err != nil {
		t.Fatalf("reconnect could not reuse log directory: %v", err)
	}
}

func TestLogStorageFailureDoesNotChangeEnvironment(t *testing.T) {
	t.Setenv("TS_LOGS_DIR", "previous-directory")
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "logs"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareLogStorage(dataDir); err == nil {
		t.Fatal("accepted a regular file as the log directory")
	}
	if got := os.Getenv("TS_LOGS_DIR"); got != "previous-directory" {
		t.Fatalf("failed setup changed TS_LOGS_DIR to %q", got)
	}
}
