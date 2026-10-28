package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"spool/internal/workspace"
)

func TestStartupErrorViewNeedsNoServiceAndKeepsRecoveryActions(t *testing.T) {
	root := t.TempDir()
	called := map[string]int{}
	var copied string
	screen := &startupFailure{root: root, version: "0.1.1", logPath: filepath.Join(root, "log", "desktop.log"), cause: errors.New("unsupported database schema 2"),
		retry: func() { called["retry"]++ }, showData: func() { called["data"]++ }, showLogs: func() { called["log"]++ }, quit: func() { called["quit"]++ }, copy: func(value string) { copied = value },
	}
	view := ui.NewTester(func(c *ui.Context) { screen.view(c, "en") }, 900, 1000)
	for _, text := range []string{"Spool could not open its data", "unsupported database schema 2", screen.hint(), "Application log: " + screen.logPath} {
		if !view.HasText(text) {
			t.Fatalf("startup error UI hid %q", text)
		}
	}
	for _, button := range []string{"Retry startup", "Show data directory", "Show application log", "Copy diagnostics", "Quit"} {
		if err := view.Click(button); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []string{"retry", "data", "log", "quit"} {
		if called[action] != 1 {
			t.Fatal("recovery action was not available", action, called)
		}
	}
	if !strings.Contains(copied, "unsupported database schema 2") || !strings.Contains(copied, filepath.Join(root, "db", "spool.sqlite")) {
		t.Fatal("diagnostic copy omitted the cause or database path", copied)
	}
}

func TestStartupErrorViewWorksEvenWithoutFileLogging(t *testing.T) {
	screen := &startupFailure{cause: errors.New("permission denied")}
	view := ui.NewTester(func(c *ui.Context) { screen.view(c, "en") }, 900, 1000)
	if !view.HasText("File logging is unavailable. Diagnostic output is sent to stderr.") {
		t.Fatal("logging failure was hidden")
	}
	if err := view.Click("Show application log"); err != nil {
		t.Fatal(err)
	}
}

func TestStartupErrorHintsCoverDatabaseAndPermissions(t *testing.T) {
	for message, hint := range map[string]string{
		"unsupported database schema 2": "newer Spool version",
		"another running instance":      "30 seconds",
		"database is locked":            "30 seconds",
		"permission denied":             "writable",
		"file is not a database":        "sidecar files intact",
	} {
		screen := &startupFailure{cause: errors.New(message)}
		if !strings.Contains(screen.hint(), hint) {
			t.Fatal(message, screen.hint())
		}
	}
}

func TestInvalidDatabaseCanBeRenderedWithoutResettingIt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "db", "spool.sqlite")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("not a SQLite database")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := workspace.OpenService(root)
	if err == nil || service != nil {
		t.Fatal("invalid database fixture did not fail", service, err)
	}
	screen := &startupFailure{root: root, cause: err}
	view := ui.NewTester(func(c *ui.Context) { screen.view(c, "en") }, 900, 1000)
	if !view.HasText(err.Error()) || !view.HasText("Retry startup") {
		t.Fatal("database failure prevented the recovery UI")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("recovery view modified or reset the invalid database", err)
	}
}
