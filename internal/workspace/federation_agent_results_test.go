package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestRemoteAgentFinalAnswerBoundary(t *testing.T) {
	text := func(id, phase, value string) chatmodel.Part {
		return chatmodel.Part{ID: id, Role: "assistant", Kind: "text", Phase: phase, Text: value}
	}
	tool := chatmodel.Part{Role: "tool", Kind: "tool_result", Text: "private tool output"}
	for _, tc := range []struct {
		name  string
		parts []chatmodel.Part
		want  string
	}{
		{"explicit", []chatmodel.Part{text("progress", "commentary", "working"), tool, text("answer", "final_answer", "Final"), text("answer", "final_answer", " answer")}, "Final answer"},
		{"legacy", []chatmodel.Part{text("progress", "", "working"), tool, text("answer", "", "Final answer")}, "Final answer"},
		{"no answer", []chatmodel.Part{text("progress", "commentary", "working"), tool}, ""},
		{"steered", []chatmodel.Part{text("old", "final_answer", "old answer"), {Role: "user", Kind: "text", Text: "follow up"}, text("new", "final_answer", "New answer")}, "New answer"},
		{"task after answer", []chatmodel.Part{text("answer", "", "Final answer"), {Kind: "tool_use", Native: &chatmodel.ToolNative{Kind: "task"}}}, "Final answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := remoteAgentFinalText(agent.AgentResult{Text: "all commentary and tool process", Parts: tc.parts})
			if got != tc.want {
				t.Fatalf("answer = %q, want %q", got, tc.want)
			}
		})
	}
}

type remoteResultRunner struct {
	fakeRunner
	started, release chan struct{}
	failure          error
}

func (r *remoteResultRunner) Run(ctx context.Context, request agent.AgentRequest, emit func(agent.AgentEvent) error) (agent.AgentResult, error) {
	for _, part := range []chatmodel.Part{
		{ID: "progress", Role: "assistant", Kind: "text", Phase: "commentary", Text: "private partial reply"},
		{ID: "tool", Role: "assistant", Kind: "tool_use", Name: "shell", Text: "private command"},
		{ID: "tool", Role: "tool", Kind: "tool_result", Text: "private tool output"},
	} {
		event := agent.AgentEvent{Type: "part", Part: &part}
		if part.Kind == "text" {
			event.Type, event.Text = "delta", part.Text
		}
		if err := emit(event); err != nil {
			return agent.AgentResult{}, err
		}
	}
	close(r.started)
	select {
	case <-r.release:
	case <-ctx.Done():
		return agent.AgentResult{}, ctx.Err()
	}
	part := chatmodel.Part{ID: "final", Role: "assistant", Kind: "text", Phase: "final_answer", Text: "**Final remote answer**"}
	if err := emit(agent.AgentEvent{Type: "delta", Text: part.Text, Part: &part}); err != nil {
		return agent.AgentResult{}, err
	}
	return agent.AgentResult{Text: "private partial reply\n**Final remote answer**"}, r.failure
}

func TestRemoteAgentMCPReturnsOnlyFinalAnswer(t *testing.T) {
	runner := &remoteResultRunner{started: make(chan struct{}), release: make(chan struct{})}
	f := remoteFixtureForTest(t, runner)
	config, release, err := f.a.agentMCPGrant(t.Context(), f.identity, f.origin, "parent-turn")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	mcp := func(operation string, arguments map[string]any) json.RawMessage {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "spool_" + operation, "arguments": arguments}})
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, config.URL, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+config.Token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				Structured struct {
					Result json.RawMessage `json:"result"`
				} `json:"structuredContent"`
			} `json:"result"`
		}
		if json.NewDecoder(response.Body).Decode(&result) != nil || result.Result.IsError || len(result.Result.Content) != 1 {
			t.Fatalf("invalid MCP response: %+v", result)
		}
		if !bytes.Equal([]byte(result.Result.Content[0].Text), result.Result.Structured.Result) {
			t.Fatal("text and structured context differ")
		}
		if strings.Contains(result.Result.Content[0].Text, "private") {
			t.Fatal("remote process leaked into context", result.Result.Content)
		}
		return result.Result.Structured.Result
	}
	body := mcp("delegate", map[string]any{"server": f.peer, "project": f.project, "agent": "codex", "message": "private task instructions", "request_id": chatmodel.NewID()})
	var task remoteAgentTask
	if json.Unmarshal(body, &task) != nil || task.Status != "running" || task.Text != "" {
		t.Fatal("delegate returned an unfinished answer", string(body))
	}
	<-runner.started
	query := map[string]any{"server": f.peer, "task_id": task.ID}
	body = mcp("task", query)
	if bytes.Contains(body, []byte(`"text"`)) || bytes.Contains(body, []byte(`"progress"`)) {
		t.Fatal("poll returned process data", string(body))
	}
	mcp("calls", map[string]any{})
	// The same task API used by Web may show progress, separately from Text.
	display, err := f.a.remoteAgent(t.Context(), f.identity, f.origin, "", remoteAgentOperation{Server: f.peer, federationAgentPayload: federationAgentPayload{Operation: "task", TaskID: task.ID}})
	if err != nil || json.Unmarshal(display, &task) != nil || task.Progress != "private partial reply" || task.Text != "" {
		t.Fatal("Web progress projection", string(display), err)
	}
	close(runner.release)
	query["wait_seconds"] = 2
	body = mcp("task", query)
	task = remoteAgentTask{}
	if json.Unmarshal(body, &task) != nil || task.Status != "completed" || task.Text != "**Final remote answer**" || task.Progress != "" {
		t.Fatal("wrong completed context", string(body))
	}
	mcp("calls", map[string]any{})
}

func TestRemoteAgentMCPExcludesUnfinishedTextAndApprovalDetails(t *testing.T) {
	for _, status := range []string{"running", "awaiting_approval", "failed", "stopped", "interrupted"} {
		for _, operation := range []string{"delegate", "task", "cancel", "calls"} {
			t.Run(status+"/"+operation, func(t *testing.T) {
				task := remoteAgentTask{ID: "task", Status: status, Text: "private partial reply", Progress: "private progress", Approval: &chatmodel.ApprovalRequest{ID: "private approval"}}
				var input any = task
				if operation == "calls" {
					input = []remoteAgentCall{{Task: task, Message: "private prompt"}}
				}
				body, _ := json.Marshal(input)
				result, err := remoteAgentMCPResult(operation, body)
				if err != nil || bytes.Contains(result, []byte("private")) || bytes.Contains(result, []byte(`"text"`)) {
					t.Fatal("unfinished context", string(result), err)
				}
				if bytes.Contains(result, []byte(`"approval_required":true`)) != (status == "awaiting_approval") {
					t.Fatal("approval status missing", string(result))
				}
			})
		}
	}
}

func TestRemoteAgentFailedTaskHasNoFinalAnswer(t *testing.T) {
	runner := &remoteResultRunner{started: make(chan struct{}), release: make(chan struct{}), failure: errors.New("fixture failure")}
	close(runner.release)
	f := remoteFixtureForTest(t, runner)
	var task remoteAgentTask
	body := f.call(t, "delegate", federationAgentPayload{Project: f.project, Backend: chatmodel.BackendCodex, Message: "fail", RequestID: chatmodel.NewID()})
	if json.Unmarshal(body, &task) != nil || task.Status != "failed" || task.Text != "" {
		t.Fatal("failure exported an answer", string(body))
	}
}
