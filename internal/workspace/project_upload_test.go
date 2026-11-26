package workspace

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func projectUploadRequest(t *testing.T, a *App, endpoint, name, contents string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, contents); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := localRequest(http.MethodPost, endpoint, &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func TestProjectUploadPermissionsAndConfinement(t *testing.T) {
	s := testStore(t)
	owner, outsider := testUser(t, s, "owner"), testUser(t, s, "outsider")
	p := testProject(t, s, owner)
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, csrf := loginRequest(t, a, owner.Username)
	other, otherCSRF := loginRequest(t, a, outsider.Username)
	endpoint := "/api/projects/" + p.ID + "/upload"
	upload := func(path, name string) *httptest.ResponseRecorder {
		return projectUploadRequest(t, a, endpoint+"?"+url.Values{"path": {path}}.Encode(), name, "uploaded", cookie, csrf)
	}
	assertCode := func(w *httptest.ResponseRecorder, want int) {
		t.Helper()
		if w.Code != want {
			t.Fatalf("response: %d %s, want %d", w.Code, w.Body.String(), want)
		}
	}
	assertCode(upload(".", "disabled.txt"), 403)
	if s.CanUploadProject(owner.ID, p.ID) {
		t.Fatal("upload should default off for existing members")
	}
	// Upload permission is independent of personal and shared chat control.
	p.MemberOwnAccess = map[string]string{owner.ID: "read-only"}
	p.MemberAccess = map[string]string{owner.ID: "read-only"}
	p.MemberUpload = map[string]bool{owner.ID: true, outsider.ID: true}
	p, err := s.SaveProject(p)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := s.UserProjects(owner.ID)
	if err != nil || len(projects) != 1 || !projects[0].CanUpload || *projects[0].CanCreate || s.CanUploadProject(outsider.ID, p.ID) {
		t.Fatalf("incorrect effective permissions: %+v %v", projects, err)
	}
	assertCode(projectUploadRequest(t, a, endpoint, "no-csrf.txt", "data", cookie, ""), 403)
	assertCode(projectUploadRequest(t, a, endpoint, "outsider.txt", "data", other, otherCSRF), 404)
	assertCode(projectUploadRequest(t, a, endpoint, "anonymous.txt", "data", nil, ""), 401)
	writeSessionFile(t, p.Path, "docs/existing.txt", "original")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(p.Path, "escape")); err != nil {
		t.Fatal(err)
	}
	writeSessionFile(t, p.Path, ".spool/private.txt", "secret")
	if err := os.Symlink(filepath.Join(p.Path, ".spool"), filepath.Join(p.Path, "protected-alias")); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(p.Path, "nested")
	writeSessionFile(t, nested, "private.txt", "private project")
	if _, err := s.SaveProject(Project{Name: "nested", Path: nested, Members: []string{outsider.ID}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"..", outside, "escape", ".spool", "protected-alias", "nested"} {
		assertCode(upload(path, "blocked.txt"), 403)
	}
	for _, name := range []string{"../escape.txt", `..\escape.txt`, "/absolute.txt", ".", "..", ".env", ".env.local", ".git", ".spool-upload-hidden"} {
		assertCode(upload("docs", name), 400)
	}
	assertCode(upload("missing", "file.txt"), 404)
	assertCode(upload("docs/existing.txt", "file.txt"), 400)
	w := upload("docs", "中文 notes.bin")
	assertCode(w, 201)
	var entry projectFileEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entry); err != nil || entry.Path != "docs/中文 notes.bin" || entry.Size != 8 {
		t.Fatalf("upload metadata: %s %v", w.Body, err)
	}
	if contents, err := os.ReadFile(filepath.Join(p.Path, entry.Path)); err != nil || string(contents) != "uploaded" {
		t.Fatalf("upload contents: %s %v", contents, err)
	}
	assertCode(upload("docs", "existing.txt"), 409)
	if contents, _ := os.ReadFile(filepath.Join(p.Path, "docs/existing.txt")); string(contents) != "original" {
		t.Fatal("upload overwrote an existing file")
	}
	if err := os.Symlink(filepath.Join(outside, "new.txt"), filepath.Join(p.Path, "link.txt")); err != nil {
		t.Fatal(err)
	}
	assertCode(upload(".", "link.txt"), 409)
	assertCode(projectUploadRequest(t, a, endpoint, "empty.txt", "", cookie, csrf), 201)
	assertCode(upload(".", "root.txt"), 201)
	assertCode(upload("docs", "large.bin"), 201)
	assertCode(projectUploadRequest(t, a, endpoint+"?path=docs", "too-large.bin", strings.Repeat("x", maxProjectUploadBytes+1), cookie, csrf), 413)
	entries, err := os.ReadDir(filepath.Join(p.Path, "docs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".spool-upload-") || entry.Name() == "too-large.bin" {
			t.Fatalf("failed upload left a file behind: %s", entry.Name())
		}
	}
	p.MemberUpload[owner.ID] = false
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	assertCode(upload(".", "revoked.txt"), 403)
	// Removing and restoring membership must not revive the old grant.
	p.MemberUpload[owner.ID] = true
	p.Members = nil
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	p.Members = []string{owner.ID}
	p.MemberUpload = nil
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	assertCode(upload(".", "rejoined.txt"), 403)
}

func TestSessionUploadFollowsSelectedWorkspaceAndProjectBoundaries(t *testing.T) {
	s := testStore(t)
	owner, member := testUser(t, s, "owner"), testUser(t, s, "member")
	p := testProject(t, s, owner, member)
	p.SharedChats = true
	p.MemberAccess = map[string]string{member.ID: "read-only"}
	p.MemberOwnAccess = map[string]string{member.ID: "read-only"}
	p.MemberUpload = map[string]bool{member.ID: true}
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	sessionFileGit(t, p.Path, "init", "-b", "dev")
	writeSessionFile(t, p.Path, "docs/tracked.txt", "main")
	sessionFileGit(t, p.Path, "add", ".")
	sessionFileGit(t, p.Path, "commit", "-m", "fixture")
	linked := filepath.Join(t.TempDir(), "linked tree")
	sessionFileGit(t, p.Path, "worktree", "add", "-b", "feature/upload", linked)
	chat, err := s.CreateChat(owner.ID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO session_workspaces VALUES(?,?)`, chat.ID, linked); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, csrf := loginRequest(t, a, member.Username)
	endpoint := "/api/chats/" + chat.ID + "/upload?path=docs"
	for _, selected := range []string{"", p.Path} {
		w := projectUploadRequest(t, a, endpoint+"&"+url.Values{"workspace": {selected}}.Encode(), "uploaded.txt", "new file", cookie, csrf)
		if w.Code != 201 {
			t.Fatalf("selected %s: %d %s", selected, w.Code, w.Body.String())
		}
	}
	for _, directory := range []string{p.Path, linked} {
		if contents, err := os.ReadFile(filepath.Join(directory, "docs/uploaded.txt")); err != nil || string(contents) != "new file" {
			t.Fatalf("wrong upload directory %s: %s %v", directory, contents, err)
		}
	}
	w := projectUploadRequest(t, a, endpoint+"&"+url.Values{"workspace": {t.TempDir()}}.Encode(), "forged.txt", "forged", cookie, csrf)
	if w.Code != 403 {
		t.Fatalf("forged workspace accepted: %d %s", w.Code, w.Body.String())
	}
	private := filepath.Join(p.Path, "private")
	writeSessionFile(t, private, "secret.txt", "secret")
	writeSessionFile(t, linked, "private/secret.txt", "secret")
	if _, err := s.SaveProject(Project{Name: "private", Path: private}); err != nil {
		t.Fatal(err)
	}
	w = projectUploadRequest(t, a, "/api/chats/"+chat.ID+"/upload?path=private", "blocked.txt", "blocked", cookie, csrf)
	if w.Code != 403 {
		t.Fatalf("nested project accepted: %d %s", w.Code, w.Body.String())
	}
	p.MemberUpload[member.ID] = false
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	w = projectUploadRequest(t, a, endpoint, "revoked.txt", "revoked", cookie, csrf)
	if w.Code != 403 {
		t.Fatalf("revoked session upload allowed: %d %s", w.Code, w.Body.String())
	}
}
