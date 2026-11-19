package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func TestCodexInternalReviewUsesRootCompletionAndSettings(t *testing.T) {
	path := "testdata/codex-review.jsonl"
	turns, err := IndexSessionTurns(chatmodel.BackendCodex, path, 0)
	if err != nil || len(turns) != 1 {
		t.Fatalf("review boundaries: %+v %v", turns, err)
	}
	turn := turns[0]
	if turn.ID != "review-root" || !slices.Equal(turn.IDs, []string{"review-worker"}) || turn.Status != chatmodel.StatusCompleted || turn.Model != "session-model" || turn.ReasoningEffort != "xhigh" || !turn.HasUserInput() {
		t.Fatalf("review metadata: %+v", turn)
	}
	transcript, err := ReadSessionTurn(chatmodel.BackendCodex, path, turn)
	if err != nil || len(transcript.Parts) != 4 || transcript.Parts[0].Text != "Review the current code changes." || transcript.Parts[3].Text != "Review complete." || transcript.Parts[3].Model != "session-model" {
		t.Fatalf("review transcript: %+v %v", transcript, err)
	}
}

func TestCodexUserEventAndResponseEchoesKeepRepeatedInputs(t *testing.T) {
	for _, eventFirst := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "rollout.jsonl")
		response := map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Repeated request"}}}}
		event := map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{"type": "UserMessage", "id": "native-input", "content": []any{map[string]any{"type": "text", "text": "Repeated request"}}}}}
		var records []map[string]any
		for range 2 {
			if eventFirst {
				records = append(records, event, response)
			} else {
				records = append(records, response, event)
			}
		}
		writeTitleRecords(t, path, records...)
		transcript, err := ParseSessionTranscript("codex", path)
		if err != nil || len(transcript.Parts) != 2 {
			t.Fatalf("event-first=%v repeated user inputs: %+v %v", eventFirst, transcript, err)
		}
	}
}

func TestCodexUserImageIsShownOnceWhateverRecordNamesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	// The response item carries what the model was sent; the event names the
	// file the user attached.
	response := map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "<image name=[Image #1]>"},
		map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAAA"},
		map[string]any{"type": "input_text", "text": "</image>"},
		map[string]any{"type": "input_text", "text": "What is in this screenshot?"}}}}
	event := map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{"type": "UserMessage", "id": "native-input", "content": []any{
		map[string]any{"type": "text", "text": "What is in this screenshot?"},
		map[string]any{"type": "localImage", "path": "/work/shot.png"}}}}}
	writeTitleRecords(t, path, response, event)
	transcript, err := ParseSessionTranscript("codex", path)
	images, texts := 0, 0
	for _, part := range transcript.Parts {
		if part.Kind == "image" {
			images++
		} else if part.Kind == "text" && part.Text == "What is in this screenshot?" {
			texts++
		}
	}
	if err != nil || images != 1 || texts != 1 || len(transcript.Parts) != 2 {
		t.Fatalf("image message repeated: %+v %v", transcript.Parts, err)
	}
}

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

func TestClaudeSessionDirectoryIgnoresDelayedEarlierTurnReplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	first, latest := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "latest")
	writeTitleRecords(t, path,
		map[string]any{"type": "user", "uuid": "first-input", "cwd": first, "message": map[string]any{"content": "First request"}},
		map[string]any{"type": "assistant", "uuid": "first-answer", "parentUuid": "first-input", "cwd": first, "message": map[string]any{"content": "First answer"}},
		map[string]any{"type": "user", "uuid": "latest-input", "parentUuid": "first-answer", "cwd": latest, "message": map[string]any{"content": "Latest request"}},
		map[string]any{"type": "assistant", "uuid": "delayed", "parentUuid": "first-input", "cwd": first, "message": map[string]any{"content": "Delayed earlier answer"}},
	)
	index, err := IndexSessionTo(chatmodel.BackendClaude, path, 0, 0)
	if err != nil || len(index.Turns) != 2 || index.Directory != latest {
		t.Fatalf("delayed earlier reply restored the old directory: %+v, %v", index, err)
	}
}

func TestClaudeInterruptionStatusUsesStructuredNotice(t *testing.T) {
	for _, test := range []struct {
		name    string
		content any
		stopped bool
	}{
		{name: "user interruption", content: "[Request interrupted by user]", stopped: true},
		{name: "tool interruption", content: "[Request interrupted by user for tool use]", stopped: true},
		{name: "literal prefix", content: "[Request interrupted by user] is an example."},
		{name: "unknown notice", content: "[Request interrupted example]"},
		{name: "request after notice", content: "[Request interrupted by user]\nTry again"},
		{name: "request in next block", content: []any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}, map[string]any{"type": "text", "text": "Try again"}}},
		{name: "notice before attachment", content: "[Request interrupted by user]\n\n" + attachedFilesMarker + "\n- \"notes.txt\": \"/tmp/notes.txt\"\n"},
		{name: "notice before image", content: []any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "aGVsbG8="}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var data strings.Builder
			encoder := json.NewEncoder(&data)
			for _, record := range []map[string]any{
				{"type": "user", "uuid": "prior", "message": map[string]any{"content": "Prior request"}},
				{"type": "assistant", "uuid": "prior-answer", "parentUuid": "prior", "message": map[string]any{"stop_reason": "end_turn", "content": "Prior answer"}},
				{"type": "user", "uuid": "current", "parentUuid": "prior-answer", "message": map[string]any{"content": test.content}},
				{"type": "assistant", "uuid": "answer", "parentUuid": "current", "message": map[string]any{"stop_reason": "end_turn", "content": "Answer"}},
			} {
				if err := encoder.Encode(record); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "claude.jsonl")
			if err := os.WriteFile(path, []byte(data.String()), 0600); err != nil {
				t.Fatal(err)
			}
			turns, err := IndexSessionTurns(chatmodel.BackendClaude, path, 0)
			wantTurns, wantStatus := 2, chatmodel.StatusCompleted
			if test.stopped {
				wantTurns, wantStatus = 1, chatmodel.StatusStopped
			}
			if err != nil || len(turns) != wantTurns || turns[len(turns)-1].Status != wantStatus {
				t.Fatalf("wrong interruption classification/status: %+v, %v", turns, err)
			}
		})
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
	large := "data:image/png;base64," + strings.Repeat("eA==", 2*1024*1024)
	if nativeImageURL(large) != large {
		t.Fatal("large native image payload was discarded")
	}
}

func TestNativeTurnsPreserveOversizedRecordsAndFollowingTurns(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "large.jsonl")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			encoder := json.NewEncoder(file)
			large := strings.Repeat("x", 25*1024*1024)
			reply := strings.Repeat("y", 700_000)
			for i, text := range []string{large, reply, "next question", "next answer"} {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				var record map[string]any
				if backend == chatmodel.BackendCodex {
					record = map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": "text", "text": text}}}}
				} else {
					record = map[string]any{"type": role, "uuid": []string{"u1", "a1", "u2", "a2"}[i], "parentUuid": []string{"", "u1", "a1", "u2"}[i], "message": map[string]any{"role": role, "content": text}}
				}
				if err := encoder.Encode(record); err != nil {
					t.Fatal(err)
				}
			}
			file.Close()
			turns, err := IndexSessionTurns(backend, path, 0)
			if err != nil || len(turns) != 2 {
				t.Fatalf("oversized input lost a turn boundary: turns=%d error=%v", len(turns), err)
			}
			for i, turn := range turns {
				transcript, err := ReadSessionTurn(backend, path, turn)
				wantInput, wantReply := large, reply
				if i == 1 {
					wantInput, wantReply = "next question", "next answer"
				}
				if err != nil || transcript.Cut || len(transcript.Parts) != 2 || transcript.Parts[0].Text != wantInput || transcript.Parts[1].Text != wantReply {
					t.Fatalf("turn %d was not parsed completely: parts=%d error=%v", i, len(transcript.Parts), err)
				}
			}
		})
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
