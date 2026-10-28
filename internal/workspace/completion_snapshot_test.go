package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestCompletedSnapshotWaitsForAllStreamedOutput(t *testing.T) {
	input := chatmodel.Part{Role: "user", Kind: "text", Text: "Run checks"}
	liveParts := []chatmodel.Part{
		input,
		{Role: "assistant", Kind: "text", Text: "I will "},
		{Role: "assistant", Kind: "text", Text: "run checks."},
		{Role: "assistant", Kind: "thinking", Text: "First "},
		{Role: "assistant", Kind: "thinking", Text: "think."},
		{Role: "assistant", Kind: "tool_use", Name: "exec_command", Text: "go test ./..."},
		{Role: "tool", Kind: "tool_result", Text: "chunk one\n"},
		{Role: "tool", Kind: "tool_result", Text: "chunk two\n"},
	}
	for _, status := range []chatmodel.SessionStatus{chatmodel.StatusCompleted, chatmodel.StatusStopped} {
		for _, scenario := range []string{"missing result", "partial result", "different result", "missing thinking", "newer answer", "no live answer"} {
			t.Run(string(status)+"/"+scenario, func(t *testing.T) {
				finished := chatmodel.Turn{ID: "turn-1", Status: status, InputEnd: 1, PartEnd: len(liveParts), Parts: liveParts}
				if scenario == "no live answer" {
					finished.Parts = append([]chatmodel.Part{input}, liveParts[3:]...)
					finished.PartEnd = len(finished.Parts)
				}
				populateTurnContent(&finished)
				reply := finished.Reply
				if scenario == "newer answer" || scenario == "no live answer" {
					reply += " All done."
				}
				fullParts := []chatmodel.Part{
					input,
					{Role: "assistant", Kind: "thinking", Text: "First think.", Model: "native-model"},
					{Role: "assistant", Kind: "tool_use", Name: "shell", Text: "go test ./...", Model: "native-model"},
					{Role: "tool", Kind: "tool_result", Text: "chunk one\nchunk two\n"},
					{Role: "assistant", Kind: "text", Text: reply},
				}
				partial := append([]chatmodel.Part(nil), fullParts...)
				switch scenario {
				case "missing result", "newer answer", "no live answer":
					partial = append(partial[:3], partial[4:]...)
				case "partial result":
					partial[3].Text = "chunk one\n"
				case "different result":
					partial = append([]chatmodel.Part(nil), liveParts...)
					partial[len(partial)-1].Text = "unrelated output with more bytes\n"
				case "missing thinking":
					partial = append(partial[:1], partial[2:]...)
				}
				snapshot := func(parts []chatmodel.Part) chatmodel.Session {
					turn := chatmodel.Turn{ID: finished.ID, Status: status, InputEnd: 1, PartEnd: len(parts), Parts: parts, Model: "native-model"}
					populateTurnContent(&turn)
					return chatmodel.Session{ID: "chat-1", Status: status, Turns: []chatmodel.Turn{turn}, Transcript: chatmodel.Transcript{Source: "codex-native", Parts: parts}}
				}
				app := &App{completed: map[string]completedTurn{"chat-1": {turn: finished}}}
				for range 2 {
					got := app.snapshot(snapshot(partial))
					if _, exists := app.completed[got.ID]; !exists {
						t.Fatal("fallback released before native tool/thinking content caught up")
					}
					last := got.Turns[0]
					if last.Reply != reply || last.Status != status || last.PartEnd != len(got.Transcript.Parts) || last.InputEnd != 1 {
						t.Fatalf("fallback lost answer, lifecycle or ranges: %+v", last)
					}
					var results, thoughts strings.Builder
					for _, part := range got.Transcript.Parts {
						if part.Kind == "tool_result" {
							results.WriteString(part.Text)
						}
						if part.Kind == "thinking" {
							thoughts.WriteString(part.Text)
						}
					}
					if results.String() != "chunk one\nchunk two\n" || thoughts.String() != "First think." {
						t.Fatal("fallback lost streamed output")
					}
				}
				got := app.snapshot(snapshot(fullParts))
				if _, exists := app.completed[got.ID]; exists {
					t.Fatal("normalized native content did not release the fallback")
				}
				if len(got.Transcript.Parts) != len(fullParts) || got.Turns[0].Reply != reply || finished.Parts[len(finished.Parts)-1].Text != "chunk two\n" {
					t.Fatal("native normalization duplicated content or mutated the fallback")
				}
			})
		}
	}
}

func TestCompletedSnapshotKeepsInputBeforeNativeWritesAnything(t *testing.T) {
	for _, input := range []chatmodel.Part{
		{Role: "user", Kind: "text", Text: "A failed prompt"},
		{Role: "user", Kind: "image", Text: "[image]"},
		{Role: "user", Kind: "context", Text: "notes.txt"},
	} {
		t.Run(input.Kind, func(t *testing.T) {
			turn := chatmodel.Turn{ID: "turn-1", Status: chatmodel.StatusFailed, InputEnd: 1, PartEnd: 1, Parts: []chatmodel.Part{input}}
			populateTurnContent(&turn)
			app := &App{completed: map[string]completedTurn{"chat-1": {turn: turn}}}
			got := app.snapshot(chatmodel.Session{ID: "chat-1", Status: turn.Status, Turns: []chatmodel.Turn{{ID: turn.ID, Status: turn.Status}}})
			if _, exists := app.completed[got.ID]; !exists || len(got.Transcript.Parts) != 1 || got.Transcript.Parts[0] != input {
				t.Fatal("empty native metadata discarded the failed input")
			}
		})
	}
}

func TestCompletedSnapshotAcceptsNativeFileChangeNormalization(t *testing.T) {
	for _, withCall := range []bool{false, true} {
		t.Run(map[bool]string{false: "event-only", true: "patch-call"}[withCall], func(t *testing.T) {
			patch := "*** Begin Patch\n*** Update File: main.go\n@@\n-old\n+new\n*** End Patch"
			diff := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new"
			parts := []chatmodel.Part{
				{Role: "user", Kind: "text", Text: "Update main.go"},
				{Role: "assistant", Kind: "tool_use", Name: "fileChange", Text: "Updated workspace files"},
				{Role: "tool", Kind: "tool_result", Text: diff},
				{Role: "assistant", Kind: "text", Text: "Updated."},
			}
			finished := chatmodel.Turn{ID: "turn-1", Status: chatmodel.StatusCompleted, InputEnd: 1, PartEnd: len(parts), Parts: parts}
			populateTurnContent(&finished)
			path := filepath.Join(t.TempDir(), "native.jsonl")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			encoder := json.NewEncoder(file)
			add := func(kind string, payload map[string]any) {
				t.Helper()
				if err := encoder.Encode(map[string]any{"type": kind, "payload": payload}); err != nil {
					t.Fatal(err)
				}
			}
			add("response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": finished.Prompt}}})
			if withCall {
				add("response_item", map[string]any{"type": "custom_tool_call", "call_id": "patch-1", "name": "apply_patch", "input": patch})
			}
			add("event_msg", map[string]any{"type": "item_completed", "item": map[string]any{"type": "FileChange", "id": "patch-1", "changes": []any{map[string]any{"path": "main.go", "diff": "@@ -1 +1 @@\n-old\n+new"}}}})
			if withCall {
				add("response_item", map[string]any{"type": "custom_tool_call_output", "call_id": "patch-1", "output": "Success. Updated main.go"})
			}
			add("response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": finished.Reply}}})
			transcript, err := agent.ParseSessionTranscript(chatmodel.BackendCodex, path)
			if err != nil {
				t.Fatal(err)
			}
			native := chatmodel.Turn{ID: finished.ID, Status: finished.Status, InputEnd: 1, PartEnd: len(transcript.Parts), Parts: transcript.Parts}
			populateTurnContent(&native)
			app := &App{completed: map[string]completedTurn{"chat-1": {turn: finished}}}
			got := app.snapshot(chatmodel.Session{ID: "chat-1", Status: native.Status, Turns: []chatmodel.Turn{native}, Transcript: transcript})
			if _, exists := app.completed[got.ID]; exists {
				t.Fatal("native file-change normalization did not release the fallback")
			}
			if len(got.Transcript.Parts) != 4 || got.Transcript.Parts[2].Text != diff || (withCall && got.Transcript.Parts[1].Text != patch) {
				t.Fatal("native file-change content was replaced by the live title", got.Transcript.Parts)
			}
		})
	}
}

func TestNativeOutputCoveragePreservesRepeatedAndOrderedContent(t *testing.T) {
	result := func(text string) chatmodel.Part {
		return chatmodel.Part{Role: "tool", Kind: "tool_result", Text: text}
	}
	for _, test := range []struct {
		name         string
		native, live []chatmodel.Part
		covered      bool
	}{
		{"joined native", []chatmodel.Part{result("firstsecond")}, []chatmodel.Part{result("first"), result("second")}, true},
		{"split native", []chatmodel.Part{result("first"), result("second")}, []chatmodel.Part{result("firstsecond")}, true},
		{"additional native", []chatmodel.Part{result("first extra second")}, []chatmodel.Part{result("first"), result("second")}, true},
		{"repeated content missing", []chatmodel.Part{result("same")}, []chatmodel.Part{result("same"), result("same")}, false},
		{"reordered output", []chatmodel.Part{result("secondfirst")}, []chatmodel.Part{result("first"), result("second")}, false},
		{"wrong kind", []chatmodel.Part{{Role: "assistant", Kind: "text", Text: "result"}}, []chatmodel.Part{result("result")}, false},
		{"different tool input", []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Text: "false"}}, []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Text: "ls"}}, false},
		{"missing repeated call", []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Text: "ls"}}, []chatmodel.Part{{Role: "assistant", Kind: "tool_use", Text: "ls"}, {Role: "assistant", Kind: "tool_use", Text: "ls"}}, false},
		{"no assistant answer", []chatmodel.Part{result("tool-only")}, []chatmodel.Part{result("tool-only")}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nativeOutputCovers(test.native, test.live); got != test.covered {
				t.Fatalf("coverage = %v, want %v", got, test.covered)
			}
		})
	}
}
