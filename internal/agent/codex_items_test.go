package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func decodeItem(t *testing.T, text string) map[string]any {
	t.Helper()
	var item map[string]any
	if err := json.Unmarshal([]byte(text), &item); err != nil {
		t.Fatal(err)
	}
	return item
}

func TestCodexReasoningContentFormats(t *testing.T) {
	for _, tc := range []struct{ name, item, text string }{
		{"native body", `{"summary":[],"content":["first","second"]}`, "first\n\nsecond"},
		{"rollout body", `{"content":[{"type":"reasoning_text","text":"first"},{"summary_text":"second"}]}`, "first\n\nsecond"},
		{"summary preferred", `{"summary":["summary"],"content":["body"]}`, "summary"},
		{"rollout summary", `{"summary":[{"type":"summary_text","text":"summary"}]}`, "summary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := decodeItem(t, tc.item)
			item["type"], item["id"] = "reasoning", "thought"
			parts := codexTranscriptParts(item, "test-model")
			if len(parts) != 1 || parts[0].Text != tc.text || parts[0].ID != "thought" || parts[0].Kind != "thinking" || parts[0].Model != "test-model" {
				t.Fatalf("reasoning: %+v", parts)
			}
		})
	}
}

// The same command, as app-server reports it live and in thread history, and
// as the rollout records it. Shapes are those written by codex-cli 0.166.0.
func TestCodexCommandItemKeepsNativeFacts(t *testing.T) {
	appServer := `{"type":"commandExecution","id":"call_1","command":"/bin/zsh -lc 'cat notes.txt | head -2 && rg -n beta . ; ls'","cwd":"/work","source":"agent","status":"failed",
		"commandActions":[{"type":"read","command":"cat notes.txt","name":"notes.txt","path":"/work/notes.txt"},{"type":"search","command":"rg -n beta .","query":"beta","path":"."},{"type":"listFiles","command":"ls","path":null}],
		"aggregatedOutput":"alpha\n","exitCode":1,"durationMs":1327}`
	rollout := `{"type":"CommandExecution","id":"call_1","command":["/bin/zsh","-lc","cat notes.txt | head -2 && rg -n beta . ; ls"],"cwd":"file:///work","source":"unified_exec_startup","status":"failed",
		"parsed_cmd":[{"type":"read","cmd":"cat notes.txt","name":"notes.txt","path":"/work/notes.txt"},{"type":"search","cmd":"rg -n beta .","query":"beta","path":"."},{"type":"list_files","cmd":"ls","path":null}],
		"aggregated_output":"alpha\n","exit_code":1,"duration":{"secs":1,"nanos":327047666}}`
	for name, text := range map[string]string{"app-server": appServer, "rollout": rollout} {
		t.Run(name, func(t *testing.T) {
			parts := codexTranscriptParts(decodeItem(t, text), "gpt")
			if len(parts) != 2 || parts[0].Kind != "tool_use" || parts[1].Kind != "tool_result" || parts[0].ID != "call_1" || parts[1].ID != "call_1" {
				t.Fatalf("call and result must share the item ID: %+v", parts)
			}
			if parts[0].Name != "shell" || !strings.Contains(parts[0].Text, "cat notes.txt | head -2") || parts[1].Text != "alpha\n" {
				t.Fatalf("unexpected text: %+v", parts)
			}
			native := parts[0].Native
			if native == nil || native.Status != chatmodel.ToolFailed || native.ExitCode == nil || *native.ExitCode != 1 || native.DurationMs != 1327 {
				t.Fatalf("status, exit code or duration lost: %+v", native)
			}
			want := []chatmodel.ToolOperation{
				{Kind: "read", Command: "cat notes.txt", Path: "/work/notes.txt"},
				{Kind: "search", Command: "rg -n beta .", Path: ".", Query: "beta"},
				{Kind: "list", Command: "ls"},
			}
			if native.Kind != "shell" || !reflect.DeepEqual(native.Operations, want) {
				t.Fatalf("kind %q operations %+v", native.Kind, native.Operations)
			}
		})
	}
}

func TestCodexCommandActionsSingleAndUnknown(t *testing.T) {
	single := codexTranscriptParts(decodeItem(t, `{"type":"commandExecution","id":"a","command":"sed -n '1,2p' notes.txt","status":"completed","exitCode":0,
		"commandActions":[{"type":"read","command":"sed -n '1,2p' notes.txt","name":"notes.txt","path":"/work/notes.txt"}]}`), "")
	if native := single[0].Native; native.Kind != "read" || native.Path != "/work/notes.txt" || len(native.Operations) != 0 || native.Status != chatmodel.ToolCompleted || *native.ExitCode != 0 {
		t.Fatalf("single action: %+v", native)
	}
	unknown := codexTranscriptParts(decodeItem(t, `{"type":"commandExecution","id":"b","command":"npm test","status":"inProgress","commandActions":[{"type":"unknown","command":"npm test"}]}`), "")
	if native := unknown[0].Native; native.Kind != "shell" || native.Path != "" || native.Status != chatmodel.ToolRunning || native.ExitCode != nil || len(unknown) != 1 {
		t.Fatalf("unknown action: %+v", native)
	}
	// A newer action type must not be shown as something it is not.
	future := codexTranscriptParts(decodeItem(t, `{"type":"commandExecution","id":"c","command":"x","commandActions":[{"type":"read","command":"cat a","path":"a"},{"type":"format","command":"x"}]}`), "")
	if native := future[0].Native; native == nil || native.Kind != "shell" || len(native.Operations) != 0 {
		t.Fatalf("unrecognized actions were classified: %+v", native)
	}
}

func TestCodexFileChangeItemKeepsEachFile(t *testing.T) {
	appServer := `{"type":"fileChange","id":"call_2","status":"completed","changes":[
		{"path":"/work/added.txt","kind":{"type":"add"},"diff":"one\ntwo\n"},
		{"path":"/work/notes.txt","kind":{"type":"update","move_path":null},"diff":"@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma\n"},
		{"path":"/work/old.txt","kind":{"type":"delete"},"diff":"gone\n"}]}`
	rollout := `{"type":"FileChange","id":"call_2","status":"completed","changes":{
		"/work/notes.txt":{"type":"update","unified_diff":"@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA\n gamma\n","move_path":null},
		"/work/added.txt":{"type":"add","content":"one\ntwo\n"},
		"/work/old.txt":{"type":"delete","content":"gone\n"}},"stdout":"Success.","stderr":""}`
	want := []chatmodel.ToolFile{
		{Path: "/work/added.txt", Kind: "create", Additions: 2},
		{Path: "/work/notes.txt", Kind: "write", Additions: 1, Deletions: 1},
		{Path: "/work/old.txt", Kind: "delete", Deletions: 1},
	}
	for name, text := range map[string]string{"app-server": appServer, "rollout": rollout} {
		t.Run(name, func(t *testing.T) {
			parts := codexTranscriptParts(decodeItem(t, text), "")
			if len(parts) != 2 || parts[0].ID != "call_2" || parts[1].ID != "call_2" || parts[0].Name != "fileChange" {
				t.Fatalf("unexpected parts: %+v", parts)
			}
			if native := parts[0].Native; native == nil || native.Status != chatmodel.ToolCompleted || !reflect.DeepEqual(native.Files, want) {
				t.Fatalf("files: %+v", native)
			}
			for _, header := range []string{"*** Add File: /work/added.txt", "*** Update File: /work/notes.txt", "*** Delete File: /work/old.txt"} {
				if !strings.Contains(parts[0].Text, header) {
					t.Fatalf("call text lacks %q: %q", header, parts[0].Text)
				}
			}
			diff := parts[1].Text
			// Added and deleted files carry content, which must become real hunks.
			for _, hunk := range []string{"--- /dev/null\n+++ b//work/added.txt\n@@ -0,0 +1,2 @@\n+one\n+two", "+++ /dev/null\n@@ -1,1 +0,0 @@\n-gone", "--- a//work/notes.txt\n+++ b//work/notes.txt\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+BETA"} {
				if !strings.Contains(diff, hunk) {
					t.Fatalf("diff lacks %q:\n%s", hunk, diff)
				}
			}
		})
	}
}

func TestCodexFileChangeMoveAndDeclined(t *testing.T) {
	parts := codexTranscriptParts(decodeItem(t, `{"type":"fileChange","id":"m","status":"declined","changes":[{"path":"a.txt","kind":{"type":"update","move_path":"b.txt"},"diff":"@@ -1 +1 @@\n-a\n+b\n\nMoved to: b.txt"}]}`), "")
	native := parts[0].Native
	if native.Status != chatmodel.ToolDeclined || !reflect.DeepEqual(native.Files, []chatmodel.ToolFile{{Path: "a.txt", Kind: "write", MovePath: "b.txt", Additions: 1, Deletions: 1}}) {
		t.Fatalf("move: %+v", native)
	}
	if !strings.Contains(parts[0].Text, "*** Move to: b.txt") || strings.Contains(parts[1].Text, "Moved to:") {
		t.Fatalf("move text: %+v", parts)
	}
}

func TestCodexFileChangesPreservePatchLikeContentAndCountHunkLines(t *testing.T) {
	parts := codexTranscriptParts(decodeItem(t, `{"type":"fileChange","id":"patch-like","status":"completed","changes":[
		{"path":"patch.txt","kind":{"type":"add"},"diff":"--- heading\n+++ literal\n@@ content\n"},
		{"path":"old.txt","kind":{"type":"delete"},"diff":"@@ old content\n"},
		{"path":"update.txt","kind":{"type":"update"},"diff":"@@ -1 +1 @@\n--- old heading\n+++ new heading\n"},
		{"path":"empty.txt","kind":{"type":"add"},"diff":""}]}`), "")
	files := parts[0].Native.Files
	if files[0].Additions != 3 || files[1].Deletions != 1 || files[2].Additions != 1 || files[2].Deletions != 1 || files[3].Additions != 0 {
		t.Fatalf("native content confused with diff syntax: %+v", files)
	}
	if !strings.Contains(parts[1].Text, "+--- heading\n++++ literal\n+@@ content") || !strings.Contains(parts[1].Text, "-@@ old content") {
		t.Fatalf("literal patch-like content was lost: %s", parts[1].Text)
	}
}

func TestCodexOtherToolItemsAreShown(t *testing.T) {
	for _, tc := range []struct {
		name, item, tool, input, output, status, kind, query string
		images                                               int
	}{
		{name: "mcp", item: `{"type":"mcpToolCall","id":"1","server":"docs","tool":"search","status":"completed","arguments":{"q":"spool"},"result":{"content":[{"type":"text","text":"found"},{"type":"image","mimeType":"image/png","data":"eA=="}],"structuredContent":null},"error":null,"durationMs":12}`,
			tool: "docs.search", input: `"q": "spool"`, output: "found\n[image]", status: chatmodel.ToolCompleted, images: 1},
		{name: "mcp error", item: `{"type":"mcpToolCall","id":"2","server":"docs","tool":"search","status":"failed","arguments":{},"result":null,"error":{"message":"timed out"}}`,
			tool: "docs.search", input: "{}", output: "timed out", status: chatmodel.ToolFailed},
		{name: "dynamic", item: `{"type":"dynamicToolCall","id":"3","namespace":"app","tool":"lookup","arguments":{"id":7},"status":"completed","contentItems":[{"type":"inputText","text":"seven"}],"success":false}`,
			tool: "app.lookup", input: `"id": 7`, output: "seven", status: chatmodel.ToolFailed},
		{name: "web search", item: `{"type":"webSearch","id":"4","query":"go generics","action":{"type":"search","query":"go generics","queries":null},"results":null}`,
			tool: "web_search", input: `"query": "go generics"`, kind: "web", query: "go generics"},
		{name: "rollout web search", item: `{"type":"web_search_call","id":"5","status":"completed","action":{"type":"search","query":"rust traits"}}`,
			tool: "web_search", input: `"query": "rust traits"`, status: chatmodel.ToolCompleted, kind: "web", query: "rust traits"},
		{name: "collab", item: `{"type":"collabAgentToolCall","id":"6","tool":"spawnAgent","status":"completed","senderThreadId":"a","receiverThreadIds":["b"],"prompt":"review","model":null,"agentsStates":{"b":{"status":"completed","message":"looks fine"}}}`,
			tool: "spawn_agent", input: `"prompt": "review"`, output: "completed: looks fine", status: chatmodel.ToolCompleted},
		{name: "output only", item: `{"type":"functionCallOutput","id":"7","name":"get_goal","namespace":null,"output":"no goal"}`,
			tool: "get_goal", output: "no goal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := codexTranscriptParts(decodeItem(t, tc.item), "")
			if len(parts) == 0 || parts[0].Kind != "tool_use" || parts[0].Name != tc.tool || parts[0].ID == "" || !strings.Contains(parts[0].Text, tc.input) {
				t.Fatalf("call: %+v", parts)
			}
			output, images := "", 0
			if len(parts) > 1 {
				if parts[1].ID != parts[0].ID {
					t.Fatalf("result does not name its call: %+v", parts)
				}
				output, images = parts[1].Text, len(parts[1].Images)
			}
			if output != tc.output || images != tc.images {
				t.Fatalf("output %q images %d", output, images)
			}
			native := parts[0].Native
			if native == nil {
				native = &chatmodel.ToolNative{}
			}
			if native.Status != tc.status || native.Kind != tc.kind || native.Query != tc.query {
				t.Fatalf("native: %+v", native)
			}
		})
	}
	// A rollout's function_call_output has a call_id and belongs to its own call.
	if parts := codexTranscriptParts(decodeItem(t, `{"type":"function_call_output","call_id":"c","output":"x"}`), ""); len(parts) != 1 || parts[0].Kind != "tool_result" || parts[0].ID != "c" {
		t.Fatalf("rollout output: %+v", parts)
	}
}

// Two commands running at once: each result must land on its own call.
func TestCodexRolloutPairsParallelCommandsByID(t *testing.T) {
	lines := []map[string]any{
		{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_a", "arguments": `{"cmd":"cat a.txt"}`}},
		{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call_b", "arguments": `{"cmd":"npm test"}`}},
		{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{"type": "CommandExecution", "id": "call_a", "command": []any{"/bin/zsh", "-lc", "cat a.txt"},
			"parsed_cmd": []any{map[string]any{"type": "read", "cmd": "cat a.txt", "name": "a.txt", "path": "a.txt"}}, "status": "completed", "aggregated_output": "A", "exit_code": 0}}},
		{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{"type": "CommandExecution", "id": "call_b", "command": []any{"/bin/zsh", "-lc", "npm test"},
			"parsed_cmd": []any{map[string]any{"type": "unknown", "cmd": "npm test"}}, "status": "failed", "aggregated_output": "B", "exit_code": 1}}},
		{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "call_b", "output": "raw B"}},
		{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "call_a", "output": "raw A"}},
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	var data []byte
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, encoded...), '\n')
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	transcript, err := ParseSessionTranscript(chatmodel.BackendCodex, path)
	if err != nil {
		t.Fatal(err)
	}
	calls, results := map[string]chatmodel.Part{}, map[string]string{}
	for _, part := range transcript.Parts {
		if part.Kind == "tool_use" {
			calls[part.ID] = part
		} else if part.Kind == "tool_result" {
			results[part.ID] = part.Text
		}
	}
	a, b := calls["call_a"], calls["call_b"]
	// The model-visible output recorded after the event is kept as the result.
	if !strings.Contains(a.Text, "cat a.txt") || !strings.Contains(b.Text, "npm test") || results["call_a"] != "raw A" || results["call_b"] != "raw B" {
		t.Fatalf("commands or outputs crossed: %+v", transcript.Parts)
	}
	if a.Native == nil || a.Native.Kind != "read" || a.Native.Path != "a.txt" || a.Native.Status != chatmodel.ToolCompleted || b.Native == nil || b.Native.Kind != "shell" || b.Native.Status != chatmodel.ToolFailed || *b.Native.ExitCode != 1 {
		t.Fatalf("native facts crossed: a=%+v b=%+v", a.Native, b.Native)
	}
}

func TestClaudeToolResultKeepsErrorFlag(t *testing.T) {
	parts := claudeTranscriptParts("user", []any{
		map[string]any{"type": "tool_result", "tool_use_id": "a", "content": "boom", "is_error": true},
		map[string]any{"type": "tool_result", "tool_use_id": "b", "content": "fine", "is_error": false},
		map[string]any{"type": "tool_result", "tool_use_id": "c", "content": "unknown"},
		map[string]any{"type": "tool_result", "tool_use_id": "d", "content": "", "is_error": true},
	}, "")
	if len(parts) != 4 || parts[0].Native == nil || parts[0].Native.Status != chatmodel.ToolFailed || parts[1].Native == nil || parts[1].Native.Status != chatmodel.ToolCompleted || parts[2].Native != nil || parts[3].Native.Status != chatmodel.ToolFailed {
		t.Fatalf("error flags: %+v", parts)
	}
}
