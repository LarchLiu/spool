package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
		{"Bash", map[string]any{"command": "rg TODO src | head -20", "description": "Search browser source"}, "read"},
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

func TestCodexReadCommandsAfterNativeTranscriptNormalization(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		kind string
	}{
		{"content search", []string{"/bin/zsh", "-lc", "rg -n 'find|browser' src | head -20"}, "read"},
		{"file with spaces", []string{"/bin/zsh", "-lc", "cat 'file with spaces.txt'"}, "read"},
		{"multiple reads", []string{"/bin/zsh", "-lc", "cat README.md\nrg TODO src"}, "read"},
		{"mixed actions", []string{"/bin/zsh", "-lc", "git status --short\ncat browser-report.json\nrg TODO src | head -20"}, "shell"},
		{"file listing", []string{"/bin/zsh", "-lc", "rg --files src"}, "list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "codex.jsonl")
			line, err := json.Marshal(map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{"type": "CommandExecution", "id": "command", "command": tc.args, "aggregated_output": "output"}}})
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
			if len(transcript.Parts) != 2 {
				t.Fatalf("unexpected parts: %+v", transcript.Parts)
			}
			if got := summarizeTool(transcript.Parts[0]).Kind; got != tc.kind {
				t.Fatalf("got %s want %s for %q", got, tc.kind, transcript.Parts[0].Text)
			}
		})
	}
}
