//go:build darwin || freebsd || linux || netbsd || openbsd

package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func fixtureLauncher(t *testing.T) AgentRequest {
	t.Helper()
	request := newAgentFixture(t, chatmodel.BackendCodex)
	directory := filepath.Join(request.WorkingDirectory, "launcher-bin")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(request.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "spool-test-agent-runtime"), body, 0700); err != nil {
		t.Fatal(err)
	}
	request.Binary = filepath.Join(directory, "codex")
	if err := os.WriteFile(request.Binary, []byte("#!/usr/bin/env spool-test-agent-runtime\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return request
}

func TestAgentLauncherFindsItsRuntimeWithDesktopPATH(t *testing.T) {
	request := fixtureLauncher(t)
	// Finder-launched apps may lack the directory containing the selected CLI.
	path := t.TempDir()
	t.Setenv("PATH", path)
	version, err := Version(context.Background(), request.Binary)
	if err != nil || !strings.HasPrefix(version, "fixture-agent 1.0.0") {
		t.Fatalf("launcher version failed with desktop PATH: %q %v", version, err)
	}
	runner := newTestRunner(t)
	runner.ConfigureBinaries(map[chatmodel.Backend]string{chatmodel.BackendCodex: request.Binary})
	catalog, err := runner.CodexModels(context.Background())
	if err != nil || len(catalog.Models) != 3 {
		t.Fatalf("launcher model discovery failed: %+v %v", catalog, err)
	}
	permissions, err := runner.PermissionModes(context.Background(), request.Backend, request.WorkingDirectory)
	if err != nil || permissions.Validate("ask") != nil {
		t.Fatalf("launcher permission discovery failed: %+v %v", permissions, err)
	}
	result, _ := runFixture(t, runner, request)
	if result.Text != "Hello world" {
		t.Fatalf("launcher turn failed: %+v", result)
	}
	if os.Getenv("PATH") != path {
		t.Fatal("agent startup changed the parent process PATH")
	}
}

func TestAgentLauncherPreservesExistingPATHPrecedence(t *testing.T) {
	request := fixtureLauncher(t)
	preferred := t.TempDir()
	if err := os.WriteFile(filepath.Join(preferred, "spool-test-agent-runtime"), []byte("#!/bin/sh\nprintf '%s\\n' 'preferred runtime'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", preferred)
	version, err := Version(context.Background(), request.Binary)
	if err != nil || version != "preferred runtime" {
		t.Fatalf("fallback directories replaced the configured runtime: %q %v", version, err)
	}
}

func TestAgentVersionFailureRetainsExitCodeAndNativeDiagnostic(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "broken-codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' 'env: node: No such file or directory' >&2\nexit 127\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := Version(context.Background(), binary)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 127 || !strings.Contains(err.Error(), "env: node: No such file or directory") {
		t.Fatalf("version probe lost the launcher failure: %v", err)
	}
}
