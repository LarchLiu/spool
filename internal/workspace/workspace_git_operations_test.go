package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	chatmodel "spool/internal/model"
)

type gitOperationFixture struct {
	t                     *testing.T
	app                   *App
	cookie                *http.Cookie
	csrf, base, directory string
}

func newGitOperationFixture(t *testing.T) gitOperationFixture {
	t.Helper()
	s := testStore(t)
	user := testUser(t, s, "git-operator")
	project := testProject(t, s, user)
	sessionFileGit(t, project.Path, "init", "-b", "dev")
	sessionFileGit(t, project.Path, "config", "user.name", "Spool Git Test")
	sessionFileGit(t, project.Path, "config", "user.email", "git-test@example.invalid")
	sessionFileGit(t, project.Path, "config", "commit.gpgsign", "false")
	app := NewApp(s, &fakeRunner{}, "", "")
	cookie, csrf := loginRequest(t, app, user.Username)
	return gitOperationFixture{t, app, cookie, csrf, "/api/projects/" + project.ID + "/git", project.Path}
}

func (f gitOperationFixture) view() workspaceGitView {
	f.t.Helper()
	response := apiRequest(f.app, "GET", f.base, "", f.cookie, "")
	var view workspaceGitView
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &view) != nil {
		f.t.Fatalf("Git view: %d %s", response.Code, response.Body.String())
	}
	return view
}
func (f gitOperationFixture) post(input workspaceGitOperation) *httptest.ResponseRecorder {
	f.t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		f.t.Fatal(err)
	}
	return apiRequest(f.app, "POST", f.base, string(body), f.cookie, f.csrf)
}
func (f gitOperationFixture) operation(action, message string, paths ...string) *httptest.ResponseRecorder {
	view := f.view()
	return f.post(workspaceGitOperation{Action: action, Message: message, Paths: paths, Branch: view.Branch, Head: view.Commit, Index: view.Index, Remote: view.Remote, RemoteBranch: view.RemoteBranch})
}
func requireGitOperation(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("Git operation: %d %s", response.Code, response.Body.String())
	}
}

func TestWorkspaceGitOperationsStagePartialDiffCommitAndUnstage(t *testing.T) {
	f := newGitOperationFixture(t)
	writeSessionFile(t, f.directory, "a.txt", "initial\n")
	requireGitOperation(t, f.operation("stage", "", "a.txt"))
	// An unborn index can be unstaged even if the working copy has changed again.
	writeSessionFile(t, f.directory, "a.txt", "staged\n")
	requireGitOperation(t, f.operation("unstage", "", "a.txt"))
	if got := sessionFileGit(t, f.directory, "ls-files"); got != "" {
		t.Fatalf("unborn index still staged: %s", got)
	}
	if body, _ := os.ReadFile(filepath.Join(f.directory, "a.txt")); string(body) != "staged\n" {
		t.Fatalf("unstage changed file: %q", body)
	}
}

func TestWorkspaceGitOperationsCompleteCommitWorkflow(t *testing.T) {
	f := newGitOperationFixture(t)
	writeSessionFile(t, f.directory, "a.txt", "initial\n")
	requireGitOperation(t, f.operation("stage", "", "a.txt"))
	requireGitOperation(t, f.operation("commit", "First commit\n\nMessage body"))
	if view := f.view(); view.LastCommit == nil || view.LastCommit.Subject != "First commit" || !view.Writable {
		t.Fatalf("commit metadata: %+v", view)
	}
	writeSessionFile(t, f.directory, "a.txt", "staged\n")
	requireGitOperation(t, f.operation("stage", "", "a.txt"))
	writeSessionFile(t, f.directory, "a.txt", "working\n")
	view := f.view()
	if len(view.Files) != 1 || !view.Files[0].Staged || !view.Files[0].Unstaged || view.Files[0].StagedAdditions != 1 || view.Files[0].UnstagedDeletions != 1 {
		t.Fatalf("partial state: %+v", view.Files)
	}
	for _, side := range []string{"staged", "unstaged"} {
		response := apiRequest(f.app, "GET", f.base+"?view=diff&path=a.txt&side="+side, "", f.cookie, "")
		var diff struct{ Patch string }
		_ = json.Unmarshal(response.Body.Bytes(), &diff)
		want, absent := "+staged", "+working"
		if side == "unstaged" {
			want, absent = "+working", "-initial"
		}
		if response.Code != 200 || !strings.Contains(diff.Patch, want) || strings.Contains(diff.Patch, absent) {
			t.Fatalf("%s diff: %d %s", side, response.Code, response.Body.String())
		}
	}
	requireGitOperation(t, f.operation("commit", "Commit staged only"))
	if got := sessionFileGit(t, f.directory, "show", "HEAD:a.txt"); got != "staged" {
		t.Fatalf("committed worktree instead of index: %s", got)
	}
	if body, _ := os.ReadFile(filepath.Join(f.directory, "a.txt")); string(body) != "working\n" {
		t.Fatalf("worktree changed: %q", body)
	}
	requireGitOperation(t, f.operation("stage", "", "a.txt"))
	requireGitOperation(t, f.operation("unstage", "", "a.txt"))
	if view := f.view(); view.Files[0].Staged || !view.Files[0].Unstaged {
		t.Fatalf("unstaged: %+v", view.Files)
	}
	if err := os.Remove(filepath.Join(f.directory, "a.txt")); err != nil {
		t.Fatal(err)
	}
	requireGitOperation(t, f.operation("stage", "", "a.txt"))
	requireGitOperation(t, f.operation("unstage", "", "a.txt"))
	if view := f.view(); !view.Files[0].Unstaged || view.Files[0].Staged {
		t.Fatalf("deleted unstage: %+v", view.Files)
	}
}

func TestWorkspaceGitOperationsRejectStaleProtectedAndConcurrentWrites(t *testing.T) {
	f := newGitOperationFixture(t)
	writeSessionFile(t, f.directory, "visible.txt", "visible\n")
	writeSessionFile(t, f.directory, ".env", "hidden\n")
	writeSessionFile(t, f.directory, "other.txt", "other\n")
	if response := f.operation("stage", "", ".env"); response.Code != 403 {
		t.Fatalf("protected stage: %d", response.Code)
	}
	if response := f.operation("stage", "", "../escape"); response.Code != 403 {
		t.Fatalf("escape stage: %d", response.Code)
	}
	requireGitOperation(t, f.operation("stage", "", "visible.txt"))
	view := f.view()
	sessionFileGit(t, f.directory, "add", "other.txt")
	response := f.post(workspaceGitOperation{Action: "commit", Message: "stale index", Branch: view.Branch, Head: view.Commit, Index: view.Index})
	if response.Code != 409 {
		t.Fatalf("stale index: %d %s", response.Code, response.Body.String())
	}
	sessionFileGit(t, f.directory, "add", "-f", ".env")
	if response := f.operation("commit", "protected index"); response.Code != 403 {
		t.Fatalf("protected commit: %d %s", response.Code, response.Body.String())
	}
	view = f.view()
	response = f.post(workspaceGitOperation{Action: "stage", Paths: []string{"other.txt"}, Branch: "different", Head: view.Commit})
	if response.Code != 409 {
		t.Fatalf("stale branch: %d", response.Code)
	}
	body := `{"action":"stage","paths":["other.txt"]}`
	if response := apiRequest(f.app, "POST", f.base, body, f.cookie, ""); response.Code != 403 {
		t.Fatalf("missing CSRF: %d", response.Code)
	}
	if response := f.operation("reset", ""); response.Code != 400 {
		t.Fatalf("unsupported operation: %d", response.Code)
	}
}

func TestWorkspaceGitOperationsFetchPublishAndRejectNonFastForward(t *testing.T) {
	f := newGitOperationFixture(t)
	remote := t.TempDir()
	sessionFileGit(t, remote, "init", "--bare")
	sessionFileGit(t, f.directory, "remote", "add", "origin", remote)
	writeSessionFile(t, f.directory, "a.txt", "initial\n")
	requireGitOperation(t, f.operation("stage", "", "a.txt"))
	requireGitOperation(t, f.operation("commit", "initial"))
	requireGitOperation(t, f.operation("push", ""))
	if got := sessionFileGit(t, remote, "rev-parse", "refs/heads/dev"); got != f.view().Commit {
		t.Fatalf("published wrong branch: %s", got)
	}
	if f.view().Upstream != "origin/dev" {
		t.Fatal("publish did not set upstream")
	}
	other := t.TempDir()
	sessionFileGit(t, other, "clone", "-b", "dev", remote, ".")
	writeSessionFile(t, other, "remote.txt", "remote\n")
	sessionFileGit(t, other, "add", ".")
	sessionFileGit(t, other, "commit", "-m", "remote update")
	sessionFileGit(t, other, "push")
	requireGitOperation(t, f.operation("fetch", ""))
	if f.view().Behind != 1 {
		t.Fatalf("fetch missing behind count: %+v", f.view())
	}
	if _, err := os.Stat(filepath.Join(f.directory, "remote.txt")); !os.IsNotExist(err) {
		t.Fatal("fetch changed working tree")
	}
	writeSessionFile(t, f.directory, "local.txt", "local\n")
	requireGitOperation(t, f.operation("stage", "", "local.txt"))
	requireGitOperation(t, f.operation("commit", "local update"))
	if response := f.operation("push", ""); response.Code != 409 {
		t.Fatalf("non-fast-forward push: %d %s", response.Code, response.Body.String())
	}
	if got := sessionFileGit(t, remote, "log", "-1", "--format=%s", "refs/heads/dev"); got != "remote update" {
		t.Fatalf("remote was overwritten: %s", got)
	}
}

func TestWorkspaceGitOperationsUseSelectedWorktreeAndProjectPermissions(t *testing.T) {
	f := newGitOperationFixture(t)
	writeSessionFile(t, f.directory, "a.txt", "initial\n")
	requireGitOperation(t, f.operation("stage", "", "a.txt"))
	requireGitOperation(t, f.operation("commit", "initial"))
	linked := filepath.Join(t.TempDir(), "linked")
	sessionFileGit(t, f.directory, "worktree", "add", "-b", "feature", linked)
	projectID := strings.Split(f.base, "/")[3]
	user, _ := f.app.store.authenticate(f.cookie.Value)
	chat, err := f.app.store.CreateChat(user.User.ID, projectID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	f.base = "/api/chats/" + chat.ID + "/git?workspace=" + linked
	writeSessionFile(t, linked, "a.txt", "linked\n")
	requireGitOperation(t, f.operation("stage", "", "a.txt"))
	if sessionFileGit(t, linked, "show", ":a.txt") != "linked" || sessionFileGit(t, f.directory, "show", ":a.txt") != "initial" {
		t.Fatal("staged in wrong worktree")
	}
	if _, err := f.app.store.db.Exec(`UPDATE project_member_own_access SET access='read-only' WHERE project_id=? AND user_id=?`, projectID, user.User.ID); err != nil {
		t.Fatal(err)
	}
	if response := f.operation("unstage", "", "a.txt"); response.Code != 403 {
		t.Fatalf("readonly chat mutation: %d %s", response.Code, response.Body.String())
	}
	f.base = "/api/projects/" + projectID + "/git"
	if response := f.operation("stage", "", "a.txt"); response.Code != 403 {
		t.Fatalf("readonly project mutation: %d", response.Code)
	}
}

func TestWorkspaceGitOperationsLiteralPathsRenamesAndSubdirectoryCommitBoundary(t *testing.T) {
	f := newGitOperationFixture(t)
	original := "original first line\noriginal second line\noriginal third line\n"
	writeSessionFile(t, f.directory, "src/file[1].txt", original)
	writeSessionFile(t, f.directory, "src/file1.txt", "other\n")
	writeSessionFile(t, f.directory, "outside.txt", "original\n")
	requireGitOperation(t, f.operation("stage", "", "src/file[1].txt", "src/file1.txt", "outside.txt"))
	requireGitOperation(t, f.operation("commit", "initial"))
	writeSessionFile(t, f.directory, "src/file[1].txt", "literal\n")
	writeSessionFile(t, f.directory, "src/file1.txt", "must stay unstaged\n")
	requireGitOperation(t, f.operation("stage", "", "src/file[1].txt"))
	if got := sessionFileGit(t, f.directory, "diff", "--cached", "--name-only"); got != "src/file[1].txt" {
		t.Fatalf("nonliteral stage: %s", got)
	}
	requireGitOperation(t, f.operation("unstage", "", "src/file[1].txt"))
	writeSessionFile(t, f.directory, "src/file[1].txt", original)
	sessionFileGit(t, f.directory, "mv", "src/file[1].txt", "src/renamed.txt")
	writeSessionFile(t, f.directory, "src/renamed.txt", original+"modified after rename\n")
	requireGitOperation(t, f.operation("stage", "", "src/renamed.txt"))
	requireGitOperation(t, f.operation("unstage", "", "src/renamed.txt"))
	if got := sessionFileGit(t, f.directory, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("rename not fully unstaged: %s", got)
	}
	user, _ := f.app.store.authenticate(f.cookie.Value)
	project, err := f.app.store.SaveProject(Project{Name: "Subdirectory", Path: filepath.Join(f.directory, "src"), Members: []string{user.User.ID}})
	if err != nil {
		t.Fatal(err)
	}
	writeSessionFile(t, f.directory, "outside.txt", "outside staged\n")
	sessionFileGit(t, f.directory, "add", "outside.txt")
	f.base = "/api/projects/" + project.ID + "/git"
	requireGitOperation(t, f.operation("stage", "", "file1.txt"))
	if response := f.operation("commit", "must not include outside"); response.Code != 403 {
		t.Fatalf("outside index committed: %d %s", response.Code, response.Body.String())
	}
}

func TestWorkspaceGitOperationsConcurrentLock(t *testing.T) {
	app := &App{}
	const count = 16
	start, results := make(chan struct{}), make(chan error, count)
	var active atomic.Int32
	for i := 0; i < count; i++ {
		go func() {
			<-start
			release, err := app.beginWorkspaceGit(context.Background(), "shared-repository")
			if err == nil {
				if active.Add(1) != 1 {
					t.Error("overlapping Git operations")
				}
				active.Add(-1)
				release()
			}
			results <- err
		}()
	}
	close(start)
	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := app.beginWorkspaceGit(ctx, "shared-repository"); err == nil {
		t.Fatal("canceled operation acquired lock")
	}
	if len(app.gitBusy) != 0 {
		t.Fatal("idle repository lock retained")
	}
}

func TestWorkspaceGitDiscardPreservesIndexAndRemovesOnlySelectedUntrackedFiles(t *testing.T) {
	f := newGitOperationFixture(t)
	writeSessionFile(t, f.directory, "file[1].txt", "committed\n")
	writeSessionFile(t, f.directory, "file1.txt", "other\n")
	requireGitOperation(t, f.operation("stage", "", "file[1].txt", "file1.txt"))
	requireGitOperation(t, f.operation("commit", "initial"))
	writeSessionFile(t, f.directory, "file[1].txt", "staged\n")
	requireGitOperation(t, f.operation("stage", "", "file[1].txt"))
	writeSessionFile(t, f.directory, "file[1].txt", "working\n")
	writeSessionFile(t, f.directory, "file1.txt", "keep working\n")
	writeSessionFile(t, f.directory, "notes/remove.txt", "remove\n")
	writeSessionFile(t, f.directory, "notes/keep.txt", "keep\n")
	index := f.view().Index
	requireGitOperation(t, f.operation("discard", "", "file[1].txt", "notes/remove.txt"))
	if body, _ := os.ReadFile(filepath.Join(f.directory, "file[1].txt")); string(body) != "staged\n" {
		t.Fatalf("did not restore index version: %q", body)
	}
	if f.view().Index != index || sessionFileGit(t, f.directory, "show", "HEAD:file[1].txt") != "committed" {
		t.Fatal("discard changed staged changes or HEAD")
	}
	if body, _ := os.ReadFile(filepath.Join(f.directory, "file1.txt")); string(body) != "keep working\n" {
		t.Fatal("literal path matched another file")
	}
	if _, err := os.Stat(filepath.Join(f.directory, "notes/remove.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked file was not removed")
	}
	if body, _ := os.ReadFile(filepath.Join(f.directory, "notes/keep.txt")); string(body) != "keep\n" {
		t.Fatal("discard removed an unselected file")
	}
	if err := os.Remove(filepath.Join(f.directory, "file[1].txt")); err != nil {
		t.Fatal(err)
	}
	requireGitOperation(t, f.operation("discard", "", "file[1].txt"))
	if body, _ := os.ReadFile(filepath.Join(f.directory, "file[1].txt")); string(body) != "staged\n" {
		t.Fatal("deleted file did not restore from index")
	}
}

func TestWorkspaceGitDiscardRejectsChangedIndexAndValidatesWholeBatch(t *testing.T) {
	f := newGitOperationFixture(t)
	writeSessionFile(t, f.directory, "a.txt", "original\n")
	requireGitOperation(t, f.operation("stage", "", "a.txt"))
	writeSessionFile(t, f.directory, "a.txt", "worktree\n")
	writeSessionFile(t, f.directory, "keep.txt", "keep\n")
	writeSessionFile(t, f.directory, ".env", "protected\n")
	view := f.view()
	sessionFileGit(t, f.directory, "add", "keep.txt")
	response := f.post(workspaceGitOperation{Action: "discard", Paths: []string{"a.txt"}, Branch: view.Branch, Head: view.Commit, Index: view.Index})
	if response.Code != 409 {
		t.Fatalf("discard accepted changed index: %d %s", response.Code, response.Body.String())
	}
	for _, invalid := range []string{".env", "../escape", "missing.txt"} {
		if response := f.operation("discard", "", "a.txt", invalid); response.Code != 403 {
			t.Fatalf("invalid discard %s: %d %s", invalid, response.Code, response.Body.String())
		}
		if body, _ := os.ReadFile(filepath.Join(f.directory, "a.txt")); string(body) != "worktree\n" {
			t.Fatal("invalid batch partially discarded files")
		}
	}
	writeSessionFile(t, f.directory, "nested/keep.txt", "nested repository\n")
	sessionFileGit(t, filepath.Join(f.directory, "nested"), "init")
	if response := f.operation("discard", "", "a.txt", "nested/"); response.Code != 403 && response.Code != 409 {
		t.Fatalf("discard accepted a nested repository: %d %s", response.Code, response.Body.String())
	}
	if body, _ := os.ReadFile(filepath.Join(f.directory, "a.txt")); string(body) != "worktree\n" {
		t.Fatal("directory rejection partially discarded files")
	}
	if body, _ := os.ReadFile(filepath.Join(f.directory, "nested/keep.txt")); string(body) != "nested repository\n" {
		t.Fatal("discard removed a nested repository")
	}
	if response := f.operation("discard", "", "keep.txt"); response.Code != 409 {
		t.Fatalf("discard accepted staged-only file: %d", response.Code)
	}
	// An unmerged index must remain untouched, rather than restoring one side.
	requireGitOperation(t, f.operation("commit", "initial"))
	sessionFileGit(t, f.directory, "checkout", "-b", "other")
	writeSessionFile(t, f.directory, "a.txt", "other\n")
	sessionFileGit(t, f.directory, "add", "a.txt")
	sessionFileGit(t, f.directory, "commit", "-m", "other")
	sessionFileGit(t, f.directory, "checkout", "dev")
	writeSessionFile(t, f.directory, "a.txt", "dev\n")
	sessionFileGit(t, f.directory, "add", "a.txt")
	sessionFileGit(t, f.directory, "commit", "-m", "dev")
	_ = runWorkspaceGitOperation(context.Background(), f.directory, "", "merge", "other")
	if response := f.operation("discard", "", "a.txt"); response.Code != 409 {
		t.Fatalf("discard accepted unmerged file: %d %s", response.Code, response.Body.String())
	}
}

func TestWorkspaceGitDiscardUsesSelectedSubdirectory(t *testing.T) {
	f := newGitOperationFixture(t)
	writeSessionFile(t, f.directory, "src/a.txt", "initial\n")
	writeSessionFile(t, f.directory, "outside.txt", "outside\n")
	requireGitOperation(t, f.operation("stage", "", "src/a.txt", "outside.txt"))
	requireGitOperation(t, f.operation("commit", "initial"))
	writeSessionFile(t, f.directory, "src/a.txt", "staged\n")
	requireGitOperation(t, f.operation("stage", "", "src/a.txt"))
	writeSessionFile(t, f.directory, "src/a.txt", "working\n")
	writeSessionFile(t, f.directory, "outside.txt", "keep\n")
	user, _ := f.app.store.authenticate(f.cookie.Value)
	project, err := f.app.store.SaveProject(Project{Name: "Subdirectory", Path: filepath.Join(f.directory, "src"), Members: []string{user.User.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.base = "/api/projects/" + project.ID + "/git"
	requireGitOperation(t, f.operation("discard", "", "a.txt"))
	if body, _ := os.ReadFile(filepath.Join(f.directory, "src/a.txt")); string(body) != "staged\n" {
		t.Fatalf("subdirectory restore: %q", body)
	}
	if body, _ := os.ReadFile(filepath.Join(f.directory, "outside.txt")); string(body) != "keep\n" {
		t.Fatal("subdirectory discard escaped scope")
	}
}
