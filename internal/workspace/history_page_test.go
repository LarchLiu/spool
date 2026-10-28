package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func appendNativeTurns(t *testing.T, path string, first, count, outputs int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i := first; i < first+count; i++ {
		id := fmt.Sprintf("native-turn-%03d", i)
		stamp := time.Date(2026, 10, 28, 0, i, 0, 0, time.UTC)
		add := func(typ string, payload any, at time.Time) {
			t.Helper()
			if err := enc.Encode(map[string]any{"type": typ, "timestamp": at.Format(time.RFC3339Nano), "payload": payload}); err != nil {
				t.Fatal(err)
			}
		}
		add("event_msg", map[string]any{"type": "task_started", "turn_id": id}, stamp)
		add("turn_context", map[string]any{"turn_id": id, "model": "model-" + fmt.Sprint(i), "effort": "high"}, stamp)
		add("response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": fmt.Sprintf("prompt-%03d", i)}}}, stamp)
		for j := 0; j < outputs; j++ {
			add("response_item", map[string]any{"type": "function_call_output", "call_id": fmt.Sprint(j), "output": strings.Repeat("x", 8000)}, stamp)
		}
		add("response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": fmt.Sprintf("answer-%03d", i)}}}, stamp.Add(12*time.Second))
		add("event_msg", map[string]any{"type": "task_complete", "turn_id": id}, stamp.Add(12*time.Second))
	}
}

func TestHistoryPagesNativeTurnsBackwardsWithoutSQLiteRows(t *testing.T) {
	store := testStore(t)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 25, 18)
	if err := store.SetNativeSessionPath(chat.ID, "native-session", path); err != nil {
		t.Fatal(err)
	}
	latest, _ := store.Get(chat.ID)
	if len(latest.Turns) == 0 || latest.Turns[len(latest.Turns)-1].Prompt != "prompt-024" {
		t.Fatal("large chat lost its newest turn")
	}
	before := latest.History.Before
	// An append between pages must not shift or invalidate an older cursor.
	appendNativeTurns(t, path, 25, 1, 18)
	page := latest
	want := 24
	seen := map[string]bool{}
	for {
		if len(page.Turns) > historyPageSize {
			t.Fatal("page exceeded turn limit")
		}
		size := 0
		for _, part := range page.Transcript.Parts {
			size += len(part.Text)
		}
		if size > 400_000 {
			t.Fatal("page exceeded text budget", size)
		}
		for i := len(page.Turns) - 1; i >= 0; i-- {
			turn := page.Turns[i]
			if seen[turn.ID] || turn.Prompt != fmt.Sprintf("prompt-%03d", want) || turn.Reply != fmt.Sprintf("answer-%03d", want) {
				t.Fatalf("page skipped/duplicated/reordered turn %d: %+v", want, turn)
			}
			if turn.FinishedAt-turn.StartedAt != 12000 || turn.Model != fmt.Sprintf("model-%d", want) || turn.ReasoningEffort != "high" {
				t.Fatal("native turn metadata missing", turn)
			}
			if turn.InputEnd <= turn.PartStart || turn.PartEnd > len(page.Transcript.Parts) {
				t.Fatal("invalid page-local range")
			}
			seen[turn.ID] = true
			want--
		}
		if !page.History.HasMore {
			break
		}
		if before == "" {
			t.Fatal("missing cursor")
		}
		var err error
		page, err = store.historyPage(chat.ID, before)
		if err != nil {
			t.Fatal(err)
		}
		if page.History.HasMore && page.History.Before == before {
			t.Fatal("nonadvancing cursor")
		}
		before = page.History.Before
	}
	if want != -1 || len(seen) != 25 {
		t.Fatal("not all native turns were readable", want, len(seen))
	}
	if _, err := store.historyPage(chat.ID, "invalid-cursor"); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	assertNoTurnTables(t, store)
}

func TestSingleOversizedTurnKeepsInputAndLatestAnswer(t *testing.T) {
	store := testStore(t)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "oversized.jsonl")
	appendNativeTurns(t, path, 0, 1, 80)
	_ = store.SetNativeSessionPath(chat.ID, "oversized-native", path)
	saved, _ := store.Get(chat.ID)
	if len(saved.Turns) != 1 || saved.Turns[0].Prompt != "prompt-000" || saved.Turns[0].Reply != "answer-000" || !saved.Transcript.Cut {
		t.Fatal("oversized turn lost its input/final answer")
	}
}

func TestFullHistoryPageCannotDropNextLiveInput(t *testing.T) {
	store := testStore(t)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "full.jsonl")
	appendNativeTurns(t, path, 0, 1, 80)
	_ = store.SetNativeSessionPath(chat.ID, "full-native", path)
	reply := strings.Repeat("z", 8000)
	app := NewApp(store, &fakeRunner{text: reply}, "", "")
	t.Cleanup(func() { app.Close() })
	observed := false
	_, err := app.runTurn(context.Background(), chat.ID, "next input", func(event agent.AgentEvent) error {
		if event.Type != "delta" {
			return nil
		}
		loaded, _ := store.Get(chat.ID)
		snapshot := historyWindow(app.snapshot(loaded))
		if len(snapshot.Turns) != 1 || len(snapshot.Transcript.Parts) == 0 || snapshot.Transcript.Parts[0].Text != "next input" || snapshot.Transcript.Parts[len(snapshot.Transcript.Parts)-1].Text != reply || snapshot.PermissionMode == "" {
			t.Fatal("historical page consumed live input/output or permission preference")
		}
		if !snapshot.History.HasMore {
			t.Fatal("trimmed native turn became unreachable")
		}
		older, err := store.historyPage(chat.ID, snapshot.History.Before)
		if err != nil || len(older.Turns) != 1 || older.Turns[0].Prompt != "prompt-000" {
			t.Fatal("history cursor depended on an unwritten live native turn", err)
		}
		observed = true
		return nil
	})
	if err != nil || !observed {
		t.Fatal("live turn failed", err, observed)
	}
}

func TestHistoryEndpointEnforcesAccessAndChatScopedCursors(t *testing.T) {
	store := testStore(t)
	alice := testUser(t, store, "alice")
	_ = testUser(t, store, "bob")
	project := testProject(t, store, alice)
	first, _ := store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	second, _ := store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 25, 0)
	for _, chat := range []chatmodel.Session{first, second} {
		if err := store.SetNativeSessionPath(chat.ID, "shared-native", path); err != nil {
			t.Fatal(err)
		}
	}
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, "alice")
	latest, _ := store.Get(first.ID)
	query := "/history?before=" + latest.History.Before
	w := apiRequest(app, "GET", "/api/chats/"+first.ID+query, "", cookie, csrf)
	var page chatmodel.Session
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Turns) != 10 || page.Transcript.Parts[0].Text != "prompt-005" || strings.Contains(w.Body.String(), path) || page.NativeSessionID != "" {
		t.Fatal("history endpoint returned the wrong page or exposed native metadata", w.Code)
	}
	w = apiRequest(app, "GET", "/api/chats/"+second.ID+query, "", cookie, csrf)
	if w.Code != 400 {
		t.Fatal("history cursor reused across chats", w.Code)
	}
	bobCookie, bobCSRF := loginRequest(t, app, "bob")
	w = apiRequest(app, "GET", "/api/chats/"+first.ID+query, "", bobCookie, bobCSRF)
	if w.Code != 404 {
		t.Fatal("history bypassed project access", w.Code)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(app, "GET", "/api/chats/"+first.ID+query, "", cookie, csrf)
	if w.Code != 503 || strings.Contains(w.Body.String(), path) {
		t.Fatal("missing native history was misreported or exposed its path", w.Code)
	}
}

func TestMigrateLegacyStoreRetainsOnlyChatAndNativeReference(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 4, 18)
	db, err := sql.Open("sqlite", filepath.Join(root, "spool.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	oldSchema := strings.Replace(schema, ",permission_mode TEXT NOT NULL DEFAULT ''", "", 1)
	if _, err = db.Exec(oldSchema + `CREATE TABLE turns(id TEXT PRIMARY KEY,session_id TEXT,sequence INTEGER,body TEXT); CREATE TABLE attachments(id TEXT,turn_id TEXT,data BLOB); CREATE TABLE session_history(session_id TEXT,turn_start INTEGER); INSERT INTO meta VALUES('schema_version','1');`); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO sessions(id,title,backend,native_session_id,native_session_path,status,created_at,updated_at) VALUES('legacy-chat','Legacy','codex','legacy-native',?,'completed',1,2)`, path)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately fewer rows than native turns: old reconstruction rejected all.
	_, err = db.Exec(`INSERT INTO turns VALUES('legacy-turn','legacy-chat',0,'{"permission_mode":"auto","started_at":1,"finished_at":2}'); INSERT INTO attachments VALUES('upload','legacy-turn',x'0102');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, ok := store.Get("legacy-chat")
	if !ok || got.PermissionMode != "auto" || got.NativeSessionID != "legacy-native" || len(got.Turns) != 2 || got.Turns[1].Prompt != "prompt-003" || !got.History.HasMore {
		t.Fatalf("migration or native-only pagination failed: %+v", got)
	}
	assertNoTurnTables(t, store)
}
