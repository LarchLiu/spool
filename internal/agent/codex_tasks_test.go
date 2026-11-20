package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestCodexPolledFollowupReactivatesKnownDescendantOnce(t *testing.T) {
	for _, tool := range []string{"followupTask", "resumeAgent", "sendInput"} {
		t.Run(tool, func(t *testing.T) {
			tasks := codexTasks{root: "parent"}
			caller := tasks.ensure("caller", "parent", "Coordinate checks")
			child := tasks.ensure("child", "caller", "Inspect parser")
			child.status = chatmodel.ToolCompleted
			emit := func(AgentEvent) error { return nil }
			// A live start can precede the receiver identities in the saved
			// completion. It must not consume that receiver's fresh call.
			if err := tasks.item(map[string]any{"id": "followup", "type": "collabAgentToolCall", "tool": tool, "status": "inProgress"}, "caller", emit); err != nil {
				t.Fatal(err)
			}
			followup := map[string]any{"id": "followup", "type": "collabAgentToolCall", "tool": tool, "status": "completed", "receiverThreadIds": []any{"child"}, "agentsStates": map[string]any{"child": map[string]any{"status": "running"}}}
			tasks.reads["items"] = codexTaskRead{task: caller, method: "thread/items/list", revision: caller.revision}
			writer := &approvalWriter{}
			p := &agentProcess{stdin: writer}
			if handled, err := tasks.response(p, map[string]any{"id": "items", "result": map[string]any{"data": []any{map[string]any{"item": followup}}}}, emit); !handled || err != nil {
				t.Fatal(handled, err)
			}
			caller.status = chatmodel.ToolCompleted
			if !tasks.running() || !child.running() {
				t.Fatal("fresh polled followup did not keep the known descendant active")
			}
			older := map[string]any{"id": "older-followup", "type": "collabAgentToolCall", "tool": tool, "receiverThreadIds": []any{"child"}, "agentsStates": map[string]any{"child": map[string]any{"status": "completed"}}}
			if err := tasks.discover(older, "caller", emit); err != nil || !child.running() {
				t.Fatal("older completed followup overwrote newly active work", err)
			}
			if err := tasks.poll(p, time.Now(), emit); err != nil {
				t.Fatal(err)
			}
			var request map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(writer.String())), &request); err != nil || request["method"] != "thread/turns/list" || nestedString(request, "params", "threadId") != "child" {
				t.Fatalf("reactivated child is not polled: %s (%v)", writer.String(), err)
			}
			if _, err := tasks.event("turn/completed", map[string]any{"threadId": "child", "turn": map[string]any{"id": "followup-turn", "status": "completed"}}, emit); err != nil {
				t.Fatal(err)
			}
			if err := tasks.discover(followup, "caller", emit); err != nil || tasks.running() {
				t.Fatal("repeated saved followup reopened completed work", err)
			}
		})
	}
}

func TestCodexSavedFollowupCannotReplayObservedLiveCall(t *testing.T) {
	tasks := codexTasks{root: "parent"}
	task := tasks.ensure("child", "parent", "Inspect")
	emit := func(AgentEvent) error { return nil }
	item := map[string]any{"id": "followup", "type": "collabAgentToolCall", "tool": "followupTask", "receiverThreadIds": []any{"child"}, "agentsStates": map[string]any{"child": map[string]any{"status": "running"}}}
	if err := tasks.item(item, "parent", emit); err != nil {
		t.Fatal(err)
	}
	task.status = chatmodel.ToolCompleted
	if err := tasks.discover(item, "parent", emit); err != nil || tasks.running() {
		t.Fatal("saved snapshot replayed a live followup", err)
	}
}

func TestCodexChildStateIsIndependentOfSpawnAndTracksDescendants(t *testing.T) {
	tasks := codexTasks{root: "parent"}
	var events []AgentEvent
	emit := func(e AgentEvent) error { events = append(events, e); return nil }
	spawn := map[string]any{"type": "collabAgentToolCall", "tool": "spawnAgent", "status": "completed", "receiverThreadIds": []any{"child"}, "prompt": "Inspect parser", "agentsStates": map[string]any{"child": map[string]any{"status": "running"}}}
	if err := tasks.item(spawn, "parent", emit); err != nil {
		t.Fatal(err)
	}
	if !tasks.running() || events[0].Part.Native.Status != chatmodel.ToolRunning {
		t.Fatal("spawn completion finished agent")
	}
	_, err := tasks.event("thread/started", map[string]any{"thread": map[string]any{"id": "grandchild", "parentThreadId": "child", "preview": "Check grammar", "agentRole": "worker"}}, emit)
	if err != nil || tasks.tasks["grandchild"].progress.ParentID != "child" {
		t.Fatal("descendant missing", err)
	}
	if ok, _ := tasks.event("thread/started", map[string]any{"thread": map[string]any{"id": "foreign", "parentThreadId": "unrelated"}}, emit); ok {
		t.Fatal("foreign thread exposed")
	}
	for _, id := range []string{"child", "grandchild"} {
		_, err := tasks.event("item/completed", map[string]any{"threadId": id, "item": map[string]any{"id": "message", "type": "agentMessage", "text": "Checked files"}}, emit)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tasks.event("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": "turn", "status": "completed"}}, emit)
		if err != nil {
			t.Fatal(err)
		}
	}
	if tasks.running() {
		t.Fatal("terminal tasks running")
	}
	if events[0].Part.Native.Task.Summary != "" {
		t.Fatal("snapshot mutated")
	}
}

func TestCodexTaskReadFailureDoesNotInventAgentFailure(t *testing.T) {
	tasks := codexTasks{root: "parent"}
	task := tasks.ensure("child", "parent", "Inspect")
	emit := func(e AgentEvent) error { return nil }
	for range 3 {
		if err := tasks.readFailure(task, "Unavailable", emit); err != nil {
			t.Fatal(err)
		}
	}
	if task.status != "unknown" || tasks.running() {
		t.Fatal("lost progress stayed active or became successful", task)
	}
}

func TestCodexHealthyChildSurvivesScatteredUnansweredReads(t *testing.T) {
	tasks := codexTasks{root: "parent"}
	task := tasks.ensure("child", "parent", "Inspect")
	emit := func(AgentEvent) error { return nil }
	p := &agentProcess{stdin: &approvalWriter{}}
	read := func(result any) {
		t.Helper()
		tasks.reads["read"] = codexTaskRead{task: task, method: "thread/turns/list", revision: task.revision}
		if handled, err := tasks.response(p, map[string]any{"id": "read", "result": result}, emit); !handled || err != nil {
			t.Fatal(handled, err)
		}
	}
	fail := func() {
		t.Helper()
		for range 2 {
			if err := tasks.readFailure(task, "Unavailable", emit); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A child still being set up has no turn; the thread answering is no failure.
	fail()
	read(map[string]any{"data": []any{}})
	fail()
	read(map[string]any{"data": []any{map[string]any{"id": "turn", "status": "in_progress"}}})
	fail()
	if _, err := tasks.event("item/agentMessage/delta", map[string]any{"threadId": "child", "delta": "Checking"}, emit); err != nil {
		t.Fatal(err)
	}
	fail()
	if !task.running() {
		t.Fatal("answered reads and live events did not clear earlier failures", task.status, task.progress.Summary)
	}
	// Its own events make reading it redundant for a while.
	before := len(tasks.reads)
	if err := tasks.poll(p, time.Now(), emit); err != nil || len(tasks.reads) != before {
		t.Fatal("child reporting its own events is still polled", err)
	}
}

func TestCodexChildStatesUseEitherSpellingInStableOrder(t *testing.T) {
	tasks := codexTasks{root: "parent"}
	emit := func(AgentEvent) error { return nil }
	spawn := map[string]any{"id": "spawn", "type": "collab_agent_tool_call", "tool": "spawnAgent", "agents_states": map[string]any{
		"d": map[string]any{"status": "pending_init"}, "b": map[string]any{"status": "not_found"}, "a": map[string]any{"status": "system_error"}, "c": map[string]any{"status": "in_progress"}}}
	if err := tasks.discover(spawn, "parent", emit); err != nil {
		t.Fatal(err)
	}
	var order, states []string
	for _, task := range tasks.order {
		order, states = append(order, task.progress.ID), append(states, task.status)
	}
	if strings.Join(order, "") != "abcd" || strings.Join(states, ",") != "failed,unknown,running,running" {
		t.Fatalf("children %v have states %v", order, states)
	}
}

func TestCodexChildRequestNamesItsOwnTurnAndProvesItAlive(t *testing.T) {
	tasks := codexTasks{root: "parent"}
	task := tasks.ensure("child", "parent", "Inspect")
	tasks.turns["child"] = "finished-turn"
	emit := func(AgentEvent) error { return nil }
	for range 3 {
		if err := tasks.readFailure(task, "Unavailable", emit); err != nil {
			t.Fatal(err)
		}
	}
	thread, turn, err := tasks.requester("item/commandExecution/requestApproval", map[string]any{"threadId": "child", "turnId": "followup-turn"}, emit)
	if err != nil || thread != "child" || turn != "followup-turn" || !task.running() || task.progress.Summary != "" {
		t.Fatalf("child request was not routed to its current turn: %q %q %v %+v", thread, turn, err, task)
	}
	if thread, turn, _ = tasks.requester("execCommandApproval", map[string]any{"conversationId": "child"}, emit); thread != "child" || turn != "followup-turn" {
		t.Fatalf("legacy child request lost its turn: %q %q", thread, turn)
	}
	if thread, _, _ = tasks.requester("item/commandExecution/requestApproval", map[string]any{"threadId": "unrelated", "turnId": "turn"}, emit); thread != "" {
		t.Fatal("foreign thread treated as a descendant")
	}
}

func TestCodexDelayedProgressReadCannotReopenCompletedChild(t *testing.T) {
	tasks := codexTasks{root: "parent"}
	task := tasks.ensure("child", "parent", "Inspect")
	emit := func(e AgentEvent) error { return nil }
	_ = emitTask(task, emit)
	tasks.reads["read"] = codexTaskRead{task: task, method: "thread/turns/list", revision: task.revision}
	task.status = chatmodel.ToolCompleted
	task.progress.Summary = "Finished"
	_ = emitTask(task, emit)
	handled, err := tasks.response(&agentProcess{}, map[string]any{"id": "read", "result": map[string]any{"data": []any{map[string]any{"id": "turn", "status": "inProgress"}}}}, emit)
	if !handled || err != nil || task.status != chatmodel.ToolCompleted || task.progress.Summary != "Finished" || len(tasks.reads) != 0 {
		t.Fatal("stale poll overwrote live outcome", task, err)
	}
}

func TestCodexLiveChildActivityInvalidatesUnchangedProgressReads(t *testing.T) {
	for _, notification := range []string{"turn/started", "approval", "legacy-approval", "tool-output"} {
		for _, method := range []string{"thread/turns/list", "thread/items/list"} {
			t.Run(notification+"/"+method, func(t *testing.T) {
				tasks := codexTasks{root: "parent"}
				task := tasks.ensure("child", "parent", "Inspect parser")
				task.progress.Summary = "Current progress"
				tasks.turns["child"] = "old-turn"
				emitted := 0
				emit := func(AgentEvent) error { emitted++; return nil }
				if err := emitTask(task, emit); err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				read := codexTaskRead{task: task, method: method, sent: now, revision: task.revision}
				tasks.reads["stale"], tasks.reads["timeout"] = read, read
				switch notification {
				case "turn/started":
					_, err := tasks.event(notification, map[string]any{"threadId": "child", "turn": map[string]any{"id": "new-turn"}}, emit)
					if err != nil {
						t.Fatal(err)
					}
				case "approval", "legacy-approval":
					_, _, err := tasks.requester("item/commandExecution/requestApproval", map[string]any{"threadId": "child", "turnId": "new-turn"}, emit)
					if err != nil {
						t.Fatal(err)
					}
					if notification == "legacy-approval" {
						read.revision = task.revision
						tasks.reads["stale"], tasks.reads["timeout"] = read, read
						if _, _, err := tasks.requester("execCommandApproval", map[string]any{"conversationId": "child"}, emit); err != nil {
							t.Fatal(err)
						}
					}
				case "tool-output":
					_, err := tasks.event("item/commandExecution/outputDelta", map[string]any{"threadId": "child", "turnId": "old-turn", "delta": "Checking files"}, emit)
					if err != nil {
						t.Fatal(err)
					}
				}
				if emitted != 1 {
					t.Fatal("unchanged row emitted duplicate progress", emitted)
				}
				expectedTurn := tasks.turns["child"]
				data := []any{map[string]any{"id": "old-turn", "status": "completed"}}
				if method == "thread/items/list" {
					data = []any{map[string]any{"item": map[string]any{"type": "agentMessage", "text": "Stale progress"}}}
				}
				p := &agentProcess{stdin: &approvalWriter{}}
				if handled, err := tasks.response(p, map[string]any{"id": "stale", "result": map[string]any{"data": data}}, emit); !handled || err != nil {
					t.Fatal(handled, err)
				}
				if err := tasks.poll(p, now.Add(11*time.Second), emit); err != nil {
					t.Fatal(err)
				}
				if !tasks.running() || tasks.turns["child"] != expectedTurn || task.progress.Summary != "Current progress" || tasks.failures["child"] != 0 {
					t.Fatalf("old read overwrote live work: status=%q turn=%q summary=%q failures=%d", task.status, tasks.turns["child"], task.progress.Summary, tasks.failures["child"])
				}
			})
		}
	}
}

func TestCodexSavedSpawnCannotReopenCompletedDescendant(t *testing.T) {
	tasks := codexTasks{root: "parent"}
	task := tasks.ensure("child", "parent", "Inspect")
	task.status = chatmodel.ToolCompleted
	emit := func(e AgentEvent) error { return nil }
	stale := map[string]any{"type": "collabAgentToolCall", "tool": "spawnAgent", "receiverThreadIds": []any{"child", "new-child"}, "agentsStates": map[string]any{"child": map[string]any{"status": "running"}, "new-child": map[string]any{"status": "running"}}}
	if err := tasks.discover(stale, "parent", emit); err != nil {
		t.Fatal(err)
	}
	if task.status != chatmodel.ToolCompleted || tasks.tasks["new-child"] == nil {
		t.Fatal("old history reopened child or missed descendant")
	}
}

func TestCodexRepeatedSpawnActivityDoesNotReopenTerminalTask(t *testing.T) {
	tasks := codexTasks{root: "parent"}
	task := tasks.ensure("child", "parent", "Inspect")
	task.status = chatmodel.ToolCompleted
	emit := func(e AgentEvent) error { return nil }
	for _, item := range []map[string]any{
		{"type": "subAgentActivity", "kind": "started", "agentThreadId": "child"},
		{"type": "collabAgentToolCall", "tool": "spawnAgent", "status": "completed", "receiverThreadIds": []any{"child"}, "agentsStates": map[string]any{"child": map[string]any{"status": "running"}}},
	} {
		if err := tasks.item(item, "parent", emit); err != nil {
			t.Fatal(err)
		}
	}
	if task.status != chatmodel.ToolCompleted {
		t.Fatal("delayed spawn reopened task")
	}
}

func TestCodexChildStepsComeFromItsEventsAndBoundedReadsOnce(t *testing.T) {
	tasks := codexTasks{root: "parent"}
	task := tasks.ensure("child", "parent", "Inspect parser")
	var latest *chatmodel.TaskProgress
	emit := func(e AgentEvent) error { latest = e.Part.Native.Task; return nil }
	command := func(id, text, status string) map[string]any {
		return map[string]any{"type": "commandExecution", "id": id, "command": text, "status": status}
	}
	note := map[string]any{"type": "agentMessage", "id": "note", "text": "Parser reads fine"}
	for _, event := range []struct {
		method string
		item   map[string]any
	}{{"item/started", command("read", "cat child.go", "inProgress")}, {"item/completed", command("read", "cat child.go", "completed")}, {"item/completed", note}} {
		if _, err := tasks.event(event.method, map[string]any{"threadId": "child", "item": event.item}, emit); err != nil {
			t.Fatal(err)
		}
	}
	if len(latest.Steps) != 2 || latest.Steps[0].Name != "shell" || latest.Steps[0].Input != "cat child.go" || latest.Steps[0].Status != chatmodel.ToolCompleted ||
		latest.Steps[1].Kind != chatmodel.TaskStepMessage || latest.Steps[1].Text != "Parser reads fine" || latest.ToolUses != 1 {
		t.Fatalf("live child steps not told: %+v", latest)
	}
	// A page is newest first, and repeats what the events already told.
	page := []any{map[string]any{"item": command("test", "go test ./...", "inProgress")}, map[string]any{"item": note}, map[string]any{"item": command("read", "cat child.go", "completed")}}
	for range 2 {
		tasks.reads["items"] = codexTaskRead{task: task, method: "thread/items/list", revision: task.revision}
		if handled, err := tasks.response(&agentProcess{}, map[string]any{"id": "items", "result": map[string]any{"data": page}}, emit); !handled || err != nil {
			t.Fatal(handled, err)
		}
	}
	steps := latest.Steps
	if len(steps) != 3 || steps[0].ID != "read" || steps[1].ID != "note" || steps[2].ID != "test" || steps[2].Status != chatmodel.ToolRunning || latest.ToolUses != 2 || latest.LastTool != "shell" {
		t.Fatalf("read page repeated or reordered steps: %+v", latest)
	}
}

func TestCodexChildStepsKeepToolTargets(t *testing.T) {
	arguments := map[string]any{
		"content": strings.Repeat("long \"quoted\" content\n", 64),
		"path":    "/work/output.txt",
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, tool, path string
		item             map[string]any
	}{
		{"mcp object", "files.write_file", "/work/output.txt", map[string]any{"type": "mcpToolCall", "id": "target", "server": "files", "tool": "write_file", "arguments": arguments}},
		{"mcp JSON", "files.write_file", "/work/output.txt", map[string]any{"type": "mcpToolCall", "id": "target", "server": "files", "tool": "write_file", "arguments": string(encoded)}},
		{"dynamic object", "files.write_file", "/work/output.txt", map[string]any{"type": "dynamicToolCall", "id": "target", "namespace": "files", "tool": "write_file", "arguments": arguments}},
		{"dynamic JSON", "files.write_file", "/work/output.txt", map[string]any{"type": "dynamicToolCall", "id": "target", "namespace": "files", "tool": "write_file", "arguments": string(encoded)}},
		{"image view", "view_image", "/work/page 1.png", map[string]any{"type": "imageView", "id": "target", "path": "/work/page 1.png"}},
		{"rollout image view", "view_image", "/work/page 1.png", map[string]any{"type": "image_view", "id": "target", "path": "/work/page 1.png"}},
	} {
		for _, transport := range []string{"live", "polled"} {
			t.Run(test.name+"/"+transport, func(t *testing.T) {
				tasks := codexTasks{root: "parent"}
				task := tasks.ensure("child", "parent", "Inspect files")
				var latest *chatmodel.TaskProgress
				emit := func(e AgentEvent) error { latest = e.Part.Native.Task; return nil }
				previous := map[string]any{"type": "commandExecution", "id": "previous", "command": "pwd", "status": "completed"}
				if handled, err := tasks.event("item/completed", map[string]any{"threadId": "child", "item": previous}, emit); !handled || err != nil {
					t.Fatal(handled, err)
				}
				for range 2 {
					if transport == "live" {
						if handled, err := tasks.event("item/completed", map[string]any{"threadId": "child", "item": test.item}, emit); !handled || err != nil {
							t.Fatal(handled, err)
						}
					} else {
						tasks.reads["items"] = codexTaskRead{task: task, method: "thread/items/list", revision: task.revision}
						page := []any{map[string]any{"item": test.item}, map[string]any{"item": previous}}
						if handled, err := tasks.response(&agentProcess{}, map[string]any{"id": "items", "result": map[string]any{"data": page}}, emit); !handled || err != nil {
							t.Fatal(handled, err)
						}
					}
				}
				if len(latest.Steps) != 2 || latest.LastTool != test.tool || latest.ToolUses != 2 {
					t.Fatalf("child tool missing or repeated: %+v", latest)
				}
				step := latest.Steps[1]
				if step.ID != "target" || step.Name != test.tool {
					t.Fatalf("child tool lost its identity: %+v", step)
				}
				var input map[string]any
				if err := json.Unmarshal([]byte(step.Input), &input); err != nil || input["path"] != test.path {
					t.Fatalf("child tool lost its structured target: %q (%v)", step.Input, err)
				}
				if _, kept := input["content"]; kept {
					t.Fatal("child step retained a large file body")
				}
			})
		}
	}
}
