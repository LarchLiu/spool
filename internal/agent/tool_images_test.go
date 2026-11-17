package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestReadCodexToolImagesKeepsTheCallAndTurnIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	image := "data:image/png;base64,aGVsbG8="
	output := func(id, image string) map[string]any {
		return map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": id, "output": []any{map[string]any{"type": "input_image", "image_url": image}}}}
	}
	writeTitleRecords(t, path,
		map[string]any{"type": "turn_context", "payload": map[string]any{"turn_id": "other-turn"}},
		output("image-call", "data:image/png;base64,b3RoZXI="),
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "wanted-turn"}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "large-other-call", "output": strings.Repeat("x", 256*1024)}},
		output("other-image-call", "data:image/png;base64,b3RoZXI="), output("image-call", image))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		turn, call string
		want       []string
	}{
		{"wanted-turn", "image-call", []string{image}},
		{"missing-turn", "image-call", nil},
		{"wanted-turn", "missing-call", nil},
	} {
		got, err := ReadCodexToolImages(context.Background(), path, test.turn, test.call)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%s/%s: got %v, %v", test.turn, test.call, got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadCodexToolImages(ctx, path, "wanted-turn", "image-call"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled image request continued", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("image read changed the native rollout")
	}
}

func TestImageToolsNormalizeNativeCodexAndClaudeRecords(t *testing.T) {
	for _, typ := range []string{"imageView", "image_view"} {
		parts := codexTranscriptParts(map[string]any{"type": typ, "path": "/work/page 1.png", "id": "image-call"}, "model")
		if len(parts) != 1 || parts[0].Name != "view_image" || parts[0].Kind != "tool_use" || parts[0].ID != "image-call" {
			t.Fatal("native Codex image view lost", parts)
		}
	}
	url := "data:image/png;base64,aGVsbG8="
	codex := codexTranscriptParts(map[string]any{"type": "function_call_output", "call_id": "codex-image", "output": []any{map[string]any{"type": "input_image", "image_url": url}}}, "")
	if len(codex) != 1 || codex[0].ID != "codex-image" || codex[0].Text != "[image]" || len(codex[0].Images) != 1 || codex[0].Images[0] != url {
		t.Fatal("Codex image result was left as raw JSON", codex)
	}
	parts := claudeTranscriptParts("assistant", []any{map[string]any{"type": "tool_use", "id": "read-image", "name": "Read", "input": map[string]any{"file_path": "page.png"}}}, "model")
	if len(parts) != 1 || parts[0].ID != "read-image" || parts[0].Name != "Read" {
		t.Fatal("Claude image call lost its identity", parts)
	}
	result := map[string]any{"type": "tool_result", "tool_use_id": "read-image", "content": []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "aGVsbG8="}}}}
	part := claudeToolResultPart(result)
	if part.ID != "read-image" || part.Role != "tool" || len(part.Images) != 1 || part.Images[0] != url {
		t.Fatal("Claude image result payload lost", part)
	}
	record := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{result}}}
	line, _ := json.Marshal(record)
	transcript, err := parseTranscriptReader(chatmodel.BackendClaude, strings.NewReader(string(line)+"\n"), "", false)
	if err != nil || len(transcript.Parts) != 1 || len(transcript.Parts[0].Images) != 1 {
		t.Fatal("native history discarded image results", transcript, err)
	}
}
