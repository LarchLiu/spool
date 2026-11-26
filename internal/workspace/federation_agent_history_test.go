package workspace

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func historyRemoteParts(t *testing.T) []chatmodel.Part {
	t.Helper()
	parts := []chatmodel.Part{}
	add := func(id, name string, input, output any, timed bool) {
		in, _ := json.Marshal(input)
		out, _ := json.Marshal(map[string]any{"result": output})
		text := string(out)
		if timed {
			text = "Wall time: 0.0061 seconds\nOutput:\n" + text
		}
		parts = append(parts, chatmodel.Part{ID: id, Role: "assistant", Kind: "tool_use", Name: name, Text: string(in)},
			chatmodel.Part{ID: id, Role: "tool", Kind: "tool_result", Text: text})
	}
	add("servers", "spool_servers", map[string]any{}, []map[string]string{{"id": "peer", "name": "Original PC"}}, true)
	add("projects", "spool_projects", map[string]string{"server": "peer"}, []map[string]string{{"id": "remote-project", "name": "Remote project"}}, true)
	task := remoteAgentTask{ID: "remote-task", RequestID: "request", Conversation: "conversation", Project: "remote-project", Backend: "codex", Status: "running", Text: "unfinished answer"}
	add("delegate", "mcp__spool_federation__spool_delegate", map[string]string{"server": "peer", "project": "remote-project", "message": "Inspect remote project"}, task, true)
	task.Status, task.Text, task.Model = "completed", "**Final answer**", "remote-model"
	add("task", "spool_task", map[string]string{"server": "peer", "task_id": task.ID}, task, true)
	task.ID, task.Status, task.Text = "newer-task", "awaiting_approval", "stale partial output"
	task.Progress = "private progress"
	task.Approval = &chatmodel.ApprovalRequest{ID: "stale-approval"}
	add("newer", "spool_delegate", map[string]string{"server": "peer", "project": "remote-project", "message": "Follow up"}, task, false)
	return parts
}

func TestRemoteCallsFromNativeHistoryRecoverFinalSnapshots(t *testing.T) {
	parts := historyRemoteParts(t)
	calls := remoteCallsFromHistory(chatmodel.Session{ID: "local-copy", Transcript: chatmodel.Transcript{Parts: parts}})
	if len(calls) != 2 || calls[0].Task.ID != "newer-task" || calls[1].Task.ID != "remote-task" {
		t.Fatalf("history did not deduplicate or retain newest-first order: %+v", calls)
	}
	for _, call := range calls {
		if !call.HistoryOnly || call.OriginChat != "local-copy" || call.ServerName != "Original PC" || call.Task.ProjectName != "Remote project" || call.Task.Progress != "" || call.Task.Approval != nil {
			t.Fatalf("history snapshot metadata or boundary incorrect: %+v", call)
		}
	}
	if calls[1].Message != "Inspect remote project" || calls[1].Task.Text != "**Final answer**" || calls[1].Task.Model != "remote-model" || calls[0].Task.Text != "" {
		t.Fatal("latest final result, original prompt or incomplete-output filtering lost")
	}
	if parts[len(parts)-1].Text == "" {
		t.Fatal("history extraction mutated native tool output")
	}
}

func TestRemoteHistoryEnvelopesAndBounds(t *testing.T) {
	task := remoteAgentTask{ID: "remote-task", Conversation: "conversation", Project: "remote-project", Backend: "claude", Status: "completed", Text: strings.Repeat("界", 4000)}
	for _, envelope := range []any{
		task,
		map[string]any{"result": task},
		map[string]any{"structuredContent": map[string]any{"result": task}},
		map[string]any{"content": []any{map[string]any{"type": "text", "text": func() string { data, _ := json.Marshal(task); return string(data) }()}}},
	} {
		data, _ := json.Marshal(envelope)
		parts := []chatmodel.Part{{ID: "call", Role: "assistant", Kind: "tool_use", Name: "spool_delegate", Text: `{"server":"peer","project":"remote-project"}`},
			{ID: "call", Role: "tool", Kind: "tool_result", Text: string(data)}}
		calls := remoteCallsFromHistory(chatmodel.Session{Transcript: chatmodel.Transcript{Parts: parts}})
		if len(calls) != 1 || len(calls[0].Task.Text) > 8196 || !calls[0].Task.Truncated {
			t.Fatal("native envelope was not decoded or bounded")
		}
		for _, invalid := range []string{`{"isError":true,"result":` + string(data) + `}`, "prose containing " + string(data), "{}", "null"} {
			parts[1].Text = invalid
			if got := remoteCallsFromHistory(chatmodel.Session{Transcript: chatmodel.Transcript{Parts: parts}}); len(got) != 0 {
				t.Fatal("unrecognized/error tool output created a remote call")
			}
		}
		parts[0].Role = "user"
		parts[1].Text = string(data)
		if got := remoteCallsFromHistory(chatmodel.Session{Transcript: chatmodel.Transcript{Parts: parts}}); len(got) != 0 {
			t.Fatal("user-provided text created a remote call")
		}
	}
}

func TestRemoteAgentDisplayCallsRecoverLocalHistoryWithoutImportingCapabilities(t *testing.T) {
	localSessionHomes(t)
	s := testStore(t)
	user := testUser(t, s, "viewer")
	project := testProject(t, s, user)
	project.SharedChats, project.SharedLocalChats = true, true
	if _, err := s.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendTitleRecord(t, path, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "Inspect project"}}}})
	for _, part := range historyRemoteParts(t) {
		item := map[string]any{"type": "function_call_output", "call_id": part.ID, "output": part.Text}
		if part.Kind == "tool_use" {
			item = map[string]any{"type": "function_call", "call_id": part.ID, "name": part.Name, "arguments": part.Text}
		}
		appendTitleRecord(t, path, map[string]any{"type": "response_item", "payload": item})
	}
	if _, err := s.db.Exec(`UPDATE sessions SET native_session_path=? WHERE id=?`, path, chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO local_sessions VALUES(?,1,?)`, chat.ID, project.Path); err != nil {
		t.Fatal(err)
	}
	app := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = app.Close() })
	_, identity, err := s.login(user.Username, "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	read := func(turn string) []remoteAgentCall {
		t.Helper()
		body, err := app.remoteAgent(t.Context(), identity, chat.ID, turn, remoteAgentOperation{federationAgentPayload: federationAgentPayload{Operation: "calls"}})
		if err != nil {
			t.Fatal(err)
		}
		var calls []remoteAgentCall
		if err := json.Unmarshal(body, &calls); err != nil {
			t.Fatal(err)
		}
		return calls
	}
	if calls := read(""); len(calls) != 2 || !calls[0].HistoryOnly || calls[1].Task.Text != "**Final answer**" {
		t.Fatalf("display did not recover native history without a trusted-server index: %+v", calls)
	}
	if calls := read("model-turn"); len(calls) != 0 {
		t.Fatal("historical snapshots became live MCP task references")
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM federation_agent_calls`).Scan(&count); err != nil || count != 0 {
		t.Fatal("display recovery wrote references into the live call index", err)
	}
	persisted := remoteAgentTask{ID: "remote-task", Conversation: "conversation", Project: "remote-project", Backend: "codex", Status: "stopped"}
	if err := app.saveRemoteAgentCall(user.ID, "peer", chat.ID, "live-request", "Live task", persisted); err != nil {
		t.Fatal(err)
	}
	if calls := read(""); len(calls) != 2 || calls[0].HistoryOnly || calls[0].Task.Status != "stopped" {
		t.Fatal("a historical snapshot replaced the live persisted reference")
	}
	outsider := testUser(t, s, "outsider")
	_, denied, _ := s.login(outsider.Username, "test-password-2026")
	if _, err := app.remoteAgent(t.Context(), denied, chat.ID, "", remoteAgentOperation{federationAgentPayload: federationAgentPayload{Operation: "calls"}}); err == nil {
		t.Fatal("history recovery bypassed parent chat access checks")
	}
}
