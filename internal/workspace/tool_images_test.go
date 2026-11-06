package workspace

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestViewedImagePathsPreserveFullNativePathsAndRecognizeBothProviders(t *testing.T) {
	path := "/work/project/build/artifacts/render/page 1.png"
	for _, name := range []string{"view_image", "functions.view_image", "mcp__tools__view_image", "imageView", "Read"} {
		body, _ := json.Marshal(map[string]string{"file_path": path})
		part := chatmodel.Part{Role: "assistant", Kind: "tool_use", Name: name, Text: string(body)}
		summary := summarizeTool(part)
		if summary.Kind != "image" || !reflect.DeepEqual(summary.ImagePaths, []string{path}) {
			t.Fatalf("%s: image path was lost or shortened: %+v", name, summary)
		}
		chat := chatmodel.Session{ID: "chat", Turns: []chatmodel.Turn{{ID: "turn", PartEnd: 1}}, Transcript: chatmodel.Transcript{Parts: []chatmodel.Part{part}}}
		slim := slimToolDetails(chat).Transcript.Parts[0]
		if slim.Text != "" || !reflect.DeepEqual(slim.Tool.ImagePaths, []string{path}) {
			t.Fatal("lazy history lost image metadata", slim)
		}
	}
	for _, tc := range []struct{ name, text string }{
		{"Read", `{"file_path":"src/main.go"}`},
		{"Read", `{"file_path":"active.svg"}`},
		{"Bash", `{"path":"screen.png"}`},
		{"view_image", `{"path":"bad\u0000.png"}`},
		{"view_image", `broken`},
	} {
		if paths := viewedImagePaths(tc.name, tc.text); len(paths) != 0 {
			t.Fatal("ordinary or invalid tool classified as image", tc)
		}
	}
	batch := `{"tool_uses":[{"recipient_name":"functions.view_image","parameters":{"path":"a.png"}},{"recipient_name":"functions.view_image","parameters":{"path":"b.png"}}]}`
	if got := viewedImagePaths("multi_tool_use.parallel", batch); !reflect.DeepEqual(got, []string{"a.png", "b.png"}) {
		t.Fatal("parallel image batch was not retained", got)
	}
	if got := viewedImagePaths("multi_tool_use.parallel", strings.Replace(batch, "functions.view_image", "functions.exec_command", 1)); len(got) != 0 {
		t.Fatal("mixed batch hid other tools")
	}
}

func TestClaudeToolImagesUseAuthorizedURLsWithoutInlineHistoryPayloads(t *testing.T) {
	s := testStore(t)
	owner, member := testUser(t, s, "owner"), testUser(t, s, "member")
	project := testProject(t, s, owner, member)
	chat, err := s.CreateChat(owner.ID, project.ID, chatmodel.BackendClaude)
	if err != nil {
		t.Fatal(err)
	}
	image := imageFixture(t)
	encoded := base64.StdEncoding.EncodeToString(image)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendTitleRecord(t, path, map[string]any{"type": "user", "uuid": "prompt", "sessionId": "claude-image", "cwd": project.Path, "message": map[string]any{"role": "user", "content": "Inspect the screenshot"}})
	appendTitleRecord(t, path, map[string]any{"type": "assistant", "uuid": "call", "parentUuid": "prompt", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "read-image", "name": "Read", "input": map[string]any{"file_path": "removed.png"}}}}})
	appendTitleRecord(t, path, map[string]any{"type": "user", "uuid": "result", "parentUuid": "call", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "read-image", "content": []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": encoded}}}}}}})
	if err := s.SetNativeSessionPath(chat.ID, "claude-image", path); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, _ := loginRequest(t, a, owner.Username)
	other, _ := loginRequest(t, a, member.Username)
	w := apiRequest(a, "GET", "/api/chats/"+chat.ID+"?tool_details=lazy", "", cookie, "")
	var slim chatmodel.Session
	if err := json.Unmarshal(w.Body.Bytes(), &slim); err != nil || w.Code != 200 {
		t.Fatalf("history: %d %s", w.Code, w.Body.String())
	}
	var endpoint string
	for _, part := range slim.Transcript.Parts {
		if part.Tool != nil && len(part.Tool.ImageURLs) == 1 {
			endpoint = part.Tool.ImageURLs[0]
		}
		if len(part.Images) != 0 {
			t.Fatal("inline image leaked into lazy history")
		}
	}
	if endpoint == "" || strings.Contains(w.Body.String(), encoded) {
		t.Fatal("image URL missing or base64 leaked", w.Body.String())
	}
	for range 2 { // Exercise the restored native history cache as well.
		w = apiRequest(a, "GET", endpoint, "", cookie, "")
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), image) || w.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("image: %d %s", w.Code, w.Body.String())
		}
	}
	if w = apiRequest(a, "HEAD", endpoint, "", cookie, ""); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD image response", w.Code)
	}
	if w = apiRequest(a, "GET", endpoint, "", other, ""); w.Code != 404 {
		t.Fatal("another member accessed private image", w.Code)
	}
	if w = apiRequest(a, "GET", endpoint, "", nil, ""); w.Code != 401 {
		t.Fatal("unauthenticated image access", w.Code)
	}
	if w = apiRequest(a, "GET", strings.Replace(endpoint, "index=0", "index=1", 1), "", cookie, ""); w.Code != 404 {
		t.Fatal("out-of-range image access", w.Code)
	}
}

func TestToolImageRejectsNonRasterPayload(t *testing.T) {
	part := chatmodel.Part{Role: "tool", Kind: "tool_result", Text: "[image]", Images: []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not an image"))}}
	turn := chatmodel.Turn{ID: "turn", PartEnd: 1}
	chat := chatmodel.Session{ID: "chat", Turns: []chatmodel.Turn{turn}, Transcript: chatmodel.Transcript{Parts: []chatmodel.Part{part}}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/?index=0&ref="+url.QueryEscape(toolPartRef(chat.ID, turn, 0, part)), nil)
	(&App{}).toolImageResponse(w, r, chat)
	if w.Code != 400 {
		t.Fatal("non-raster tool image served", w.Code)
	}
}
