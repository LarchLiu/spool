package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"spool/internal/issues"
	"spool/internal/workspace"
)

func TestGitHubCLICheckDoesNotBlockUIAndPublishesOnUIThread(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	check := githubCLICheck{ctx: ctx, status: issues.CLIStatus{State: issues.CLIReady, Login: "old"}}
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	updates := make(chan func(), 1)
	returned := make(chan struct{})
	go func() {
		check.refresh(func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
			if args[0] == "--version" {
				close(entered)
				<-release
				return []byte("gh fixture"), nil
			}
			return []byte(`{"login":"octocat"}`), nil
		}, func(fn func()) { updates <- fn })
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("GitHub check blocked the UI")
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("GitHub check never started")
	}
	if !check.pending || check.ready() {
		t.Fatal("pending check must block enablement even after an earlier success")
	}
	check.refresh(func(context.Context, string, string, ...string) ([]byte, error) {
		t.Error("duplicate check started")
		return nil, nil
	}, func(func()) { t.Error("duplicate update queued") })
	release <- struct{}{}
	select {
	case apply := <-updates:
		if !check.pending || check.status.Login != "old" {
			t.Fatal("worker changed UI state outside the update callback")
		}
		apply()
	case <-time.After(2 * time.Second):
		t.Fatal("GitHub result was not dispatched to the UI")
	}
	if check.pending || !check.ready() || check.status.Login != "octocat" {
		t.Fatalf("check did not become ready: %+v", check)
	}
	check.refresh(func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, errors.New("cannot execute")
	}, func(fn func()) { updates <- fn })
	select {
	case apply := <-updates:
		apply()
	case <-time.After(2 * time.Second):
		t.Fatal("recheck did not complete")
	}
	if check.ready() || check.status.State != issues.CLIUnavailable || check.status.Login != "" {
		t.Fatal("failed recheck retained a successful status")
	}
}

func TestGitHubCLICheckCancelsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	check := githubCLICheck{ctx: ctx}
	entered := make(chan struct{})
	exited := make(chan struct{})
	check.refresh(func(ctx context.Context, _, _ string, _ ...string) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	}, func(func()) { t.Error("shutdown check updated a closed window") })
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("check did not start")
	}
	cancel()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel the running command")
	}
}

func TestIssueAutomationCheckboxRequiresReadyGitHubCLI(t *testing.T) {
	for _, state := range []issues.CLIState{"", issues.CLIMissing, issues.CLIUnavailable, issues.CLILoginRequired, issues.CLIInvalidCredentials, issues.CLINetworkUnavailable, issues.CLIVerificationFailed, issues.CLIReady} {
		t.Run(string(state), func(t *testing.T) {
			p := &panel{preferences: workspace.UIPreferences{Language: "en"}, github: githubCLICheck{status: issues.CLIStatus{State: state, Login: "octocat"}}}
			p.project.Path = "/project"
			p.projectGit = githubRepositoryCheck{directory: p.project.Path, repository: "fixture/issues"}
			view := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).FillWidth().Children(func() { p.projectIssuesView(c) })
			}, 900, 1400)
			message := p.tr(p.github.message(), "user", p.github.status.Login)
			if !view.HasText(message) || !view.HasText("Recheck GitHub CLI") {
				t.Fatal("GitHub status and recovery action must be visible while automation is disabled")
			}
			if err := view.Click("Enable issue automation"); err != nil {
				t.Fatal(err)
			}
			if p.project.Issues.Enabled != (state == issues.CLIReady) {
				t.Fatalf("checkbox bypassed readiness state %q", state)
			}
		})
	}
}

func TestIssueAutomationCheckboxStaysDisabledDuringRecheck(t *testing.T) {
	p := &panel{preferences: workspace.UIPreferences{Language: "en"}, github: githubCLICheck{pending: true, status: issues.CLIStatus{State: issues.CLIReady, Login: "octocat"}}}
	p.project.Path = "/project"
	p.projectGit = githubRepositoryCheck{directory: p.project.Path, repository: "fixture/issues"}
	view := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Children(func() { p.projectIssuesView(c) })
	}, 900, 600)
	if !view.HasText("Checking GitHub CLI installation and sign-in...") {
		t.Fatal("pending recheck did not show progress")
	}
	if err := view.Click("Enable issue automation"); err != nil {
		t.Fatal(err)
	}
	if p.project.Issues.Enabled {
		t.Fatal("a previous successful check allowed enablement during a recheck")
	}
}

func TestIssueAutomationCanBeDisabledWithoutGitHubCLI(t *testing.T) {
	p := &panel{preferences: workspace.UIPreferences{Language: "en"}, project: workspace.Project{Issues: workspace.IssuesAutomation{Enabled: true}}, github: githubCLICheck{status: issues.CLIStatus{State: issues.CLIMissing}}}
	view := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Children(func() { p.projectIssuesView(c) })
	}, 900, 1400)
	if err := view.Click("Enable issue automation"); err != nil {
		t.Fatal(err)
	}
	if p.project.Issues.Enabled {
		t.Fatal("missing gh prevented disabling an existing schedule")
	}
	if err := view.Click("Enable issue automation"); err != nil {
		t.Fatal(err)
	}
	if p.project.Issues.Enabled {
		t.Fatal("missing gh allowed re-enabling automation")
	}
}

func TestIssueEnablementValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   issues.CLIState
		pending bool
		enabled bool
		saved   bool
		blocked bool
	}{
		{name: "unchecked", blocked: true, enabled: true},
		{name: "missing", state: issues.CLIMissing, blocked: true, enabled: true},
		{name: "pending", state: issues.CLIReady, pending: true, blocked: true, enabled: true},
		{name: "ready", state: issues.CLIReady, enabled: true},
		{name: "disabled", state: issues.CLIMissing},
		{name: "existing enabled", state: issues.CLIMissing, saved: true, enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &panel{github: githubCLICheck{status: issues.CLIStatus{State: tc.state}, pending: tc.pending}}
			p.projectGit = githubRepositoryCheck{directory: "/project", repository: "fixture/issues"}
			if tc.saved {
				p.projects = []workspace.Project{{ID: "project", Issues: workspace.IssuesAutomation{Enabled: true}}}
			}
			err := p.validateIssueEnablement(workspace.Project{ID: "project", Path: "/project", Issues: workspace.IssuesAutomation{Enabled: tc.enabled}})
			if (err != nil) != tc.blocked {
				t.Fatalf("unexpected enablement result: %v", err)
			}
		})
	}
}

func TestGitHubCLINetworkHintDoesNotSuggestLogin(t *testing.T) {
	check := githubCLICheck{status: issues.CLIStatus{State: issues.CLINetworkUnavailable}}
	if strings.Contains(check.message(), "gh auth login") {
		t.Fatal("network errors must not be diagnosed as invalid credentials")
	}
}

func TestGitHubCLIHintsAreLocalized(t *testing.T) {
	for _, locale := range []string{"zh-CN", "ja"} {
		p := &panel{preferences: workspace.UIPreferences{Language: locale}}
		for _, state := range []issues.CLIState{"", issues.CLIReady, issues.CLIMissing, issues.CLIUnavailable, issues.CLILoginRequired, issues.CLIInvalidCredentials, issues.CLINetworkUnavailable, issues.CLIVerificationFailed} {
			check := githubCLICheck{status: issues.CLIStatus{State: state}}
			message := check.message()
			translated := p.tr(message, "user", "octocat")
			if translated == message || strings.Contains(translated, "{user}") {
				t.Errorf("%s: missing translation or interpolation for %q", locale, message)
			}
		}
	}
}
