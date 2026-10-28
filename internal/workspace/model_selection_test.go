package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
	"spool/internal/testutil/agentfixture"
)

type selectionRunner struct{ fakeRunner }

func (r *selectionRunner) Run(_ context.Context, request agent.AgentRequest, emit func(agent.AgentEvent) error) (agent.AgentResult, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	r.mu.Unlock()
	id := "native-" + string(request.Backend)
	if err := emit(agent.AgentEvent{Type: "session", SessionID: id}); err != nil {
		return agent.AgentResult{}, err
	}
	return agent.AgentResult{SessionID: id, Model: request.Model, Text: "Selected agent reply"}, nil
}

func TestGroupedModelSelectionAndMissingHistoryGuard(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	testAgent(t, s)
	if err := s.saveAgent(AgentInfo{Backend: chatmodel.BackendClaude, Enabled: true, Installed: true}); err != nil {
		t.Fatal(err)
	}
	runner := &selectionRunner{}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	w := apiRequest(a, "POST", "/api/chats", `{"project_id":"`+p.ID+`"}`, cookie, csrf)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var chat chatmodel.Session
	json.Unmarshal(w.Body.Bytes(), &chat)
	if len(runner.requests) != 0 {
		t.Fatal("creating a chat started an agent before model selection")
	}
	for i, model := range []string{"test-claude", "other-claude"} {
		backend := chatmodel.BackendClaude
		body, _ := json.Marshal(map[string]string{"message": "Exact raw input", "backend": string(backend), "model": model})
		w = apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", string(body), cookie, csrf)
		if w.Code != 202 {
			t.Fatal(w.Body.String())
		}
		saved := waitChat(t, s, chat.ID)
		if saved.Backend != backend || saved.Model != model || len(saved.Turns) != 1 || saved.Turns[0].Backend != backend {
			t.Fatalf("selection was not saved: %#v", saved)
		}
		runner.mu.Lock()
		request := runner.requests[i]
		runner.mu.Unlock()
		wantNative := ""
		if i > 0 {
			wantNative = "native-" + string(backend)
		}
		if request.Backend != backend || request.Model != model || request.NativeSessionID != wantNative || request.Prompt != "Exact raw input" || request.WorkingDirectory != p.Path {
			t.Fatalf("wrong agent or context: %#v", request)
		}
	}
	w = apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"Cross-agent rejected","backend":"codex","model":"test-model"}`, cookie, csrf)
	saved, _ := s.Get(chat.ID)
	if w.Code != 409 || saved.Backend != chatmodel.BackendClaude || len(saved.Turns) != 1 {
		t.Fatal("chat switched agents after the first message")
	}
	s.ConfigureAgent(chatmodel.BackendClaude, false, "")
	w = apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"Rejected","backend":"claude","model":"test-claude"}`, cookie, csrf)
	saved, _ = s.Get(chat.ID)
	if w.Code != 409 || saved.Backend != chatmodel.BackendClaude || len(saved.Turns) != 1 {
		t.Fatal("disabled agent accepted another message")
	}
	w = apiRequest(a, "GET", "/api/chats/"+chat.ID, "", cookie, csrf)
	if strings.Contains(w.Body.String(), "native-") {
		t.Fatal("provider contexts leaked to Web")
	}
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = reopened.beginTurn(chat.ID, "After restart", chatmodel.BackendCodex, chatmodel.CodexOptions{Model: "test-model"}); !errors.Is(err, errSessionConversion) {
		t.Fatal("switch without native history was accepted after reopening SQLite")
	}
	resumed, _ := reopened.Get(chat.ID)
	if resumed.NativeSessionID != "native-claude" {
		t.Fatal("provider resume context changed after rejected switch")
	}
}

func TestAgentSwitchRollbackAndRunningGuard(t *testing.T) {
	s := testStore(t)
	chat, _ := s.createSession("", "", chatmodel.BackendCodex)
	s.SetNativeSession(chat.ID, "codex-native")
	s.db.Exec(`CREATE TRIGGER reject_switch BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT,'fixture write failure'); END`)
	if _, err := s.beginTurn(chat.ID, "Switch", chatmodel.BackendClaude, chatmodel.CodexOptions{Model: "test-claude"}); err == nil {
		t.Fatal("expected insert failure")
	}
	saved, _ := s.Get(chat.ID)
	if saved.Backend != chatmodel.BackendCodex || saved.NativeSessionID != "codex-native" {
		t.Fatal("failed switch partially committed")
	}
	s.db.Exec(`DROP TRIGGER reject_switch`)
	if _, err := s.BeginTurn(chat.ID, "Working"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.beginTurn(chat.ID, "Switch", chatmodel.BackendClaude, chatmodel.CodexOptions{}); err == nil {
		t.Fatal("switched agents during a running turn")
	}
}

func TestFailedTurnWithoutNativeHistoryCannotBeConverted(t *testing.T) {
	s := testStore(t)
	chat, _ := s.createSession("", "", chatmodel.BackendCodex)
	turn, err := s.beginTurn(chat.ID, "First message", chatmodel.BackendClaude, chatmodel.CodexOptions{Model: "test-claude"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTurn(chat.ID, turn.ID, agent.AgentResult{}, errors.New("fixture failure"), chatmodel.StatusFailed); err != nil {
		t.Fatal(err)
	}
	if _, err = s.beginTurn(chat.ID, "Try another agent", chatmodel.BackendCodex, chatmodel.CodexOptions{}); !errors.Is(err, errSessionConversion) {
		t.Fatal("a failed task with no history was converted")
	}
	if _, err = s.beginTurn(chat.ID, "Try another model", chatmodel.BackendClaude, chatmodel.CodexOptions{Model: "other-claude"}); err != nil {
		t.Fatal("agent could not change its own model:", err)
	}
}

func TestClaudeModelSelectionUsesPersistentControlChannel(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	runner := newTestRunner(t)
	for _, model := range []string{"test-claude", "other-claude", "other-claude", ""} {
		request.Model = model
		result, _ := runFixture(t, runner, request)
		if model != "" && result.Model != model {
			t.Fatalf("Claude used model %q, want %q", result.Model, model)
		}
		request.NativeSessionID = result.SessionID
	}
	if fixtureCount(t, request, "spawn") != 1 || fixtureCount(t, request, "user") != 4 {
		t.Fatal("changing Claude model restarted the CLI or sent extra messages")
	}
	var changes []any
	for _, entry := range fixtureLog(t, request) {
		value, _ := entry["value"].(map[string]any)
		if agentfixture.NestedString(value, "request", "subtype") == "set_model" {
			control, _ := value["request"].(map[string]any)
			changes = append(changes, control["model"])
		}
	}
	if len(changes) != 3 || changes[0] != "test-claude" || changes[1] != "other-claude" || changes[2] != nil {
		t.Fatalf("wrong Claude model control requests: %#v", changes)
	}
}

func TestClaudeRejectedModelNeverSendsUserMessage(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	request.Model = "reject-model"
	_, err := newTestRunner(t).Run(context.Background(), request, nil)
	if err == nil || !strings.Contains(err.Error(), "model selection failed") {
		t.Fatalf("model rejection was not reported: %v", err)
	}
	if fixtureCount(t, request, "user") != 0 {
		t.Fatal("message was sent using an unselected model")
	}
}
