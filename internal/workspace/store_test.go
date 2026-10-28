package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestTurnTransitionsAdvanceSessionRevision(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	chat, err := store.createSession("", "", chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a backward clock adjustment, which also covers same-ms writes.
	future := time.Now().Add(time.Hour).UnixMilli()
	if _, err = store.db.Exec(`UPDATE sessions SET updated_at=? WHERE id=?`, future, chat.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := store.BeginTurn(chat.ID, "Hello")
	if err != nil {
		t.Fatal(err)
	}
	running, _ := store.Get(chat.ID)
	if running.UpdatedAt <= future {
		t.Fatal("starting a turn did not advance the SSE revision")
	}
	if err = store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{Text: "Hi"}, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	finished, _ := store.Get(chat.ID)
	if finished.UpdatedAt <= running.UpdatedAt {
		t.Fatal("finishing a turn did not advance the SSE revision")
	}
	if _, err = store.SaveProject(Project{Name: "Missing directory"}); err == nil {
		t.Fatal("a blank project directory must not silently become the server cwd")
	}
}

func TestStorePersistsSessionMetadataWithoutConversationBodies(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.createSession("", "", chatmodel.BackendClaude)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := store.BeginTurn(session.ID, "Draw a tiny fox")
	if err != nil {
		t.Fatal(err)
	}
	result := agent.AgentResult{SessionID: "native-session-123", Model: "sonnet", Text: "Done", Activities: []string{"Used Write"}}
	if err := store.FinishTurn(session.ID, turn.ID, result, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}

	loaded, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := loaded.Get(session.ID)
	if !ok {
		t.Fatal("session missing after reload")
	}
	if got.Title != "Draw a tiny fox" || got.NativeSessionID != result.SessionID || len(got.Turns) != 0 {
		t.Fatalf("unexpected session after reload: %#v", got)
	}
	if len(got.Transcript.Parts) != 0 {
		t.Fatalf("conversation body was persisted in SQLite: %#v", got.Transcript)
	}
	var transcriptTables int
	if err = loaded.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='transcripts'`).Scan(&transcriptTables); err != nil || transcriptTables != 0 {
		t.Fatalf("unexpected transcript table: count=%d err=%v", transcriptTables, err)
	}
	assertNoTurnTables(t, loaded)
	var version string
	if err = loaded.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil || version != "2" {
		t.Fatalf("schema version = %q, %v", version, err)
	}
	info, err := os.Stat(filepath.Join(root, "db", "spool.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("history should be private, got permissions %o", info.Mode().Perm())
	}
}

func TestRunningCheckpointStoresMetadataWithoutTranscriptParts(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.createSession("", "", chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := store.BeginTurn(session.ID, "Hello")
	if err != nil {
		t.Fatal(err)
	}
	appendTurnText(&turn, chatmodel.Part{Role: "assistant", Kind: "text", Model: "gpt-6", Text: "Hello "})
	appendTurnText(&turn, chatmodel.Part{Role: "assistant", Kind: "text", Model: "gpt-6", Text: "world"})
	appendTurnPart(&turn, chatmodel.Part{Role: "assistant", Kind: "tool_use", Name: "shell", Text: "go test"})
	if err = store.SaveRunningTurn(session.ID, turn); err != nil {
		t.Fatal(err)
	}

	appendTurnText(&turn, chatmodel.Part{Role: "assistant", Kind: "text", Model: "gpt-6", Text: " again"})
	if err = store.SaveRunningTurn(session.ID, turn); err != nil {
		t.Fatal(err)
	}
	if err = store.FinishTurn(session.ID, turn.ID, agent.AgentResult{Model: "gpt-6", Text: "Hello world again"}, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}

	got, ok := store.Get(session.ID)
	if !ok {
		t.Fatal("finished session missing")
	}
	if len(got.Transcript.Parts) != 0 {
		t.Fatalf("running transcript was copied into SQLite: %#v", got.Transcript.Parts)
	}
	if len(got.Turns) != 1 || got.Turns[0].PartStart != 0 || got.Turns[0].InputEnd != 0 || got.Turns[0].PartEnd != 0 || got.Turns[0].Reply != "" {
		t.Fatalf("turn metadata is inconsistent: %#v", got.Turns)
	}
}

func TestSQLiteIgnoresOldJSONData(t *testing.T) {
	for _, content := range []string{"not json", `{"version":1,"sessions":[{"id":"old-chat-123","backend":"codex"}]}`} {
		t.Run(content[:min(8, len(content))], func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "sessions.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			store, err := OpenStore(root)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if len(store.Sessions()) != 0 {
				t.Fatal("old JSON data was unexpectedly imported")
			}
			original, err := os.ReadFile(path)
			if err != nil || string(original) != content {
				t.Fatal("old JSON data must remain untouched")
			}
		})
	}
}

func TestSetInterruptedMarksOnlyRunningTurns(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.createSession("", "", chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginTurn(session.ID, "still working"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetInterrupted(); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(session.ID)
	if got.Status != chatmodel.StatusInterrupted || got.Turns[0].Status != chatmodel.StatusInterrupted {
		t.Fatalf("running turn was not recovered as interrupted: %#v", got)
	}
	if !strings.Contains(got.Turns[0].Error, "restarted") {
		t.Fatalf("missing restart explanation: %q", got.Turns[0].Error)
	}
}

func assertNoTurnTables(t *testing.T, store *Store) {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('turns','attachments','session_history','session_sources','transcripts')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unexpected durable history tables: %d %v", count, err)
	}
}
