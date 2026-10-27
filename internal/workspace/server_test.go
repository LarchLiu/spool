package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type fakeRunner struct {
	mu       sync.Mutex
	requests []agent.AgentRequest
	text     string
	err      error
	catalog  *chatmodel.CodexCatalog
}

type gatedRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *gatedRunner) Binary(chatmodel.Backend) (string, error) { return "/fake-agent", nil }
func (r *gatedRunner) Run(ctx context.Context, request agent.AgentRequest, emit func(agent.AgentEvent) error) (agent.AgentResult, error) {
	close(r.started)
	select {
	case <-r.release:
	case <-ctx.Done():
		return agent.AgentResult{}, ctx.Err()
	}
	if emit != nil {
		if err := emit(agent.AgentEvent{Type: "delta", Text: "streamed"}); err != nil {
			return agent.AgentResult{}, err
		}
	}
	return agent.AgentResult{SessionID: "native-session-123", Text: "streamed"}, nil
}

func (f *fakeRunner) Binary(chatmodel.Backend) (string, error) { return "/fake-agent", nil }
func (f *fakeRunner) CodexModels(context.Context) (chatmodel.CodexCatalog, error) {
	if f.catalog != nil {
		return f.catalog.Clone(), nil
	}
	return fixtureCatalog(), nil
}
func (f *fakeRunner) Run(_ context.Context, request agent.AgentRequest, emit func(agent.AgentEvent) error) (agent.AgentResult, error) {
	f.mu.Lock()
	f.requests = append(f.requests, request)
	f.mu.Unlock()
	if f.err != nil {
		return agent.AgentResult{}, f.err
	}
	model := request.Model
	if model == "" {
		model = "fixture-model"
	}
	for _, e := range []agent.AgentEvent{{Type: "session", SessionID: "native-session-123"}, {Type: "activity", Text: "Read source"}, {Type: "model", Model: model, ReasoningEffort: request.ReasoningEffort}, {Type: "delta", Text: f.text}} {
		if emit != nil {
			if err := emit(e); err != nil {
				return agent.AgentResult{}, err
			}
		}
	}
	return agent.AgentResult{SessionID: "native-session-123", Model: model, ReasoningEffort: request.ReasoningEffort, Text: f.text, Activities: []string{"Read source"}}, nil
}
func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func testUser(t *testing.T, s *Store, name string) User {
	t.Helper()
	u, err := s.SaveUser(UserInput{Username: name, Nickname: name, Password: "test-password-2026"})
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func testProject(t *testing.T, s *Store, users ...User) Project {
	t.Helper()
	v := Project{Name: "Example", Path: t.TempDir()}
	for _, u := range users {
		v.Members = append(v.Members, u.ID)
	}
	p, err := s.SaveProject(v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func testAgent(t *testing.T, s *Store) {
	t.Helper()
	if err := s.saveAgent(AgentInfo{Backend: chatmodel.BackendCodex, Enabled: true, Installed: true, Catalog: fixtureCatalog()}); err != nil {
		t.Fatal(err)
	}
}
func loginRequest(t *testing.T, a *App, name string) (*http.Cookie, string) {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"`+name+`","password":"test-password-2026"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var v struct {
		CSRF string `json:"csrf"`
	}
	json.Unmarshal(w.Body.Bytes(), &v)
	return w.Result().Cookies()[0], v.CSRF
}
func apiRequest(a *App, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		r.AddCookie(cookie)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func waitChat(t *testing.T, s *Store, id string) chatmodel.Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v, _ := s.Get(id)
		if v.Status != chatmodel.StatusRunning {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("chat did not finish")
	return chatmodel.Session{}
}
func TestAuthenticatedChatUsesProjectDirectoryAndPersistsChoices(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	project := testProject(t, s, u)
	testAgent(t, s)
	runner := &fakeRunner{text: "Streaming reply"}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	created := apiRequest(a, "POST", "/api/chats", `{"project_id":"`+project.ID+`","backend":"codex"}`, cookie, csrf)
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	var chat chatmodel.Session
	json.Unmarshal(created.Body.Bytes(), &chat)
	prompt := "  Keep raw input\n with spacing  "
	b, _ := json.Marshal(map[string]string{"message": prompt, "model": "test-model", "reasoning_effort": "xhigh"})
	w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", string(b), cookie, csrf)
	if w.Code != 202 {
		t.Fatalf("message: %d %s", w.Code, w.Body.String())
	}
	v := waitChat(t, s, chat.ID)
	if v.UserID != u.ID || v.ProjectID != project.ID || v.Turns[0].Reply != "" || len(v.Transcript.Parts) != 0 || v.ReasoningEffort != "xhigh" {
		t.Fatalf("wrong saved chat: %#v", v)
	}
	runner.mu.Lock()
	request := runner.requests[0]
	runner.mu.Unlock()
	if request.WorkingDirectory != project.Path || request.Prompt != prompt || request.Model != "test-model" {
		t.Fatalf("wrong agent request: %#v", request)
	}
	w = apiRequest(a, "GET", "/api/chats/"+chat.ID, "", cookie, csrf)
	if strings.Contains(w.Body.String(), "native-session") || strings.Contains(w.Body.String(), project.Path) {
		t.Fatal("private paths/session IDs leaked")
	}
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, _ := reopened.Get(chat.ID)
	if saved.Turns[0].Reply != "" || len(saved.Transcript.Parts) != 0 {
		t.Fatal("conversation body unexpectedly persisted in SQLite")
	}
}

func TestSnapshotClampsStaleLivePartOffset(t *testing.T) {
	s := testStore(t)
	user := testUser(t, s, "snapshot")
	project := testProject(t, s, user)
	chat, err := s.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.beginTurn(chat.ID, "hello", chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	a.live[chat.ID] = chatmodel.Turn{
		ID:        turn.ID,
		PartStart: 20,
		InputEnd:  20,
		Status:    chatmodel.StatusRunning,
	}
	loaded, ok := s.Get(chat.ID)
	if !ok {
		t.Fatal("chat disappeared")
	}
	snapshot := a.snapshot(loaded)
	if len(snapshot.Turns) != 1 || snapshot.Turns[0].PartStart > len(snapshot.Transcript.Parts) {
		t.Fatalf("stale live offset was not clamped: %#v", snapshot)
	}
}

func TestResumedTurnSnapshotKeepsHydratedHistoryWhileStreaming(t *testing.T) {
	s := testStore(t)
	user := testUser(t, s, "resume-stream")
	project := testProject(t, s, user)
	chat, err := s.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	runner := &gatedRunner{started: make(chan struct{}), release: make(chan struct{})}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() {
		select {
		case <-runner.release:
		default:
			close(runner.release)
		}
		_ = a.Close()
	})
	chat.Transcript = chatmodel.Transcript{Parts: []chatmodel.Part{
		{Role: "user", Kind: "text", Text: "previous question"},
		{Role: "assistant", Kind: "text", Text: "previous answer"},
	}}
	if err := a.startTurnWithInput(chat, messageInput{Message: "follow up"}, chatmodel.CodexOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not start")
	}
	loaded, ok := s.Get(chat.ID)
	if !ok {
		t.Fatal("chat disappeared")
	}
	snapshot := a.snapshot(loaded)
	if len(snapshot.Transcript.Parts) != 3 || snapshot.Transcript.Parts[0].Text != "previous question" || snapshot.Transcript.Parts[2].Text != "follow up" {
		t.Fatalf("streaming snapshot lost hydrated history: %#v", snapshot.Transcript.Parts)
	}
	close(runner.release)
	waitChat(t, s, chat.ID)
}

func TestUserAndProjectAccessCannotBeForged(t *testing.T) {
	s := testStore(t)
	alice := testUser(t, s, "alice")
	bob := testUser(t, s, "bob")
	project := testProject(t, s, alice, bob)
	private := testProject(t, s, alice)
	testAgent(t, s)
	chat, _ := s.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, csrf := loginRequest(t, a, "bob")
	for _, path := range []string{"/api/chats/" + chat.ID, "/api/chats/" + chat.ID + "/events"} {
		if w := apiRequest(a, "GET", path, "", cookie, csrf); w.Code != 404 {
			t.Fatalf("cross-user leak at %s: %d", path, w.Code)
		}
	}
	if w := apiRequest(a, "POST", "/api/chats", `{"project_id":"`+private.ID+`","backend":"codex"}`, cookie, csrf); w.Code != 403 {
		t.Fatal("ungranted project accepted")
	}
	if w := apiRequest(a, "GET", "/api/projects", "", nil, ""); w.Code != 401 {
		t.Fatal("anonymous access allowed")
	}
	if w := apiRequest(a, "POST", "/api/chats", `{}`, cookie, "invalid"); w.Code != 403 {
		t.Fatal("missing CSRF allowed")
	}
	for _, path := range []string{"/api/users", "/api/settings", "/api/projects/create", "/api/agents/manage"} {
		if w := apiRequest(a, "POST", path, `{}`, cookie, csrf); w.Code != 404 {
			t.Fatal("desktop management exposed remotely")
		}
	}
}

func TestSharedProjectChatReadAccessIsReadOnly(t *testing.T) {
	s := testStore(t)
	alice := testUser(t, s, "alice")
	bob := testUser(t, s, "bob")
	project := testProject(t, s, alice, bob)
	project.SharedChats = true
	project, _ = s.SaveProject(project)
	chat, _ := s.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, csrf := loginRequest(t, a, "bob")
	if w := apiRequest(a, "GET", "/api/chats/"+chat.ID, "", cookie, csrf); w.Code != 200 {
		t.Fatalf("shared chat was not readable: %d %s", w.Code, w.Body.String())
	}
	if w := apiRequest(a, "GET", "/api/chats?project_id="+project.ID, "", cookie, csrf); w.Code != 200 || !strings.Contains(w.Body.String(), chat.ID) {
		t.Fatalf("shared chat missing from history: %d %s", w.Code, w.Body.String())
	}
	if w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"not owner"}`, cookie, csrf); w.Code != 404 {
		t.Fatalf("shared member mutated chat: %d %s", w.Code, w.Body.String())
	}
}
func TestPasswordsHashedAndDisablingRevokesLogins(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, csrf := loginRequest(t, a, "alice")
	var hash string
	s.db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, u.ID).Scan(&hash)
	if !strings.HasPrefix(hash, "$2") || strings.Contains(hash, "test-password") {
		t.Fatal("password not hashed")
	}
	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if w := apiRequest(a, "GET", "/api/me", "", cookie, csrf); w.Code != 401 {
		t.Fatal("disabled login remained valid")
	}
}
func TestModelMismatchRejectedBeforeTurnAndClaudeNeedsAdvertisedEffort(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	testAgent(t, s)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	a := NewApp(s, &fakeRunner{}, "", "")
	cookie, csrf := loginRequest(t, a, "alice")
	w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"hello","model":"other-model","reasoning_effort":"xhigh"}`, cookie, csrf)
	saved, _ := s.Get(chat.ID)
	if w.Code != 400 || len(saved.Turns) != 0 {
		t.Fatal("unsupported level reached agent")
	}
	if err := a.validateSelection(context.Background(), chatmodel.Session{Backend: chatmodel.BackendClaude}, chatmodel.CodexOptions{Model: "test-model", ReasoningEffort: "high"}); err == nil {
		t.Fatal("unadvertised reasoning level allowed for Claude")
	}
}
func TestSSEReconnectIncludesCompletedHistory(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	testAgent(t, s)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	a := NewApp(s, &fakeRunner{text: "Reply"}, "", "")
	cookie, csrf := loginRequest(t, a, "alice")
	apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"hello"}`, cookie, csrf)
	waitChat(t, s, chat.ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
	r.AddCookie(cookie)
	stream := newStreamRecorder()
	go a.ServeHTTP(stream, r)
	var body strings.Builder
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !strings.Contains(body.String(), "\n\n") {
		select {
		case chunk := <-stream.writes:
			body.Write(chunk)
		case <-deadline.C:
			t.Fatal("missing SSE snapshot")
		}
	}
	cancel()
	if value := body.String(); !strings.Contains(value, "event: snapshot") || strings.Contains(value, "Reply") {
		t.Fatal(value)
	}
}
func TestPresenceDeduplicatesPeopleAndExpires(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := &Presence{}
	_, id, err := s.login(u.Username, "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	p.Touch(id, "client-111", true)
	p.Touch(id, "client-222", false)
	list := p.List(s)
	if len(list) != 1 || list[0].Devices != 2 || list[0].State != "online" {
		t.Fatalf("wrong presence %#v", list)
	}
	p.mu.Lock()
	for key, value := range p.clients {
		value.Seen = time.Now().Add(-2 * time.Minute)
		p.clients[key] = value
	}
	p.mu.Unlock()
	if len(p.List(s)) != 0 {
		t.Fatal("stale presence remained")
	}
}
func TestStaticPWAShellDoesNotExposeDatabase(t *testing.T) {
	s := testStore(t)
	a := NewApp(s, &fakeRunner{}, "", "")
	for _, path := range []string{"/", "/manifest.webmanifest", "/sw.js", "/icon-192.png", "/icon-512.png", "/icon-maskable-512.png", "/apple-touch-icon.png", "/favicon.png", "/logo.png", "/offline.html", "/offline.js"} {
		w := apiRequest(a, "GET", path, "", nil, "")
		if w.Code != 200 {
			t.Fatalf("missing %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/spool.sqlite", "/sessions.json", "/agents/x", "/api/not-found"} {
		if w := apiRequest(a, "GET", path, "", nil, ""); w.Code == 200 {
			t.Fatalf("private route exposed: %s", path)
		}
	}
	for _, name := range []string{"agents", "sessions.json"} {
		if _, err := os.Stat(filepath.Join(s.Root(), name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s", name)
		}
	}
}
func TestOriginAllowsConfiguredProxyButRejectsForeignSite(t *testing.T) {
	s := testStore(t)
	settings := DefaultSettings()
	settings.PublicURL = "https://chat.example.test"
	s.SaveSettings(settings)
	a := NewApp(s, &fakeRunner{}, "", "")
	for origin, want := range map[string]bool{"https://chat.example.test": true, "https://evil.example": false, "http://127.0.0.1:8787": true} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/login", nil)
		r.Header.Set("Origin", origin)
		if a.sameOrigin(r) != want {
			t.Fatalf("wrong origin decision: %s", origin)
		}
	}
}
