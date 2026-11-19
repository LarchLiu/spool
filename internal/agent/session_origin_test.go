package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestCodexCompactionPreservesRetainedInputOrigins(t *testing.T) {
	root := conversionHomes(t)
	origins := []sessionOrigin{
		{Backend: "claude", Model: "original-claude", ReasoningEffort: "max"},
		{Backend: "codex", Model: "earlier-codex", ReasoningEffort: "high"},
		{Backend: "claude", Model: "another-claude", ReasoningEffort: "medium"},
	}
	image := "data:image/png;base64,aGVsbG8="
	var source []carryItem
	var retained []any
	for i, origin := range origins {
		images := []string(nil)
		content := []any{map[string]any{"type": "input_text", "text": "Repeated question"}}
		if i == 1 {
			images = []string{image}
			content = append(content, map[string]any{"type": "input_image", "image_url": image})
		}
		source = append(source, carryItem{Kind: "user", Text: "Repeated question", Images: images, Origin: origin}, carryItem{Kind: "assistant", Text: "Earlier answer", Origin: origin})
		retained = append(retained, map[string]any{"type": "message", "role": "user", "content": content})
	}
	converted, err := writeCodexCarry(filepath.Join(root, "codex"), root, source, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(converted.Path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	current := sessionOrigin{Backend: "codex", Model: "current-codex", ReasoningEffort: "low"}
	retained = append(retained, map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "New compaction summary"}}})
	for _, record := range []any{
		map[string]any{"type": "turn_context", "payload": map[string]any{"turn_id": "current", "model": current.Model, "effort": current.ReasoningEffort}},
		map[string]any{"type": "compacted", "payload": map[string]any{"replacement_history": retained}},
		// A second compaction must retain the same provenance again.
		map[string]any{"type": "compacted", "payload": map[string]any{"replacement_history": retained}},
	} {
		if err := encoder.Encode(record); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	origins = append(origins, current)
	sourcePath := converted.Path
	before, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range []chatmodel.Backend{"codex", "claude", "codex"} {
		reader := codexCarryItems
		if backend == "claude" {
			reader = claudeCarryItems
		}
		items, err := reader(converted.Path)
		if err != nil || len(items) != len(origins) {
			t.Fatalf("%s compacted history: %+v %v", backend, items, err)
		}
		for i, item := range items {
			if item.Kind != "user" || item.Origin != origins[i] {
				t.Fatalf("%s retained input %d has origin %+v, want %+v", backend, i, item.Origin, origins[i])
			}
			if i == 1 && (len(item.Images) != 1 || item.Images[0] != image) {
				t.Fatalf("%s lost retained image: %+v", backend, item)
			}
		}
		to := chatmodel.BackendClaude
		if backend == "claude" {
			to = "codex"
		}
		converted, err = ConvertSession(backend, to, converted.Path, root)
		if err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(sourcePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("conversion modified the compacted source", err)
	}
}

func TestConvertedHistoryKeepsOriginsAcrossFormatsAndPages(t *testing.T) {
	root := conversionHomes(t)
	origins := []sessionOrigin{
		{Backend: "codex", Model: "original-codex", ReasoningEffort: "high"},
		{Backend: "claude", Model: "original-claude", ReasoningEffort: "max"},
	}
	var items []carryItem
	for _, origin := range origins {
		items = append(items, carryItem{Kind: "user", Text: "question", Origin: origin}, carryItem{Kind: "assistant", Text: "answer", Origin: origin})
	}
	now := time.Now()
	converted, err := writeClaudeCarry(filepath.Join(root, "claude"), root, items, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range []chatmodel.Backend{"claude", "codex", "claude", "codex"} {
		turns, err := IndexSessionTurns(backend, converted.Path, 0)
		if err != nil || len(turns) != 2 {
			t.Fatalf("%s turn boundaries: %+v %v", backend, turns, err)
		}
		for i, turn := range turns {
			want := origins[i]
			if turn.Backend != want.Backend || turn.Model != want.Model || turn.ReasoningEffort != want.ReasoningEffort {
				t.Fatalf("%s turn %d: %+v, want %+v", backend, i, turn, want)
			}
		}
		later, err := IndexSessionTurns(backend, converted.Path, turns[1].Start)
		if err != nil || len(later) != 1 || later[0].Backend != origins[1].Backend || later[0].Model != origins[1].Model {
			t.Fatalf("%s paged origin: %+v %v", backend, later, err)
		}
		if backend == "codex" {
			metadata := &CodexMetadata{}
			contexts, err := metadata.Read(converted.Path, []CodexHistoryTurn{{ID: turns[0].ID}, {ID: turns[1].ID}})
			if err != nil || len(contexts) != 2 {
				t.Fatalf("native page metadata: %+v %v", contexts, err)
			}
			for i, turn := range turns {
				context := contexts[turn.ID]
				if context.Backend != origins[i].Backend || context.Model != origins[i].Model || context.ReasoningEffort != origins[i].ReasoningEffort {
					t.Fatalf("native page turn %d: %+v", i, context)
				}
			}
		}
		to := chatmodel.BackendCodex
		if backend == to {
			to = chatmodel.BackendClaude
		}
		converted, err = ConvertSession(backend, to, converted.Path, root)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyConvertedHistoryOriginsEndAtNativeTurns(t *testing.T) {
	root := conversionHomes(t)
	for _, backend := range []chatmodel.Backend{"claude", "codex"} {
		t.Run(string(backend), func(t *testing.T) {
			writer := writeClaudeCarry
			original := chatmodel.BackendCodex
			if backend == "codex" {
				writer, original = writeCodexCarry, chatmodel.BackendClaude
			}
			// No origin fields: the envelope is identical to existing conversions.
			converted, err := writer(root, root, []carryItem{{Kind: "user", Text: "old question"}, {Kind: "assistant", Text: "old answer"}}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(converted.Path)
			var appended bytes.Buffer
			encoder := json.NewEncoder(&appended)
			if backend == "codex" {
				for _, line := range []any{
					map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "native"}},
					map[string]any{"type": "turn_context", "payload": map[string]any{"turn_id": "native", "model": "current-codex", "effort": "medium"}},
					map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "new question"}}}},
					map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "new answer"}}}},
				} {
					_ = encoder.Encode(line)
				}
			} else {
				_ = encoder.Encode(map[string]any{"type": "user", "uuid": "native", "message": map[string]any{"content": "new question"}})
				_ = encoder.Encode(map[string]any{"type": "assistant", "uuid": "reply", "parentUuid": "native", "message": map[string]any{"model": "current-claude", "content": "new answer"}})
			}
			if err := os.WriteFile(converted.Path, append(data, appended.Bytes()...), 0600); err != nil {
				t.Fatal(err)
			}
			turns, err := IndexSessionTurns(backend, converted.Path, 0)
			if err != nil || len(turns) != 2 || turns[0].Backend != original || turns[1].Backend != backend || turns[1].Model != "current-"+string(backend) {
				t.Fatalf("legacy vs native attribution: %+v %v", turns, err)
			}
		})
	}
}

func TestNativeClaudeSyntheticMessageDoesNotImplyCodex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data := []byte("{\"type\":\"user\",\"uuid\":\"user\",\"message\":{\"content\":\"/command\"}}\n" +
		"{\"type\":\"assistant\",\"uuid\":\"answer\",\"parentUuid\":\"user\",\"message\":{\"model\":\"<synthetic>\",\"content\":\"Command output\"}}\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	turns, err := IndexSessionTurns("claude", path, 0)
	if err != nil || len(turns) != 1 || turns[0].Backend != "claude" {
		t.Fatalf("native synthetic output misattributed: %+v %v", turns, err)
	}
}

func TestLegacyClaudeCarryAttributesUserOnlyTurns(t *testing.T) {
	root := conversionHomes(t)
	converted, err := writeClaudeCarry(filepath.Join(root, "claude"), root, []carryItem{
		{Kind: "user", Text: "earliest user-only question"},
		{Kind: "user", Text: "later question"},
		{Kind: "assistant", Text: "later answer"},
		{Kind: "user", Text: "last user-only question"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	turns, err := IndexSessionTurns("claude", converted.Path, 0)
	if err != nil || len(turns) != 3 {
		t.Fatalf("user-only history missing: %+v %v", turns, err)
	}
	for i, turn := range turns {
		if turn.Backend != "codex" {
			t.Fatalf("legacy turn %d without a reply has wrong origin: %+v", i, turn)
		}
	}
	items, err := claudeCarryItems(converted.Path)
	if err != nil || len(items) != 4 {
		t.Fatalf("legacy carry items: %+v %v", items, err)
	}
	for _, item := range items {
		if item.Origin.Backend != "codex" {
			t.Fatalf("next conversion would lose the user-only origin: %+v", item)
		}
	}
	back, err := ConvertSession("claude", "codex", converted.Path, root)
	if err != nil {
		t.Fatal(err)
	}
	turns, err = IndexSessionTurns("codex", back.Path, 0)
	if err != nil || len(turns) != 3 {
		t.Fatalf("converted user-only boundaries: %+v %v", turns, err)
	}
	for _, turn := range turns {
		if turn.Backend != "codex" {
			t.Fatalf("converted user-only origin: %+v", turn)
		}
	}
}
