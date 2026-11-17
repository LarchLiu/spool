package workspace

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestWorkspaceGitChangesHistoryAndDiff(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "git-owner")
	p := testProject(t, s, u)
	sessionFileGit(t, p.Path, "init", "-b", "dev")
	writeSessionFile(t, p.Path, "src/a file.txt", "first\nold\n")
	writeSessionFile(t, p.Path, "deleted.txt", "deleted contents\n")
	writeSessionFile(t, p.Path, "rename.txt", "renamed contents\n")
	writeSessionFile(t, p.Path, "binary.dat", "old\x00data")
	sessionFileGit(t, p.Path, "add", ".")
	sessionFileGit(t, p.Path, "commit", "-m", "initial fixture")
	initial := sessionFileGit(t, p.Path, "rev-parse", "HEAD")
	writeSessionFile(t, p.Path, "src/a file.txt", "first\nnew\nthird\n")
	sessionFileGit(t, p.Path, "add", "src/a file.txt")
	writeSessionFile(t, p.Path, "src/a file.txt", "first\nlatest\nthird\n")
	if err := os.Remove(filepath.Join(p.Path, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	sessionFileGit(t, p.Path, "mv", "rename.txt", "renamed file.txt")
	writeSessionFile(t, p.Path, "binary.dat", "new\x00data")
	writeSessionFile(t, p.Path, "untracked 中文.txt", "new file\nsecond")
	writeSessionFile(t, p.Path, ".env", "SECRET=hidden")
	writeSessionFile(t, p.Path, "private/hidden.txt", "private contents")
	if _, err := s.SaveProject(Project{Name: "Private", Path: filepath.Join(p.Path, "private"), Members: []string{u.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(p.Path, "private/hidden.txt"), filepath.Join(p.Path, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, a, u.Username)
	get := func(values url.Values) *httptest.ResponseRecorder {
		return apiRequest(a, "GET", "/api/projects/"+p.ID+"/git?"+values.Encode(), "", cookie, "")
	}
	w := get(nil)
	var view workspaceGitView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 {
		t.Fatalf("list: %d %s: %v", w.Code, w.Body.String(), err)
	}
	if !view.Repository || view.Branch != "dev" || len(view.Files) != 5 {
		t.Fatalf("list: %+v", view)
	}
	files := map[string]workspaceGitFile{}
	for _, file := range view.Files {
		files[file.Path] = file
	}
	if file := files["src/a file.txt"]; !file.Staged || file.Status != "MM" || file.Additions != 2 || file.Deletions != 1 {
		t.Fatalf("staged and working changes: %+v", file)
	}
	if file := files["renamed file.txt"]; file.PreviousPath != "rename.txt" {
		t.Fatalf("rename: %+v", file)
	}
	if !files["binary.dat"].Binary || !files["untracked 中文.txt"].Untracked {
		t.Fatalf("kinds: %+v", files)
	}
	for path, expected := range map[string]string{"src/a file.txt": "+latest", "deleted.txt": "-deleted contents", "renamed file.txt": "rename to renamed file.txt", "untracked 中文.txt": "+second"} {
		w := get(url.Values{"view": {"diff"}, "path": {path}})
		if w.Code != 200 || !strings.Contains(w.Body.String(), expected) {
			t.Fatalf("diff %q: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{".env", "private/hidden.txt", "alias.txt", "../outside", "unchanged.txt"} {
		w := get(url.Values{"view": {"diff"}, "path": {path}})
		if w.Code != 403 {
			t.Fatalf("protected diff %q: %d %s", path, w.Code, w.Body.String())
		}
	}
	w = get(url.Values{"view": {"diff"}, "path": {"binary.dat"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"binary":true`) {
		t.Fatalf("binary: %d %s", w.Code, w.Body.String())
	}
	w = get(url.Values{"view": {"history"}})
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || len(view.Commits) != 1 || view.Commits[0].ID != initial || view.Commits[0].Time == 0 {
		t.Fatalf("history: %d %s: %v", w.Code, w.Body.String(), err)
	}
	w = get(url.Values{"commit": {initial}, "view": {"diff"}, "path": {"src/a file.txt"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "+old") || strings.Contains(w.Body.String(), "+latest") {
		t.Fatalf("historical diff: %d %s", w.Code, w.Body.String())
	}
	if w := get(url.Values{"commit": {"HEAD;echo invalid"}}); w.Code != 400 {
		t.Fatalf("invalid commit: %d", w.Code)
	}
}

func TestWorkspaceGitFollowsSelectedWorktreeAndSubdirectory(t *testing.T) {
	s := testStore(t)
	u, other := testUser(t, s, "git-owner"), testUser(t, s, "git-other")
	p := testProject(t, s, u, other)
	sessionFileGit(t, p.Path, "init", "-b", "dev")
	writeSessionFile(t, p.Path, "src/file.txt", "original\n")
	sessionFileGit(t, p.Path, "add", ".")
	sessionFileGit(t, p.Path, "commit", "-m", "fixture")
	linked := filepath.Join(t.TempDir(), "worktree")
	sessionFileGit(t, p.Path, "worktree", "add", "-b", "feature", linked)
	writeSessionFile(t, linked, "src/file.txt", "worktree\n")
	writeSessionFile(t, p.Path, "src/file.txt", "main\n")
	chat, err := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, a, u.Username)
	base := "/api/chats/" + chat.ID + "/git?"
	for _, directory := range []string{p.Path, linked} {
		w := apiRequest(a, "GET", base+url.Values{"workspace": {directory}, "view": {"diff"}, "path": {"src/file.txt"}}.Encode(), "", cookie, "")
		want := "+main"
		if directory == linked {
			want = "+worktree"
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("selected diff: %d %s", w.Code, w.Body.String())
		}
	}
	otherCookie, _ := loginRequest(t, a, other.Username)
	if w := apiRequest(a, "GET", base, "", otherCookie, ""); w.Code != 404 {
		t.Fatalf("private chat leaked: %d", w.Code)
	}
	if w := apiRequest(a, "GET", base+url.Values{"workspace": {t.TempDir()}}.Encode(), "", cookie, ""); w.Code != 403 {
		t.Fatalf("foreign workspace accepted: %d", w.Code)
	}
	sub, err := s.SaveProject(Project{Name: "Subdirectory", Path: filepath.Join(p.Path, "src"), Members: []string{u.ID}})
	if err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "GET", "/api/projects/"+sub.ID+"/git", "", cookie, "")
	var view workspaceGitView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || len(view.Files) != 1 || view.Files[0].Path != "file.txt" || view.Files[0].Additions != 1 {
		t.Fatalf("subdirectory: %d %s: %v", w.Code, w.Body.String(), err)
	}
	w = apiRequest(a, "GET", "/api/projects/"+sub.ID+"/git?view=diff&path=file.txt", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "+main") {
		t.Fatalf("subdirectory diff: %d %s", w.Code, w.Body.String())
	}
}

func TestWorkspaceGitNonRepositoryUnbornAndBoundedDiff(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "git-owner")
	p := testProject(t, s, u)
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, a, u.Username)
	get := func(query string) *httptest.ResponseRecorder {
		return apiRequest(a, "GET", "/api/projects/"+p.ID+"/git"+query, "", cookie, "")
	}
	if w := get(""); w.Code != 200 || !strings.Contains(w.Body.String(), `"repository":false`) {
		t.Fatalf("non-repository: %d %s", w.Code, w.Body.String())
	}
	sessionFileGit(t, p.Path, "init", "-b", "dev")
	writeSessionFile(t, p.Path, "initial.txt", "initial\n")
	sessionFileGit(t, p.Path, "add", ".")
	if w := get("?view=diff&path=initial.txt"); w.Code != 200 || !strings.Contains(w.Body.String(), "+initial") {
		t.Fatalf("unborn diff: %d %s", w.Code, w.Body.String())
	}
	if w := get("?view=history"); w.Code != 200 || !strings.Contains(w.Body.String(), `"commits":[]`) {
		t.Fatalf("unborn history: %d %s", w.Code, w.Body.String())
	}
	writeSessionFile(t, p.Path, "large.txt", strings.Repeat("large line\n", maxFilePreviewBytes/8))
	if w := get("?view=diff&path=large.txt"); w.Code != 200 || !strings.Contains(w.Body.String(), `"truncated":true`) || w.Body.Len() > maxFilePreviewBytes*2 {
		t.Fatalf("large diff: %d %d bytes", w.Code, w.Body.Len())
	}
}

func TestWorkspaceGitDiffBinaryMetadata(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "git-owner")
	p := testProject(t, s, u)
	sessionFileGit(t, p.Path, "init", "-b", "dev")
	writeSessionFile(t, p.Path, "text.txt", "Binary files context differ\nBinary files original differ\n")
	writeSessionFile(t, p.Path, "binary.dat", "original\x00data")
	sessionFileGit(t, p.Path, "add", ".")
	sessionFileGit(t, p.Path, "commit", "-m", "initial")
	writeSessionFile(t, p.Path, "text.txt", "Binary files context differ\nBinary files staged differ\n")
	writeSessionFile(t, p.Path, "binary.dat", "staged\x00data")
	sessionFileGit(t, p.Path, "add", ".")
	writeSessionFile(t, p.Path, "text.txt", "Binary files context differ\nBinary files working differ\n")
	writeSessionFile(t, p.Path, "binary.dat", "working\x00data")
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, a, u.Username)
	check := func(values url.Values) {
		t.Helper()
		for _, path := range []string{"text.txt", "binary.dat"} {
			values.Set("path", path)
			w := apiRequest(a, "GET", "/api/projects/"+p.ID+"/git?"+values.Encode(), "", cookie, "")
			var diff struct {
				Patch  string `json:"patch"`
				Binary bool   `json:"binary"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &diff); err != nil || w.Code != 200 {
				t.Fatalf("diff %v: %d %s: %v", values, w.Code, w.Body.String(), err)
			}
			if diff.Binary != (path == "binary.dat") || path == "text.txt" && !strings.Contains(diff.Patch, "+Binary files ") {
				t.Errorf("binary classification for %v: %+v", values, diff)
			}
		}
	}
	for _, side := range []string{"", "staged", "unstaged"} {
		check(url.Values{"view": {"diff"}, "side": {side}})
	}
	sessionFileGit(t, p.Path, "commit", "-m", "staged changes")
	check(url.Values{"view": {"diff"}, "commit": {sessionFileGit(t, p.Path, "rev-parse", "HEAD")}})
}

func TestWorkspaceGitMergeCommitUsesFirstParent(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "git-owner")
	p := testProject(t, s, u)
	sessionFileGit(t, p.Path, "init", "-b", "dev")
	writeSessionFile(t, p.Path, "src/file.txt", "initial\n")
	sessionFileGit(t, p.Path, "add", ".")
	sessionFileGit(t, p.Path, "commit", "-m", "initial")
	sessionFileGit(t, p.Path, "checkout", "-b", "feature")
	writeSessionFile(t, p.Path, "src/file.txt", "feature\n")
	writeSessionFile(t, p.Path, "src/added.txt", "added\n")
	sessionFileGit(t, p.Path, "add", ".")
	sessionFileGit(t, p.Path, "commit", "-m", "feature")
	sessionFileGit(t, p.Path, "checkout", "dev")
	writeSessionFile(t, p.Path, "main.txt", "main branch\n")
	sessionFileGit(t, p.Path, "add", ".")
	sessionFileGit(t, p.Path, "commit", "-m", "main branch")
	sessionFileGit(t, p.Path, "merge", "--no-ff", "feature", "-m", "Merge feature")
	commit := sessionFileGit(t, p.Path, "rev-parse", "HEAD")
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, a, u.Username)
	base := "/api/projects/" + p.ID + "/git?"
	w := apiRequest(a, "GET", base+"view=history", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), commit) {
		t.Fatalf("merge history: %d %s", w.Code, w.Body.String())
	}
	check := func(base, prefix string) {
		t.Helper()
		w := apiRequest(a, "GET", base+url.Values{"commit": {commit}}.Encode(), "", cookie, "")
		var view workspaceGitView
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || len(view.Files) != 2 {
			t.Errorf("merge changes: %d %s: %v", w.Code, w.Body.String(), err)
			return
		}
		for _, file := range view.Files {
			want := "+feature"
			deletions := 1
			if file.Path == prefix+"added.txt" {
				want, deletions = "+added", 0
			} else if file.Path != prefix+"file.txt" {
				t.Fatalf("unexpected merge file: %+v", file)
			}
			if file.Additions != 1 || file.Deletions != deletions {
				t.Errorf("merge statistics: %+v", file)
			}
			w = apiRequest(a, "GET", base+url.Values{"view": {"diff"}, "commit": {commit}, "path": {file.Path}}.Encode(), "", cookie, "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), want) || !strings.Contains(w.Body.String(), "@@ -") || strings.Contains(w.Body.String(), "@@@") {
				t.Errorf("merge diff %q: %d %s", file.Path, w.Code, w.Body.String())
			}
		}
	}
	check(base, "src/")
	sub, err := s.SaveProject(Project{Name: "Source", Path: filepath.Join(p.Path, "src"), Members: []string{u.ID}})
	if err != nil {
		t.Fatal(err)
	}
	check("/api/projects/"+sub.ID+"/git?", "")
}

func TestWorkspaceGitRenameStatistics(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "git-owner")
	p := testProject(t, s, u)
	sessionFileGit(t, p.Path, "init", "-b", "dev")
	old, next := "src/old\t中文.txt", "src/renamed\t中文.txt"
	writeSessionFile(t, p.Path, old, "one\ntwo\nthree\nfour\nfive\nsix\n")
	sessionFileGit(t, p.Path, "add", ".")
	sessionFileGit(t, p.Path, "commit", "-m", "initial")
	sessionFileGit(t, p.Path, "mv", old, next)
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, a, u.Username)
	base := "/api/projects/" + p.ID + "/git?"
	check := func(query string, add, del, stagedAdd, stagedDel, unstagedAdd, unstagedDel int) {
		t.Helper()
		w := apiRequest(a, "GET", base+query, "", cookie, "")
		var view workspaceGitView
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || len(view.Files) != 1 {
			t.Errorf("rename: %d %s: %v", w.Code, w.Body.String(), err)
			return
		}
		file := view.Files[0]
		if file.Path != next || file.PreviousPath != old || file.Additions != add || file.Deletions != del ||
			file.StagedAdditions != stagedAdd || file.StagedDeletions != stagedDel || file.UnstagedAdditions != unstagedAdd || file.UnstagedDeletions != unstagedDel {
			t.Errorf("rename statistics: %+v", file)
		}
	}
	check("", 0, 0, 0, 0, 0, 0)
	writeSessionFile(t, p.Path, next, "one\ntwo\nchanged\nfour\nfive\nsix\n")
	check("", 1, 1, 0, 0, 1, 1)
	sessionFileGit(t, p.Path, "add", next)
	check("", 1, 1, 1, 1, 0, 0)
	writeSessionFile(t, p.Path, next, "one\ntwo\nchanged\nfour\nfive\nsix\nadded\n")
	check("", 2, 1, 1, 1, 1, 0)
	sessionFileGit(t, p.Path, "commit", "-m", "Rename with edits")
	commit := sessionFileGit(t, p.Path, "rev-parse", "HEAD")
	check(url.Values{"commit": {commit}}.Encode(), 1, 1, 0, 0, 0, 0)
	w := apiRequest(a, "GET", base+url.Values{"view": {"diff"}, "commit": {commit}, "path": {next}}.Encode(), "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "rename from") || !strings.Contains(w.Body.String(), "+changed") || strings.Contains(w.Body.String(), "+added") {
		t.Fatalf("renamed historical diff: %d %s", w.Code, w.Body.String())
	}
}
