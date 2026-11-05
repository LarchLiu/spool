package workspace

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"spool/internal/issues"
	chatmodel "spool/internal/model"
)

func sessionFileGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_AUTHOR_NAME=Spool Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Spool Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeSessionFile(t *testing.T, directory, path, body string) string {
	t.Helper()
	path = filepath.Join(directory, path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSessionFilesFollowWorktreeAndProtectBoundaries(t *testing.T) {
	s := testStore(t)
	owner, member := testUser(t, s, "owner"), testUser(t, s, "member")
	p := testProject(t, s, owner, member)
	sessionFileGit(t, p.Path, "init", "-b", "dev")
	writeSessionFile(t, p.Path, "same.txt", "main checkout")
	sessionFileGit(t, p.Path, "add", "same.txt")
	sessionFileGit(t, p.Path, "commit", "-m", "fixture")
	linked := filepath.Join(t.TempDir(), "linked tree")
	sessionFileGit(t, p.Path, "worktree", "add", "-b", "feature/viewer", linked)
	writeSessionFile(t, linked, "same.txt", "worktree contents")
	chat, err := s.CreateChat(owner.ID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO session_workspaces VALUES(?,?)`, chat.ID, linked); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, a, owner.Username)
	other, _ := loginRequest(t, a, member.Username)
	get := func(endpoint, path string) *httptest.ResponseRecorder {
		return apiRequest(a, "GET", "/api/chats/"+chat.ID+"/"+endpoint+"?"+url.Values{"path": {path}}.Encode(), "", cookie, "")
	}
	workspace := get("workspace", "")
	var info sessionWorkspace
	if err := json.Unmarshal(workspace.Body.Bytes(), &info); err != nil || workspace.Code != 200 || !info.Available || info.Kind != "worktree" || info.Branch != "feature/viewer" || info.Path != canonicalDirectory(linked) {
		t.Fatalf("workspace: %d %s", workspace.Code, workspace.Body.String())
	}
	for _, path := range []string{"same.txt", filepath.Join(linked, "same.txt")} {
		w := get("file", path)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "worktree contents") || strings.Contains(w.Body.String(), "main checkout") {
			t.Fatalf("worktree file: %d %s", w.Code, w.Body.String())
		}
	}
	w := apiRequest(a, "GET", "/api/projects/"+p.ID+"/file?path=same.txt", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "main checkout") {
		t.Fatalf("legacy project root: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "GET", "/api/chats/"+chat.ID+"/workspace", "", other, "")
	if w.Code != 404 {
		t.Fatalf("project member accessed private chat workspace: %d", w.Code)
	}
	private := filepath.Join(p.Path, "private")
	writeSessionFile(t, private, "secret.txt", "private data")
	writeSessionFile(t, linked, "private/secret.txt", "private worktree data")
	if _, err := s.SaveProject(Project{Name: "private", Path: private, Members: []string{owner.ID}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../same.txt", ".git", "private/secret.txt", "private"} {
		if w := get("file", path); w.Code != 403 {
			t.Fatalf("boundary %q: %d %s", path, w.Code, w.Body.String())
		}
	}
	w = get("files", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), `"name":"private"`) || !strings.Contains(w.Body.String(), "same.txt") {
		t.Fatalf("private subtree listing: %d %s", w.Code, w.Body.String())
	}
	sessionFileGit(t, linked, "switch", "--detach")
	w = get("workspace", "")
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil || w.Code != 200 || !strings.Contains(w.Body.String(), `"commit":`) || strings.Contains(w.Body.String(), `"branch":`) {
		t.Fatalf("detached workspace: %d %s", w.Code, w.Body.String())
	}
	sessionFileGit(t, p.Path, "worktree", "remove", "--force", linked)
	w = get("workspace", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":false`) {
		t.Fatalf("missing worktree: %d %s", w.Code, w.Body.String())
	}
	if w := get("file", "same.txt"); w.Code != 404 || strings.Contains(w.Body.String(), "main checkout") {
		t.Fatalf("missing worktree fell back to main: %d %s", w.Code, w.Body.String())
	}
}

func TestSessionFilesReadNativeDirectoryAndRejectForgedWorktree(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			localSessionHomes(t)
			s := testStore(t)
			u := testUser(t, s, "owner")
			p := testProject(t, s, u)
			sessionFileGit(t, p.Path, "init", "-b", "dev")
			writeSessionFile(t, p.Path, "readme.txt", "main")
			sessionFileGit(t, p.Path, "add", ".")
			sessionFileGit(t, p.Path, "commit", "-m", "fixture")
			linked := filepath.Join(t.TempDir(), "worktree")
			sessionFileGit(t, p.Path, "worktree", "add", "-b", "native", linked)
			chat, err := s.CreateChat(u.ID, p.ID, backend)
			if err != nil {
				t.Fatal(err)
			}
			path := writeLocalSession(t, backend, "native-workspace-file", linked, 1)
			if err := s.SetNativeSessionPath(chat.ID, "native-workspace-file", path); err != nil {
				t.Fatal(err)
			}
			a := NewApp(s, &fakeRunner{}, "", "")
			cookie, _ := loginRequest(t, a, u.Username)
			w := apiRequest(a, "GET", "/api/chats/"+chat.ID+"/workspace", "", cookie, "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), canonicalDirectory(linked)) || !strings.Contains(w.Body.String(), `"branch":"native"`) {
				t.Fatalf("native directory: %d %s", w.Code, w.Body.String())
			}
			// A different cwd in provider metadata updates viewing only.
			if backend == chatmodel.BackendCodex {
				appendTitleRecord(t, path, map[string]any{"type": "turn_context", "payload": map[string]any{"cwd": p.Path}})
			} else {
				appendTitleRecord(t, path, map[string]any{"type": "assistant", "cwd": p.Path, "message": map[string]any{"content": "done"}})
			}
			w = apiRequest(a, "GET", "/api/chats/"+chat.ID+"/workspace", "", cookie, "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"main"`) || !strings.Contains(w.Body.String(), `"branch":"dev"`) {
				t.Fatalf("updated cwd: %d %s", w.Code, w.Body.String())
			}
			forged := t.TempDir()
			localGitWorktree(t, p.Path, forged)
			if backend == chatmodel.BackendCodex {
				appendTitleRecord(t, path, map[string]any{"type": "turn_context", "payload": map[string]any{"cwd": forged}})
			} else {
				appendTitleRecord(t, path, map[string]any{"type": "assistant", "cwd": forged, "message": map[string]any{"content": "done"}})
			}
			w = apiRequest(a, "GET", "/api/chats/"+chat.ID+"/workspace", "", cookie, "")
			if w.Code != 403 {
				t.Fatalf("unregistered worktree accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestIssueSessionFilesReadExistingTaskProgress(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "owner")
	p := testProject(t, s, u)
	sessionFileGit(t, p.Path, "init", "-b", "dev")
	directory, err := issues.PrepareDirectory(s.Root(), p.Path)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	chat.AutomationChat = true
	owner := issues.NewOwner("test", "run-session-files")
	owner.Chat = chat.ID
	ledger, err := issues.Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	ledger.Records[42] = &issues.Record{Number: 42, Status: "processing", Owner: owner, Worktree: filepath.Join(p.Path, "planned")}
	if err := ledger.Save(); err != nil {
		t.Fatal(err)
	}
	task, err := issues.PrepareTask(directory, *ledger.Records[42])
	if err != nil {
		t.Fatal(err)
	}
	task.Records[42].Worktree = filepath.Join(p.Path, "actual")
	if err := task.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := s.sessionFileDirectory(chat)
	if err != nil || got != task.Records[42].Worktree {
		t.Fatalf("task progress directory = %q, %v", got, err)
	}
}
