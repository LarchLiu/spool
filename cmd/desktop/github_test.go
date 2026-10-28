package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
			showPath := state != "" && state != issues.CLIReady
			if view.HasText("GitHub CLI executable (blank = auto-detect)") != showPath || view.HasText("Save GitHub CLI path") != showPath {
				t.Fatalf("manual path controls must only appear after a failed check: %q", state)
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
	if view.HasText("GitHub CLI executable (blank = auto-detect)") || view.HasText("Save GitHub CLI path") {
		t.Fatal("successful verification must not reveal manual path controls during a recheck")
	}
	if err := view.Click("Enable issue automation"); err != nil {
		t.Fatal(err)
	}
	if p.project.Issues.Enabled {
		t.Fatal("a previous successful check allowed enablement during a recheck")
	}
}

func TestGitHubCLIManualPathControlsStayHiddenWhileChecking(t *testing.T) {
	for _, state := range []issues.CLIState{"", issues.CLIMissing, issues.CLILoginRequired, issues.CLIReady} {
		t.Run(string(state), func(t *testing.T) {
			p := &panel{preferences: workspace.UIPreferences{Language: "en"}, github: githubCLICheck{pending: true, status: issues.CLIStatus{State: state}}}
			view := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).FillWidth().Children(func() { p.projectIssuesView(c) })
			}, 900, 1400)
			if view.HasText("GitHub CLI executable (blank = auto-detect)") || view.HasText("Save GitHub CLI path") {
				t.Fatal("manual path controls must stay hidden until a failed check completes")
			}
			if !view.HasText("Recheck GitHub CLI") || !view.HasText("Checking GitHub CLI installation and sign-in...") {
				t.Fatal("checking state and recheck action must remain visible")
			}
		})
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
		for _, key := range []string{
			"GitHub CLI executable (blank = auto-detect)",
			"Save GitHub CLI path",
			"Applies to all projects. Enter the absolute path to gh (gh.exe on Windows), without quotes. Leave blank to auto-detect.",
			"GitHub CLI path must point to an existing executable using an absolute path.",
		} {
			if p.tr(key) == key {
				t.Errorf("%s: missing manual-path translation for %q", locale, key)
			}
		}
	}
}

func TestSaveGitHubCLIPathPublishesOnUIThreadAndRechecks(t *testing.T) {
	store, err := workspace.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("invalid executable fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	p := &panel{service: &workspace.Service{Store: store}, githubPath: "  " + executable + "  ", github: githubCLICheck{ctx: ctx, status: issues.CLIStatus{State: issues.CLIReady}}}
	updates := make(chan func(), 2)
	for _, path := range []string{p.githubPath, "relative/gh", ""} {
		p.githubPath = path
		p.saveGitHubCLIPath(func(fn func()) { updates <- fn })
		if !p.busy {
			t.Fatal("save did not mark the UI busy")
		}
		select {
		case apply := <-updates:
			if !p.busy || p.github.pending {
				t.Fatal("worker changed UI state before dispatch")
			}
			apply()
		case <-time.After(2 * time.Second):
			t.Fatal("save did not dispatch a result")
		}
		if p.busy {
			t.Fatal("save left the UI busy")
		}
		if path == "relative/gh" {
			if p.github.pending || p.message == "Saved" {
				t.Fatal("invalid path started a recheck or reported success")
			}
			continue
		}
		if p.message != "Saved" || !p.github.pending || p.github.ready() {
			t.Fatal("successful save must start a fresh check before allowing enablement")
		}
		if p.githubPath != strings.TrimSpace(path) {
			t.Fatal("saved path was not normalized")
		}
		select {
		case apply := <-updates:
			apply()
		case <-time.After(5 * time.Second):
			t.Fatal("saving did not run the CLI check")
		}
	}
}
