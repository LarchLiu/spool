package agentfixture

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

type Fixture struct{ ID, Binary, Directory string }

func New(t *testing.T) Fixture {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("process fixture requires /bin/sh")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := filepath.Join(root, "fake-agent")
	script := "#!/bin/sh\nexport SPOOL_TEST_AGENT=1\ncd '" + strings.ReplaceAll(root, "'", "'\\''") + "' || exit 2\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run=^TestAgentHelperProcess$ -- \"$@\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return Fixture{ID: chatmodel.NewID(), Binary: binary, Directory: root}
}

func Log(t *testing.T, directory string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "fixture-log.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func Count(t *testing.T, directory, method string) int {
	t.Helper()
	n := 0
	for _, entry := range Log(t, directory) {
		if entry["method"] == method {
			n++
		}
	}
	return n
}

func Catalog() chatmodel.CodexCatalog {
	return chatmodel.CodexCatalog{
		DefaultModel: "test-model", DefaultEffort: "high",
		Models: []chatmodel.CodexModel{
			{Model: "test-model", DisplayName: "Test Model", DefaultEffort: "medium", Efforts: []chatmodel.ReasoningOption{{Effort: "low"}, {Effort: "medium"}, {Effort: "high"}, {Effort: "xhigh"}}},
			{Model: "other-model", DisplayName: "Other Model", DefaultEffort: "low", Efforts: []chatmodel.ReasoningOption{{Effort: "low"}, {Effort: "high"}}},
			{Model: "fixed-model", DisplayName: "Fixed Model", DefaultEffort: "none", Efforts: []chatmodel.ReasoningOption{}},
		},
	}
}
func Run(t *testing.T) {
	if os.Getenv("SPOOL_TEST_AGENT") != "1" {
		return
	}
	logFile, err := os.OpenFile("fixture-log.jsonl", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(2)
	}
	defer logFile.Close()
	var outputMu sync.Mutex
	send := func(value any) {
		outputMu.Lock()
		defer outputMu.Unlock()
		_ = json.NewEncoder(os.Stdout).Encode(value)
	}
	log := func(value map[string]any) { value["pid"] = os.Getpid(); _ = json.NewEncoder(logFile).Encode(value) }
	log(map[string]any{"method": "spawn", "args": os.Args})
	for _, arg := range os.Args {
		if arg == "--version" {
			fmt.Println("fixture-agent 1.0.0")
			return
		}
	}
	codex := false
	for _, arg := range os.Args {
		if arg == "app-server" {
			codex = true
		}
	}
	thread := "native-session-123"
	for i, arg := range os.Args {
		if arg == "--resume" && i+1 < len(os.Args) {
			thread = os.Args[i+1]
		}
	}
	claudeModel := "test-model"
	claudeEffort := "high"
	claudeMode := "acceptEdits"
	turn := 0
	var chatter sync.Once
	scanner := scannerFor(os.Stdin)
	for scanner.Scan() {
		var value map[string]any
		if json.Unmarshal(scanner.Bytes(), &value) != nil {
			os.Exit(3)
		}
		method := StringValue(value["method"])
		if !codex {
			method = StringValue(value["type"])
		}
		log(map[string]any{"method": method, "value": value, "effort": claudeEffort, "permission_mode": claudeMode})
		params, _ := value["params"].(map[string]any)
		if codex {
			switch method {
			case "initialize":
				send(map[string]any{"id": value["id"], "result": map[string]any{}})
			case "config/read":
				send(map[string]any{"id": value["id"], "result": map[string]any{"config": map[string]any{"model": "test-model", "model_reasoning_effort": "high"}}})
			case "configRequirements/read":
				var requirements any
				if data, err := os.ReadFile("fixture-policy.json"); err == nil {
					_ = json.Unmarshal(data, &requirements)
				}
				send(map[string]any{"id": value["id"], "result": map[string]any{"requirements": requirements}})
			case "experimentalFeature/list":
				if cursor, exists := params["cursor"]; exists && cursor == "" {
					send(map[string]any{"id": value["id"], "error": map[string]any{"code": -32600, "message": "invalid cursor"}})
					continue
				}
				send(map[string]any{"id": value["id"], "result": map[string]any{"data": []any{map[string]any{"name": "guardian_approval", "enabled": true}}, "nextCursor": nil}})
			case "model/list":
				models := []chatmodel.CodexModel{Catalog().Models[0]}
				var cursor any = "page-2"
				if params["cursor"] == "page-2" {
					models = Catalog().Models[1:]
					cursor = nil
				}
				send(map[string]any{"id": value["id"], "result": map[string]any{"data": models, "nextCursor": cursor}})
			case "thread/start", "thread/resume":
				if method == "thread/resume" {
					thread = StringValue(params["threadId"])
				}
				model := StringValue(params["model"])
				if model == "" {
					model = "test-model"
				}
				send(map[string]any{"id": value["id"], "result": map[string]any{"thread": map[string]any{"id": thread}, "model": model, "approvalsReviewer": params["approvalsReviewer"]}})
				// Replayed output must not be attached to the next user turn.
				send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": thread, "turnId": "old-turn", "itemId": "old-item", "delta": "STALE"}})
			case "turn/start":
				// Handled below.
			default:
				continue
			}
			if method != "turn/start" {
				continue
			}
		} else if method == "control_request" {
			if NestedString(value, "request", "subtype") == "set_permission_mode" {
				mode := NestedString(value, "request", "mode")
				if _, err := os.Stat("reject-auto"); err == nil && mode == "auto" {
					send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": value["request_id"], "error": "fixture project policy denied auto"}})
					continue
				}
				claudeMode = mode
				send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": value["request_id"], "response": map[string]any{"mode": mode}}})
				continue
			}
			if NestedString(value, "request", "subtype") == "set_model" && NestedString(value, "request", "model") == "reject-model" {
				send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": value["request_id"], "error": "fixture model rejected"}})
				continue
			}
			if NestedString(value, "request", "subtype") == "set_model" {
				claudeModel = NestedString(value, "request", "model")
				if claudeModel == "" {
					claudeModel = "test-model"
				}
			}
			if NestedString(value, "request", "subtype") == "apply_flag_settings" {
				if claudeModel == "reject-effort" {
					send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": value["request_id"], "error": "fixture effort rejected"}})
					continue
				}
				claudeEffort = NestedString(value, "request", "settings", "effortLevel")
				if claudeEffort == "" {
					claudeEffort = "high"
				}
			}
			body := map[string]any{"models": []any{map[string]any{"value": "test-claude", "displayName": "Test Claude", "supportedEffortLevels": []string{"low", "high"}}}}
			if NestedString(value, "request", "subtype") == "get_settings" {
				effort := claudeEffort
				if claudeModel == "clamp-effort" {
					effort = "low"
				}
				body = map[string]any{"applied": map[string]any{"model": claudeModel, "effort": effort}}
			}
			send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": value["request_id"], "response": body}})
			continue
		} else if method != "user" {
			continue
		}
		turn++
		turnID := fmt.Sprintf("fixture-turn-%d", turn)
		prompt := NestedString(value, "message", "content")
		if parts, ok := value["message"].(map[string]any); ok && prompt == "" {
			if blocks, ok := parts["content"].([]any); ok && len(blocks) > 0 {
				first, _ := blocks[0].(map[string]any)
				prompt = StringValue(first["text"])
			}
		}
		if codex {
			input, _ := params["input"].([]any)
			if len(input) > 0 {
				part, _ := input[0].(map[string]any)
				prompt = StringValue(part["text"])
			}
			if strings.Contains(prompt, "queued-delta") {
				send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{
					"threadId": thread, "turnId": turnID, "itemId": "reply", "delta": "Queued ",
				}})
			}
			send(map[string]any{"id": value["id"], "result": map[string]any{"turn": map[string]any{"id": turnID, "status": "inProgress"}}})
		} else if turn == 1 {
			send(map[string]any{"type": "system", "subtype": "init", "session_id": thread, "model": claudeModel})
		}
		if strings.Contains(prompt, "spawn-child") {
			child := exec.Command("sleep", "30")
			if child.Start() != nil {
				os.Exit(4)
			}
			_ = os.WriteFile("child-pid.txt", []byte(fmt.Sprint(child.Process.Pid)), 0o600)
		}
		if strings.Contains(prompt, "silent-task") {
			time.Sleep(300 * time.Millisecond)
		}
		notify := func(method string, params map[string]any) {
			params["threadId"], params["turnId"] = thread, turnID
			send(map[string]any{"method": method, "params": params})
		}
		text := "Hello world"
		if strings.Contains(prompt, "block-task") || strings.Contains(prompt, "exit-task") {
			text = "Partial reply"
		}
		if codex {
			notify := func(method string, params map[string]any) {
				params["threadId"], params["turnId"] = thread, turnID
				send(map[string]any{"method": method, "params": params})
			}
			notify("item/started", map[string]any{"item": map[string]any{"id": "reasoning", "type": "reasoning", "summary": []any{}}})
			notify("item/reasoning/summaryTextDelta", map[string]any{"itemId": "reasoning", "summaryIndex": 0, "delta": "Checking the fixture"})
			notify("item/reasoning/summaryTextDelta", map[string]any{"itemId": "reasoning", "summaryIndex": 0, "delta": " before running the tool."})
			notify("item/completed", map[string]any{"item": map[string]any{"id": "reasoning", "type": "reasoning", "summary": []any{map[string]any{"text": "Checking the fixture before running the tool."}}}})
			notify("item/started", map[string]any{"item": map[string]any{"id": "tool", "type": "commandExecution", "command": []string{"git", "status", "--short"}}})
			notify("item/commandExecution/outputDelta", map[string]any{"itemId": "tool", "delta": "fixture output\n"})
			if strings.Contains(prompt, "file-change") {
				notify("item/started", map[string]any{"item": map[string]any{"id": "file-change", "type": "fileChange"}})
				notify("item/fileChange/patchUpdated", map[string]any{"itemId": "file-change", "changes": []any{map[string]any{"path": "main.go", "diff": "@@ -1 +1 @@\n-old\n+new"}}})
				notify("item/completed", map[string]any{"item": map[string]any{"id": "file-change", "type": "fileChange", "changes": []any{map[string]any{"path": "main.go", "diff": "@@ -1 +1 @@\n-old\n+new"}}}})
			}
			kind := "agentMessage"
			if strings.Contains(prompt, "full-only") {
				kind = "agent_message"
			} else {
				notify("item/agentMessage/delta", map[string]any{"itemId": "reply", "delta": text})
			}
			if strings.Contains(prompt, "queued-delta") {
				text = "Queued " + text
			}
			notify("item/completed", map[string]any{"item": map[string]any{"id": "reply", "type": kind, "text": text}})
			if strings.Contains(prompt, "multiple-items") {
				notify("item/completed", map[string]any{"item": map[string]any{"id": "reply-2", "type": "agentMessage", "text": " More"}})
			}
		} else {
			if !strings.Contains(prompt, "full-only") {
				send(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": text}}})
			}
			content := []any{map[string]any{"type": "tool_use", "id": "tool", "name": "Read"}}
			if strings.Contains(prompt, "thinking-tool") {
				content = append([]any{map[string]any{"type": "thinking", "thinking": "Fixture thought"}}, content...)
			}
			send(map[string]any{"type": "assistant", "message": map[string]any{"model": claudeModel, "content": content}})
			if strings.Contains(prompt, "structured-diff") {
				send(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "tool", "content": "edited"}}}, "toolUseResult": map[string]any{"filePath": "main.go", "structuredPatch": []any{map[string]any{"oldStart": 1, "oldLines": 1, "newStart": 1, "newLines": 1, "lines": []any{"-old", "+new"}}}}})
			}
		}
		if strings.Contains(prompt, "exit-task") {
			os.Exit(7)
		}
		if strings.Contains(prompt, "block-task") {
			time.Sleep(time.Hour)
		}
		if strings.Contains(prompt, "permission-test") {
			if codex {
				send(map[string]any{"id": "permission-1", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": thread, "turnId": turnID, "command": "echo permission-check", "reason": "Fixture permission check"}})
			} else {
				send(map[string]any{"type": "control_request", "request_id": "permission-1", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "input": map[string]any{"command": "echo permission-check"}}})
			}
			if !scanner.Scan() {
				os.Exit(8)
			}
			var reply map[string]any
			_ = json.Unmarshal(scanner.Bytes(), &reply)
			log(map[string]any{"method": "permission-reply", "value": reply})
		}
		if codex {
			status := "completed"
			if strings.Contains(prompt, "failed-turn") {
				status = "failed"
			}
			notify("turn/completed", map[string]any{"turn": map[string]any{"id": turnID, "status": status, "error": map[string]any{"message": "fixture failure"}}})
		} else {
			subtype := "success"
			if strings.Contains(prompt, "failed-turn") {
				subtype = "error_during_execution"
			}
			send(map[string]any{"type": "result", "subtype": subtype, "session_id": thread, "result": text, "errors": []string{"fixture failure"}})
		}
		if strings.Contains(prompt, "idle-chatter") {
			chatter.Do(func() {
				go func() {
					for {
						time.Sleep(10 * time.Millisecond)
						send(map[string]any{"method": "thread/status/changed", "type": "system", "subtype": "heartbeat"})
					}
				}()
			})
		}
	}
	os.Exit(0)
}

func scannerFor(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 24*1024*1024)
	return scanner
}

func StringValue(value any) string {
	text, _ := value.(string)
	return text
}

func NestedString(value map[string]any, keys ...string) string {
	var current any = value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	return StringValue(current)
}
