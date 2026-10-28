package issues

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testIssue(n int, state string, labels ...string) Issue {
	v := Issue{Number: n, Title: fmt.Sprintf("Issue %d", n), State: state, URL: fmt.Sprintf("https://github.com/o/r/issues/%d", n)}
	for _, label := range labels {
		v.Labels = append(v.Labels, struct {
			Name string `json:"name"`
		}{label})
	}
	return v
}

func TestLedgerSyncPreservesNotesOwnersHistoryAndPRStates(t *testing.T) {
	root := t.TempDir()
	l, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	owner := NewOwner("instance", "old-run")
	l.Records[1] = &Record{Number: 1, Status: "processing", Owner: owner, Branch: "codex/issue-1-fix", Worktree: "/isolated/1", Categories: []string{"web"}, Notes: "keep manual notes", Validation: "go test failed", PRs: []string{"https://github.com/o/r/pull/11"}, Extra: map[string]json.RawMessage{"custom": json.RawMessage(`{"keep":true}`)}}
	merged := "2026-10-27T01:00:00Z"
	snapshot := []Issue{testIssue(1, "open", "server"), testIssue(2, "open", "web"), testIssue(3, "closed", "app"), testIssue(4, "open", "app"), testIssue(5, "open")}
	snapshot[0].PRs = []PR{{URL: "https://github.com/o/r/pull/11", State: "MERGED", MergedAt: &merged}}
	snapshot[1].PRs = []PR{{URL: "https://github.com/o/r/pull/12", State: "OPEN", Draft: true}}
	snapshot[3].PRs = []PR{{URL: "https://github.com/o/r/pull/14", State: "CLOSED"}}
	l.Sync(snapshot, []string{"web", "server", "app"})
	if err = l.Save(); err != nil {
		t.Fatal(err)
	}
	l, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	r := l.Records[1]
	var compact bytes.Buffer
	_ = json.Compact(&compact, r.Extra["custom"])
	if r.Status != "processing" || r.Owner != owner || r.Notes != "keep manual notes" || r.Validation != "go test failed" || len(r.Categories) != 2 || compact.String() != `{"keep":true}` {
		t.Fatalf("lost existing claim: %#v", r)
	}
	for n, want := range map[int]string{2: "pr", 3: "close", 4: "open", 5: "open"} {
		if l.Records[n].Status != want {
			t.Errorf("#%d = %s, want %s", n, l.Records[n].Status, want)
		}
	}
	body, _ := os.ReadFile(l.Path)
	if !json.Valid(body) {
		t.Fatal("ledger is not standalone JSON")
	}
	before := string(body)
	if err = l.Save(); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(l.Path)
	if string(body) != before {
		t.Fatal("unchanged sync rewrote ledger")
	}
	_ = os.WriteFile(l.Path, append(body, []byte("\nHuman concurrent edit")...), 0600)
	if l.Save() == nil {
		t.Fatal("overwrote concurrent edit")
	}
}

func TestSelectionResumesEndedClaimsWithoutBlockingUnrelatedLabels(t *testing.T) {
	labels := []string{"frontend", "API / v2", "custom label"}
	snapshot := []Issue{testIssue(1, "open", "frontend"), testIssue(2, "open", "API / v2"), testIssue(3, "open", "custom label"), testIssue(4, "open", "Frontend")}
	l := &Ledger{Records: map[int]*Record{}}
	l.Sync(snapshot, labels)
	selected, resume := l.Select(snapshot, labels, func(Owner) bool { return false })
	if selected == nil || selected.Number != 1 || resume {
		t.Fatal("did not choose the smallest eligible issue")
	}
	l.Records[1].Status = "processing"
	l.Records[1].Owner = NewOwner("instance", "live")
	selected, resume = l.Select(snapshot, labels, func(Owner) bool { return false })
	if selected == nil || selected.Number != 2 || resume {
		t.Fatal("live processing blocked an unrelated label")
	}
	selected, resume = l.Select(snapshot, labels, func(Owner) bool { return true })
	if selected == nil || selected.Number != 1 || !resume {
		t.Fatal("ended claim was not resumed first")
	}
	l.Records[2].Status = "processing"
	l.Records[2].Owner = Owner{Automation: "human"}
	l.Records[1].Categories = []string{"frontend", "API / v2"}
	selected, resume = l.Select(snapshot, labels, func(o Owner) bool { return o.Automation == "issues" })
	if selected == nil || selected.Number != 3 || resume {
		t.Fatal("overlapping multi-label claim resumed, or unrelated label was blocked")
	}
	l.Records[1].Status = "pr"
	l.Records[1].Owner = NewOwner("instance", "live")
	snapshot[2] = testIssue(3, "open", "frontend")
	if selected, _ := l.Select(snapshot, labels, func(Owner) bool { return false }); selected != nil {
		t.Fatal("pr status released labels while executor was still running")
	}
}

func TestJSONLedgerVerifiesPRsBeforeSelection(t *testing.T) {
	for _, state := range []string{"OPEN", "CLOSED"} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			link := "https://github.com/o/r/pull/81"
			text := fmt.Sprintf(`{"version":1,"records":[{"number":41,"status":"pr","prs":[%q],"notes":"keep"}]}`, link)
			writeLedgerFixture(t, root, "issues.json", text)
			ledger, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			verified := 0
			g := GitHub{Repository: "o/r", Run: func(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
				call := strings.Join(args, " ")
				switch {
				case strings.HasPrefix(call, "pr view "+link):
					verified++
					return []byte(fmt.Sprintf(`{"url":%q,"state":%q,"headRefName":"existing","baseRefName":"dev"}`, link, state)), nil
				case strings.Contains(call, "api graphql"):
					return []byte(`[{"data":{"repository":{"issue":{"timelineItems":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}]`), nil
				case strings.Contains(call, "/issues?state=all"):
					return []byte(`[[{"number":41,"state":"open","html_url":"https://github.com/o/r/issues/41","labels":[{"name":"web"}]}]]`), nil
				case strings.Contains(call, "/timeline?"):
					return []byte(`[[]]`), nil
				default:
					return nil, fmt.Errorf("unexpected command: %s", call)
				}
			}}
			snapshot, err := g.Snapshot(context.Background(), ledger)
			if err != nil {
				t.Fatal(err)
			}
			if verified != 1 || len(ledger.Records[41].PRs) != 1 {
				t.Fatalf("recorded PR was not verified exactly once: %d, %#v", verified, ledger.Records[41])
			}
			ledger.Sync(snapshot, []string{"web", "server", "app"})
			selected, _ := ledger.Select(snapshot, []string{"web", "server", "app"}, func(Owner) bool { return true })
			if state == "OPEN" && (selected != nil || ledger.Records[41].Status != "pr") {
				t.Fatal("reclaimed an issue with a verified open PR")
			}
			if state == "CLOSED" && (selected == nil || selected.Number != 41) {
				t.Fatal("closed PR prevented safe new work")
			}
			if err = ledger.Save(); err != nil {
				t.Fatal(err)
			}
			reloaded, err := Load(root)
			if err != nil || reloaded.Records[41].Notes != "keep" || len(reloaded.Records[41].PRs) != 1 {
				t.Fatal("JSON ledger lost notes or verified PR")
			}
		})
	}
}

func TestProjectLockNoReentryNoAgeBasedStealAndOwnReleaseOnly(t *testing.T) {
	root := t.TempDir()
	owner := NewOwner("instance", "run-1")
	l, err := Acquire(root, owner, func(Owner) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Acquire(root, NewOwner("instance", "run-2"), func(Owner) bool { return true }); !errors.Is(err, ErrLocked) {
		t.Fatalf("reentry accepted: %v", err)
	}
	if err = l.Release(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".issues-automation.lock")
	_ = os.Mkdir(path, 0700)
	body, _ := json.Marshal(owner)
	_ = os.WriteFile(filepath.Join(path, "owner.json"), body, 0600)
	if _, err = Acquire(root, NewOwner("instance", "run-3"), func(Owner) bool { return false }); !errors.Is(err, ErrLockOwnerUnknown) {
		t.Fatal("unproven stale owner was cleared")
	}
	l, err = Acquire(root, NewOwner("instance", "run-3"), func(Owner) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	changed := NewOwner("instance", "someone-else")
	body, _ = json.Marshal(changed)
	_ = os.WriteFile(filepath.Join(path, "owner.json"), body, 0600)
	if l.Release() == nil {
		t.Fatal("removed someone else's lock")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("foreign lock was deleted")
	}
	if owner.Ended("different", func(string) bool { return true }) {
		t.Fatal("living PID treated as stale")
	}
}

func TestSnapshotPaginatesExcludesPRsChecksDraftAndLinkedPRs(t *testing.T) {
	ctx := context.Background()
	var calls []string
	g := GitHub{Repository: "o/r", Run: func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if name != "gh" {
			t.Fatalf("unexpected program %s", name)
		}
		switch {
		case strings.HasPrefix(call, "pr view"):
			return []byte(`{"url":"https://github.com/o/r/pull/81","state":"OPEN","isDraft":true,"headRefName":"feature","baseRefName":"dev","mergedAt":null}`), nil
		case strings.Contains(call, "api graphql"):
			return []byte(`[{"data":{"repository":{"issue":{"timelineItems":{"nodes":[{"__typename":"ConnectedEvent","source":{"__typename":"PullRequest","url":"https://github.com/o/r/pull/81"},"subject":{"__typename":"Issue"}}],"pageInfo":{"hasNextPage":false}}}}}}]`), nil
		case strings.Contains(call, "/timeline?"):
			return []byte(`[[],[]]`), nil
		case strings.Contains(call, "/issues?state=all"):
			return []byte(`[[{"number":1,"title":"first","state":"open","html_url":"https://github.com/o/r/issues/1","labels":[{"name":"web"}]},{"number":2,"state":"open","pull_request":{}}],[{"number":101,"title":"late page","state":"closed","html_url":"https://github.com/o/r/issues/101"}]]`), nil
		default:
			t.Fatalf("unexpected gh call %s", call)
			return nil, nil
		}
	}}
	snapshot, err := g.Snapshot(ctx, &Ledger{Records: map[int]*Record{}})
	if err != nil || len(snapshot) != 2 || snapshot[1].Number != 101 || len(snapshot[0].PRs) != 1 || !snapshot[0].PRs[0].Draft {
		t.Fatalf("bad snapshot: %#v %v", snapshot, err)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "api ") && (!strings.Contains(call, "--paginate") || !strings.Contains(call, "--slurp")) {
			t.Errorf("unpaginated request: %s", call)
		}
	}
	count := 0
	for _, call := range calls {
		if strings.HasPrefix(call, "pr view") {
			count++
		}
	}
	if count != 1 {
		t.Fatal("PR state cache not used")
	}
}

func TestSnapshotInvalidOrFailedPageIsNotAnEmptyList(t *testing.T) {
	for _, body := range []string{"null", "[]", "[null]", "{}", "[[{}]]"} {
		g := GitHub{Repository: "o/r", Run: func(context.Context, string, string, ...string) ([]byte, error) { return []byte(body), nil }}
		if _, err := g.Snapshot(context.Background(), &Ledger{Records: map[int]*Record{}}); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	for _, message := range []string{"dial tcp: no such host", "HTTP 401: Bad credentials", "HTTP 403: forbidden"} {
		err := RemoteError(errors.New(message)).Error()
		if strings.Contains(message, "dial tcp") && (strings.Contains(err, "凭据无效") || !strings.Contains(err, "沙箱")) {
			t.Fatal("network error mistaken for invalid credentials")
		}
		if strings.Contains(message, "401") && !strings.Contains(err, "凭据无效") {
			t.Fatal("explicit invalid credentials not recognized")
		}
	}
}

func TestGitHubReadOnlyIntegration(t *testing.T) {
	repo := os.Getenv("SPOOL_TEST_ISSUES_REPO")
	if repo == "" {
		t.Skip("set SPOOL_TEST_ISSUES_REPO for an opt-in read-only GitHub test")
	}
	if !ValidRepository(repo) {
		t.Fatal("invalid test repository")
	}
	g := GitHub{Run: Run, Repository: repo, Directory: os.Getenv("SPOOL_TEST_ISSUES_DIRECTORY")}
	snapshot, err := g.Snapshot(context.Background(), &Ledger{Records: map[int]*Record{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range snapshot {
		t.Logf("#%d state=%s categories=%v linkedPRs=%d", issue.Number, issue.State, Categories(issue, []string{"web", "server", "app"}), len(issue.PRs))
	}
	t.Logf("read %d issues without mutating GitHub or a local checkout", len(snapshot))
}

func TestInspectLinkedWorktreeUsesMainCheckoutAndDoesNotModifyFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		body, err := Run(ctx, root, "git", args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(body))
	}
	git("init", "-b", "dev")
	git("remote", "add", "origin", "git@github.com:fixture/issues.git")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "initial")
	linked := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "--detach", linked, "HEAD")
	marker := filepath.Join(linked, "user-file.txt")
	_ = os.WriteFile(marker, []byte("untouched dirty user file"), 0600)
	checkout, err := Inspect(ctx, Run, linked, "fixture/issues")
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(root)
	if checkout.Root != canonical || checkout.Repository != "fixture/issues" || checkout.Branch != "(detached)" || !strings.Contains(checkout.Status, "user-file.txt") {
		t.Fatalf("wrong checkout inspection: %#v", checkout)
	}
	data := t.TempDir()
	mainState, err := PrepareDirectory(data, canonical)
	if err != nil {
		t.Fatal(err)
	}
	linkedState, err := PrepareDirectory(data, checkout.Root)
	if err != nil || linkedState != mainState {
		t.Fatalf("linked worktree did not share main checkout state: %q, %v", linkedState, err)
	}
	body, _ := os.ReadFile(marker)
	if string(body) != "untouched dirty user file" {
		t.Fatal("inspection changed user work")
	}
	if _, err = Inspect(ctx, Run, linked, "another/repository"); err == nil {
		t.Fatal("mismatched origin accepted")
	}
	if checkout, err = Inspect(ctx, Run, linked, ""); err != nil || checkout.Repository != "fixture/issues" {
		t.Fatalf("repository was not inferred: %#v, %v", checkout, err)
	}
}

func TestLockRejectsSymlinkStateDirectory(t *testing.T) {
	root, elsewhere := t.TempDir(), t.TempDir()
	directory := filepath.Join(root, "issues")
	if err := os.Symlink(elsewhere, directory); err != nil {
		t.Skip(err)
	}
	if _, err := Acquire(directory, NewOwner("instance", "run"), func(Owner) bool { return true }); err == nil {
		t.Fatal("followed symlink state directory")
	}
	entries, _ := os.ReadDir(elsewhere)
	if len(entries) != 0 {
		t.Fatal("wrote automation state through a symlink")
	}
}
