package agentfixture

import (
	"strings"
	"time"
)

// A normal prompt, not a slash command, starts two children. The main reply
// finishes first so the host must keep consuming their lifecycle events.
func fixtureLiveTasks(codex bool, prompt, thread, turn string, send func(any), polls map[string]int) {
	if codex {
		notify := func(method, id string, body map[string]any) {
			body["threadId"], body["turnId"] = id, turn
			send(map[string]any{"method": method, "params": body})
		}
		for _, id := range []string{"child-one", "child-two"} {
			notify("item/completed", thread, map[string]any{"item": map[string]any{"type": "collabAgentToolCall", "id": "spawn-" + id, "tool": "spawnAgent", "status": "completed", "prompt": "Inspect " + id, "receiverThreadIds": []any{id}, "agentsStates": map[string]any{id: map[string]any{"status": "running"}}}})
			if strings.Contains(prompt, "poll") {
				polls[id] = 0
			}
		}
		notify("item/agentMessage/delta", thread, map[string]any{"itemId": "reply", "delta": "Main report"})
		notify("turn/completed", thread, map[string]any{"turn": map[string]any{"id": turn, "status": "completed"}})
		if strings.Contains(prompt, "poll") {
			return
		}
		if strings.Contains(prompt, "block") {
			time.Sleep(time.Hour)
		}
		time.Sleep(150 * time.Millisecond)
		if strings.Contains(prompt, "wait-progress") {
			for _, id := range []string{"child-one", "child-two"} {
				notify("item/completed", id, map[string]any{"item": map[string]any{"type": "agentMessage", "id": "progress-" + id, "text": "Checking child files"}})
			}
			time.Sleep(1500 * time.Millisecond)
		}
		for _, id := range []string{"child-one", "child-two"} {
			notify("item/completed", id, map[string]any{"item": map[string]any{"type": "commandExecution", "id": "read-" + id, "command": "cat child.go", "status": "completed"}})
			notify("item/completed", id, map[string]any{"item": map[string]any{"type": "agentMessage", "id": "message-" + id, "text": "Child summary " + id}})
			notify("turn/completed", id, map[string]any{"turn": map[string]any{"id": turn, "status": "completed"}})
		}
		return
	}
	for _, id := range []string{"one", "two"} {
		send(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "call-" + id, "name": "Agent", "input": map[string]any{"description": "Inspect " + id, "subagent_type": "Explore", "prompt": "Check files", "run_in_background": true}}}}})
		send(map[string]any{"type": "system", "subtype": "task_started", "task_id": id, "tool_use_id": "call-" + id, "description": "Inspect " + id, "task_type": "local_agent", "is_backgrounded": true})
		send(map[string]any{"type": "user", "toolUseResult": map[string]any{"isAsync": true, "agentId": id}, "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call-" + id, "content": "Launched"}}}})
	}
	send(map[string]any{"type": "assistant", "message": map[string]any{"id": "reply", "content": []any{map[string]any{"type": "text", "text": "Main report"}}, "stop_reason": "end_turn"}})
	send(map[string]any{"type": "result", "subtype": "success", "session_id": thread, "result": "Main report"})
	if strings.Contains(prompt, "block") {
		time.Sleep(time.Hour)
	}
	time.Sleep(150 * time.Millisecond)
	if strings.Contains(prompt, "wait-progress") {
		for _, id := range []string{"one", "two"} {
			send(map[string]any{"type": "system", "subtype": "task_progress", "task_id": id, "summary": "Checking child files", "last_tool_name": "Read"})
		}
		time.Sleep(1500 * time.Millisecond)
	}
	for _, id := range []string{"one", "two"} {
		send(map[string]any{"type": "system", "subtype": "task_progress", "task_id": id, "summary": "Checking child files", "last_tool_name": "Read", "usage": map[string]any{"tool_uses": 3}})
		send(map[string]any{"type": "system", "subtype": "task_notification", "task_id": id, "status": "completed", "summary": "Child summary " + id})
	}
	// Claude Code answers a notification that reaches an idle session with a
	// turn of its own, after the reply the user has already been given.
	send(map[string]any{"type": "system", "subtype": "init", "session_id": thread})
	send(map[string]any{"type": "assistant", "message": map[string]any{"id": "followup", "content": []any{map[string]any{"type": "text", "text": " Children finished"}}, "stop_reason": "end_turn"}})
	send(map[string]any{"type": "result", "subtype": "success", "session_id": thread, "result": " Children finished"})
}
