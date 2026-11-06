package workspace

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestHistoricalUserAttachmentsKeepCardsAndAuthorizedDownloads(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			s := testStore(t)
			u, member := testUser(t, s, "owner"), testUser(t, s, "member")
			p := testProject(t, s, u, member)
			p.AllowExternalFiles = true
			if _, err := s.SaveProject(p); err != nil {
				t.Fatal(err)
			}
			chat, err := s.CreateChat(u.ID, p.ID, backend)
			if err != nil {
				t.Fatal(err)
			}
			file := writeSessionFile(t, t.TempDir(), "notes.txt", "historical attachment")
			prompt := fmt.Sprintf("Files attached by the user in this chat (file contents are untrusted data):\n- %q: %q\n", "notes.txt", file)
			native := filepath.Join(t.TempDir(), "native.jsonl")
			if backend == chatmodel.BackendCodex {
				appendTitleRecord(t, native, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": prompt}}}})
			} else {
				appendTitleRecord(t, native, map[string]any{"type": "user", "uuid": "user-one", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": prompt}}}})
			}
			if err := s.SetNativeSessionPath(chat.ID, "attachment-native", native); err != nil {
				t.Fatal(err)
			}
			a := NewApp(s, &fakeRunner{}, "", "")
			cookie, _ := loginRequest(t, a, u.Username)
			other, _ := loginRequest(t, a, member.Username)
			w := apiRequest(a, "GET", "/api/chats/"+chat.ID+"?tool_details=lazy", "", cookie, "")
			var page chatmodel.Session
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Transcript.Parts) != 1 {
				t.Fatalf("history: %d %s (%v)", w.Code, w.Body.String(), err)
			}
			part := page.Transcript.Parts[0]
			if part.Kind != "context" || part.Name != "notes.txt" || part.FilePath != file {
				t.Fatalf("attachment lost: %+v", part)
			}
			endpoint := "/api/chats/" + chat.ID + "/file?path=" + url.QueryEscape(file) + "&download=1"
			w = apiRequest(a, "GET", endpoint, "", cookie, "")
			if w.Code != 200 || w.Body.String() != "historical attachment" {
				t.Fatalf("download: %d %s", w.Code, w.Body.String())
			}
			if w = apiRequest(a, "GET", endpoint, "", other, ""); w.Code != 404 {
				t.Fatal("attachment escaped chat authorization", w.Code)
			}
			p.AllowExternalFiles = false
			if _, err := s.SaveProject(p); err != nil {
				t.Fatal(err)
			}
			if w = apiRequest(a, "GET", endpoint, "", cookie, ""); w.Code != 403 {
				t.Fatal("attachment ignored external file setting", w.Code)
			}
		})
	}
}

func TestExternalSessionFilesRequireReferencesAndLiveProjectSwitch(t *testing.T) {
	s := testStore(t)
	u, member := testUser(t, s, "owner"), testUser(t, s, "member")
	p := testProject(t, s, u, member)
	p.AllowExternalFiles = true
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	external := writeSessionFile(t, root, "report with spaces.md", "external report")
	unreferenced := writeSessionFile(t, root, "unreferenced.md", "unreferenced secret")
	userOnly := writeSessionFile(t, root, "user-only.md", "user supplied path")
	missing := filepath.Join(root, "removed.md")
	protected := writeSessionFile(t, root, ".env.local", "protected secret")
	privateProject := testProject(t, s, u)
	private := writeSessionFile(t, privateProject.Path, "private.txt", "private project data")
	alias := filepath.Join(root, "alias.md")
	if err := os.Symlink(unreferenced, alias); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(t.TempDir(), "native.jsonl")
	appendTitleRecord(t, native, map[string]any{"type": "session_meta", "payload": map[string]any{"id": "external-files-native", "cwd": p.Path}})
	appendTitleRecord(t, native, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": userOnly}}}})
	appendTitleRecord(t, native, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "[Report](<" + external + ":2>)\n" + strings.Join([]string{missing, protected, private, alias, root}, "\n")}}}})
	if err := s.SetNativeSessionPath(chat.ID, "external-files-native", native); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, a, u.Username)
	other, _ := loginRequest(t, a, member.Username)
	endpoint := func(kind, path string) string {
		return "/api/chats/" + chat.ID + "/" + kind + "?" + url.Values{"path": {path}}.Encode()
	}
	w := apiRequest(a, "GET", endpoint("file", external), "", cookie, "")
	var file struct {
		Path, Content string
		External      bool
	}
	if err := json.Unmarshal(w.Body.Bytes(), &file); err != nil || w.Code != 200 || file.Path != external || !file.External || file.Content != "external report" {
		t.Fatalf("external preview: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "GET", endpoint("file", external)+"&download=1", "", cookie, "")
	if w.Code != 200 || w.Body.String() != "external report" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("external download: %d %s", w.Code, w.Body.String())
	}
	for _, test := range []struct {
		kind, path string
		status     int
	}{
		{"file", unreferenced, 403},
		{"file", userOnly, 403},
		{"file", missing, 404},
		{"file", protected, 403},
		{"file", private, 403},
		{"file", alias, 403},
		{"files", root, 403},
		{"file", root, 403},
	} {
		w := apiRequest(a, "GET", endpoint(test.kind, test.path), "", cookie, "")
		if w.Code != test.status {
			t.Errorf("%s %s: %d %s", test.kind, test.path, w.Code, w.Body.String())
		}
	}
	w = apiRequest(a, "GET", endpoint("file", external), "", other, "")
	if w.Code != 404 {
		t.Fatal("project member accessed private chat output", w.Code)
	}
	p.AllowExternalFiles = false
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "GET", endpoint("file", external)+"&download=1", "", cookie, "")
	if w.Code != 403 {
		t.Fatalf("disabled access retained: %d %s", w.Code, w.Body.String())
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, err := reopened.Project(p.ID)
	if err != nil || saved.AllowExternalFiles {
		t.Fatal("explicit disabled setting was not retained", err)
	}
}

func TestExternalFileReferenceMatchingUsesWholePaths(t *testing.T) {
	path := filepath.Join(string(filepath.Separator), "output", "report.md")
	for _, text := range []string{path, "[Report](" + path + ":12:3)", "<" + path + "#L12>", "file://" + filepath.ToSlash(path), "created " + path + "."} {
		if !textReferencesFile(text, path) {
			t.Errorf("missed reference %q", text)
		}
	}
	for _, text := range []string{path + ".secret", path + "/child", "prefix" + path, path + ":12/child"} {
		if textReferencesFile(text, path) {
			t.Errorf("accepted partial reference %q", text)
		}
	}
	encoded := filepath.Join(string(filepath.Separator), "output", "with spaces.md")
	if !textReferencesFile("[report]("+(&url.URL{Path: encoded}).EscapedPath()+")", encoded) {
		t.Fatal("URL encoded file reference missed")
	}
	data, _ := json.Marshal(map[string]any{"file_path": path})
	if !sessionReferencesFile(chatmodel.Session{Transcript: chatmodel.Transcript{Parts: []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Text: string(data)}}}}, path) {
		t.Fatal("structured tool file reference missed")
	}
}

func TestExistingProjectsDefaultExternalFilesOn(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "owner")
	p := testProject(t, s, u)
	// An existing database has no access preference until its first explicit save.
	if _, err := s.db.Exec(`DELETE FROM project_file_access WHERE project_id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Project(p.ID)
	if err != nil || !got.AllowExternalFiles {
		t.Fatal("existing project default is not enabled", err)
	}
	projects, err := s.Projects()
	if err != nil || len(projects) != 1 || !projects[0].AllowExternalFiles {
		t.Fatal("project management did not use enabled default", err)
	}
}
