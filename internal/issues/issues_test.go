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
	_ = os.Mkdir(filepath.Join(root, "docs"), 0755)
	manual := "# Human notes\n\nDo not erase this.\n"
	_ = os.WriteFile(filepath.Join(root, "docs", "issues.md"), []byte(manual), 0600)
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
	l.Sync(snapshot)
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
	if !strings.HasPrefix(string(body), manual) {
		t.Fatal("human text lost")
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

func TestSelectionResumesOwnEndedRunNeverStealsAndHonorsAllLabels(t *testing.T) {
	snapshot := []Issue{testIssue(1, "open", "web", "server"), testIssue(2, "open", "app"), testIssue(3, "open", "Web"), testIssue(4, "open")}
	l := &Ledger{Records: map[int]*Record{}}
	l.Sync(snapshot)
	selected, resume := l.Select(snapshot, func(Owner) bool { return false })
	if selected == nil || selected.Number != 1 || resume {
		t.Fatal("did not choose smallest numbered eligible issue")
	}
	l.Records[2].Status = "processing"
	l.Records[2].Owner = Owner{Automation: "human"}
	if selected, _ = l.Select(snapshot, func(Owner) bool { return true }); selected != nil {
		t.Fatal("risked overlapping unknown file scope")
	}
	l.Records[2].Owner = NewOwner("instance", "old")
	if selected, _ = l.Select(snapshot, func(Owner) bool { return false }); selected != nil {
		t.Fatal("stole a live automation claim")
	}
	selected, resume = l.Select(snapshot, func(Owner) bool { return true })
	if selected == nil || selected.Number != 2 || !resume {
		t.Fatal("own prior claim not resumed first")
	}
	l.Records[1].Status = "processing"
	l.Records[1].Owner = Owner{Automation: "another"}
	l.Records[1].Files = []string{"shared"}
	l.Records[2].Files = []string{"shared/config.go"}
	if selected, _ = l.Select(snapshot, func(Owner) bool { return true }); selected != nil {
		t.Fatal("overlapping file ranges admitted")
	}
	l.Records[1].Files = []string{"web"}
	l.Records[2].Files = []string{"app"}
	selected, resume = l.Select(snapshot, func(Owner) bool { return true })
	if selected == nil || !resume {
		t.Fatal("disjoint existing claim should resume")
	}
	l.Records[2].Categories = []string{"web", "app"}
	if selected, _ = l.Select(snapshot, func(Owner) bool { return true }); selected != nil {
		t.Fatal("multi-category occupancy ignored")
	}
}

func TestLegacyTableAndMalformedLedgerFailClosed(t *testing.T) {
	for _, text := range []string{"issue #1 processing owned by somebody", ledgerStart + "invalid" + ledgerEnd, "<!-- spool:issues:v2 -->"} {
		root := t.TempDir()
		_ = os.Mkdir(filepath.Join(root, "docs"), 0755)
		_ = os.WriteFile(filepath.Join(root, "docs", "issues.md"), []byte(text), 0600)
		if _, err := Load(root); err == nil {
			t.Errorf("accepted unsafe ledger %q", text)
		}
	}
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, "docs"), 0755)
	text := "| Issue | Status | Owner | Notes |\n|---|---|---|---|\n| #41 | processing | human | keep |\n"
	_ = os.WriteFile(filepath.Join(root, "docs", "issues.md"), []byte(text), 0600)
	l, err := Load(root)
	if err != nil || l.Records[41].Status != "processing" || l.Records[41].Owner.Automation != "human" {
		t.Fatalf("legacy claim lost: %#v %v", l, err)
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
	path := filepath.Join(root, "docs", ".issues-automation.lock")
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
		t.Logf("#%d state=%s categories=%v linkedPRs=%d", issue.Number, issue.State, Categories(issue), len(issue.PRs))
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
	if checkout.Root != canonical || checkout.Branch != "(detached)" || !strings.Contains(checkout.Status, "user-file.txt") {
		t.Fatalf("wrong checkout inspection: %#v", checkout)
	}
	body, _ := os.ReadFile(marker)
	if string(body) != "untouched dirty user file" {
		t.Fatal("inspection changed user work")
	}
	if _, err = Inspect(ctx, Run, linked, "another/repository"); err == nil {
		t.Fatal("mismatched origin accepted")
	}
}

func TestLockRejectsSymlinkStateDirectory(t *testing.T) {
	root, elsewhere := t.TempDir(), t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(root, "docs")); err != nil {
		t.Skip(err)
	}
	if _, err := Acquire(root, NewOwner("instance", "run"), func(Owner) bool { return true }); err == nil {
		t.Fatal("followed symlink docs directory")
	}
	entries, _ := os.ReadDir(elsewhere)
	if len(entries) != 0 {
		t.Fatal("wrote automation state outside the main checkout")
	}
}
