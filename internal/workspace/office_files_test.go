package workspace

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func officeWordFixture(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for name, text := range map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": "<document><body><p><r><t>Authorized Office preview</t></r></p></body></document>"} {
		f, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestOfficeFilePreviewKeepsProjectAndSessionAuthorization(t *testing.T) {
	s := testStore(t)
	owner, other := testUser(t, s, "owner"), testUser(t, s, "other")
	p := testProject(t, s, owner, other)
	data := officeWordFixture(t)
	if err := os.WriteFile(filepath.Join(p.Path, "report.docx"), data, 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "外部报告.docx")
	if err := os.WriteFile(outside, data, 0600); err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateChat(owner.ID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(t.TempDir(), "native.jsonl")
	quoted, _ := json.Marshal(outside)
	appendTitleRecord(t, native, map[string]any{"type": "session_meta", "payload": map[string]any{"id": "office-native", "cwd": p.Path}})
	appendTitleRecord(t, native, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": ":codex-file-citation{path=" + string(quoted) + " purpose=\"output\"}"}}}})
	if err = s.SetNativeSessionPath(chat.ID, "office-native", native); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, _ := loginRequest(t, a, owner.Username)
	otherCookie, _ := loginRequest(t, a, other.Username)
	project := "/api/projects/" + p.ID + "/file?path=report.docx&office=1"
	for _, endpoint := range []string{project, "/api/chats/" + chat.ID + "/file?path=report.docx&office=1"} {
		w := apiRequest(a, "GET", endpoint, "", cookie, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Authorized Office preview") || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("Office preview: %d %s", w.Code, w.Body.String())
		}
	}
	if w := apiRequest(a, "GET", project, "", nil, ""); w.Code != 401 {
		t.Fatal("Unauthenticated Office request", w.Code)
	}
	for _, endpoint := range []string{project + "&sheet=-1", project + "&row=nope", project + "&row=1048576", "/api/projects/" + p.ID + "/file?path=" + url.QueryEscape(outside) + "&office=1"} {
		w := apiRequest(a, "GET", endpoint, "", cookie, "")
		if w.Code < 400 {
			t.Fatal("Accepted invalid Office request", endpoint)
		}
	}
	external := "/api/chats/" + chat.ID + "/file?path=" + url.QueryEscape(outside) + "&office=1"
	if w := apiRequest(a, "GET", external, "", cookie, ""); w.Code != 403 {
		t.Fatal("External switch ignored", w.Code)
	}
	p.AllowExternalFiles = true
	if _, err = s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	if w := apiRequest(a, "GET", external, "", cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Authorized Office preview") {
		t.Fatal("Referenced external Office file unavailable", w.Code, w.Body.String())
	}
	if w := apiRequest(a, "GET", external, "", otherCookie, ""); w.Code != 404 {
		t.Fatal("Private chat Office preview exposed", w.Code)
	}
	if err = os.WriteFile(filepath.Join(p.Path, "fake.docx"), []byte("<script>fake</script>"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := apiRequest(a, "GET", "/api/projects/"+p.ID+"/file?path=fake.docx&office=1", "", cookie, ""); w.Code != 400 {
		t.Fatal("Malformed document previewed", w.Code)
	}
	w := apiRequest(a, "GET", "/api/projects/"+p.ID+"/file?path=report.docx&download=1", "", cookie, "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal("Original download changed")
	}
}
