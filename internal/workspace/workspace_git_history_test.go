package workspace

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWorkspaceGitHistoryPagesTagsAndOutgoingCommits(t *testing.T) {
	t.Parallel()
	s := testStore(t)
	u := testUser(t, s, "git-history")
	p := testProject(t, s, u)
	sessionFileGit(t, p.Path, "init", "-b", "dev")
	// Import real Git objects in one process; staging and committing each
	// revision is covered by the operation tests, not history pagination.
	var history strings.Builder
	for i := 0; i < 105; i++ {
		message := fmt.Sprintf("History commit %d", i)
		if i == 104 {
			message += "\n\nRelease details\nSecond line with 中文\n\nFinal paragraph"
		}
		body := fmt.Sprintf("revision %d\n", i)
		fmt.Fprintf(&history, "commit refs/heads/dev\nauthor Spool Test <test@example.invalid> %d +0000\ncommitter Spool Test <test@example.invalid> %d +0000\ndata %d\n%s\nM 100644 inline file.txt\ndata %d\n%s\n", 1700000000+i, 1700000000+i, len(message), message, len(body), body)
	}
	sessionFileGitInput(t, p.Path, strings.NewReader(history.String()), "fast-import", "--quiet")
	sessionFileGit(t, p.Path, "reset", "--hard", "HEAD")
	ids := strings.Fields(sessionFileGit(t, p.Path, "rev-list", "--reverse", "HEAD"))
	if len(ids) != 105 {
		t.Fatalf("imported %d commits, want 105", len(ids))
	}
	sessionFileGit(t, p.Path, "tag", "v1.0,release", ids[104])
	sessionFileGit(t, p.Path, "tag", "-a", "release/v0.9", "-m", "Annotated release", ids[49])
	sessionFileGit(t, p.Path, "tag", "v0.1", ids[0])
	sessionFileGit(t, p.Path, "remote", "add", "origin", filepath.Join(t.TempDir(), "remote.git"))
	sessionFileGit(t, p.Path, "update-ref", "refs/remotes/origin/dev", ids[99])
	sessionFileGit(t, p.Path, "branch", "--set-upstream-to=origin/dev")
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, a, u.Username)
	get := func(cursor string) workspaceGitView {
		t.Helper()
		w := apiRequest(a, "GET", "/api/projects/"+p.ID+"/git?"+url.Values{"view": {"history"}, "cursor": {cursor}}.Encode(), "", cookie, "")
		var view workspaceGitView
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 {
			t.Fatalf("history: %d %s: %v", w.Code, w.Body.String(), err)
		}
		return view
	}
	first := get("")
	if len(first.Commits) != 50 || first.NextCursor == "" || first.Commits[0].ID != ids[104] {
		t.Fatalf("first page: %+v", first)
	}
	detail := func(id string) workspaceGitView {
		t.Helper()
		w := apiRequest(a, "GET", "/api/projects/"+p.ID+"/git?view=changes&commit="+id, "", cookie, "")
		var view workspaceGitView
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || view.SelectedCommit == nil {
			t.Fatalf("commit detail: %d %s: %v", w.Code, w.Body.String(), err)
		}
		return view
	}
	selected := detail(ids[104]).SelectedCommit
	wantTime, _ := strconv.ParseInt(sessionFileGit(t, p.Path, "show", "--no-patch", "--format=%at", ids[104]), 10, 64)
	if selected.ID != ids[104] || selected.Author != "Spool Test" || selected.Time != wantTime || selected.Message != "History commit 104\n\nRelease details\nSecond line with 中文\n\nFinal paragraph" || !selected.Unpushed || len(selected.Tags) != 1 || selected.Tags[0] != "v1.0,release" {
		t.Fatalf("selected commit metadata: %+v", selected)
	}
	annotated := detail(ids[49]).SelectedCommit
	if annotated.Subject != "History commit 49" || annotated.Message != annotated.Subject || annotated.Unpushed || len(annotated.Tags) != 1 || annotated.Tags[0] != "release/v0.9" {
		t.Fatalf("annotated selected commit: %+v", annotated)
	}
	// New commits after the first page must not move its continuation boundary.
	writeSessionFile(t, p.Path, "file.txt", "new head\n")
	sessionFileGit(t, p.Path, "add", ".")
	sessionFileGit(t, p.Path, "commit", "-m", "New commit during pagination")
	second := get(first.NextCursor)
	third := get(second.NextCursor)
	if len(second.Commits) != 50 || second.NextCursor == "" || len(third.Commits) != 5 || third.NextCursor != "" {
		t.Fatalf("page sizes: %d/%d/%d, continuation: %q", len(first.Commits), len(second.Commits), len(third.Commits), third.NextCursor)
	}
	all := append(append(first.Commits, second.Commits...), third.Commits...)
	for i, item := range all {
		if item.ID != ids[104-i] || item.Unpushed != (i < 5) {
			t.Errorf("history row %d: %+v", i, item)
		}
		wantSubject := fmt.Sprintf("History commit %d", 104-i)
		wantMessage := wantSubject
		if i == 0 {
			wantMessage += "\n\nRelease details\nSecond line with 中文\n\nFinal paragraph"
		}
		if item.Subject != wantSubject || item.Message != wantMessage {
			t.Errorf("history row %d message: subject %q, message %q; want %q, %q", i, item.Subject, item.Message, wantSubject, wantMessage)
		}
		wantTag := ""
		switch item.ID {
		case ids[104]:
			wantTag = "v1.0,release"
		case ids[49]:
			wantTag = "release/v0.9"
		case ids[0]:
			wantTag = "v0.1"
		}
		if wantTag == "" && len(item.Tags) != 0 || wantTag != "" && (len(item.Tags) != 1 || item.Tags[0] != wantTag) {
			t.Errorf("tags at %d: %+v, want %q", i, item.Tags, wantTag)
		}
	}
	latest := sessionFileGit(t, p.Path, "rev-parse", "HEAD")
	if view := detail(ids[104]); view.Commit != latest || view.SelectedCommit.ID != ids[104] {
		t.Fatalf("selected commit replaced current HEAD: %+v", view)
	}
	sessionFileGit(t, p.Path, "update-ref", "refs/remotes/origin/dev", latest)
	if detail(ids[104]).SelectedCommit.Unpushed {
		t.Error("commit detail retained outgoing marker after push")
	}
	for _, item := range get("").Commits {
		if item.Unpushed {
			t.Errorf("pushed commit marked outgoing: %+v", item)
		}
	}
	sessionFileGit(t, p.Path, "branch", "--unset-upstream")
	for _, item := range get("").Commits {
		if !item.Unpushed {
			t.Errorf("unpublished branch missing marker: %+v", item)
		}
	}
	sessionFileGit(t, p.Path, "remote", "remove", "origin")
	for _, item := range get("").Commits {
		if item.Unpushed {
			t.Errorf("no remote but marked outgoing: %+v", item)
		}
	}
	for _, cursor := range []string{"bad", ids[0] + ":-1", ids[0] + ":0", ids[0] + ":oops"} {
		w := apiRequest(a, "GET", "/api/projects/"+p.ID+"/git?view=history&cursor="+url.QueryEscape(cursor), "", cookie, "")
		if w.Code != 400 {
			t.Errorf("invalid cursor %q: %d %s", cursor, w.Code, w.Body.String())
		}
	}
	sessionFileGit(t, p.Path, "update-ref", "refs/heads/dev", ids[10])
	w := apiRequest(a, "GET", "/api/projects/"+p.ID+"/git?view=history&cursor="+url.QueryEscape(first.NextCursor), "", cookie, "")
	if w.Code != 409 {
		t.Fatalf("rewritten history: %d %s", w.Code, w.Body.String())
	}
}
