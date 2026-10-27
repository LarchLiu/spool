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
		id = "11111111-1111-4111-8111-111111111111"
		path = filepath.Join(request.WorkingDirectory, string(request.Backend)+"-"+id+".jsonl")
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
	if request.Backend == chatmodel.BackendCodex {
		_ = encoder.Encode(map[string]any{"type": "turn_context", "payload": map[string]any{"model": request.Model}})
		for _, payload := range []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": request.Prompt}}},
			map[string]any{"type": "reasoning", "summary": []any{map[string]any{"text": thought}}},
			map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": reply}}},
		} {
			_ = encoder.Encode(map[string]any{"type": "response_item", "payload": payload})
		}
	} else {
		userID := chatmodel.NewID()
		_ = encoder.Encode(map[string]any{"uuid": userID, "parentUuid": parent, "sessionId": id, "type": "user", "message": map[string]any{"role": "user", "content": request.Prompt}})
		_ = encoder.Encode(map[string]any{"uuid": chatmodel.NewID(), "parentUuid": userID, "sessionId": id, "type": "assistant", "message": map[string]any{"role": "assistant", "model": request.Model, "content": []any{map[string]any{"type": "thinking", "thinking": thought}, map[string]any{"type": "text", "text": reply}}}})
	}
	if err := emit(agent.AgentEvent{Type: "session", SessionID: id}); err != nil {
		return agent.AgentResult{}, err
	}
	return agent.AgentResult{SessionID: id, Model: request.Model, Text: reply}, nil
}

func TestChatConvertsRepeatedlyAndReopensOriginalHistory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	store := testStore(t)
	u := testUser(t, store, "alice")
	bob := testUser(t, store, "bob")
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
	chat, err := store.CreateChat(u.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	backends := []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendCodex, chatmodel.BackendClaude, chatmodel.BackendClaude, chatmodel.BackendCodex, chatmodel.BackendClaude}
	for i, backend := range backends {
		prompt := "message-" + string(rune('A'+i))
		model := "test-model"
		permission := "ask"
		if backend == chatmodel.BackendClaude {
			model, permission = "test-claude", "acceptEdits"
		}
		w := apiRequest(app, "GET", "/api/permissions?project_id="+project.ID+"&chat_id="+chat.ID+"&backend="+string(backend), "", cookie, csrf)
		if w.Code != 200 {
			t.Fatal("target permissions unavailable", w.Code, w.Body.String())
		}
		payload, _ := json.Marshal(map[string]string{"backend": string(backend), "model": model, "permission_mode": permission, "message": prompt})
		w = apiRequest(app, "POST", "/api/chats/"+chat.ID+"/messages", string(payload), cookie, csrf)
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		saved := waitChat(t, store, chat.ID)
		runner.mu.Lock()
		request := runner.requests[i]
		runner.mu.Unlock()
		if request.NativeSessionID == "" && i > 0 {
			t.Fatal("continuation lost its native context")
		}
		// The fixture's first file is outside the normal provider directory;
		// production locates this from the CLI's native ID after completion.
		if i == 0 {
			path := filepath.Join(project.Path, "codex-"+saved.NativeSessionID+".jsonl")
			if err := store.SetNativeSessionPath(chat.ID, saved.NativeSessionID, path); err != nil {
				t.Fatal(err)
			}
			saved, _ = store.Get(chat.ID)
		}
		if saved.ID != chat.ID || saved.Backend != backend || len(saved.Turns) != i+1 || saved.PermissionMode != permission {
			t.Fatalf("wrong conversation selection: %+v", saved)
		}
		for j, turn := range saved.Turns {
			want := "message-" + string(rune('A'+j))
			if turn.Prompt != want || turn.Backend != backends[j] || turn.Reply != "Reply from "+string(backends[j])+": "+want {
				t.Fatalf("history duplicated, lost, or attributed to another agent at %d: %+v", j, turn)
			}
			foundThinking := false
			for _, part := range turn.Parts {
				foundThinking = foundThinking || part.Kind == "thinking"
			}
			if !foundThinking {
				t.Fatal("Web lost original reasoning after handoff", j)
			}
		}
		if i > 0 && backend != backends[i-1] {
			data, _ := os.ReadFile(request.NativeSessionPath)
			if strings.Contains(string(data), "Private reasoning for message-"+string(rune('A'+i-1))) {
				t.Fatal("provider reasoning was imported into target context")
			}
		}
	}
	// Reopening a running converted chat must retain earlier ranges as well
	// as text; otherwise the Web transcript exists but prior turns look empty.
	previous, _ := store.Get(chat.ID)
	gate := &gatedRunner{started: make(chan struct{}), release: make(chan struct{})}
	liveApp := NewApp(store, gate, "", "")
	t.Cleanup(func() { liveApp.Close() })
	previous.Backend = chatmodel.BackendCodex
	if err := liveApp.startTurnWithInput(previous, messageInput{Message: "message-G"}, chatmodel.CodexOptions{Model: "test-model"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.started:
	case <-time.After(2 * time.Second):
		t.Fatal("converted turn never started")
	}
	running, _ := store.Get(chat.ID)
	running = liveApp.snapshot(running)
	if running.Turns[0].PartEnd == 0 || running.Transcript.Parts[0].Text != "message-A" {
		t.Fatal("reconnecting during handoff lost earlier turn ranges")
	}
	// Reject a delayed session event from a superseded turn.
	if err := store.setTurnNativeSession(chat.ID, running.Turns[0].ID, "obsolete-session"); err == nil {
		t.Fatal("old provider event replaced a new agent context")
	}
	close(gate.release)
	waitChat(t, store, chat.ID)
	w := apiRequest(app, "GET", "/api/chats/"+chat.ID, "", cookie, csrf)
	if strings.Contains(w.Body.String(), root) || strings.Contains(w.Body.String(), "native_session") {
		t.Fatal("native session details leaked to Web")
	}
	bobCookie, bobCSRF := loginRequest(t, app, "bob")
	w = apiRequest(app, "POST", "/api/chats/"+chat.ID+"/messages", `{"backend":"codex","message":"not mine"}`, bobCookie, bobCSRF)
	if w.Code != 404 {
		t.Fatal("shared viewer converted another owner's chat")
	}
	reopened, err := OpenStore(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, _ := reopened.Get(chat.ID)
	if len(saved.Turns) != len(backends)+1 || saved.Turns[0].Prompt != "message-A" || saved.Turns[len(backends)-1].Prompt != "message-F" {
		t.Fatalf("conversion history lost after restart: %+v", saved)
	}
}

func TestConvertedSessionRollbackRemovesOnlyNewNativeFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	store := testStore(t)
	u := testUser(t, store, "alice")
	project := testProject(t, store, u)
	chat, _ := store.CreateChat(u.ID, project.ID, chatmodel.BackendCodex)
	turn, _ := store.BeginTurn(chat.ID, "first")
	source, _ := filepath.Abs("../agent/testdata/carry/codex-tools.jsonl")
	_ = store.SetNativeSessionPath(chat.ID, "original-session", source)
	_ = store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{}, nil, chatmodel.StatusCompleted)
	before, _ := os.ReadFile(source)
	_, _ = store.db.Exec(`CREATE TRIGGER reject_conversion BEFORE INSERT ON turns BEGIN SELECT RAISE(ABORT,'fixture write failure'); END`)
	if _, err := store.beginTurn(chat.ID, "continue", chatmodel.BackendClaude); err == nil {
		t.Fatal("expected transaction failure")
	}
	saved, _ := store.Get(chat.ID)
	if saved.Backend != chatmodel.BackendCodex || saved.NativeSessionID != "original-session" || len(saved.Turns) != 1 {
		t.Fatal("failed conversion changed the original chat")
	}
	var count int
	_ = store.db.QueryRow(`SELECT count(*) FROM session_history WHERE session_id=?`, chat.ID).Scan(&count)
	if count != 0 {
		t.Fatal("failed conversion retained partial history references")
	}
	files, _ := filepath.Glob(filepath.Join(root, "claude", "projects", "*", "*.jsonl"))
	if len(files) != 0 {
		t.Fatal("failed transaction left a converted session")
	}
	after, _ := os.ReadFile(source)
	if !bytes.Equal(before, after) {
		t.Fatal("rollback modified the source")
	}
}

func TestConvertedHistoryKeepsGlobalTranscriptBounds(t *testing.T) {
	store := testStore(t)
	root := t.TempDir()
	chat := chatmodel.Session{ID: "bounded-conversation", Backend: chatmodel.BackendCodex}
	var histories []nativeHistory
	for segment := 0; segment < 2; segment++ {
		path := filepath.Join(root, string(rune('a'+segment))+".jsonl")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		encoder := json.NewEncoder(f)
		for turn := 0; turn < 15; turn++ {
			chat.Turns = append(chat.Turns, chatmodel.Turn{ID: chatmodel.NewID(), Backend: chatmodel.BackendCodex})
			for _, role := range []string{"user", "assistant"} {
				_ = encoder.Encode(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": "text", "text": strings.Repeat("x", 8000)}}}})
			}
		}
		f.Close()
		histories = append(histories, nativeHistory{start: segment * 15, backend: chatmodel.BackendCodex, path: path})
		chat.NativeSessionID, chat.NativeSessionPath = "bounded-native-id", path
	}
	merged := store.hydrateConvertedSession(chat, histories, root)
	size := 0
	for _, part := range merged.Transcript.Parts {
		size += len(part.Text)
	}
	if !merged.Transcript.Cut || size > 400_000 {
		t.Fatal("multiple native segments bypassed the shared transcript limit", size)
	}
	for _, turn := range merged.Turns {
		if turn.PartStart < 0 || turn.InputEnd < turn.PartStart || turn.PartEnd < turn.InputEnd || turn.PartEnd > len(merged.Transcript.Parts) {
			t.Fatal("truncation left invalid turn ranges", turn)
		}
	}
}
