package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"spool/internal/issues"
)

func TestGitHubCLIPathPersistsAndRejectsInvalidChanges(t *testing.T) {
	s := testStore(t)
	if path, err := s.GitHubCLIPath(); err != nil || path != "" {
		t.Fatalf("new installation should auto-detect: %q, %v", path, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveGitHubCLIPath("  " + executable + "  "); err != nil {
		t.Fatal(err)
	}
	other, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	for _, invalid := range []string{"gh", "relative/gh", filepath.Join(t.TempDir(), "missing-gh"), t.TempDir()} {
		if err := s.SaveGitHubCLIPath(invalid); err == nil {
			t.Fatalf("invalid path %q was accepted", invalid)
		}
		if path, err := other.GitHubCLIPath(); err != nil || path != executable {
			t.Fatalf("invalid edit changed persisted path: %q, %v", path, err)
		}
	}
	if err := s.SaveSettings(DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	if path, err := other.GitHubCLIPath(); err != nil || path != executable {
		t.Fatalf("saving service settings replaced GitHub CLI path: %q, %v", path, err)
	}
	if err := s.SaveGitHubCLIPath("  "); err != nil {
		t.Fatal(err)
	}
	if path, err := other.GitHubCLIPath(); err != nil || path != "" {
		t.Fatalf("clearing the path did not restore auto-detection: %q, %v", path, err)
	}
}

func TestRunIssueCommandUsesLiveSavedGitHubCLIPath(t *testing.T) {
	s := testStore(t)
	directory := filepath.Join(t.TempDir(), "custom GitHub CLI")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(directory, name)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(executable, path); err != nil {
		data, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SPOOL_TEST_SAVED_GH_PROCESS", "1")
	t.Setenv("PATH", t.TempDir())
	// Capture the runner before saving, as an already-running scheduler does.
	run := s.RunIssueCommand
	if err := s.SaveGitHubCLIPath(path); err != nil {
		t.Fatal(err)
	}
	hint, err := s.issueGitHubCLIHint()
	encoded, _ := json.Marshal(path)
	if err != nil || !strings.Contains(hint, string(encoded)) || !strings.Contains(hint, "PowerShell") {
		t.Fatalf("agent instructions lost the configured path or cross-platform quoting: %q, %v", hint, err)
	}
	prompt, err := issuePrompt(Project{Name: "fixture"}, issues.Checkout{Root: directory, Repository: "fixture/issues"}, issues.Issue{Number: 1}, issues.Record{}, false, hint, "http://127.0.0.1/callback", "fixture-token")
	if err != nil || !strings.HasPrefix(prompt, hint) {
		t.Fatalf("scheduled agent prompt omitted the executable hint: %v", err)
	}
	status := issues.CheckCLI(context.Background(), func(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
		args = append([]string{"-test.run=^TestSavedGitHubCLIProcess$", "--"}, args...)
		return run(ctx, directory, name, args...)
	})
	if status != (issues.CLIStatus{State: issues.CLIReady, Login: "configured-user"}) {
		t.Fatalf("checks/scheduled commands did not use saved executable: %+v", status)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := run(context.Background(), "", "gh", "--version"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed override must fail rather than auto-detect another gh: %v", err)
	}
}

func TestSavedGitHubCLIProcess(t *testing.T) {
	if os.Getenv("SPOOL_TEST_SAVED_GH_PROCESS") != "1" {
		return
	}
	if os.Args[len(os.Args)-1] == "--version" {
		fmt.Print("gh version configured fixture")
	} else {
		fmt.Print(`{"login":"configured-user"}`)
	}
	os.Exit(0)
}
