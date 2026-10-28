package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"spool/internal/issues"
	"spool/internal/workspace"
)

func TestProjectGitCheckRunsInBackgroundAndPublishesOnUIThread(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var check githubRepositoryCheck
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	updates := make(chan func(), 1)
	returned := make(chan struct{})
	go func() {
		check.refresh(ctx, root, func(_ context.Context, _, name string, args ...string) ([]byte, error) {
			if name != "git" {
				t.Errorf("unexpected command: %s", name)
			}
			if args[0] == "rev-parse" {
				close(entered)
				<-release
				return []byte(root), nil
			}
			return []byte("git@github.com:fixture/issues.git"), nil
		}, func(fn func()) { updates <- fn })
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Git detection blocked the UI")
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Git detection never started")
	}
	if !check.pending || check.ready(root) {
		t.Fatal("pending Git detection allowed enablement")
	}
	release <- struct{}{}
	select {
	case apply := <-updates:
		if !check.pending || check.repository != "" {
			t.Fatal("worker changed repository state outside the UI callback")
		}
		apply()
	case <-time.After(2 * time.Second):
		t.Fatal("Git detection result was not dispatched")
	}
	if !check.ready(root) || check.repository != "fixture/issues" {
		t.Fatalf("repository not detected: %+v", check)
	}
	check.refresh(ctx, root, func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, errors.New("not a Git repository")
	}, func(fn func()) { updates <- fn })
	if check.ready(root) || check.repository != "" {
		t.Fatal("recheck retained a previous successful repository")
	}
	select {
	case apply := <-updates:
		apply()
	case <-time.After(2 * time.Second):
		t.Fatal("failed recheck did not complete")
	}
	if check.pending || check.ready(root) {
		t.Fatal("failed Git detection allowed enablement")
	}
}

func TestProjectGitCheckIgnoresSupersededDirectoryResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var check githubRepositoryCheck
	updates := make(chan func(), 1)
	run := func(_ context.Context, directory, _ string, args ...string) ([]byte, error) {
		if args[0] == "rev-parse" {
			return []byte(directory), nil
		}
		return []byte("https://github.com/fixture/" + filepath.Base(directory) + ".git"), nil
	}
	refresh := func(directory string) func() {
		t.Helper()
		check.refresh(ctx, directory, run, func(fn func()) { updates <- fn })
		select {
		case apply := <-updates:
			return apply
		case <-time.After(2 * time.Second):
			t.Fatal("Git detection did not complete")
			return nil
		}
	}
	first, _ := filepath.EvalSymlinks(t.TempDir())
	second, _ := filepath.EvalSymlinks(t.TempDir())
	oldResult := refresh(first)
	newResult := refresh(second)
	oldResult()
	if check.directory != second || !check.pending || check.repository != "" {
		t.Fatal("outdated Git result replaced the current directory's state")
	}
	newResult()
	if !check.ready(second) || check.ready(first) {
		t.Fatal("repository readiness is not scoped to the selected directory")
	}
	check.refresh(ctx, "", run, func(func()) { t.Error("empty directory scheduled a Git check") })
	if check.ready(second) || check.repository != "" || check.pending {
		t.Fatal("clearing the directory retained a valid Git repository")
	}
}

func TestProjectGitCheckCancelsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var check githubRepositoryCheck
	entered, exited := make(chan struct{}), make(chan struct{})
	check.refresh(ctx, t.TempDir(), func(ctx context.Context, _, _ string, _ ...string) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	}, func(func()) { t.Error("shutdown Git check updated a closed window") })
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Git check did not start")
	}
	cancel()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel Git detection")
	}
}

func TestIssueAutomationRequiresDetectedProjectGit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		git   githubRepositoryCheck
		ready bool
	}{
		{name: "unchecked"},
		{name: "no GitHub origin", git: githubRepositoryCheck{directory: "/project"}},
		{name: "pending", git: githubRepositoryCheck{directory: "/project", repository: "fixture/issues", pending: true}},
		{name: "different directory", git: githubRepositoryCheck{directory: "/another", repository: "fixture/issues"}},
		{name: "ready", git: githubRepositoryCheck{directory: "/project", repository: "fixture/issues"}, ready: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &panel{preferences: workspace.UIPreferences{Language: "en"}, github: githubCLICheck{status: issues.CLIStatus{State: issues.CLIReady}}, projectGit: tc.git}
			p.project = workspace.Project{ID: "project", Path: "/project"}
			view := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).FillWidth().Children(func() { p.projectIssuesView(c) })
			}, 900, 1400)
			if !view.HasText(p.tr(tc.git.message(p.project.Path), "repository", tc.git.repository)) || !view.HasText("Recheck project Git") {
				t.Fatal("Git detection status and recovery action are missing")
			}
			if err := view.Click("Enable issue automation"); err != nil {
				t.Fatal(err)
			}
			if p.project.Issues.Enabled != tc.ready {
				t.Fatal("checkbox bypassed project Git readiness")
			}
			p.project.Issues.Enabled = true
			p.projects = []workspace.Project{p.project}
			if err := p.validateIssueEnablement(p.project); (err == nil) != tc.ready {
				t.Fatalf("save bypassed Git readiness: %v", err)
			}
			if err := view.Click("Enable issue automation"); err != nil {
				t.Fatal(err)
			}
			if p.project.Issues.Enabled {
				t.Fatal("missing Git information prevented disabling a schedule")
			}
			if err := p.validateIssueEnablement(p.project); err != nil {
				t.Fatalf("disabled schedule must not require Git: %v", err)
			}
		})
	}
}

func TestProjectGitHintsAreLocalized(t *testing.T) {
	for _, locale := range []string{"zh-CN", "ja"} {
		p := &panel{preferences: workspace.UIPreferences{Language: locale}}
		for _, check := range []githubRepositoryCheck{{}, {directory: "/project"}, {directory: "/project", pending: true}, {directory: "/project", repository: "fixture/issues"}} {
			message := check.message(check.directory)
			translated := p.tr(message, "repository", check.repository)
			if translated == message || strings.Contains(translated, "{repository}") {
				t.Errorf("%s: missing translation for %q", locale, message)
			}
		}
	}
}
