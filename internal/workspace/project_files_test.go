package workspace

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectFilesRequireMembershipAndStayInsideProject(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	p := testProject(t, s, alice)
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(p.Path, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/development-plan.md", "# Plan\n\nThe actual local file.\n")
	write(".env.local", "SECRET=never-return-this")
	write(".spool/spool.sqlite", "private database")
	write("binary.bin", "a\x00b")
	write("page.html", "<script>alert(1)</script>")
	write("large.txt", strings.Repeat("x", maxFilePreviewBytes+20))
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(p.Path, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(p.Path, ".env.local"), filepath.Join(p.Path, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	rootAlias := filepath.Join(t.TempDir(), "project-alias")
	if err := os.Symlink(p.Path, rootAlias); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, csrf := loginRequest(t, a, alice.Username)
	other, _ := loginRequest(t, a, bob.Username)
	endpoint := func(kind, path string) string {
		return "/api/projects/" + p.ID + "/" + kind + "?" + url.Values{"path": {path}}.Encode()
	}
	for name, request := range map[string]struct {
		path   string
		cookie *http.Cookie
		want   int
	}{
		"unauthenticated":   {endpoint("files", ""), nil, 401},
		"non-member":        {endpoint("files", ""), other, 404},
		"traversal":         {endpoint("file", "../secret.txt"), cookie, 403},
		"absolute outside":  {endpoint("file", outside), cookie, 403},
		"symlink escape":    {endpoint("file", "escape.txt"), cookie, 403},
		"protected alias":   {endpoint("file", "alias.txt"), cookie, 403},
		"environment":       {endpoint("file", ".env.local"), cookie, 403},
		"database":          {endpoint("file", ".spool/spool.sqlite"), cookie, 403},
		"missing":           {endpoint("file", "missing.md"), cookie, 404},
		"directory preview": {endpoint("file", "docs"), cookie, 400},
		"file listing":      {endpoint("files", "binary.bin"), cookie, 400},
	} {
		t.Run(name, func(t *testing.T) {
			w := apiRequest(a, "GET", request.path, "", request.cookie, "")
			if w.Code != request.want || strings.Contains(w.Body.String(), "outside-secret") || strings.Contains(w.Body.String(), "never-return-this") {
				t.Fatalf("response %d: %s", w.Code, w.Body.String())
			}
		})
	}
	w := apiRequest(a, "GET", endpoint("files", ""), "", cookie, "")
	var listing struct {
		Entries []projectFileEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil || w.Code != 200 {
		t.Fatalf("listing: %d %s", w.Code, w.Body.String())
	}
	if len(listing.Entries) != 4 || listing.Entries[0].Kind != "directory" || listing.Entries[0].Path != "docs" {
		t.Fatalf("unexpected listing: %+v", listing)
	}
	for _, path := range []string{"docs/development-plan.md", filepath.Join(p.Path, "docs/development-plan.md"), filepath.Join(rootAlias, "docs/development-plan.md")} {
		w = apiRequest(a, "GET", endpoint("file", path), "", cookie, "")
		var file struct{ Path, Kind, Content string }
		if err := json.Unmarshal(w.Body.Bytes(), &file); err != nil || w.Code != 200 || file.Path != "docs/development-plan.md" || file.Kind != "text" || !strings.Contains(file.Content, "actual local file") {
			t.Fatalf("preview: %d %s", w.Code, w.Body.String())
		}
	}
	w = apiRequest(a, "GET", endpoint("file", "binary.bin"), "", cookie, "")
	if !strings.Contains(w.Body.String(), `"kind":"binary"`) {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(a, "GET", endpoint("file", "large.txt"), "", cookie, "")
	if !strings.Contains(w.Body.String(), `"truncated":true`) || w.Body.Len() > maxFilePreviewBytes+500 {
		t.Fatal("preview limit not applied")
	}
	write("unicode.txt", strings.Repeat("x", maxFilePreviewBytes-1)+"\u4e2d\u6587")
	w = apiRequest(a, "GET", endpoint("file", "unicode.txt"), "", cookie, "")
	var unicodeFile struct {
		Kind      string
		Content   string
		Truncated bool
	}
	if err := json.Unmarshal(w.Body.Bytes(), &unicodeFile); err != nil || unicodeFile.Kind != "text" || !unicodeFile.Truncated || len(unicodeFile.Content) != maxFilePreviewBytes-1 {
		t.Fatal("UTF-8 preview boundary is invalid")
	}
	w = apiRequest(a, "GET", endpoint("file", "page.html")+"&download=1", "", cookie, "")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe download: %d %v", w.Code, w.Header())
	}
	w = apiRequest(a, "GET", endpoint("file", "page.html")+"&image=1", "", cookie, "")
	if w.Code != 400 {
		t.Fatalf("HTML served inline: %d", w.Code)
	}
	w = apiRequest(a, "POST", endpoint("file", "docs/development-plan.md"), `{}`, cookie, csrf)
	if w.Code != 404 {
		t.Fatalf("file API unexpectedly permits mutations: %d", w.Code)
	}
	p.Archived = true
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "GET", endpoint("files", ""), "", cookie, "")
	if w.Code != 404 {
		t.Fatal("archived project still readable")
	}
}
