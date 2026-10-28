package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func writeTitleRecords(t *testing.T, path string, records ...map[string]any) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, record := range records {
		if err := json.NewEncoder(file).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeSessionIndexCollectsTitlesWithoutChangingTurns(t *testing.T) {
	for _, backend := range []string{"codex", "claude"} {
		t.Run(backend, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "native.jsonl")
			records := []map[string]any{
				{"type": "session_meta", "payload": map[string]any{"title": " Native Codex title "}},
				{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "question"}}}},
				{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "answer"}}}},
			}
			want := "Native Codex title"
			if backend == "claude" {
				records = []map[string]any{
					{"type": "summary", "summary": "Legacy summary"},
					{"type": "user", "uuid": "u1", "message": map[string]any{"role": "user", "content": "question"}},
					{"type": "assistant", "uuid": "a1", "parentUuid": "u1", "message": map[string]any{"role": "assistant", "content": "answer"}},
					{"type": "ai-title", "aiTitle": "Generated title"},
					{"type": "custom-title", "customTitle": " Native custom title "},
					{"type": "ai-title", "aiTitle": "Later generated title"},
					{"type": "custom-title", "customTitle": "  "},
					{"type": "custom-title", "customTitle": "Sidechain title", "isSidechain": true},
				}
				want = "Native custom title"
			}
			writeTitleRecords(t, path, records...)
			before, _ := os.ReadFile(path)
			index, err := IndexSessionTo(chatmodel.Backend(backend), path, 0, int64(len(before)))
			if err != nil || index.Title != want || len(index.Turns) != 1 {
				t.Fatalf("index = %+v, error = %v", index, err)
			}
			transcript, err := ReadSessionTurn(chatmodel.Backend(backend), path, index.Turns[0])
			if err != nil || len(transcript.Parts) != 2 || transcript.Parts[0].Text != "question" || transcript.Parts[1].Text != "answer" {
				t.Fatalf("title metadata changed conversation content: %+v, %v", transcript, err)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("title extraction modified native history")
			}
		})
	}
}

func TestClaudeSessionTitlePriorityAndSnapshotBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native.jsonl")
	for _, step := range []struct {
		record map[string]any
		want   string
	}{
		{map[string]any{"type": "summary", "summary": "Legacy title"}, "Legacy title"},
		{map[string]any{"type": "ai-title", "aiTitle": "First title"}, "First title"},
		{map[string]any{"type": "ai-title", "aiTitle": "Updated title"}, "Updated title"},
		{map[string]any{"type": "summary", "summary": "Later summary"}, "Updated title"},
		{map[string]any{"type": "custom-title", "customTitle": "Renamed title"}, "Renamed title"},
		{map[string]any{"type": "custom-title", "customTitle": "Latest rename"}, "Latest rename"},
	} {
		writeTitleRecords(t, path, step.record)
		index, err := IndexSessionTo(chatmodel.BackendClaude, path, 0, 0)
		if err != nil || index.Title != step.want {
			t.Fatalf("title = %q, want %q, error = %v", index.Title, step.want, err)
		}
	}
	info, _ := os.Stat(path)
	writeTitleRecords(t, path, map[string]any{"type": "custom-title", "customTitle": "Outside snapshot"})
	index, err := IndexSessionTo(chatmodel.BackendClaude, path, 0, info.Size())
	if err != nil || index.Title != "Latest rename" {
		t.Fatalf("title escaped the indexed snapshot: %+v, %v", index, err)
	}
}

func TestCodexTitleIndexRefreshesIndependentlyOfRollout(t *testing.T) {
	root := t.TempDir()
	native := filepath.Join(root, "sessions", "2026", "10", "28", "native.jsonl")
	index := filepath.Join(root, "session_index.jsonl")
	var titles CodexSessionTitles
	if got := titles.Title(native, "session-one"); got != "" {
		t.Fatalf("missing index returned %q", got)
	}
	writeTitleRecords(t, index,
		map[string]any{"id": "session-one", "thread_name": "First title"},
		map[string]any{"id": "session-two", "thread_name": "Other title"},
	)
	if got := titles.Title(native, "session-one"); got != "First title" {
		t.Fatalf("title = %q", got)
	}
	writeTitleRecords(t, index,
		map[string]any{"id": "session-one", "thread_name": " Updated native title "},
		map[string]any{"id": "session-one", "thread_name": "\t"},
	)
	for range 3 {
		if got := titles.Title(native, "session-one"); got != "Updated native title" {
			t.Fatalf("stale title after append: %q", got)
		}
	}
	if got := titles.Title(native, "session-two"); got != "Other title" {
		t.Fatalf("another chat's title was replaced: %q", got)
	}
	if got := titles.Title(native, "missing-session"); got != "" {
		t.Fatalf("unknown session returned another title: %q", got)
	}
	if err := os.WriteFile(index, []byte("{bad JSON}\n{\"id\":\"session-one\",\"thread_name\":\"After rewrite\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := titles.Title(native, "session-one"); got != "After rewrite" {
		t.Fatalf("stale title after rewrite: %q", got)
	}
	if got := titles.Title(native, "session-two"); got != "" {
		t.Fatalf("removed session title remained in the cache: %q", got)
	}
}

func TestCodexTitleIndexUsesSessionHomeAndConfiguredFallback(t *testing.T) {
	configured, actual := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", configured)
	writeTitleRecords(t, filepath.Join(configured, "session_index.jsonl"), map[string]any{"id": "same-session", "thread_name": "Configured home"})
	writeTitleRecords(t, filepath.Join(actual, "session_index.jsonl"), map[string]any{"id": "same-session", "thread_name": "Rollout home"})
	var titles CodexSessionTitles
	for _, dir := range []string{"sessions", "archived_sessions"} {
		if got := titles.Title(filepath.Join(actual, dir, "native.jsonl"), "same-session"); got != "Rollout home" {
			t.Fatalf("title used another provider home: %q", got)
		}
	}
	if got := titles.Title(filepath.Join(t.TempDir(), "copy.jsonl"), "same-session"); got != "Configured home" {
		t.Fatalf("copied rollout did not use CODEX_HOME: %q", got)
	}
	if strings.Contains(codexTitleIndexPath("relative.jsonl"), "sessions/") {
		t.Fatal("relative path resolved as a session home")
	}
}
