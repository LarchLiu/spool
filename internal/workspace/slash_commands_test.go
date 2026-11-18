package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestNativeCommandEndpointIsAuthorizedAndDoesNotCreateTurns(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	p := testProject(t, s, alice, bob)
	testAgent(t, s)
	chat, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	runner := &fakeRunner{text: "native output"}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	bobCookie, bobCSRF := loginRequest(t, a, "bob")
	path := "/api/chats/" + chat.ID + "/commands"
	for _, tt := range []struct {
		command string
		code    int
	}{{"/unknown", 400}, {"/review", 400}, {"/goal", 400}, {"/plan work", 400}} {
		if w := apiRequest(a, "POST", path, `{"command":"`+tt.command+`"}`, cookie, csrf); w.Code != tt.code {
			t.Fatalf("%s: %d %s", tt.command, w.Code, w.Body.String())
		}
	}
	if w := apiRequest(a, "POST", path, `{"command":"/plan"}`, nil, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := apiRequest(a, "POST", path, `{"command":"/plan"}`, cookie, "wrong"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := apiRequest(a, "POST", path, `{"command":"/plan"}`, bobCookie, bobCSRF); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w := apiRequest(a, "POST", path, `{"command":"/plan","model":"chosen-model","reasoning_effort":"high","permission_mode":"ask"}`, cookie, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "native output") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	saved, _ := s.Get(chat.ID)
	if len(saved.Turns) != 0 || saved.NativeSessionID != "native-session-123" || saved.Model != "chosen-model" || saved.ReasoningEffort != "high" || saved.UpdatedAt != chat.UpdatedAt {
		t.Fatalf("control changed history or lost settings: %#v", saved)
	}
	if len(runner.requests) != 1 || runner.requests[0].Prompt != "/plan" {
		t.Fatal(runner.requests)
	}
	if _, err := s.BeginTurn(chat.ID, "running"); err != nil {
		t.Fatal(err)
	}
	if w := apiRequest(a, "POST", path, `{"command":"/plan"}`, cookie, csrf); w.Code != 409 {
		t.Fatal(w.Code)
	}
}

func TestNativeCommandBlocksTurnsAndShutdownCancelsCommand(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	testAgent(t, s)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	runner := &gatedRunner{started: make(chan struct{}), release: make(chan struct{})}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	path := "/api/chats/" + chat.ID
	finished := make(chan int, 1)
	go func() {
		finished <- apiRequest(a, "POST", path+"/commands", `{"command":"/plan"}`, cookie, csrf).Code
	}()
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("command did not start")
	}
	for _, endpoint := range []string{"commands", "messages", "goal"} {
		payload := map[string]string{"commands": `{"command":"/plan"}`, "messages": `{"message":"work"}`, "goal": `{"action":"clear"}`}[endpoint]
		if w := apiRequest(a, "POST", path+"/"+endpoint, payload, cookie, csrf); w.Code != 409 {
			t.Fatalf("overlapping %s: %d %s", endpoint, w.Code, w.Body.String())
		}
	}
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel command")
	}
	if code := <-finished; code != 409 {
		t.Fatal(code)
	}
	saved, _ := s.Get(chat.ID)
	if len(saved.Turns) != 0 {
		t.Fatal("command created a conversation turn")
	}
}

func TestSlashCommandsCannotBeQueuedOrSteeredDuringReply(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	testAgent(t, s)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	runner := &gatedRunner{started: make(chan struct{}), release: make(chan struct{})}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	path := "/api/chats/" + chat.ID + "/messages"
	if w := apiRequest(a, "POST", path, `{"message":"work"}`, cookie, csrf); w.Code != 202 {
		t.Fatal(w.Code)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("reply did not start")
	}
	for _, delivery := range []string{"queue", "steer"} {
		w := apiRequest(a, "POST", path, `{"message":"/review main","delivery":"`+delivery+`"}`, cookie, csrf)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "Wait for the running response") {
			t.Fatalf("%s: %d %s", delivery, w.Code, w.Body.String())
		}
	}
	close(runner.release)
}

func TestNativeForkCreatesIndependentSessionWithoutInference(t *testing.T) {
	s := testStore(t)
	u, other := testUser(t, s, "alice"), testUser(t, s, "bob")
	p := testProject(t, s, u, other)
	fixture := newAgentFixture(t, chatmodel.BackendCodex)
	if err := s.saveAgent(AgentInfo{Backend: chatmodel.BackendCodex, Enabled: true, Installed: true, Executable: fixture.Binary}); err != nil {
		t.Fatal(err)
	}
	runner := newTestRunner(t)
	runner.ConfigureBinaries(agentBinaries(mustAgents(t, s)))
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	if err := s.SetNativeSession(chat.ID, "native-session-123"); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := loginRequest(t, a, "alice")
	bobCookie, bobCSRF := loginRequest(t, a, "bob")
	path, payload := "/api/chats/"+chat.ID+"/fork", `{"backend":"codex","model":"other-model"}`
	if w := apiRequest(a, "POST", path, payload, bobCookie, bobCSRF); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w := apiRequest(a, "POST", path, payload, cookie, csrf)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var created chatmodel.Session
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	fork, ok := s.Get(created.ID)
	if !ok || fork.ID == chat.ID || fork.NativeSessionID == "" || fork.NativeSessionID == "native-session-123" || fork.Model != "other-model" || fork.ProjectID != p.ID || len(fork.Turns) != 0 {
		t.Fatalf("invalid fork: %#v", fork)
	}
	source, _ := s.Get(chat.ID)
	if source.NativeSessionID != "native-session-123" || source.Model != chat.Model || source.UpdatedAt != chat.UpdatedAt {
		t.Fatal("fork modified source")
	}
	if fixtureCount(t, fixture, "thread/fork") != 1 || fixtureCount(t, fixture, "turn/start") != 0 {
		t.Fatal("fork started inference")
	}
}

func TestNativeCommandModelAndPlanSettingsPersist(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			s := testStore(t)
			u := testUser(t, s, "alice")
			p := testProject(t, s, u)
			fixture := newAgentFixture(t, backend)
			if err := s.saveAgent(AgentInfo{Backend: backend, Enabled: true, Installed: true, Executable: fixture.Binary}); err != nil {
				t.Fatal(err)
			}
			// The fixture launcher changes into its private project before running.
			runner := newTestRunner(t)
			runner.ConfigureBinaries(agentBinaries(mustAgents(t, s)))
			a := NewApp(s, runner, "", "")
			t.Cleanup(func() { _ = a.Close() })
			chat, _ := s.CreateChat(u.ID, p.ID, backend)
			cookie, csrf := loginRequest(t, a, "alice")
			path := "/api/chats/" + chat.ID + "/commands"
			command := `{"command":"/plan","model":"other-model"}`
			if backend == chatmodel.BackendClaude {
				command = `{"command":"/model other-model"}`
			}
			if w := apiRequest(a, "POST", path, command, cookie, csrf); w.Code != 200 {
				t.Fatalf("model: %d %s", w.Code, w.Body.String())
			}
			saved, _ := s.Get(chat.ID)
			if saved.Model != "other-model" || len(saved.Turns) != 0 {
				t.Fatal(saved)
			}
			if w := apiRequest(a, "POST", path, `{"command":"/plan"}`, cookie, csrf); w.Code != 200 {
				t.Fatalf("plan: %d %s", w.Code, w.Body.String())
			}
			saved, _ = s.Get(chat.ID)
			if backend == chatmodel.BackendClaude && saved.PermissionMode != "plan" {
				t.Fatal(saved.PermissionMode)
			}
			if fixtureCount(t, fixture, "turn/start") != 0 {
				t.Fatal("control started inference")
			}
		})
	}
}

func mustAgents(t *testing.T, s *Store) []AgentInfo {
	t.Helper()
	agents, err := s.Agents()
	if err != nil {
		t.Fatal(err)
	}
	return agents
}
