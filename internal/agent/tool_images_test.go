package agent

import (
	"encoding/json"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

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
