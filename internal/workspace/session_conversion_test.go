package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

// Persists minimal provider-owned records, unlike fakeRunner which deliberately
// has no history. This exercises conversion/resume without provider inference.
type historyRunner struct{ fakeRunner }

func (r *historyRunner) PermissionModes(_ context.Context, backend chatmodel.Backend, _ string) (chatmodel.PermissionCatalog, error) {
	mode := chatmodel.DefaultPermissionMode(backend)
	return chatmodel.PermissionCatalog{Backend: backend, Default: mode, Modes: []chatmodel.PermissionMode{{ID: mode, Available: true}}}, nil
}

func (r *historyRunner) Run(_ context.Context, request agent.AgentRequest, emit func(agent.AgentEvent) error) (agent.AgentResult, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	r.mu.Unlock()
	id, path := request.NativeSessionID, request.NativeSessionPath
	if id == "" {
		id = chatmodel.NewID()
	}
	if path == "" {
		if request.Backend == chatmodel.BackendCodex {
			path = filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "2026", "10", "28", "rollout-test-"+id+".jsonl")
		} else {
			path = filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "-"+strings.ReplaceAll(strings.TrimPrefix(request.WorkingDirectory, "/"), "/", "-"), id+".jsonl")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return agent.AgentResult{}, err
		}
	}
	parent := ""
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range bytes.Split(data, []byte("\n")) {
			var record map[string]any
			if json.Unmarshal(line, &record) == nil && record["uuid"] != nil {
				parent, _ = record["uuid"].(string)
			}
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return agent.AgentResult{}, err
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	reply := "Reply from " + string(request.Backend) + ": " + request.Prompt
	thought := "Private reasoning for " + request.Prompt
	turnID := chatmodel.NewID()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if request.Backend == chatmodel.BackendCodex {
		_ = encoder.Encode(map[string]any{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": turnID}})
		_ = encoder.Encode(map[string]any{"timestamp": stamp, "type": "turn_context", "payload": map[string]any{"turn_id": turnID, "model": request.Model}})
		for _, payload := range []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": request.Prompt}}},
			map[string]any{"type": "reasoning", "summary": []any{map[string]any{"text": thought}}},
			map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": reply}}},
		} {
			_ = encoder.Encode(map[string]any{"timestamp": stamp, "type": "response_item", "payload": payload})
		}
		_ = encoder.Encode(map[string]any{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": turnID}})
	} else {
		userID := turnID
		_ = encoder.Encode(map[string]any{"timestamp": stamp, "promptId": turnID, "uuid": userID, "parentUuid": parent, "sessionId": id, "type": "user", "message": map[string]any{"role": "user", "content": request.Prompt}})
		_ = encoder.Encode(map[string]any{"timestamp": stamp, "uuid": chatmodel.NewID(), "parentUuid": userID, "sessionId": id, "type": "assistant", "message": map[string]any{"role": "assistant", "model": request.Model, "stop_reason": "end_turn", "content": []any{map[string]any{"type": "thinking", "thinking": thought}, map[string]any{"type": "text", "text": reply}}}})
	}
	if err := f.Sync(); err != nil {
		return agent.AgentResult{}, err
	}
	if err := emit(agent.AgentEvent{Type: "session", SessionID: id}); err != nil {
		return agent.AgentResult{}, err
	}
	return agent.AgentResult{TurnID: turnID, SessionID: id, Model: request.Model, Text: reply}, nil
}

func TestAgentSwitchCreatesIndependentChatsWithoutSourceLinks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	store := testStore(t)
	u, bob := testUser(t, store, "alice"), testUser(t, store, "bob")
	project := testProject(t, store, u, bob)
	project.SharedChats = true
	if _, err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	testAgent(t, store)
	_ = store.saveAgent(AgentInfo{Backend: chatmodel.BackendClaude, Enabled: true, Installed: true})
	runner := &historyRunner{}
	app := NewApp(store, runner, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, "alice")
	original, _ := store.CreateChat(u.ID, project.ID, chatmodel.BackendCodex)
	send := func(id string, backend chatmodel.Backend, prompt string) chatmodel.Session {
		t.Helper()
		model := "test-model"
		if backend == chatmodel.BackendClaude {
			model = "test-claude"
		}
		payload, _ := json.Marshal(map[string]string{"backend": string(backend), "model": model, "message": prompt})
		w := apiRequest(app, "POST", "/api/chats/"+id+"/messages", string(payload), cookie, csrf)
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		var accepted chatmodel.Session
		if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		return waitChat(t, store, accepted.ID)
	}
	a := send(original.ID, chatmodel.BackendCodex, "message-A")
	if len(a.Turns) != 1 || a.Turns[0].Prompt != "message-A" {
		t.Fatalf("native history missing: %+v", a)
	}
	before, _ := os.ReadFile(a.NativeSessionPath)
	b := send(a.ID, chatmodel.BackendClaude, "message-B")
	if b.ID == a.ID || b.NativeSessionID == a.NativeSessionID || b.Backend != chatmodel.BackendClaude {
		t.Fatal("switch reused original chat/session")
	}
	after, _ := os.ReadFile(a.NativeSessionPath)
	if !bytes.Equal(before, after) {
		t.Fatal("conversion modified original native file")
	}
	unchanged, _ := store.Get(a.ID)
	if unchanged.Backend != chatmodel.BackendCodex || unchanged.NativeSessionID != a.NativeSessionID || len(unchanged.Turns) != 1 {
		t.Fatal("original chat mutated")
	}
	if len(b.Turns) != 2 || b.Turns[0].Prompt != "message-A" || b.Turns[1].Prompt != "message-B" {
		t.Fatalf("converted context missing/duplicated: %+v", b.Turns)
	}
	for _, part := range b.Turns[0].Parts {
		if part.Kind == "thinking" {
			t.Fatal("old reasoning imported into another provider")
		}
	}
	a = send(a.ID, chatmodel.BackendCodex, "original-only")
	b = send(b.ID, chatmodel.BackendClaude, "branch-only")
	c := send(b.ID, chatmodel.BackendCodex, "message-C")
	d := send(c.ID, chatmodel.BackendClaude, "message-D")
	if c.ID == a.ID || c.ID == b.ID || d.ID == b.ID || d.ID == c.ID {
		t.Fatal("repeated switch reused an earlier chat")
	}
	for _, chat := range []chatmodel.Session{b, c, d} {
		for _, turn := range chat.Turns {
			if turn.Prompt == "original-only" {
				t.Fatal("later original messages leaked into converted chat")
			}
		}
	}
	if len(a.Turns) != 2 || len(b.Turns) != 3 || len(c.Turns) != 4 || len(d.Turns) != 5 {
		t.Fatal("independent histories have wrong length", len(a.Turns), len(b.Turns), len(c.Turns), len(d.Turns))
	}
	// The target's native file alone is sufficient after restart.
	reopened, err := OpenStore(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, _ := reopened.Get(d.ID)
	if len(saved.Turns) != 5 || saved.Turns[0].Prompt != "message-A" || saved.Turns[4].Prompt != "message-D" {
		t.Fatal("native-only reload lost context")
	}
	assertNoTurnTables(t, reopened)
	bobCookie, bobCSRF := loginRequest(t, app, "bob")
	w := apiRequest(app, "POST", "/api/chats/"+a.ID+"/fork", `{"backend":"claude"}`, bobCookie, bobCSRF)
	if w.Code != 404 {
		t.Fatal("shared viewer forked another owner's chat")
	}
	w = apiRequest(app, "GET", "/api/chats/"+d.ID, "", cookie, csrf)
	if strings.Contains(w.Body.String(), root) || strings.Contains(w.Body.String(), "native_session") {
		t.Fatal("native paths leaked to web")
	}
	runner.mu.Lock()
	requests := len(runner.requests)
	runner.mu.Unlock()
	w = apiRequest(app, "POST", "/api/chats/"+d.ID+"/fork", `{"backend":"codex","model":"test-model"}`, cookie, csrf)
	if w.Code != 201 {
		t.Fatal("explicit agent switch failed", w.Code, w.Body.String())
	}
	var converted chatmodel.Session
	if err := json.Unmarshal(w.Body.Bytes(), &converted); err != nil {
		t.Fatal(err)
	}
	if converted.ID == d.ID || converted.Status != chatmodel.StatusReady || len(converted.Turns) != len(d.Turns) {
		t.Fatal("explicit switch did not create an independent ready chat")
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.requests) != requests {
		t.Fatal("switching agents submitted an inference request")
	}
}

func TestConvertedSessionRollbackRemovesOnlyNewNativeFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	store := testStore(t)
	u := testUser(t, store, "alice")
	project := testProject(t, store, u)
	chat, _ := store.CreateChat(u.ID, project.ID, chatmodel.BackendCodex)
	source, _ := filepath.Abs("../agent/testdata/carry/codex-tools.jsonl")
	_ = store.SetNativeSessionPath(chat.ID, "original-session", source)
	before, _ := os.ReadFile(source)
	_, err := store.db.Exec(`CREATE TRIGGER reject_conversion BEFORE INSERT ON sessions BEGIN SELECT RAISE(ABORT,'fixture write failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.forkChat(chat.ID, chatmodel.BackendClaude, chatmodel.CodexOptions{}); err == nil {
		t.Fatal("expected transaction failure")
	}
	saved, _ := store.Get(chat.ID)
	if saved.Backend != chatmodel.BackendCodex || saved.NativeSessionID != "original-session" {
		t.Fatal("failed conversion changed original")
	}
	files, _ := filepath.Glob(filepath.Join(root, "claude", "projects", "*", "*.jsonl"))
	if len(files) != 0 {
		t.Fatal("failed conversion left a new native file")
	}
	after, _ := os.ReadFile(source)
	if !bytes.Equal(before, after) {
		t.Fatal("rollback modified source")
	}
}
