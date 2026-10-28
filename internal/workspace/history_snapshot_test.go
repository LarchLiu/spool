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

func TestHistoryLoadsAllNativeTurnsWithoutSQLiteRows(t *testing.T) {
	store := testStore(t)
	indexes, reads := countNativeHistoryReads(store)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 25, 18)
	if err := store.SetNativeSessionPath(chat.ID, "native-session", path); err != nil {
		t.Fatal(err)
	}
	latest, _ := store.Get(chat.ID)
	if len(latest.Turns) != 25 || len(latest.Transcript.Parts) != 500 || latest.History != nil || latest.Transcript.Cut {
		t.Fatal("large chat did not load its complete history")
	}
	seen := map[string]bool{}
	for i, turn := range latest.Turns {
		if seen[turn.ID] || turn.Prompt != fmt.Sprintf("prompt-%03d", i) || turn.Reply != fmt.Sprintf("answer-%03d", i) {
			t.Fatalf("history skipped/duplicated/reordered turn %d: %+v", i, turn)
		}
		if turn.FinishedAt-turn.StartedAt != 12000 || turn.Model != fmt.Sprintf("model-%d", i) || turn.ReasoningEffort != "high" {
			t.Fatal("native turn metadata missing", turn)
		}
		if turn.PartStart != i*20 || turn.InputEnd != i*20+1 || turn.PartEnd != (i+1)*20 {
			t.Fatal("invalid full-history range")
		}
		seen[turn.ID] = true
	}
	_, _ = store.Get(chat.ID)
	if indexes.Load() != 1 || reads.Load() != 25 {
		t.Fatal("full history was not reused from cache")
	}
	appendNativeTurns(t, path, 25, 1, 18)
	updated, _ := store.Get(chat.ID)
	if len(updated.Turns) != 26 || updated.Turns[0].Prompt != "prompt-000" || updated.Turns[25].Prompt != "prompt-025" || indexes.Load() != 2 || reads.Load() != 51 {
		t.Fatal("append did not refresh the complete native history")
	}
	assertNoTurnTables(t, store)
}

func TestSingleOversizedTurnKeepsEveryPart(t *testing.T) {
	store := testStore(t)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "oversized.jsonl")
	appendNativeTurns(t, path, 0, 1, 80)
	_ = store.SetNativeSessionPath(chat.ID, "oversized-native", path)
	saved, _ := store.Get(chat.ID)
	if len(saved.Turns) != 1 || saved.Turns[0].Prompt != "prompt-000" || saved.Turns[0].Reply != "answer-000" || saved.Transcript.Cut || len(saved.Transcript.Parts) != 82 {
		t.Fatal("oversized turn lost content or was marked truncated")
	}
	for _, part := range saved.Transcript.Parts[1:81] {
		if part.Text != strings.Repeat("x", 8000) || part.Cut != 0 {
			t.Fatal("tool output was truncated")
		}
	}
}

func TestConvertedHistoryCachesToolNotesAsActivitiesNotReplies(t *testing.T) {
	store := testStore(t)
	indexes, reads := countNativeHistoryReads(store)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "converted.jsonl")
	patch := "*** Begin Patch\n*** Add File: pong.html\n" + strings.Repeat("+complete game line\n", 20_000) + "*** End Patch"
	note := "[apply_patch]\n```\n" + patch + "\n```\n\n[result]\n```\nSuccess\n```\n"
	records := []map[string]any{
		{"type": "session_meta", "payload": map[string]any{"cli_version": "0.0.0", "originator": "codex_cli_rs", "source": "cli"}},
		{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "add a pong game"}}}},
		{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": note}}}},
		{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Added the game"}}}},
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetNativeSessionPath(chat.ID, "converted-native", path); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		saved, ok := store.Get(chat.ID)
		if !ok || len(saved.Turns) != 1 || saved.Turns[0].Reply != "Added the game" || len(saved.Turns[0].Parts) != 4 {
			t.Fatalf("tool note leaked into the assistant reply: %+v, found=%v", saved.Turns, ok)
		}
		parts := saved.Turns[0].Parts
		if parts[1].Kind != "tool_use" || parts[1].Name != "apply_patch" || parts[1].Text != patch || parts[2].Kind != "tool_result" || parts[2].Text != "Success" {
			t.Fatal("converted activity lost its complete input or result")
		}
	}
	if indexes.Load() != 1 || reads.Load() != 1 {
		t.Fatal("converted tool activity was not reused from the history cache")
	}
}

func TestHistoryCachesAllTurnsRegardlessOfContentSize(t *testing.T) {
	store := testStore(t)
	_, reads := countNativeHistoryReads(store)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "crossing.jsonl")
	appendNativeTurns(t, path, 0, 1, 1)
	appendNativeTurns(t, path, 1, 1, 80)
	appendNativeTurns(t, path, 2, 2, 6)
	_ = store.SetNativeSessionPath(chat.ID, "crossing-native", path)
	page, _ := store.Get(chat.ID)
	if len(page.Turns) != 4 || page.Turns[0].Prompt != "prompt-000" || page.History != nil || reads.Load() != 4 {
		t.Fatal("full snapshot skipped history")
	}
	if len(page.Turns[1].Parts) != 82 || page.Transcript.Cut {
		t.Fatal("large turn was not parsed completely")
	}
	_, _ = store.Get(chat.ID)
	if reads.Load() != 4 {
		t.Fatal("complete parsed turns were not cached")
	}
}

func TestHistoryHasNoTurnCountLimit(t *testing.T) {
	store := testStore(t)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "many.jsonl")
	appendNativeTurns(t, path, 0, 100, 0)
	_ = store.SetNativeSessionPath(chat.ID, "many-native", path)
	page, _ := store.Get(chat.ID)
	if len(page.Turns) != 100 || page.History != nil || page.Turns[0].Prompt != "prompt-000" {
		t.Fatal("history was limited by turn count")
	}
}

func TestFullHistoryCannotLimitLiveContent(t *testing.T) {
	store := testStore(t)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "full.jsonl")
	appendNativeTurns(t, path, 0, 2, 80)
	_ = store.SetNativeSessionPath(chat.ID, "full-native", path)
	reply := strings.Repeat("z", 900_000)
	app := NewApp(store, &fakeRunner{text: reply}, "", "")
	t.Cleanup(func() { app.Close() })
	observed := false
	_, err := app.runTurn(context.Background(), chat.ID, "next input", func(event agent.AgentEvent) error {
		if event.Type != "delta" {
			return nil
		}
		loaded, _ := store.Get(chat.ID)
		snapshot := app.snapshot(loaded)
		if len(snapshot.Turns) != 3 || len(snapshot.Transcript.Parts) != 167 || snapshot.Transcript.Parts[0].Text != "prompt-000" || snapshot.Transcript.Parts[164].Text != "next input" || snapshot.Transcript.Parts[166].Text != reply || snapshot.Turns[2].Reply != reply || snapshot.PermissionMode == "" {
			t.Fatal("history consumed live input/output or permission preference")
		}
		observed = true
		return nil
	})
	if err != nil || !observed {
		t.Fatal("live turn failed", err, observed)
	}
	loaded, _ := store.Get(chat.ID)
	completed := app.snapshot(loaded)
	last := completed.Turns[len(completed.Turns)-1]
	if last.Reply != reply || last.Parts[len(last.Parts)-1].Text != reply {
		t.Fatal("completed response discarded full live content before native history caught up")
	}
}

func TestLiveAndStoppedHistoryKeepTheirFullBaseAndRanges(t *testing.T) {
	store := testStore(t)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "live-boundaries.jsonl")
	appendNativeTurns(t, path, 0, 3, 18)
	_ = store.SetNativeSessionPath(chat.ID, "live-boundaries", path)
	base, _ := store.Get(chat.ID)
	turn, err := store.BeginTurn(chat.ID, "next input")
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	turn = app.seedLiveTurn(base, turn)
	turn.Reply = strings.Repeat("z", 900_000)
	appendTurnText(&turn, chatmodel.Part{Role: "assistant", Kind: "text", Text: turn.Reply})
	turn.Attachments = []chatmodel.Attachment{{ID: "live-upload", Name: "upload.png"}}
	app.live[chat.ID] = turn
	if err := store.SaveRunningTurn(chat.ID, turn); err != nil {
		t.Fatal(err)
	}
	// Native output may lag and normalize differently from the live base.
	appendNativeTurns(t, path, 3, 1, 80)
	latest, _ := store.Get(chat.ID)
	if len(latest.Turns) != 4 || len(latest.Turns[3].Attachments) != 1 {
		t.Fatal("native hydration did not preserve the current upload")
	}
	check := func(snapshot chatmodel.Session) {
		t.Helper()
		if len(snapshot.Turns) != 4 || snapshot.Turns[0].Prompt != "prompt-000" || snapshot.Turns[2].Prompt != "prompt-002" || snapshot.Turns[3].Reply != turn.Reply || snapshot.History != nil {
			t.Fatal("native hydration changed the full live base")
		}
		for _, turn := range snapshot.Turns {
			if turn.PartEnd > len(snapshot.Transcript.Parts) || snapshot.Transcript.Parts[turn.PartStart].Text != turn.Prompt {
				t.Fatal("turn range points into a differently based snapshot")
			}
		}
	}
	check(app.snapshot(latest))
	stopped, err := app.stopTurn(latest, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(stopped)
	if stopped.Turns[3].Status != chatmodel.StatusStopped {
		t.Fatal("stop response lost the terminal lifecycle status")
	}
	loaded, _ := store.Get(chat.ID)
	replayed := app.snapshot(loaded)
	last := replayed.Turns[len(replayed.Turns)-1]
	if last.Reply != turn.Reply || last.Status != chatmodel.StatusStopped || len(last.Attachments) != 1 {
		t.Fatal("replaying a stopped turn lost complete output or uploads while native history lagged")
	}
}

func TestCompletionRebasesOnlyCurrentTurnAfterNativeNormalizationChanges(t *testing.T) {
	store := testStore(t)
	chat, _ := store.createSession("", "", chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "completed-boundaries.jsonl")
	appendNativeTurns(t, path, 0, 3, 18)
	_ = store.SetNativeSessionPath(chat.ID, "completed-boundaries", path)
	base, _ := store.Get(chat.ID)
	turn, err := store.BeginTurn(chat.ID, "next input")
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	turn = app.seedLiveTurn(base, turn)
	turn.Reply = strings.Repeat("z", 900_000)
	appendTurnText(&turn, chatmodel.Part{Role: "assistant", Kind: "text", Text: turn.Reply})
	turn.Status = chatmodel.StatusCompleted
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	appendNativeTurns(t, path, 0, 3, 3)
	appendNativeTurns(t, path, 3, 1, 18)
	if err := store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{TurnID: "native-turn-003"}, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	latest, _ := store.Get(chat.ID)
	merged, used := mergeCompletedTurn(latest, completedTurn{turn: turn})
	if !used || len(merged.Turns) != 4 || merged.Turns[0].Prompt != "prompt-000" || merged.Turns[3].Reply != turn.Reply || merged.History != nil {
		t.Fatal("completion replaced native history with a differently based live snapshot")
	}
	for _, turn := range merged.Turns {
		if turn.PartEnd > len(merged.Transcript.Parts) || merged.Transcript.Parts[turn.PartStart].Text != turn.Prompt || merged.Transcript.Parts[turn.PartEnd-1].Text != turn.Reply {
			t.Fatal("completion left invalid full-history content ranges")
		}
	}
	if merged.Turns[3].PartStart != 15 || merged.Turns[3].PartStart != merged.Turns[2].PartEnd || merged.Turns[3].InputEnd != 16 {
		t.Fatal("completion did not rebase the current input range")
	}
}

func TestChatEndpointPagesFullyCachedHistoryAndEnforcesAccess(t *testing.T) {
	store := testStore(t)
	indexes, reads := countNativeHistoryReads(store)
	alice := testUser(t, store, "alice")
	_ = testUser(t, store, "bob")
	project := testProject(t, store, alice)
	first, _ := store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 25, 18)
	if err := store.SetNativeSessionPath(first.ID, "shared-native", path); err != nil {
		t.Fatal(err)
	}
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, "alice")
	w := apiRequest(app, "GET", "/api/chats/"+first.ID, "", cookie, csrf)
	var page chatmodel.Session
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Turns) != 2 || page.Transcript.Parts[0].Text != "prompt-023" || strings.Contains(w.Body.String(), path) || page.NativeSessionID != "" || page.History == nil || !page.History.HasMore {
		t.Fatal("chat endpoint did not return a latest page or exposed native metadata", w.Code)
	}
	if indexes.Load() != 1 || reads.Load() != 25 {
		t.Fatal("first page did not fully parse and cache the native history")
	}
	firstCursor := page.History.Before
	seen := map[string]bool{}
	want := 24
	for {
		for i := len(page.Turns) - 1; i >= 0; i-- {
			turn := page.Turns[i]
			if seen[turn.ID] || page.Transcript.Parts[turn.PartStart].Text != fmt.Sprintf("prompt-%03d", want) {
				t.Fatal("transport pagination lost, duplicated or reordered a turn")
			}
			seen[turn.ID] = true
			want--
		}
		if !page.History.HasMore {
			break
		}
		before := page.History.Before
		w = apiRequest(app, "GET", "/api/chats/"+first.ID+"/history?before="+before, "", cookie, csrf)
		page = chatmodel.Session{}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.History == nil || len(page.Turns) == 0 || page.History.Before == before || page.NativeSessionID != "" || strings.Contains(w.Body.String(), path) {
			t.Fatal("history page failed or did not advance", w.Code)
		}
	}
	if len(seen) != 25 || want != -1 || reads.Load() != 25 || indexes.Load() != 1 {
		t.Fatal("pages did not reuse the full decoded cache")
	}
	second, _ := store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	_ = store.SetNativeSessionPath(second.ID, "shared-native", path)
	for _, target := range []string{first.ID + "/history?before=unused", second.ID + "/history?before=" + firstCursor} {
		w = apiRequest(app, "GET", "/api/chats/"+target, "", cookie, csrf)
		if w.Code != 400 {
			t.Fatal("invalid or cross-chat cursor accepted", w.Code)
		}
	}
	bobCookie, bobCSRF := loginRequest(t, app, "bob")
	w = apiRequest(app, "GET", "/api/chats/"+first.ID, "", bobCookie, bobCSRF)
	if w.Code != 404 {
		t.Fatal("history bypassed project access", w.Code)
	}
	w = apiRequest(app, "GET", "/api/chats/"+first.ID+"/history?before="+firstCursor, "", bobCookie, bobCSRF)
	if w.Code != 404 {
		t.Fatal("history pagination bypassed project access", w.Code)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(app, "GET", "/api/chats/"+first.ID, "", cookie, csrf)
	page = chatmodel.Session{}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.History == nil || page.History.Error == "" || strings.Contains(w.Body.String(), path) || strings.Contains(w.Body.String(), "shared-native") {
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
	if !ok || got.PermissionMode != "auto" || got.NativeSessionID != "legacy-native" || len(got.Turns) != 4 || got.Turns[3].Prompt != "prompt-003" || got.History != nil {
		t.Fatalf("migration or full native history failed: %+v", got)
	}
	assertNoTurnTables(t, store)
}
