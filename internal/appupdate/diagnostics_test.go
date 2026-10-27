package appupdate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRestartIsConfirmedOnlyWhenExpectedVersionIsReady(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDiagnostics(root, "0.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.RequestRestart("0.1.2"); err != nil {
		t.Fatal(err)
	}
	d, err = OpenDiagnostics(root, "0.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Ready(); err == nil {
		t.Fatal("old build falsely confirmed a restart")
	}
	if _, err = os.Stat(filepath.Join(root, "update-pending.json")); err != nil {
		t.Fatal("unconfirmed record was discarded")
	}
	d, err = OpenDiagnostics(root, "0.1.2")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(d.Path)
	if strings.Contains(string(data), "restart_confirmed") {
		t.Fatal("startup before GUI readiness confirmed a restart")
	}
	if err = d.Ready(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "update-pending.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(d.Path)
	for _, event := range []string{"restart_requested", "restart_unconfirmed", "restart_confirmed"} {
		if !strings.Contains(string(data), event) {
			t.Fatal(event)
		}
	}
}

func TestDiagnosticsRotateRedactAndFailVisibly(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDiagnostics(root, "0.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(d.Path, make([]byte, 256<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = d.Record("install_failed", "0.1.2", errors.New("Get https://cdn.test/file?token=secret: unexpected EOF")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(d.Path + ".1"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(d.Path)
	if strings.Contains(string(data), "secret") || !strings.Contains(string(data), "install_failed") {
		t.Fatal(string(data))
	}
	info, _ := os.Stat(d.Path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal(info.Mode())
	}
	if err = d.RequestRestart("invalid"); err == nil {
		t.Fatal("invalid pending version accepted")
	}
	if err = os.Mkdir(d.pendingPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = d.RequestRestart("0.1.2"); err == nil {
		t.Fatal("pending record write failure was hidden")
	}
}

func TestDiagnosticsReplacePendingAndRetainLoggingAfterCorruption(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDiagnostics(root, "0.1.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"0.1.2", "0.1.3"} {
		if err = d.RequestRestart(version); err != nil {
			t.Fatal(err)
		}
	}
	d, err = OpenDiagnostics(root, "0.1.3")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Ready(); err != nil {
		t.Fatal("replacement pending version was not confirmed:", err)
	}
	if err = os.WriteFile(d.pendingPath, []byte("invalid JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err = OpenDiagnostics(root, "0.1.3")
	if err != nil {
		t.Fatal("corrupt pending record disabled diagnostics:", err)
	}
	if err = d.Ready(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(d.Path)
	if err != nil || !strings.Contains(string(data), "restart_record_invalid") {
		t.Fatalf("corruption was not reported: %s, %v", data, err)
	}
}
