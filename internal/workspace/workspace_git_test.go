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
