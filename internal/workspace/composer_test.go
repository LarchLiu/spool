package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
	"spool/internal/testutil/agentfixture"
)

func (f *fakeRunner) PermissionModes(_ context.Context, backend chatmodel.Backend, _ string) (chatmodel.PermissionCatalog, error) {
	c := chatmodel.PermissionCatalog{Backend: backend, Default: chatmodel.DefaultPermissionMode(backend)}
	for _, id := range []string{"ask", "auto", "manual", "acceptEdits", "plan", "dontAsk", "bypassPermissions"} {
		if chatmodel.ValidatePermissionMode(backend, id) == nil {
			c.Modes = append(c.Modes, chatmodel.PermissionMode{ID: id, Available: true})
		}
	}
	c.Current = c.Default
	return c, nil
}

func imageFixture(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a/nkAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func uploadRequest(t *testing.T, a *App, chat string, cookie *http.Cookie, csrf string, fields map[string]string, files ...uploadedAttachment) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="files"; filename="`+file.Name+`"`)
		header.Set("Content-Type", file.MediaType)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(file.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/chats/"+chat+"/messages", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func TestAttachmentsPersistInSQLiteWithOwnerScopedDownloads(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	p := testProject(t, s, alice, bob)
	testAgent(t, s)
	chat, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	other, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	runner := &fakeRunner{text: "Attachment reply"}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	image := uploadedAttachment{Attachment: chatmodel.Attachment{Name: "picture.png", MediaType: "image/png"}, Data: imageFixture(t)}
	text := uploadedAttachment{Attachment: chatmodel.Attachment{Name: "notes.txt", MediaType: "text/plain"}, Data: []byte("user file contents")}
	w := uploadRequest(t, a, chat.ID, cookie, csrf, map[string]string{"message": "Review these", "permission_mode": "auto"}, image, text)
	if w.Code != 202 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	saved := waitChat(t, s, chat.ID)
	files := saved.Turns[0].Attachments
	if len(files) != 2 || saved.PermissionMode != "auto" || files[0].MediaType != "image/png" || files[1].Name != text.Name {
		t.Fatalf("lost attachment metadata: %#v", saved)
	}
	runner.mu.Lock()
	request := runner.requests[0]
	runner.mu.Unlock()
	if request.PermissionMode != "auto" || len(request.Attachments) != 2 || request.Prompt != "Review these" {
		t.Fatalf("wrong runner payload: %#v", request)
	}
	for i, file := range request.Attachments {
		data, err := os.ReadFile(file.Path)
		if err != nil || len(data) != files[i].Size || strings.HasPrefix(file.Path, p.Path+string(os.PathSeparator)) || strings.HasPrefix(file.Path, s.Root()+string(os.PathSeparator)) {
			t.Fatalf("bad temporary attachment: %#v %v", file, err)
		}
	}
	path := "/api/chats/" + chat.ID + "/attachments?id=" + files[0].ID
	w = apiRequest(a, "GET", path, "", cookie, csrf)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), image.Data) || w.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("image preview did not serve private original bytes")
	}
	w = apiRequest(a, "GET", "/api/chats/"+chat.ID+"/attachments?id="+files[1].ID, "", cookie, csrf)
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") || w.Body.String() != string(text.Data) {
		t.Fatal("file download failed")
	}
	bobCookie, bobCSRF := loginRequest(t, a, "bob")
	for _, access := range []struct {
		chat   string
		cookie *http.Cookie
		csrf   string
	}{{chat.ID, bobCookie, bobCSRF}, {other.ID, cookie, csrf}, {chat.ID, nil, ""}} {
		w := apiRequest(a, "GET", "/api/chats/"+access.chat+"/attachments?id="+files[0].ID, "", access.cookie, access.csrf)
		if w.Code != 404 && w.Code != 401 {
			t.Fatal("attachment ownership bypass")
		}
	}
	w = uploadRequest(t, a, chat.ID, cookie, "bad-csrf", nil, text)
	if w.Code != 403 {
		t.Fatal("upload bypassed CSRF")
	}
	// Files-only messages work, and resumed prompts get historical file paths.
	w = uploadRequest(t, a, chat.ID, cookie, csrf, nil, text)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	waitChat(t, s, chat.ID)
	runner.mu.Lock()
	request = runner.requests[1]
	runner.mu.Unlock()
	if len(request.Attachments) != 1 || len(request.Files) != 3 {
		t.Fatal("history files were not rematerialized")
	}
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var persisted []byte
	if err := reopened.db.QueryRow(`SELECT data FROM attachments WHERE id=?`, files[0].ID).Scan(&persisted); err != nil || !bytes.Equal(persisted, image.Data) {
		t.Fatal("SQLite attachment lost")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(request.Files[0].Path); !os.IsNotExist(err) {
		t.Fatal("temporary attachments were not removed on shutdown")
	}
}

func TestInvalidAttachmentsNeverCreateTurnsOrBlobs(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	testAgent(t, s)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	for _, files := range [][]uploadedAttachment{
		{{Attachment: chatmodel.Attachment{Name: "empty.txt", MediaType: "text/plain"}}},
		{{Attachment: chatmodel.Attachment{Name: "large.txt", MediaType: "text/plain"}, Data: bytes.Repeat([]byte("x"), maxAttachmentBytes+1)}},
		{{Attachment: chatmodel.Attachment{Name: "fake.png", MediaType: "image/png"}, Data: []byte("not an image")}},
		{{Attachment: chatmodel.Attachment{Name: "active.svg", MediaType: "image/svg+xml"}, Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)}},
	} {
		w := uploadRequest(t, a, chat.ID, cookie, csrf, map[string]string{"message": "Do not accept"}, files...)
		if w.Code != 400 {
			t.Fatalf("invalid upload accepted: %d %s", w.Code, w.Body.String())
		}
	}
	files := make([]uploadedAttachment, 7)
	for i := range files {
		files[i] = uploadedAttachment{Attachment: chatmodel.Attachment{Name: "file.txt", MediaType: "text/plain"}, Data: []byte("x")}
	}
	if w := uploadRequest(t, a, chat.ID, cookie, csrf, nil, files...); w.Code != 400 {
		t.Fatal("too many files accepted")
	}
	saved, _ := s.Get(chat.ID)
	var count int
	_ = s.db.QueryRow(`SELECT count(*) FROM attachments`).Scan(&count)
	if len(saved.Turns) != 0 || count != 0 {
		t.Fatal("invalid uploads left durable state")
	}
}

func TestNativePermissionSelectionAndApprovalsReuseCLI(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "permission-test"
			calls := 0
			request.Approve = func(_ context.Context, approval chatmodel.ApprovalRequest) (bool, error) {
				calls++
				if approval.Title == "" {
					t.Error("missing native approval title")
				}
				return true, nil
			}
			runner := newTestRunner(t)
			for _, mode := range map[chatmodel.Backend][]string{chatmodel.BackendCodex: {"ask", "auto"}, chatmodel.BackendClaude: {"manual", "auto", "plan", "acceptEdits", "dontAsk", "bypassPermissions"}}[backend] {
				request.PermissionMode = mode
				result, _ := runFixture(t, runner, request)
				request.NativeSessionID = result.SessionID
			}
			if calls < 2 || fixtureCount(t, request, "spawn") != 1 {
				t.Fatal("mode selection restarted CLI or approvals never arrived")
			}
			for _, entry := range fixtureLog(t, request) {
				value, _ := entry["value"].(map[string]any)
				if entry["method"] == "permission-reply" {
					if backend == chatmodel.BackendCodex && agentfixture.NestedString(value, "result", "decision") != "accept" {
						t.Fatal("Codex approval was not sent")
					}
					if backend == chatmodel.BackendClaude && (agentfixture.NestedString(value, "response", "response", "behavior") != "allow" || agentfixture.NestedString(value, "response", "response", "updatedInput", "command") != "echo permission-check") {
						t.Fatal("Claude approval input changed")
					}
				}
				if entry["method"] == "turn/start" && agentfixture.NestedString(value, "params", "approvalsReviewer") != "user" && agentfixture.NestedString(value, "params", "approvalsReviewer") != "auto_review" {
					t.Fatal("invalid Codex reviewer")
				}
			}
		})
	}
}

func TestPermissionDiscoveryIsProjectScopedAndHonorsPolicies(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			runner := newTestRunner(t)
			runner.ConfigureBinaries(map[chatmodel.Backend]string{backend: request.Binary})
			if backend == chatmodel.BackendCodex {
				_ = os.WriteFile(filepath.Join(request.WorkingDirectory, "fixture-policy.json"), []byte(`{"allowedApprovalsReviewers":["user"],"allowedApprovalPolicies":["on-request"],"allowedSandboxModes":["workspace-write"]}`), 0600)
			} else {
				_ = os.WriteFile(filepath.Join(request.WorkingDirectory, "reject-auto"), nil, 0600)
			}
			catalog, err := runner.PermissionModes(context.Background(), backend, request.WorkingDirectory)
			if err != nil || catalog.Validate("auto") == nil || catalog.Validate(chatmodel.DefaultPermissionMode(backend)) != nil {
				t.Fatalf("policy not honored: %#v %v", catalog, err)
			}
			_, _ = runner.PermissionModes(context.Background(), backend, request.WorkingDirectory)
			if fixtureCount(t, request, "spawn") != 1 {
				t.Fatal("project cache was not used")
			}
			_, err = runner.PermissionModes(context.Background(), backend, t.TempDir())
			if err != nil || fixtureCount(t, request, "spawn") != 2 {
				t.Fatalf("another project reused global capabilities: %v", err)
			}
			if fixtureCount(t, request, "user") != 0 || fixtureCount(t, request, "turn/start") != 0 || fixtureCount(t, request, "thread/start") != 0 {
				t.Fatal("permission discovery sent inference or created a chat")
			}
		})
	}
}

func TestCodexAutoApprovalDiscoveryUsesNativeFeaturePagination(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendCodex)
	runner := newTestRunner(t)
	runner.ConfigureBinaries(map[chatmodel.Backend]string{chatmodel.BackendCodex: request.Binary})
	catalog, err := runner.PermissionModes(context.Background(), chatmodel.BackendCodex, request.WorkingDirectory)
	if err != nil || catalog.Validate("auto") != nil {
		t.Fatalf("native auto review was hidden: %#v %v", catalog, err)
	}
}

type interactiveRunner struct{ fakeRunner }

func (f *interactiveRunner) Run(ctx context.Context, request agent.AgentRequest, emit func(agent.AgentEvent) error) (agent.AgentResult, error) {
	allowed, err := request.Approve(ctx, chatmodel.ApprovalRequest{Title: "Test tool", Detail: "echo safe"})
	if err != nil {
		return agent.AgentResult{}, err
	}
	text := "denied"
	if allowed {
		text = "approved"
	}
	_ = emit(agent.AgentEvent{Type: "delta", Text: text})
	return agent.AgentResult{Text: text}, nil
}

// streamRecorder lets SSE tests exercise ServeHTTP without binding a real
// localhost socket. Sandboxed test runners commonly disallow listen(2).
type streamRecorder struct {
	header http.Header
	writes chan []byte
}

func newStreamRecorder() *streamRecorder {
	return &streamRecorder{header: make(http.Header), writes: make(chan []byte, 64)}
}

func (w *streamRecorder) Header() http.Header { return w.header }
func (w *streamRecorder) WriteHeader(int)     {}
func (w *streamRecorder) Write(p []byte) (int, error) {
	copyOfWrite := append([]byte(nil), p...)
	w.writes <- copyOfWrite
	return len(p), nil
}
func (w *streamRecorder) Flush() {}

func TestApprovalHTTPRequiresOwnerCSRFAndCurrentOneShotRequest(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	p := testProject(t, s, alice, bob)
	testAgent(t, s)
	chat, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	a := NewApp(s, &interactiveRunner{}, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	bc, bs := loginRequest(t, a, "bob")
	if w := apiRequest(a, "GET", "/api/permissions?project_id="+p.ID+"&backend=codex&chat_id="+chat.ID, "", bc, bs); w.Code != 404 {
		t.Fatal("cross-user capability access")
	}
	w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"permission","permission_mode":"ask"}`, cookie, csrf)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var turn chatmodel.Turn
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		saved, _ := s.Get(chat.ID)
		snapshot := a.snapshot(saved)
		turn = snapshot.Turns[0]
		if turn.PendingApproval != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if turn.PendingApproval == nil {
		t.Fatal("approval did not reach live snapshot")
	}
	body, _ := json.Marshal(map[string]any{"id": turn.PendingApproval.ID, "turn_id": turn.ID, "allow": true})
	path := "/api/chats/" + chat.ID + "/approvals"
	if w := apiRequest(a, "POST", path, string(body), bc, bs); w.Code != 404 {
		t.Fatal("another user answered approval")
	}
	if w := apiRequest(a, "POST", path, string(body), cookie, "bad"); w.Code != 403 {
		t.Fatal("approval skipped CSRF")
	}
	if w := apiRequest(a, "POST", path, strings.Replace(string(body), turn.ID, chatmodel.NewID(), 1), cookie, csrf); w.Code != 409 {
		t.Fatal("wrong turn answered approval")
	}
	if w := apiRequest(a, "POST", path, string(body), cookie, csrf); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := apiRequest(a, "POST", path, string(body), cookie, csrf); w.Code != 409 {
		t.Fatal("approval replay accepted")
	}
	saved := waitChat(t, s, chat.ID)
	if saved.Status != chatmodel.StatusCompleted || saved.Turns[0].PendingApproval != nil {
		t.Fatalf("approval did not complete/clear: %#v", saved.Turns[0])
	}
	a.liveMu.Lock()
	_, stillPending := a.approvals[turn.PendingApproval.ID]
	live, stillLive := a.live[chat.ID]
	a.liveMu.Unlock()
	if stillPending || (stillLive && live.PendingApproval != nil) {
		t.Fatal("approval remained actionable after completion")
	}
}

func TestNativeAttachmentPayloads(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			imagePath := filepath.Join(t.TempDir(), "pixel.png")
			_ = os.WriteFile(imagePath, imageFixture(t), 0600)
			file := chatmodel.AgentAttachment{Attachment: chatmodel.Attachment{ID: chatmodel.NewID(), Name: "pixel.png", MediaType: "image/png", Size: len(imageFixture(t))}, Path: imagePath}
			request.Attachments, request.Files = []chatmodel.AgentAttachment{file}, []chatmodel.AgentAttachment{file}
			runFixture(t, newTestRunner(t), request)
			for _, entry := range fixtureLog(t, request) {
				if entry["method"] != "turn/start" && entry["method"] != "user" {
					continue
				}
				value, _ := entry["value"].(map[string]any)
				var parts []any
				if backend == chatmodel.BackendCodex {
					params, _ := value["params"].(map[string]any)
					parts, _ = params["input"].([]any)
				} else {
					message, _ := value["message"].(map[string]any)
					parts, _ = message["content"].([]any)
				}
				if len(parts) != 2 {
					t.Fatalf("native image missing: %#v", value)
				}
				text, _ := parts[0].(map[string]any)
				image, _ := parts[1].(map[string]any)
				if !strings.Contains(agentfixture.StringValue(text["text"]), imagePath) {
					t.Fatal("file path not passed to agent")
				}
				if backend == chatmodel.BackendCodex && (image["type"] != "localImage" || image["path"] != imagePath) {
					t.Fatal("bad Codex image payload")
				}
				if backend == chatmodel.BackendClaude && (image["type"] != "image" || agentfixture.NestedString(image, "source", "media_type") != "image/png" || agentfixture.NestedString(image, "source", "data") == "") {
					t.Fatal("bad Claude image payload")
				}
				return
			}
			t.Fatal("no native user input found")
		})
	}
}

func TestSSEDoesNotCarryAttachmentsIntoFollowingTurn(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	testAgent(t, s)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	a := NewApp(s, &fakeRunner{text: "Reply"}, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	file := uploadedAttachment{Attachment: chatmodel.Attachment{Name: "notes.txt", MediaType: "text/plain"}, Data: []byte("Attachment")}
	if w := uploadRequest(t, a, chat.ID, cookie, csrf, map[string]string{"message": "first"}, file); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	first := waitChat(t, s, chat.ID).Turns[0]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
	r.AddCookie(cookie)
	stream := newStreamRecorder()
	go a.ServeHTTP(stream, r)
	var pending bytes.Buffer
	readTurn := func() chatmodel.Turn {
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			for {
				frameEnd := bytes.Index(pending.Bytes(), []byte("\n\n"))
				if frameEnd < 0 {
					break
				}
				frame := string(pending.Next(frameEnd + 2))
				for _, line := range strings.Split(frame, "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event struct {
						Turn chatmodel.Turn `json:"turn"`
					}
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && event.Turn.ID != "" {
						return event.Turn
					}
				}
			}
			select {
			case chunk := <-stream.writes:
				_, _ = pending.Write(chunk)
			case <-deadline.C:
				t.Fatal("missing SSE turn")
				return chatmodel.Turn{}
			}
		}
	}
	if turn := readTurn(); turn.ID != first.ID || len(turn.Attachments) != 1 {
		t.Fatal("first SSE turn lost attachment")
	}
	if w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"second"}`, cookie, csrf); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	second := waitChat(t, s, chat.ID).Turns[1]
	for {
		turn := readTurn()
		if turn.ID == second.ID && turn.Status == chatmodel.StatusCompleted {
			if len(turn.Attachments) != 0 {
				t.Fatal("previous turn attachments leaked into following SSE turn")
			}
			break
		}
	}
}

func TestApprovalCancellationDoesNotLeaveStaleRequests(t *testing.T) {
	s := testStore(t)
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = a.Close() })
	chatID, turnID := chatmodel.NewID(), chatmodel.NewID()
	a.live[chatID] = chatmodel.Turn{ID: turnID, Status: chatmodel.StatusRunning}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := a.requestApproval(ctx, chatID, turnID, chatmodel.ApprovalRequest{Title: "Tool"})
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		a.liveMu.Lock()
		pending := len(a.approvals)
		a.liveMu.Unlock()
		if pending == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("approval did not observe cancellation")
	}
	a.liveMu.Lock()
	count, pending := len(a.approvals), a.live[chatID].PendingApproval
	a.liveMu.Unlock()
	if count != 0 || pending != nil {
		t.Fatal("cancelled approval remained actionable")
	}
	if _, err := a.requestApproval(ctx, chatID, turnID, chatmodel.ApprovalRequest{Title: "Too late"}); err == nil {
		t.Fatal("cancelled handler published a stale request")
	}
}
