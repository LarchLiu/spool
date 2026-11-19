package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestClaudeRejectedFollowupFinishesAfterBackgroundTask(t *testing.T) {
	for _, state := range []string{"cancelled", "rejected", "discarded"} {
		t.Run(state, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			id := NewSteerID()
			// No turn answers this notification; the wait for one must end by itself.
			p := &agentProcess{initialized: true, sessionID: NewSteerID(), permissionMode: chatmodel.DefaultPermissionMode("claude"), ctx: ctx, stdin: &approvalWriter{}, messages: make(chan map[string]any), taskWait: 20 * time.Millisecond}
			p.queued = []map[string]any{
				{"type": "system", "subtype": "task_started", "task_id": "child", "task_type": "local_agent", "is_backgrounded": true},
				{"type": "assistant", "message": map[string]any{"id": "reply", "content": []any{map[string]any{"type": "text", "text": "Main report"}}}},
				{"type": "result", "subtype": "success", "result": "Main report"},
				{"type": "command_lifecycle", "command_uuid": id, "state": state},
				{"type": "system", "subtype": "task_notification", "task_id": "child", "status": "completed", "summary": "Child done"},
			}
			sent, rejected, childFinished := false, 0, false
			result, err, reusable := p.runClaude(ctx, AgentRequest{Backend: "claude", Prompt: "Inspect project"}, func(event AgentEvent) error {
				if event.Type == "delta" && !sent {
					sent = true
					return p.steerClaude(id, AgentRequest{Prompt: "Also inspect tests"})
				}
				if event.Type == "unsteered" && event.SteerID == id {
					rejected++
				}
				if event.Part != nil && event.Part.Native != nil && event.Part.Native.Task != nil && event.Part.Native.Status == chatmodel.ToolCompleted {
					childFinished = true
				}
				return nil
			})
			if err != nil || !reusable || result.Text != "Main report" || result.Steered != 0 || rejected != 1 || !childFinished {
				t.Fatalf("rejected followup left the request unfinished: result=%+v rejected=%d childFinished=%v reusable=%v err=%v", result, rejected, childFinished, reusable, err)
			}
			if err := p.steerClaude(NewSteerID(), AgentRequest{Prompt: "Too late"}); !errors.Is(err, ErrNoActiveTurn) {
				t.Fatalf("finished request still accepts steering: %v", err)
			}
		})
	}
}

func TestClaudeForkedReviewProgressPrecedesAndDoesNotReplaceReport(t *testing.T) {
	request := newAgentFixture(t, chatmodel.BackendClaude)
	runner := newTestRunner(t)
	for _, prompt := range []string{"/code-review", "/review main"} {
		request.Prompt = prompt
		result, events := runFixture(t, runner, request)
		request.NativeSessionID = result.SessionID
		var tasks []chatmodel.Part
		seenReport := false
		for _, event := range events {
			if event.Type == "delta" {
				seenReport = true
			}
			if event.Part == nil || event.Part.Kind != "tool_use" {
				continue
			}
			if seenReport {
				t.Fatal("task progress arrived after the report")
			}
			tasks = append(tasks, *event.Part)
		}
		if result.Text != "Hello world" || !seenReport || len(tasks) != 3 {
			t.Fatalf("missing progress or duplicated report: result=%#v tasks=%#v", result, tasks)
		}
		if tasks[0].Name != "/code-review" || tasks[0].Native.Status != chatmodel.ToolRunning || tasks[0].Text != "/code-review" ||
			!strings.Contains(tasks[1].Text, "Checking changed files\nTool: Read\nTool uses: 3") || tasks[1].Native.DurationMs != 1500 ||
			tasks[2].Native.Status != chatmodel.ToolCompleted || tasks[2].Native.DurationMs != 2000 || tasks[0].ID != tasks[2].ID {
			t.Fatalf("incorrect task timeline: %#v", tasks)
		}
		for _, part := range tasks {
			if strings.Contains(part.Text, "PRIVATE") {
				t.Fatal("nested prompt or transcript leaked", part)
			}
		}
		if len(result.Activities) != 1 {
			t.Fatal("progress updates appended duplicate activities", result.Activities)
		}
	}
	if fixtureCount(t, request, "spawn") != 1 {
		t.Fatal("review did not reuse the native session")
	}
}

func TestClaudeTasksJoinToolsNotificationsAndNestedAgents(t *testing.T) {
	var tasks claudeTasks
	var events []AgentEvent
	emit := func(event AgentEvent) error { events = append(events, event); return nil }
	call := map[string]any{"id": "call-one", "name": "Agent", "input": map[string]any{"description": "Inspect database", "subagent_type": "Explore", "prompt": "Check migrations", "run_in_background": true}}
	if ok, err := tasks.call(call, "", emit); !ok || err != nil {
		t.Fatal(ok, err)
	}
	start := map[string]any{"subtype": "task_started", "task_id": "native-one", "tool_use_id": "call-one", "description": "Inspect database", "task_type": "local_agent", "is_backgrounded": true}
	if _, err := tasks.system(start, emit); err != nil {
		t.Fatal(err)
	}
	if len(tasks.order) != 1 || tasks.tasks["native-one"] != tasks.tools["call-one"] {
		t.Fatal("tool and notification duplicated task")
	}
	if _, err := tasks.result(map[string]any{"tool_use_id": "call-one", "content": "launched"}, map[string]any{"toolUseResult": map[string]any{"isAsync": true, "agentId": "native-one"}}, emit); err != nil {
		t.Fatal(err)
	}
	if !tasks.running() {
		t.Fatal("async launch finished the task")
	}
	nested := map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "read", "name": "Read"}, map[string]any{"type": "tool_use", "id": "call-two", "name": "Task", "input": map[string]any{"description": "Inspect schema"}}}}}
	if err := tasks.nested(nested, "call-one", emit); err != nil {
		t.Fatal(err)
	}
	if len(tasks.order) != 2 || tasks.tools["call-two"].progress.ParentID != "native-one" {
		t.Fatal("nested child missing")
	}
	for _, id := range []string{"native-one", "call-two"} {
		_, err := tasks.system(map[string]any{"subtype": "task_notification", "task_id": id, "status": "completed", "summary": "Done"}, emit)
		if err != nil {
			t.Fatal(err)
		}
	}
	if tasks.running() {
		t.Fatal("completed children kept turn active")
	}
	if events[0].Part.Native.Task.ID != "call-one" || events[0].Part.Native.Task.Summary != "" {
		t.Fatal("emitted snapshot mutated")
	}
	if tasks.tools["call-one"].progress.LastTool != "Read" {
		t.Fatal("nested tool progress missing")
	}
}

func TestClaudeTasksTrackAnyNativeAgentAndTerminalEvents(t *testing.T) {
	for _, title := range []string{"/deep-research", "/custom-skill", "Inspect project"} {
		for _, status := range []string{"completed", "failed", "stopped"} {
			t.Run(title+status, func(t *testing.T) {
				var tasks claudeTasks
				var events []AgentEvent
				emit := func(e AgentEvent) error { events = append(events, e); return nil }
				for _, value := range []map[string]any{
					{"subtype": "task_started", "task_id": "one", "description": title, "task_type": "local_agent", "ambient": true, "skip_transcript": true, "spawn_depth": 3, "parent_task_id": "parent"},
					{"subtype": "task_progress", "task_id": "one", "summary": "Checking files", "last_tool_name": "Read"},
					{"subtype": "task_notification", "task_id": "one", "status": status},
					{"subtype": "task_notification", "task_id": "one", "status": status},
					{"subtype": "task_progress", "task_id": "one", "summary": "late progress"},
				} {
					if _, err := tasks.system(value, emit); err != nil {
						t.Fatal(err)
					}
				}
				if len(events) != 3 || tasks.running() || events[2].Part.Native.Status == chatmodel.ToolRunning {
					t.Fatalf("bad terminal lifecycle: %#v", events)
				}
			})
		}
	}
}

func TestClaudeBackgroundShellNotificationsAreNotSubagents(t *testing.T) {
	var tasks claudeTasks
	emit := func(e AgentEvent) error { t.Fatal("shell job displayed as subagent", e); return nil }
	for _, event := range []map[string]any{
		{"subtype": "task_started", "task_id": "shell", "task_type": "local_bash"},
		{"subtype": "task_progress", "task_id": "shell", "summary": "Still running"},
		{"subtype": "task_notification", "task_id": "shell", "status": "completed"},
		// Only task_started names a task's type. Work begun by an earlier
		// request reports its later events without one.
		{"subtype": "task_progress", "task_id": "earlier", "summary": "Still running"},
		{"subtype": "task_notification", "task_id": "earlier", "tool_use_id": "earlier-call", "status": "completed"},
	} {
		if handled, err := tasks.system(event, emit); !handled || err != nil {
			t.Fatal(handled, err)
		}
	}
	if tasks.running() || len(tasks.order) != 0 {
		t.Fatal("unstarted job kept the subagent wait alive", tasks.order)
	}
}

func TestClaudeSilentChildCannotHoldFinishedTurnOpen(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	p := &agentProcess{initialized: true, sessionID: NewSteerID(), permissionMode: chatmodel.DefaultPermissionMode("claude"), ctx: ctx, stdin: &approvalWriter{}, messages: make(chan map[string]any), taskWait: 20 * time.Millisecond}
	p.queued = []map[string]any{
		{"type": "system", "subtype": "task_started", "task_id": "child", "task_type": "local_agent", "is_backgrounded": true},
		{"type": "assistant", "message": map[string]any{"id": "reply", "content": []any{map[string]any{"type": "text", "text": "Main report"}}}},
		{"type": "result", "subtype": "success", "result": "Main report"},
	}
	var last chatmodel.Part
	result, err, reusable := p.runClaude(ctx, AgentRequest{Backend: "claude", Prompt: "Inspect project"}, func(event AgentEvent) error {
		if event.Part != nil && event.Part.Native != nil && event.Part.Native.Task != nil {
			last = *event.Part
		}
		return nil
	})
	if err != nil || !reusable || result.Text != "Main report" || last.Native == nil || last.Native.Status != "unknown" {
		t.Fatalf("silent child was not given up on as unknown: result=%+v task=%+v reusable=%v err=%v", result, last, reusable, err)
	}
}

func TestClaudeTurnAnsweringTaskNotificationBelongsToItsRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	p := &agentProcess{initialized: true, sessionID: NewSteerID(), permissionMode: chatmodel.DefaultPermissionMode("claude"), ctx: ctx, stdin: &approvalWriter{}, messages: make(chan map[string]any, 8), taskWait: time.Second}
	p.queued = []map[string]any{
		{"type": "system", "subtype": "task_started", "task_id": "child", "task_type": "local_agent", "is_backgrounded": true},
		{"type": "assistant", "message": map[string]any{"id": "reply", "content": []any{map[string]any{"type": "text", "text": "Main report"}}}},
		{"type": "result", "subtype": "success", "result": "Main report"},
		{"type": "system", "subtype": "task_notification", "task_id": "child", "status": "completed", "summary": "Child done"},
	}
	// The answering turn starts only after the last child has been reported,
	// and its first event need not be in the pipe yet.
	go func() {
		time.Sleep(50 * time.Millisecond)
		p.messages <- map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_start", "message": map[string]any{"id": "followup"}}}
		time.Sleep(50 * time.Millisecond)
		p.messages <- map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": " Child confirmed"}}}
		p.messages <- map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_stop"}}
		p.messages <- map[string]any{"type": "result", "subtype": "success", "result": " Child confirmed"}
	}()
	result, err, reusable := p.runClaude(ctx, AgentRequest{Backend: "claude", Prompt: "Inspect project"}, func(AgentEvent) error { return nil })
	if err != nil || !reusable || result.Text != "Main report Child confirmed" || len(p.messages) != 0 {
		t.Fatalf("answering turn was left for the next request: result=%+v left=%d reusable=%v err=%v", result, len(p.messages), reusable, err)
	}
}
