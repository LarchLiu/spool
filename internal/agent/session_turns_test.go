package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestClaudeNativeTurnsUsePromptAndParentChains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	add := func(typ, id, parent, prompt string, content any, extra map[string]any) {
		t.Helper()
		record := map[string]any{"type": typ, "uuid": id, "parentUuid": parent, "promptId": prompt, "timestamp": "2026-10-28T00:00:00Z", "message": map[string]any{"role": typ, "content": content, "model": "claude-test"}}
		for k, v := range extra {
			record[k] = v
		}
		if err := enc.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	add("user", "u1", "", "p1", "first", nil)
	add("assistant", "a1", "u1", "", []any{map[string]any{"type": "tool_use", "id": "call-1", "name": "Read", "input": map[string]any{"file_path": "x"}}}, nil)
	add("user", "t1", "a1", "p1", []any{map[string]any{"type": "tool_result", "tool_use_id": "call-1", "content": "tool output"}}, nil)
	add("user", "interrupted", "t1", "p1", "[Request interrupted by user for tool use]", nil)
	add("user", "u2", "t1", "p2", "second", nil)
	add("assistant", "late1", "t1", "", []any{map[string]any{"type": "text", "text": "late first reply"}}, nil)
	add("user", "summary", "u2", "p2", "This session is being continued from a previous conversation", map[string]any{"isCompactSummary": true})
	add("user", "side", "u2", "side", "side conversation", map[string]any{"isSidechain": true})
	add("assistant", "a2", "u2", "", []any{map[string]any{"type": "text", "text": "second reply"}}, nil)
	add("user", "u3", "a2", "", "legacy third", nil)
	add("assistant", "a3", "u3", "", []any{map[string]any{"type": "text", "text": "third reply"}}, nil)
	f.Close()
	turns, err := IndexSessionTurns(chatmodel.BackendClaude, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 3 || turns[0].PromptID != "p1" || turns[1].PromptID != "p2" || turns[2].ID != "u3" {
		t.Fatalf("wrong native groups: %+v", turns)
	}
	for i, turn := range turns {
		transcript, err := ReadSessionTurn(chatmodel.BackendClaude, path, turn)
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for _, part := range transcript.Parts {
			text.WriteString(part.Text + "\n")
		}
		want := []string{"late first reply", "second reply", "third reply"}[i]
		if !strings.Contains(text.String(), want) || strings.Contains(text.String(), "side conversation") || strings.Contains(text.String(), "Request interrupted") || strings.Contains(text.String(), "continued from") {
			t.Fatalf("wrong group %d: %s", i, text.String())
		}
		if i == 0 && strings.Contains(text.String(), "second") {
			t.Fatal("a delayed parent-linked message swallowed a newer prompt")
		}
	}
}

func TestNativeImagePayloadSurvivesNormalization(t *testing.T) {
	url := "data:image/png;base64,aGVsbG8="
	codex := codexTranscriptParts(map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": url}}}, "")
	claude := claudeTranscriptParts("user", []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "aGVsbG8="}}}, "")
	if len(codex) != 1 || len(claude) != 1 || codex[0].ImageURL != url || claude[0].ImageURL != url {
		t.Fatal("native image bytes lost")
	}
	if nativeImageURL("file:///private/secret.png") != "" || nativeImageURL("https://example.com/a.png") != "" || nativeImageURL("data:image/svg+xml;base64,eA==") != "" {
		t.Fatal("unsafe image URL exposed")
	}
}

func TestClaudeToolUseDoesNotCompleteTurnOrCountBackgroundTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	records := []map[string]any{
		{"type": "user", "uuid": "user", "promptId": "prompt", "timestamp": "2026-10-28T00:00:00Z", "message": map[string]any{"content": "work"}},
		{"type": "assistant", "uuid": "tool", "parentUuid": "user", "perTurnEffort": "high", "timestamp": "2026-10-28T00:00:03Z", "message": map[string]any{"stop_reason": "tool_use", "model": "claude-test", "content": []any{map[string]any{"type": "tool_use", "name": "Read", "id": "call", "input": map[string]any{}}}}},
		{"type": "progress", "parentUuid": "tool", "timestamp": "2026-10-28T01:00:00Z"},
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(f)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	turns, err := IndexSessionTurns(chatmodel.BackendClaude, path, 0)
	if err != nil || len(turns) != 1 || turns[0].Status != chatmodel.StatusRunning || turns[0].FinishedAt-turns[0].StartedAt != 3000 || turns[0].ReasoningEffort != "high" {
		t.Fatalf("wrong incomplete turn metadata: %+v, %v", turns, err)
	}
}
