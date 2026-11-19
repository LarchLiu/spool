package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestLiveSubagentsContinueAfterMainReply(t *testing.T) {
	for _, scenario := range []struct {
		backend chatmodel.Backend
		prompt  string
	}{{"claude", "subagent-live"}, {"codex", "subagent-live"}, {"codex", "subagent-live poll"}} {
		t.Run(string(scenario.backend)+scenario.prompt, func(t *testing.T) {
			request := newAgentFixture(t, scenario.backend)
			request.Prompt = scenario.prompt
			runner := newTestRunner(t)
			runner.SetIdleTimeout(20 * time.Millisecond)
			var reply strings.Builder
			var afterReport bool
			terminal := map[string]bool{}
			snapshots := map[string]*chatmodel.TaskProgress{}
			latest := map[string]*chatmodel.TaskProgress{}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result, err := runner.Run(ctx, request, func(e AgentEvent) error {
				if e.Type == "delta" {
					reply.WriteString(e.Text)
				}
				if e.Part == nil || e.Part.Native == nil || e.Part.Native.Task == nil {
					return nil
				}
				task := e.Part.Native.Task
				latest[task.ID] = task
				if snapshots[task.ID] == nil {
					snapshots[task.ID] = task
				}
				if reply.Len() > 0 && e.Part.Native.Status == chatmodel.ToolCompleted {
					afterReport = true
					terminal[task.ID] = true
				}
				return nil
			})
			// Claude Code answers its children's notifications with one more turn.
			want := "Main report"
			if scenario.backend == chatmodel.BackendClaude {
				want += " Children finished"
			}
			if err != nil || result.Text != want || reply.String() != result.Text || !afterReport || len(terminal) != 2 {
				t.Fatalf("lost background progress: result=%#v reply=%q terminal=%v err=%v", result, reply.String(), terminal, err)
			}
			for _, snapshot := range snapshots {
				if snapshot.Summary != "" {
					t.Fatal("prior task snapshot mutated", snapshot)
				}
			}
			if strings.Contains(scenario.prompt, "poll") {
				if fixtureCount(t, request, "thread/turns/list") < 4 || fixtureCount(t, request, "thread/items/list") < 2 {
					t.Fatal("child polling not exercised")
				}
				for _, task := range latest {
					if task.Summary != "Checking child files" || task.LastTool != "shell" {
						t.Fatal("polled child details missing", task)
					}
				}
				if fixtureCount(t, request, "thread/resume") != 0 {
					t.Fatal("progress reader resumed child")
				}
			}
		})
	}
}

func TestLiveSubagentWaitCanBeCancelledAfterMainReply(t *testing.T) {
	for _, backend := range []chatmodel.Backend{"claude", "codex"} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "subagent-live block"
			runner := newTestRunner(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result, err := runner.Run(ctx, request, func(e AgentEvent) error {
				if e.Type == "delta" {
					cancel()
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || result.Text != "Main report" {
				t.Fatal("background wait did not cancel", result, err)
			}
		})
	}
}

func TestChildApprovalIgnoresUnrelatedTurnCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	p := &agentProcess{ctx: ctx, messages: make(chan map[string]any, 1)}
	p.messages <- map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "parent", "turn": map[string]any{"id": "parent-turn"}}}
	response, err := p.awaitApproval(ctx, func(ctx context.Context, _ chatmodel.ApprovalRequest) (chatmodel.ApprovalResponse, error) {
		// Wait until the parent completion has actually been read and queued.
		for len(p.messages) > 0 {
			select {
			case <-ctx.Done():
				return chatmodel.ApprovalResponse{}, ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
		return chatmodel.ApprovalResponse{Allow: true}, nil
	}, chatmodel.ApprovalRequest{}, func(map[string]any) bool { return false }, func(value map[string]any) bool { return nestedString(value, "params", "threadId") == "child" })
	if err != nil || !response.Allow || len(p.queued) != 1 {
		t.Fatal("other completion settled child approval", response, err)
	}
}

func TestApprovalWaitOutlastsOtherThreadsStreaming(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	p := &agentProcess{ctx: ctx, messages: make(chan map[string]any)}
	const tokens = 3 * maxApprovalBacklog
	go func() {
		for i := range tokens {
			thread := []string{"parent", "sibling"}[i/100%2]
			select {
			case p.messages <- map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": thread, "turnId": "turn", "itemId": "reply", "delta": "x"}}:
			case <-ctx.Done():
				return
			}
		}
		for range tokens {
			select {
			case p.messages <- map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "index": 0.0, "delta": map[string]any{"type": "text_delta", "text": "y"}}}:
			case <-ctx.Done():
				return
			}
		}
		p.messages <- map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "child"}}
	}()
	_, err := p.awaitApproval(ctx, func(ctx context.Context, _ chatmodel.ApprovalRequest) (chatmodel.ApprovalResponse, error) {
		<-ctx.Done()
		return chatmodel.ApprovalResponse{}, ctx.Err()
	}, chatmodel.ApprovalRequest{}, func(map[string]any) bool { return false }, func(value map[string]any) bool { return nestedString(value, "params", "threadId") == "child" })
	if !errors.Is(err, errApprovalResolved) {
		t.Fatalf("streaming siblings ended the wait: %v", err)
	}
	text := ""
	for _, value := range p.queued {
		text += nestedString(value, "params", "delta") + nestedString(value, "event", "delta", "text")
	}
	// One event per run of deltas on a thread, the Claude run, and the completion.
	if len(p.queued) != tokens/100+3 || text != strings.Repeat("x", tokens)+strings.Repeat("y", tokens) {
		t.Fatalf("queued %d events holding %d bytes of %d", len(p.queued), len(text), 2*tokens)
	}
}

func TestClaudeChildApprovalWaitsForItsTaskCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	writer := &approvalWriter{}
	p := &agentProcess{ctx: ctx, stdin: writer, messages: make(chan map[string]any, 2)}
	p.messages <- map[string]any{"type": "result", "subtype": "success"}
	p.messages <- map[string]any{"type": "system", "subtype": "task_notification", "task_id": "child", "status": "completed"}
	stopped := make(chan struct{})
	p.approve = func(ctx context.Context, _ chatmodel.ApprovalRequest) (chatmodel.ApprovalResponse, error) {
		defer close(stopped)
		<-ctx.Done()
		return chatmodel.ApprovalResponse{}, ctx.Err()
	}
	err := p.handleClaudeApproval(ctx, map[string]any{"type": "control_request", "request_id": "approval", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "tool_use_id": "child-command", "agent_id": "child", "input": map[string]any{"command": "echo child"}}})
	if err != nil || len(p.queued) != 2 || writer.Len() != 0 {
		t.Fatal("parent result dismissed child approval", len(p.queued), writer.String(), err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("approval handler did not stop")
	}
}
