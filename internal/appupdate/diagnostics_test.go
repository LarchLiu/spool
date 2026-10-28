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

func TestUpdateLogsMoveIntoLogDirectoryWithoutLosingHistory(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"update.log", "update.log.1"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("existing-"+name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d, err := OpenDiagnostics(root, "0.1.2")
	if err != nil {
		t.Fatal(err)
	}
	if d.Path != filepath.Join(root, "log", "update.log") {
		t.Fatal("update log escaped log/", d.Path)
	}
	for _, name := range []string{"update.log", "update.log.1"} {
		data, err := os.ReadFile(filepath.Join(root, "log", name))
		if err != nil || !strings.Contains(string(data), "existing-"+name) {
			t.Fatalf("legacy log was not preserved: %s, %s, %v", name, data, err)
		}
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatal("legacy log was left outside log/", name, err)
		}
	}
	if _, err := OpenDiagnostics(root, "0.1.2"); err != nil {
		t.Fatal("log migration is not idempotent", err)
	}
}

func TestUpdateLogsStayWithinSizeCap(t *testing.T) {
	d, err := OpenDiagnostics(t.TempDir(), "0.1.2")
	if err != nil {
		t.Fatal(err)
	}
	for range 300 {
		if err := d.Record("failed", "0.1.2", errors.New(strings.Repeat("x", 2048))); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{d.Path, d.Path + ".1"} {
		info, err := os.Stat(path)
		if err != nil || info.Size() > 256<<10 {
			t.Fatalf("update log exceeds size cap: %s, %v, %v", path, info, err)
		}
	}
}
