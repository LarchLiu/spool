package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chatmodel "spool/internal/model"
)

func writeLocalSessionRecords(t *testing.T, path string, records ...map[string]any) {
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

func TestLocalSessionDiscoveryIgnoresSidechainsAndUsesNativeTitles(t *testing.T) {
	root := t.TempDir()
	codex, claude := filepath.Join(root, "codex"), filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	write := func(path string, records ...map[string]any) {
		t.Helper()
		writeLocalSessionRecords(t, path, records...)
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
	generations := map[string]string{}
	for _, session := range sessions {
		titles[session.ID] = session.Title
		generations[session.ID] = session.Generation
		if !session.TitleAuthoritative {
			t.Fatalf("complete transcript or title index was treated as a provisional title: %+v", session)
		}
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
		if session.Generation != generations[session.ID] || !session.TitleAuthoritative {
			t.Fatal("title-only refresh changed the file generation or lost title authority")
		}
	}
}

func localSessionDiscoveryFile(t *testing.T, backend chatmodel.Backend) (path, directory string) {
	t.Helper()
	root := t.TempDir()
	codex, claude := filepath.Join(root, "codex"), filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	path = filepath.Join(codex, "sessions", "rollout-native-session.jsonl")
	if backend == chatmodel.BackendClaude {
		path = filepath.Join(claude, "projects", "project", "native-session.jsonl")
	}
	return path, root
}

func TestLocalSessionDiscoveryReadsLargeIdentityRecords(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		for _, preface := range []bool{false, true} {
			name := string(backend)
			if preface {
				name += "/large-preface"
			}
			t.Run(name, func(t *testing.T) {
				path, directory := localSessionDiscoveryFile(t, backend)
				large := strings.Repeat("A", 400000)
				record := map[string]any{"type": "session_meta", "payload": map[string]any{"id": "native-session", "cwd": directory, "source": "cli", "title": "First request", "developer_instructions": large}}
				if backend == chatmodel.BackendClaude {
					record = map[string]any{"type": "user", "sessionId": "native-session", "cwd": directory, "message": map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": large}},
						map[string]any{"type": "text", "text": "First request"},
					}}}
				}
				var records []map[string]any
				if preface {
					records = append(records, map[string]any{"type": "file-history-snapshot", "snapshot": map[string]any{"data": large}})
				}
				writeLocalSessionRecords(t, path, append(records, record)...)
				var index LocalSessionIndex
				sessions, err := index.Discover()
				if err != nil || len(sessions) != 1 {
					t.Fatalf("large identity record was not discovered: %+v %v", sessions, err)
				}
				v := sessions[0]
				if v.ID != "native-session" || v.Directory != directory || v.Title != "First request" || !v.TitleAuthoritative {
					t.Fatalf("large identity record lost metadata: %+v", v)
				}
				sessions, err = index.Discover()
				if err != nil || len(sessions) != 1 || sessions[0].Generation != v.Generation {
					t.Fatalf("unchanged large record was not cached: %+v %v", sessions, err)
				}
			})
		}
	}
}

func TestLocalSessionDiscoveryRetriesUnreadableFiles(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		for _, previouslyCached := range []bool{false, true} {
			name := string(backend)
			if previouslyCached {
				name += "/changed-cached-file"
			}
			t.Run(name, func(t *testing.T) {
				path, directory := localSessionDiscoveryFile(t, backend)
				record := func(title string) map[string]any {
					if backend == chatmodel.BackendClaude {
						return map[string]any{"type": "user", "sessionId": "native-session", "cwd": directory, "message": map[string]any{"role": "user", "content": title}}
					}
					return map[string]any{"type": "session_meta", "payload": map[string]any{"id": "native-session", "cwd": directory, "title": title}}
				}
				writeLocalSessionRecords(t, path, record("Original request"))
				var index LocalSessionIndex
				if previouslyCached {
					if sessions, err := index.Discover(); err != nil || len(sessions) != 1 {
						t.Fatalf("initial discovery failed: %+v %v", sessions, err)
					}
					writeLocalSessionRecords(t, path, record("Updated and longer request"))
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0600) })
				if err := os.Chmod(path, 0); err != nil {
					t.Fatal(err)
				}
				if file, err := os.Open(path); err == nil {
					file.Close()
					t.Skip("cannot simulate an unreadable file")
				}
				before, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if sessions, err := index.Discover(); err != nil || len(sessions) != 0 {
					t.Fatalf("unreadable file was not skipped: %+v %v", sessions, err)
				}
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				after, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !os.SameFile(before, after) {
					t.Fatal("permission recovery changed the file generation")
				}
				if sessions, err := index.Discover(); err != nil || len(sessions) != 1 || sessions[0].ID != "native-session" {
					t.Fatalf("readable file was not retried: %+v %v", sessions, err)
				}
			})
		}
	}
}
