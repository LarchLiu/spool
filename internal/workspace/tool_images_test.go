package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type imageCodexHistory struct {
	fakeCodexHistory
	path string
	hide bool
}

func (f *imageCodexHistory) imagePart() chatmodel.Part {
	body, _ := json.Marshal(map[string]string{"path": f.path})
	return chatmodel.Part{ID: "image-call", Role: "assistant", Kind: "tool_use", Name: "view_image", Text: string(body)}
}

func (f *imageCodexHistory) Turns(context.Context, string, string, int) (agent.CodexTurnPage, error) {
	turn := agent.CodexHistoryTurn{ID: "image-turn", Status: chatmodel.StatusCompleted, Parts: []chatmodel.Part{{Role: "user", Kind: "text", Text: "Inspect the screenshot"}, f.imagePart()}}
	if f.hide {
		turn.ID, turn.Parts = "later-turn", []chatmodel.Part{{Role: "user", Kind: "text", Text: "Next task"}}
	}
	return agent.CodexTurnPage{Turns: []agent.CodexHistoryTurn{turn}}, nil
}

func (f *imageCodexHistory) Item(_ context.Context, _ string, turn, item string) ([]chatmodel.Part, error) {
	f.items++
	if turn == "image-turn" && item == "image-call" {
		return []chatmodel.Part{f.imagePart()}, nil
	}
	return nil, nil
}

func TestCodexNativeImageViewPreviewsSavedPixelsAfterFileRemovalAndWindowEviction(t *testing.T) {
	s := testStore(t)
	owner, member := testUser(t, s, "owner"), testUser(t, s, "member")
	project := testProject(t, s, owner, member)
	project.AllowExternalFiles = false
	if _, err := s.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateChat(owner.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	image := imageFixture(t)
	encoded := base64.StdEncoding.EncodeToString(image)
	removed := filepath.Join(t.TempDir(), "removed.png")
	native := filepath.Join(t.TempDir(), "rollout.jsonl")
	appendTitleRecord(t, native, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "image-turn"}})
	appendTitleRecord(t, native, map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "image-call", "output": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + encoded}}}})
	appendTitleRecord(t, native, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "later-turn"}})
	if err := s.SetNativeSessionPath(chat.ID, "native-image-thread", native); err != nil {
		t.Fatal(err)
	}
	fake := &imageCodexHistory{path: removed}
	s.codexHistory.reader, s.codexHistory.threads = fake, nil
	indexes, reads := countNativeHistoryReads(s)
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, _ := loginRequest(t, a, owner.Username)
	other, _ := loginRequest(t, a, member.Username)
	w := apiRequest(a, "GET", "/api/chats/"+chat.ID+"?tool_details=lazy", "", cookie, "")
	var page chatmodel.Session
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Transcript.Parts) != 2 {
		t.Fatalf("native history: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	summary := page.Transcript.Parts[1].Tool
	if summary == nil || len(summary.ImageURLs) != 1 || !strings.Contains(summary.ImageURLs[0], "ref=native-tool-") || strings.Contains(w.Body.String(), encoded) {
		t.Fatalf("native image view lost its lazy image URL: %+v", summary)
	}
	endpoint := summary.ImageURLs[0]
	for range 2 {
		w = apiRequest(a, "GET", endpoint, "", cookie, "")
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), image) || w.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("saved image: %d %s", w.Code, w.Body.String())
		}
		fake.hide = true
		s.codexHistory.forget("native-image-thread")
	}
	if fake.items == 0 || indexes.Load() != 0 || reads.Load() != 0 {
		t.Fatalf("image access failed to use the item locator, or decoded unrelated turns: items=%d indexes=%d reads=%d", fake.items, indexes.Load(), reads.Load())
	}
	for _, test := range []struct {
		endpoint string
		cookie   *http.Cookie
		status   int
	}{
		{endpoint, other, 404}, {endpoint, nil, 401},
		{strings.Replace(endpoint, "index=0", "index=1", 1), cookie, 404},
		{"/api/chats/" + chat.ID + "/file?path=" + url.QueryEscape(removed) + "&image=1", cookie, 404},
	} {
		if w := apiRequest(a, "GET", test.endpoint, "", test.cookie, ""); w.Code != test.status {
			t.Fatalf("access boundary: got %d, want %d: %s", w.Code, test.status, w.Body.String())
		}
	}
	if _, err := os.Stat(removed); !os.IsNotExist(err) {
		t.Fatal("preview unexpectedly depended on the original file", err)
	}
	// Old rollouts can omit pixels. Their fallback retains the normal file policy.
	for _, test := range []struct {
		path   string
		status int
	}{
		{writeSessionFile(t, project.Path, "available.png", string(image)), 200},
		{writeSessionFile(t, t.TempDir(), "external.png", string(image)), 403},
		{writeSessionFile(t, project.Path, ".env.screen.png", string(image)), 403},
	} {
		part := fake.imagePart()
		part.ID = "call-without-saved-output"
		body, _ := json.Marshal(map[string]string{"path": test.path})
		part.Text = string(body)
		turn := chatmodel.Turn{NativeHistory: true, NativeTurnID: "image-turn", PartEnd: 1}
		chat.NativeSessionPath, chat.Turns = native, []chatmodel.Turn{turn}
		chat.Transcript.Parts = []chatmodel.Part{part}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/?index=0&ref="+url.QueryEscape(toolPartRef(chat.ID, turn, 0, part)), nil)
		a.toolImageResponse(w, r, chat)
		if w.Code != test.status || w.Code == 200 && !bytes.Equal(w.Body.Bytes(), image) {
			t.Fatalf("file fallback: %d, want %d: %s", w.Code, test.status, w.Body.String())
		}
	}
}

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
