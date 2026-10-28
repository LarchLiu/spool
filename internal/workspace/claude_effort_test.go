package workspace

import (
	"encoding/json"
	"testing"

	chatmodel "spool/internal/model"
)

func TestClaudeEffortAPIValidatesCatalogAndPersistsSelections(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	if err := s.saveAgent(AgentInfo{Backend: chatmodel.BackendClaude, Enabled: true, Installed: true, Catalog: chatmodel.CodexCatalog{Models: []chatmodel.CodexModel{
		{Model: "test-claude", Efforts: []chatmodel.ReasoningOption{{Effort: "low"}, {Effort: "high"}}},
		{Model: "fixed-claude"},
	}}}); err != nil {
		t.Fatal(err)
	}
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendClaude)
	runner := &fakeRunner{text: "Effort reply"}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	for _, options := range []chatmodel.CodexOptions{
		{Model: "test-claude", ReasoningEffort: "xhigh"},
		{Model: "test-claude", ReasoningEffort: "ultra"},
		{Model: "fixed-claude", ReasoningEffort: "high"},
		{Model: "unknown-claude", ReasoningEffort: "high"},
	} {
		body, _ := json.Marshal(map[string]string{"message": "Rejected", "model": options.Model, "reasoning_effort": options.ReasoningEffort})
		w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", string(body), cookie, csrf)
		saved, _ := s.Get(chat.ID)
		if w.Code != 400 || len(saved.Turns) != 0 || len(runner.requests) != 0 {
			t.Fatalf("unsupported choice created a turn: %s", w.Body.String())
		}
	}
	for i, effort := range []string{"low", "high", ""} {
		body, _ := json.Marshal(map[string]string{"message": "Effort selection", "model": "test-claude", "reasoning_effort": effort})
		w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", string(body), cookie, csrf)
		if w.Code != 202 {
			t.Fatal(w.Body.String())
		}
		saved := waitChat(t, s, chat.ID)
		if saved.ReasoningEffort != effort || saved.Turns[len(saved.Turns)-1].ReasoningEffort != effort {
			t.Fatalf("effort not saved: %#v", saved)
		}
		runner.mu.Lock()
		request := runner.requests[i]
		runner.mu.Unlock()
		if request.ReasoningEffort != effort {
			t.Fatalf("runner did not receive effort: %#v", request)
		}
	}
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, _ := reopened.Get(chat.ID)
	if len(saved.Turns) != 0 || saved.ReasoningEffort != "" || saved.Model != "test-claude" {
		t.Fatal("SQLite must retain the current selection, not per-turn effort history")
	}
}
