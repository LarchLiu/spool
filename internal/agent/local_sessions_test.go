package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	chatmodel "spool/internal/model"
)

func TestLocalSessionDiscoveryIgnoresSidechainsAndUsesNativeTitles(t *testing.T) {
	root := t.TempDir()
	codex, claude := filepath.Join(root, "codex"), filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	write := func(path string, records ...map[string]any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(path)
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
	meta := func(id string, source any) map[string]any {
		return map[string]any{"type": "session_meta", "payload": map[string]any{"id": id, "cwd": root, "source": source}}
	}
	write(filepath.Join(codex, "sessions", "rollout-primary.jsonl"), meta("codex-primary", "cli"))
	write(filepath.Join(codex, "sessions", "rollout-sidechain.jsonl"), meta("codex-sidechain", map[string]any{"subagent": map[string]any{"thread_spawn": "parent"}}))
	write(filepath.Join(codex, "sessions", "rollout-invalid.jsonl"), meta("../../invalid", "cli"))
	write(filepath.Join(codex, "archived_sessions", "rollout-archived.jsonl"), meta("codex-archived", "cli"))
	write(filepath.Join(codex, "session_index.jsonl"), map[string]any{"id": "codex-primary", "thread_name": "Named Codex chat"})
	user := map[string]any{"type": "user", "sessionId": "claude-primary", "cwd": root, "message": map[string]any{"role": "user", "content": "<environment_context>runtime</environment_context>\nActual user request"}}
	write(filepath.Join(claude, "projects", "project", "claude-primary.jsonl"), user)
	sidechain := map[string]any{"type": "user", "sessionId": "claude-sidechain", "cwd": root, "isSidechain": true}
	write(filepath.Join(claude, "projects", "project", "agent-sidechain.jsonl"), sidechain)
	write(filepath.Join(claude, "projects", "project", "subagents", "nested.jsonl"), user)
	var index LocalSessionIndex
	sessions, err := index.Discover()
	if err != nil || len(sessions) != 2 {
		t.Fatalf("discovery included hidden sessions: %+v %v", sessions, err)
	}
	titles := map[string]string{}
	for _, session := range sessions {
		titles[session.ID] = session.Title
	}
	if titles["codex-primary"] != "Named Codex chat" || titles["claude-primary"] != "Actual user request" {
		t.Fatalf("native titles were not projected correctly: %+v", titles)
	}
	write(filepath.Join(codex, "session_index.jsonl"), map[string]any{"id": "codex-primary", "thread_name": "Renamed without a turn"})
	sessions, err = index.Discover()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.Backend == chatmodel.BackendCodex && session.Title != "Renamed without a turn" {
			t.Fatal("cached discovery missed provider title-index changes")
		}
	}
}
