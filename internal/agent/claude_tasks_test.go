package agent

import (
	"context"
	"errors"
	"fmt"
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

func TestClaudeChildStepsAreABoundedWindowOnItsWork(t *testing.T) {
	var tasks claudeTasks
	var events []AgentEvent
	emit := func(e AgentEvent) error { events = append(events, e); return nil }
	latest := func() *chatmodel.TaskProgress { return events[len(events)-1].Part.Native.Task }
	if _, err := tasks.call(map[string]any{"id": "call", "name": "Agent", "input": map[string]any{"description": "Inspect parser", "prompt": "Check the grammar"}}, "", emit); err != nil {
		t.Fatal(err)
	}
	nested := func(kind string, block map[string]any) {
		t.Helper()
		if err := tasks.nested(map[string]any{"type": kind, "message": map[string]any{"content": []any{block}}}, "call", emit); err != nil {
			t.Fatal(err)
		}
	}
	nested("assistant", map[string]any{"type": "text", "text": "Reading the grammar"})
	nested("assistant", map[string]any{"type": "tool_use", "id": "read", "name": "Read", "input": map[string]any{"file_path": "/repo/grammar.go", "limit": 20.0}})
	nested("assistant", map[string]any{"type": "tool_use", "id": "write", "name": "Write", "input": map[string]any{"file_path": "/repo/out.go", "content": strings.Repeat("x", 4096)}})
	started := latest()
	nested("user", map[string]any{"type": "tool_result", "tool_use_id": "read", "content": "package grammar"})
	nested("user", map[string]any{"type": "tool_result", "tool_use_id": "write", "content": "denied", "is_error": true})
	steps := latest().Steps
	// A file's body is not something a step's label is read from.
	if len(steps) != 3 || steps[0].Kind != chatmodel.TaskStepMessage || steps[0].Text != "Reading the grammar" ||
		steps[1].Name != "Read" || steps[1].Status != chatmodel.ToolCompleted || steps[1].Input != `{"file_path":"/repo/grammar.go","limit":20}` ||
		steps[2].Name != "Write" || steps[2].Status != chatmodel.ToolFailed || steps[2].Input != `{"file_path":"/repo/out.go"}` {
		t.Fatalf("child steps not told: %+v", steps)
	}
	if latest().Prompt != "Check the grammar" || latest().StartedAt == 0 {
		t.Fatalf("child prompt or start missing: %+v", latest())
	}
	if len(started.Steps) != 3 || started.Steps[1].Status != chatmodel.ToolRunning || started.Steps[2].Status != chatmodel.ToolRunning {
		t.Fatalf("emitted steps mutated: %+v", started.Steps)
	}
	for i := range 30 {
		nested("assistant", map[string]any{"type": "tool_use", "id": fmt.Sprintf("bash-%d", i), "name": "Bash", "input": map[string]any{"command": "echo"}})
	}
	// A call that newer steps pushed out is not brought back by being told again.
	nested("assistant", map[string]any{"type": "tool_use", "id": "read", "name": "Read", "input": map[string]any{"file_path": "/repo/grammar.go"}})
	steps = latest().Steps
	if len(steps) != taskStepLimit || steps[0].ID != "bash-10" || steps[len(steps)-1].ID != "bash-29" || latest().ToolUses != 32 {
		t.Fatalf("steps are not the latest %d: %d steps from %q to %q, %d calls", taskStepLimit, len(steps), steps[0].ID, steps[len(steps)-1].ID, latest().ToolUses)
	}
}

func TestClaudeChildToldOnlyByProgressEventsStillShowsSteps(t *testing.T) {
	var tasks claudeTasks
	var events []AgentEvent
	emit := func(e AgentEvent) error { events = append(events, e); return nil }
	system := func(value map[string]any) {
		t.Helper()
		value["task_id"] = "one"
		if _, err := tasks.system(value, emit); err != nil {
			t.Fatal(err)
		}
	}
	system(map[string]any{"subtype": "task_started", "tool_use_id": "call", "description": "Inspect parser", "task_type": "local_agent"})
	system(map[string]any{"subtype": "task_progress", "summary": "Reading files", "last_tool_name": "Read", "usage": map[string]any{"tool_uses": 1.0}})
	system(map[string]any{"subtype": "task_progress", "summary": "Reading files", "last_tool_name": "Grep", "usage": map[string]any{"tool_uses": 3.0}})
	// No call was counted since: the same tool is not a new step.
	system(map[string]any{"subtype": "task_progress", "summary": "Reading files", "last_tool_name": "Grep", "usage": map[string]any{"tool_uses": 3.0}})
	steps := events[len(events)-1].Part.Native.Task.Steps
	if len(steps) != 3 || steps[0].Kind != chatmodel.TaskStepMessage || steps[0].Text != "Reading files" || steps[1].Name != "Read" || steps[2].Name != "Grep" {
		t.Fatalf("progress events told no steps: %+v", steps)
	}
	// The child's own calls, once forwarded, are the steps; progress adds none.
	call := map[string]any{"type": "tool_use", "id": "grep", "name": "Grep", "input": map[string]any{"pattern": "TODO"}}
	if err := tasks.nested(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{call}}}, "call", emit); err != nil {
		t.Fatal(err)
	}
	system(map[string]any{"subtype": "task_progress", "summary": "Searching", "last_tool_name": "Bash", "usage": map[string]any{"tool_uses": 5.0}})
	steps = events[len(events)-1].Part.Native.Task.Steps
	if len(steps) != 3 || steps[2].ID != "grep" || steps[2].Input != `{"pattern":"TODO"}` {
		t.Fatalf("forwarded call duplicated its announcement: %+v", steps)
	}
	system(map[string]any{"subtype": "task_notification", "status": "completed", "summary": "Done"})
	if done := events[len(events)-1].Part.Native; done.Status != chatmodel.ToolCompleted || done.DurationMs <= 0 {
		t.Fatalf("finished child has no duration: %+v", done)
	}
}
