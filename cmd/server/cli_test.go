package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"spool/internal/workspace"
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "server-cli-homes-")
	if err != nil {
		panic(err)
	}
	for name, value := range map[string]string{
		"CODEX_HOME": filepath.Join(root, "codex"), "CLAUDE_CONFIG_DIR": filepath.Join(root, "claude"),
		"SPOOL_CODEX_BIN": filepath.Join(root, "missing-codex"), "SPOOL_CLAUDE_BIN": filepath.Join(root, "missing-claude"),
		"SPOOL_RELAY_REGISTRATION_TOKEN": "",
	} {
		if err := os.Setenv(name, value); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

func invoke(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	var out, stderr bytes.Buffer
	err := runCLI(context.Background(), append([]string{"--data-dir", root}, args...), &out, &stderr)
	return out.String(), err
}

func cliOK(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := invoke(t, root, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}

func decode[T any](t *testing.T, text string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("decode %s: %v", text, err)
	}
	return v
}

func TestEveryCommandHelpHasNoDatabaseSideEffects(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-exist")
	var visit func(*command)
	visit = func(c *command) {
		parts := strings.Fields(strings.TrimPrefix(c.path, "spool-server"))
		for _, args := range [][]string{append(append([]string{}, parts...), "--help"), append([]string{"help"}, parts...)} {
			out := cliOK(t, root, args...)
			if !strings.Contains(out, "Usage: "+c.path) || !strings.Contains(out, "--data-dir") {
				t.Fatalf("incomplete help: %s", out)
			}
			c.flags.VisitAll(func(f *flag.Flag) {
				if !strings.Contains(out, "-"+f.Name) {
					t.Errorf("missing %s in %s", f.Name, c.path)
				}
			})
		}
		for _, child := range c.children {
			visit(child)
		}
	}
	visit(newCLI(&bytes.Buffer{}))
	cliOK(t, root)
	cliOK(t, root, "version")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("help/version touched data directory: %v", err)
	}
}

func TestInvalidCommandsDoNotCreateData(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-exist")
	for _, args := range [][]string{
		{"user", "unknown"}, {"user", "add"}, {"project", "update", "--project", "p"},
		{"agent", "set", "--backend", "other", "--enabled=false"}, {"config", "set"},
		{"user", "list", "extra"}, {"project", "show", "--unknown"},
		{"project", "member", "set", "--project", "p", "--user", "u", "--access", "root"},
	} {
		_, err := invoke(t, root, args...)
		var invalid *usageError
		if !errors.As(err, &invalid) {
			t.Errorf("%v: expected usage error, got %v", args, err)
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("invalid syntax touched data: %v", err)
	}
}

func TestLocalAdministrationPreservesFieldsAndPermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	t.Setenv("SPOOL_NEW_PASSWORD", "test-password-long-enough")
	alice := decode[workspace.User](t, cliOK(t, root, "user", "add", "--username", "alice"))
	cliOK(t, root, "user", "add", "--username", "bob", "--nickname", "Bob")
	cliOK(t, root, "user", "update", "--user", "alice", "--nickname", "Alice")
	cliOK(t, root, "user", "disable", "--user", "alice")
	users := decode[[]workspace.User](t, cliOK(t, root, "user", "list"))
	if users[0].Nickname != "Alice" || !users[0].Disabled {
		t.Fatalf("update: %+v", users)
	}
	cliOK(t, root, "user", "enable", "--user", alice.ID)
	project := decode[localProject](t, cliOK(t, root, "project", "add", "--name", "Example", "--directory", t.TempDir(), "--members", "alice", "--shared-chats=true"))
	cliOK(t, root, "project", "member", "set", "--project", project.ID, "--user", "bob", "--access", "read-only", "--own-access", "read-only")
	cliOK(t, root, "project", "member", "set", "--project", "Example", "--user", "bob")
	cliOK(t, root, "project", "update", "--project", "Example", "--name", "Renamed")
	s, err := workspace.OpenAdministration(root)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := findUser(s.Store, "bob")
	if err != nil {
		t.Fatal(err)
	}
	p, err := findProject(s.Store, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Renamed" || !p.SharedChats || !s.Store.CanAccessProject(bob.ID, p.ID) || s.Store.CanCreateChat(bob.ID, p.ID) || p.MemberAccess[bob.ID] != "read-only" {
		t.Fatalf("permissions lost: %+v", p)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	cliOK(t, root, "project", "member", "remove", "--project", project.ID, "--user", "bob")
	cliOK(t, root, "project", "archive", "--project", project.ID)
	pj := decode[localProject](t, cliOK(t, root, "project", "show", "--project", project.ID))
	if !pj.Archived || len(pj.Members) != 1 || pj.Path != project.Path {
		t.Fatalf("project: %+v", pj)
	}
	cliOK(t, root, "project", "unarchive", "--project", project.ID)
	cliOK(t, root, "issues", "set", "--project", project.ID, "--owner", "alice", "--interval", "15", "--labels", "bug, cli")
	cliOK(t, root, "issues", "set", "--project", project.ID, "--model", "example")
	i := decode[workspace.IssuesAutomation](t, cliOK(t, root, "issues", "show", "--project", project.ID))
	if i.Enabled || i.IntervalMinutes != 15 || i.UserID != alice.ID || len(i.Labels) != 2 {
		t.Fatalf("schedule fields lost: %+v", i)
	}
}

func TestConfigAgentAndLocking(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	cliOK(t, root, "config", "set", "--port", "9797", "--idle-minutes", "30", "--web=false")
	cliOK(t, root, "config", "set", "--history-cache-mib", "128")
	v := decode[workspace.Settings](t, cliOK(t, root, "config", "show"))
	if v.Port != 9797 || v.IdleMinutes != 30 || v.WebEnabled || v.HistoryCacheMiB != 128 {
		t.Fatalf("settings overwritten: %+v", v)
	}
	if _, err := invoke(t, root, "config", "set", "--port", "0"); err == nil {
		t.Fatal("invalid port accepted")
	}
	cliOK(t, root, "agent", "set", "--backend", "codex", "--enabled=false", "--path", "/custom/codex")
	cliOK(t, root, "agent", "set", "--backend", "codex", "--path=")
	a := decode[[]localAgent](t, cliOK(t, root, "agent", "list"))
	if a[0].Enabled || a[0].Executable != "" {
		t.Fatalf("agent fields lost: %+v", a)
	}
	s, err := workspace.OpenAdministration(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := invoke(t, root, "user", "list"); err == nil || !strings.Contains(err.Error(), "another running instance") {
		t.Fatalf("active lease not respected: %v", err)
	}
	cliOK(t, root, "user", "list", "--help")
}

func TestLegacyUserCreationAndGlobalDataDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	t.Setenv("SPOOL_NEW_PASSWORD", "legacy-password-long-enough")
	cliOK(t, root, "-add-user", "legacy")
	users := decode[[]workspace.User](t, cliOK(t, root, "user", "list"))
	if len(users) != 1 || users[0].Username != "legacy" {
		t.Fatalf("legacy user: %+v", users)
	}
	var out, stderr bytes.Buffer
	err := runCLI(context.Background(), []string{"user", "list", "--data-dir", root}, &out, &stderr)
	if err != nil || !strings.Contains(out.String(), "legacy") {
		t.Fatalf("trailing global: %s %v", out.String(), err)
	}
}

func TestIssueAutomationEnrollmentAndMembershipValidation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mock gh executable")
	}
	root := filepath.Join(t.TempDir(), "data")
	repo := t.TempDir()
	for _, args := range [][]string{{"init", repo}, {"-C", repo, "remote", "add", "origin", "https://github.com/example/spool-cli.git"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	gh := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'gh version test'; else echo '{\"login\":\"test\"}'; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPOOL_NEW_PASSWORD", "test-password-long-enough")
	cliOK(t, root, "user", "add", "--username", "alice")
	cliOK(t, root, "project", "add", "--name", "Example", "--directory", repo, "--members", "alice")
	cliOK(t, root, "github", "set", "--path", gh)
	cliOK(t, root, "github", "check")
	cliOK(t, root, "issues", "set", "--project", "Example", "--owner", "alice", "--enabled=true", "--label", "UI, v2", "--label", "后端")
	i := decode[workspace.IssuesAutomation](t, cliOK(t, root, "issues", "show", "--project", "Example"))
	if !i.Enabled || i.Repository != "example/spool-cli" || len(i.Labels) != 2 || i.Labels[0] != "UI, v2" {
		t.Fatalf("automation: %+v", i)
	}
	cliOK(t, root, "user", "disable", "--user", "alice")
	if _, err := invoke(t, root, "issues", "set", "--project", "Example", "--interval", "15"); err == nil {
		t.Fatal("disabled automation controller accepted")
	}
	cliOK(t, root, "user", "enable", "--user", "alice")
	if _, err := invoke(t, root, "project", "member", "remove", "--project", "Example", "--user", "alice"); err == nil {
		t.Fatal("automation owner removed")
	}
	cliOK(t, root, "issues", "set", "--project", "Example", "--enabled=false")
	cliOK(t, root, "project", "member", "remove", "--project", "Example", "--user", "alice")
}

func TestInvalidTailscaleKeyDoesNotEnableListenerOrLeakKey(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	key := "private-invalid-key"
	t.Setenv("SPOOL_TSNET_AUTH_KEY", key)
	_, err := invoke(t, root, "tailscale", "enroll")
	if err == nil || strings.Contains(err.Error(), key) {
		t.Fatalf("invalid/redacted key error: %v", err)
	}
	v := decode[workspace.Settings](t, cliOK(t, root, "config", "show"))
	if v.TSNetEnabled {
		t.Fatal("invalid enrollment enabled Tailscale")
	}
}
