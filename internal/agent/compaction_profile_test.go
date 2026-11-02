package agent

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

// Opt-in verification of an explicitly selected native session. Read a fixed
// prefix without modifying/copying it; report counts only, never native text.
func TestNativeCompactionProfile(t *testing.T) {
	path := os.Getenv("SPOOL_HISTORY_PROFILE")
	if path == "" {
		t.Skip("set SPOOL_HISTORY_PROFILE to an explicitly selected native transcript")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	summaries := make(map[[32]byte]bool)
	lastResponse := ""
	compactions, internalBlocks := 0, 0
	err = readJSONLines(io.NewSectionReader(file, 0, info.Size()), func(line []byte) error {
		var record map[string]any
		if json.Unmarshal(line, &record) != nil {
			return nil
		}
		payload, _ := record["payload"].(map[string]any)
		if record["type"] == "response_item" {
			lastResponse = ""
			if payload["type"] == "message" {
				content, _ := payload["content"].([]any)
				for _, raw := range content {
					block, _ := raw.(map[string]any)
					text := stringValue(block["text"])
					if payload["role"] == "assistant" {
						lastResponse += text
					} else if payload["role"] == "user" && strings.HasPrefix(text, "<codex_internal_context") {
						internalBlocks++
					}
				}
			}
		} else if record["type"] == "compacted" {
			compactions++
			message := stringValue(payload["message"])
			if lastResponse != "" && (message == lastResponse || strings.HasSuffix(message, "\n"+lastResponse)) {
				summaries[sha256.Sum256([]byte(lastResponse))] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	turns, err := IndexSessionTurnsTo(chatmodel.BackendCodex, path, 0, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	parts, leakedSummaries, leakedInternal := 0, 0, 0
	for _, turn := range turns {
		transcript, err := ReadSessionTurn(chatmodel.BackendCodex, path, turn)
		if err != nil {
			t.Fatal(err)
		}
		parts += len(transcript.Parts)
		for _, part := range transcript.Parts {
			if part.Role == "assistant" && part.Kind == "text" && summaries[sha256.Sum256([]byte(part.Text))] {
				leakedSummaries++
			}
			if part.Role == "user" && strings.HasPrefix(strings.TrimSpace(part.Text), "<codex_internal_context") {
				leakedInternal++
			}
		}
	}
	t.Logf("bytes=%d compact_records=%d matched_internal_summaries=%d internal_context_blocks=%d turns=%d display_parts=%d leaked_summaries=%d leaked_internal_context=%d", info.Size(), compactions, len(summaries), internalBlocks, len(turns), parts, leakedSummaries, leakedInternal)
	if leakedSummaries != 0 || leakedInternal != 0 {
		t.Fatal("provider-only context leaked into the display transcript")
	}
}
