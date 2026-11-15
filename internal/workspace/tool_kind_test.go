package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestToolOperationsConformance(t *testing.T) {
	encoded, err := os.ReadFile("../../android/app/src/test/resources/tool-operations.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID, Name, Text string
		Input          json.RawMessage
		Operations     []chatmodel.ToolOperation
	}
	if err := json.Unmarshal(encoded, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			text := tc.Text
			if len(tc.Input) > 0 {
				text = string(tc.Input)
			}
			part := chatmodel.Part{Role: "assistant", Kind: "tool_use", Name: tc.Name, Text: text}
			summary := summarizeTool(part)
			if len(tc.Operations) != 0 && !reflect.DeepEqual(summary.Operations, tc.Operations) || len(summary.Operations) != len(tc.Operations) {
				t.Fatalf("got %+v; want %+v", summary.Operations, tc.Operations)
			}
			part.Tool = summary
			if cached := summarizeTool(part); !reflect.DeepEqual(cached.Operations, summary.Operations) {
				t.Fatal("cached operations changed")
			}
			chat := chatmodel.Session{ID: "chat", Transcript: chatmodel.Transcript{Parts: []chatmodel.Part{part}}, Turns: []chatmodel.Turn{{ID: "turn", PartEnd: 1}}}
			projected := slimToolDetails(chat).Transcript.Parts[0]
			if projected.Text != "" || !reflect.DeepEqual(projected.Tool.Operations, summary.Operations) || part.Text != text {
				t.Fatal("lazy projection lost operations or mutated original input")
			}
		})
	}
}

// The same fixture runs through the server, Web and native Android classifiers.
func TestToolClassificationConformance(t *testing.T) {
	encoded, err := os.ReadFile("../../android/app/src/test/resources/tool-classification.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID, Name, Text, Kind, Path, Query string
		Input                             json.RawMessage
	}
	if err := json.Unmarshal(encoded, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			text := tc.Text
			if len(tc.Input) > 0 {
				text = string(tc.Input)
			}
			part := chatmodel.Part{Role: "assistant", Kind: "tool_use", Name: tc.Name, Text: text}
			summary := summarizeTool(part)
			if summary.Kind != tc.Kind || summary.Path != tc.Path || summary.Query != tc.Query {
				t.Fatalf("got kind=%q path=%q query=%q; want %q %q %q", summary.Kind, summary.Path, summary.Query, tc.Kind, tc.Path, tc.Query)
			}
			part.Tool = summary
			if cached := summarizeTool(part); cached.Kind != tc.Kind || cached.Path != tc.Path || cached.Query != tc.Query {
				t.Fatal("cached summary changed", cached)
			}
		})
	}
}

func TestClaudeToolClassificationAfterNativeTranscriptNormalization(t *testing.T) {
	// Actual Claude assistant content/input shape, with descriptions and source
	// strings that used to override the tool's identity.
	tools := []struct {
		Name  string
		Input any
		Kind  string
	}{
		{"Bash", map[string]any{"command": "sh android/tools/run.sh npm test", "description": "Find browser and read files"}, "shell"},
		{"Bash", map[string]any{"command": "cat build/browser-report.json"}, "read"},
		{"Bash", map[string]any{"command": "rg TODO src | head -20", "description": "Search browser source"}, "search"},
		{"Read", map[string]any{"file_path": "android/tools/run.sh"}, "read"},
		{"Grep", map[string]any{"pattern": "find browser", "path": "src"}, "search"},
		{"Glob", map[string]any{"pattern": "**/browser*.js"}, "list"},
		{"Edit", map[string]any{"file_path": "src/tools.js", "old_string": "cat", "new_string": "find browser"}, "write"},
		{"Write", map[string]any{"file_path": "src/browser.js", "content": "find cat rg"}, "create"},
		{"WebSearch", map[string]any{"query": "find files"}, "web"},
	}
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	var lines []byte
	for _, tool := range tools {
		line, err := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": tool.Name, "name": tool.Name, "input": tool.Input}}}})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line...)
		lines = append(lines, '\n')
	}
	if err := os.WriteFile(path, lines, 0600); err != nil {
		t.Fatal(err)
	}
	transcript, err := agent.ParseSessionTranscript(chatmodel.BackendClaude, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Parts) != len(tools) {
		t.Fatalf("unexpected normalized parts: %+v", transcript.Parts)
	}
	for i, part := range transcript.Parts {
		if got := summarizeTool(part).Kind; got != tools[i].Kind {
			t.Errorf("%s: got %s want %s", part.Name, got, tools[i].Kind)
		}
	}
}

func TestCodexCommandsAfterNativeTranscriptNormalization(t *testing.T) {
	read := map[string]any{"type": "read", "cmd": "cat README.md", "name": "README.md", "path": "README.md"}
	search := map[string]any{"type": "search", "cmd": "rg TODO src", "query": "TODO", "path": "src"}
	for _, tc := range []struct {
		name              string
		args              []string
		parsed            []any
		kind, path, query string
		operations        int
	}{
		// Without Codex's own reading, the display classifier decides.
		{name: "content search", args: []string{"/bin/zsh", "-lc", "rg -n 'find|browser' src | head -20"}, kind: "search", path: "src", query: "find|browser"},
		{name: "file with spaces", args: []string{"/bin/zsh", "-lc", "cat 'file with spaces.txt'"}, kind: "read", path: "file with spaces.txt"},
		{name: "read and search", args: []string{"/bin/zsh", "-lc", "cat README.md\nrg TODO src"}, kind: "shell", operations: 2},
		{name: "mixed actions", args: []string{"/bin/zsh", "-lc", "git status --short\ncat browser-report.json\nrg TODO src | head -20"}, kind: "shell", operations: 3},
		{name: "file listing", args: []string{"/bin/zsh", "-lc", "rg --files src"}, kind: "list", path: "src"},
		// Codex's reading wins, even where the classifier would say otherwise.
		{name: "native single action", args: []string{"/bin/zsh", "-lc", "custom-search TODO"}, parsed: []any{search}, kind: "search", path: "src", query: "TODO"},
		{name: "native actions", args: []string{"/bin/zsh", "-lc", "cat README.md; rg TODO src | head"}, parsed: []any{read, search}, kind: "shell", operations: 2},
		// An unknown native action leaves display classification to the literal command.
		{name: "native unknown", args: []string{"/bin/zsh", "-lc", "head -20 README.md"}, parsed: []any{map[string]any{"type": "unknown", "cmd": "head -20 README.md"}}, kind: "read", path: "README.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "codex.jsonl")
			item := map[string]any{"type": "CommandExecution", "id": "command", "command": tc.args, "aggregated_output": "output", "status": "failed", "exit_code": 2}
			if tc.parsed != nil {
				item["parsed_cmd"] = tc.parsed
			}
			line, err := json.Marshal(map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": item}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(line, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			transcript, err := agent.ParseSessionTranscript(chatmodel.BackendCodex, path)
			if err != nil {
				t.Fatal(err)
			}
			if len(transcript.Parts) != 2 || transcript.Parts[0].ID != "command" || transcript.Parts[1].ID != "command" {
				t.Fatalf("unexpected parts: %+v", transcript.Parts)
			}
			summary := summarizeTool(transcript.Parts[0])
			if summary.Kind != tc.kind || summary.Path != tc.path || summary.Query != tc.query || len(summary.Operations) != tc.operations {
				t.Fatalf("got %+v for %q", summary, transcript.Parts[0].Text)
			}
			if summary.Status != chatmodel.ToolFailed || summary.ExitCode == nil || *summary.ExitCode != 2 {
				t.Fatalf("outcome lost: %+v", summary)
			}
		})
	}
}

func TestCodexUnknownCommandSummary(t *testing.T) {
	for _, tc := range []struct {
		name, script, kind, path, query string
		operations                      []chatmodel.ToolOperation
	}{
		{
			name: "sequential reads and command", kind: "shell",
			script: "cat internal/agent/codex_metadata.go\nsed -n '65,110p' internal/workspace/history_cache.go\ngit diff --stat",
			operations: []chatmodel.ToolOperation{
				{Kind: "read", Command: "cat internal/agent/codex_metadata.go", Path: "internal/agent/codex_metadata.go"},
				{Kind: "read", Command: "sed -n '65,110p' internal/workspace/history_cache.go", Path: "internal/workspace/history_cache.go"},
				{Kind: "shell", Command: "git diff --stat"},
			},
		},
		{
			name: "pipelines in sequence", kind: "shell",
			script: "cat README.md | head -20; rg TODO src | head -10 && git diff --stat",
			operations: []chatmodel.ToolOperation{
				{Kind: "read", Command: "cat README.md | head -20", Path: "README.md"},
				{Kind: "search", Command: "rg TODO src | head -10", Path: "src", Query: "TODO"},
				{Kind: "shell", Command: "git diff --stat"},
			},
		},
		{name: "search pipeline", script: "rg TODO src | head -20", kind: "search", path: "src", query: "TODO"},
		{name: "read pipeline", script: "cat README.md | grep TODO | head -20", kind: "read", path: "README.md"},
		{name: "list pipeline", script: "rg --files src | head -20", kind: "list", path: "src"},
		{name: "writing pipeline", script: "rg TODO src | sort -o sorted.txt", kind: "shell"},
		{name: "expansion", script: "cat $(find src); git diff --stat", kind: "shell"},
	} {
		for _, format := range []string{"app-server", "rollout"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				command := `/bin/zsh -lc "` + tc.script + `"`
				item := map[string]any{"id": "command", "status": "failed"}
				if format == "app-server" {
					item["type"], item["command"] = "commandExecution", command
					item["commandActions"] = []any{map[string]any{"type": "unknown", "command": command}}
					item["exitCode"], item["durationMs"] = 2, 123
				} else {
					item["type"], item["command"] = "CommandExecution", []string{"/bin/zsh", "-lc", tc.script}
					item["parsed_cmd"] = []any{map[string]any{"type": "unknown", "cmd": command}}
					item["exit_code"], item["duration_ms"] = 2, 123
				}
				line, err := json.Marshal(map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": item}})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "codex.jsonl")
				if err := os.WriteFile(path, append(line, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
				transcript, err := agent.ParseSessionTranscript(chatmodel.BackendCodex, path)
				if err != nil {
					t.Fatal(err)
				}
				if len(transcript.Parts) != 2 {
					t.Fatalf("call/result pairing lost: %+v", transcript.Parts)
				}
				chat := chatmodel.Session{ID: "chat", Transcript: transcript, Turns: []chatmodel.Turn{{ID: "turn", PartEnd: 2}}}
				part := slimToolDetails(chat).Transcript.Parts[0]
				summary := part.Tool
				if part.Text != "" || summary.Kind != tc.kind || summary.Path != tc.path || summary.Query != tc.query || !reflect.DeepEqual(summary.Operations, tc.operations) {
					t.Fatalf("client summary = %+v; want kind=%s path=%s query=%s operations=%+v", summary, tc.kind, tc.path, tc.query, tc.operations)
				}
				if summary.Status != chatmodel.ToolFailed || summary.ExitCode == nil || *summary.ExitCode != 2 || summary.DurationMs != 123 {
					t.Fatalf("native outcome lost: %+v", summary)
				}
				if native := transcript.Parts[0].Native; native.Kind != "shell" || len(native.Operations) != 0 {
					t.Fatalf("provider facts were mutated: %+v", native)
				}
			})
		}
	}
}

func TestNativeFileChangeSummary(t *testing.T) {
	files := []chatmodel.ToolFile{{Path: "/work/a/b/c/new.go", Kind: "create", Additions: 3}, {Path: "/work/old.go", Kind: "delete", Deletions: 2}}
	// The model made this change through a shell heredoc; the name says "shell".
	part := chatmodel.Part{ID: "call", Role: "assistant", Kind: "tool_use", Name: "exec_command", Text: `{"cmd":"apply_patch <<'PATCH'\n*** Begin Patch\nPATCH"}`,
		Native: &chatmodel.ToolNative{Status: chatmodel.ToolDeclined, Files: files}}
	summary := summarizeTool(part)
	if summary.Kind != "write" || summary.Path != "" || summary.Status != chatmodel.ToolDeclined || !reflect.DeepEqual(summary.Files, files) || summary.Changes == nil || *summary.Changes != (chatmodel.ToolChanges{Additions: 3, Deletions: 2}) || len(summary.Operations) != 0 {
		t.Fatalf("mixed file change: %+v", summary)
	}
	part.Native = &chatmodel.ToolNative{Status: chatmodel.ToolCompleted, Files: files[:1]}
	if summary = summarizeTool(part); summary.Kind != "create" || summary.Path != "…/b/c/new.go" || summary.Changes.Additions != 3 {
		t.Fatalf("single created file: %+v", summary)
	}
	// A cached summary must not outlive the native facts it was made from.
	part.Tool = summary
	part.Native = &chatmodel.ToolNative{Status: chatmodel.ToolFailed, Files: files[1:]}
	if summary = summarizeTool(part); summary.Kind != "delete" || summary.Status != chatmodel.ToolFailed {
		t.Fatalf("stale summary reused: %+v", summary)
	}
	encoded, err := json.Marshal(slimToolDetails(chatmodel.Session{ID: "chat", Transcript: chatmodel.Transcript{Parts: []chatmodel.Part{part}}, Turns: []chatmodel.Turn{{ID: "turn", PartEnd: 1}}}))
	if err != nil || !strings.Contains(string(encoded), `"status":"failed"`) || !strings.Contains(string(encoded), `"files":[{"path":"/work/old.go","kind":"delete","additions":0,"deletions":2}]`) || strings.Contains(string(encoded), "apply_patch") {
		t.Fatalf("lazy projection: %s %v", encoded, err)
	}
}

// Output of calls running at once interleaves; each chunk names its call.
func TestLiveToolOutputAndOutcomeJoinTheirCall(t *testing.T) {
	turn := chatmodel.Turn{}
	appendTurnPart(&turn, chatmodel.Part{ID: "a", Role: "assistant", Kind: "tool_use", Name: "shell", Text: "npm test", Native: &chatmodel.ToolNative{Status: chatmodel.ToolRunning}})
	appendTurnPart(&turn, chatmodel.Part{ID: "b", Role: "assistant", Kind: "tool_use", Name: "shell", Text: "go test ./...", Native: &chatmodel.ToolNative{Status: chatmodel.ToolRunning}})
	for _, chunk := range []chatmodel.Part{{ID: "a", Text: "a1 "}, {ID: "b", Text: "b1 "}, {ID: "a", Text: "a2"}, {ID: "b", Text: "b2"}} {
		chunk.Role, chunk.Kind = "tool", "tool_result"
		appendTurnPart(&turn, chunk)
	}
	exit := 1
	if !updateTurnTool(&turn, chatmodel.Part{ID: "a", Kind: "tool_use", Text: "npm test", Native: &chatmodel.ToolNative{Status: chatmodel.ToolFailed, ExitCode: &exit, DurationMs: 40}}) {
		t.Fatal("a finished call was not found")
	}
	if updateTurnTool(&turn, chatmodel.Part{ID: "missing", Kind: "tool_use"}) || updateTurnTool(&turn, chatmodel.Part{Kind: "tool_use"}) {
		t.Fatal("an unknown call was updated")
	}
	if len(turn.Parts) != 4 || turn.PartEnd != 4 {
		t.Fatalf("unexpected parts: %+v", turn.Parts)
	}
	results := map[string]string{}
	for _, part := range turn.Parts {
		if part.Kind == "tool_result" {
			results[part.ID] = part.Text
			if part.Tool == nil || part.Tool.Bytes != len(part.Text) {
				t.Fatalf("result summary is stale: %+v", part)
			}
		}
	}
	if results["a"] != "a1 a2" || results["b"] != "b1 b2" {
		t.Fatalf("output crossed between calls: %v", results)
	}
	appendTurnPart(&turn, chatmodel.Part{ID: "a", Role: "tool", Kind: "tool_result", Text: "final corrected output", Replace: true})
	if len(turn.Parts) != 4 || turn.Parts[2].Text != "final corrected output" || turn.Parts[3].Text != "b1 b2" {
		t.Fatalf("complete replacement duplicated or changed another call: %+v", turn.Parts)
	}
	if a, b := turn.Parts[0].Tool, turn.Parts[1].Tool; a.Status != chatmodel.ToolFailed || a.ExitCode == nil || *a.ExitCode != 1 || a.DurationMs != 40 || b.Status != chatmodel.ToolRunning {
		t.Fatalf("outcome: a=%+v b=%+v", a, b)
	}
	appendTurnPart(&turn, chatmodel.Part{ID: "empty-error", Role: "tool", Kind: "tool_result", Native: &chatmodel.ToolNative{Status: chatmodel.ToolFailed}})
	if len(turn.Parts) != 5 || turn.Parts[4].Tool.Status != chatmodel.ToolFailed {
		t.Fatal("empty error result lost its outcome")
	}
}
