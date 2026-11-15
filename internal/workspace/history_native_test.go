package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

// fakeCodexHistory serves turns 0..count-1 of one thread, newest first, the
// way thread/turns/list pages them.
type fakeCodexHistory struct {
	count     int
	legacy    bool
	err       error
	pages     int
	threads   int
	turnBytes int
	items     int
	idPrefix  string
	note      string // appended to each answer
}

func (f *fakeCodexHistory) Close() {}

func (f *fakeCodexHistory) Item(_ context.Context, _ string, turn, item string) ([]chatmodel.Part, error) {
	f.items++
	if strings.HasPrefix(item, "thought-") {
		return []chatmodel.Part{{ID: item, Role: "assistant", Kind: "thinking", Text: "stored reasoning"}}, nil
	}
	return []chatmodel.Part{{ID: item, Role: "tool", Kind: "tool_result", Text: strings.Repeat("x", f.turnBytes)}}, nil
}

func (f *fakeCodexHistory) Thread(_ context.Context, id string) (agent.CodexThread, error) {
	f.threads++
	if f.err != nil {
		return agent.CodexThread{}, f.err
	}
	return agent.CodexThread{ID: id, Paginated: !f.legacy, Name: "Native name"}, nil
}

func (f *fakeCodexHistory) Turns(_ context.Context, _ string, before string, limit int) (agent.CodexTurnPage, error) {
	f.pages++
	end := f.count
	if before != "" {
		end, _ = strconv.Atoi(before)
	}
	start := max(0, end-limit)
	page := agent.CodexTurnPage{}
	if start > 0 {
		page.Before = strconv.Itoa(start)
	}
	exit := 1
	for i := start; i < end; i++ {
		id := fmt.Sprintf("%sturn-%03d", f.idPrefix, i)
		page.Turns = append(page.Turns, agent.CodexHistoryTurn{ID: id, Status: chatmodel.StatusCompleted, StartedAt: int64(i+1) * 1000, FinishedAt: int64(i+1)*1000 + 500, Parts: []chatmodel.Part{
			{Role: "user", Kind: "text", Text: "prompt-" + id},
			{ID: "call-" + id, Role: "assistant", Kind: "tool_use", Name: "shell", Text: "npm test", Native: &chatmodel.ToolNative{Status: chatmodel.ToolFailed, ExitCode: &exit}},
			{ID: "call-" + id, Role: "tool", Kind: "tool_result", Text: strings.Repeat("x", f.turnBytes)},
			{ID: "msg-" + id, Role: "assistant", Kind: "text", Phase: "final_answer", Text: "answer-" + id + f.note},
		}})
	}
	return page, nil
}

func nativeCodexChat(t *testing.T, store *Store, reader codexHistoryReader) (chatmodel.Session, string) {
	t.Helper()
	chat, err := store.createSession("", "", chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	appendNativeTurns(t, path, 0, 2, 1)
	if err := store.SetNativeSessionPath(chat.ID, "native-thread", path); err != nil {
		t.Fatal(err)
	}
	store.codexHistory.reader = reader
	return chat, path
}

func TestCodexHistoryReadsNativeBodiesWithCachedRolloutMetadata(t *testing.T) {
	store := testStore(t)
	indexes, reads := countNativeHistoryReads(store)
	fake := &fakeCodexHistory{count: 37, turnBytes: 100}
	chat, _ := nativeCodexChat(t, store, fake)
	latest, ok := store.Get(chat.ID)
	if !ok || latest.History != nil && latest.History.Error != "" {
		t.Fatalf("native history failed: %+v", latest.History)
	}
	// Small turns: pages are read until the window is worth a transport page.
	if len(latest.Turns) != 37 || latest.Turns[0].NativeTurnID != "turn-000" || latest.Turns[36].NativeTurnID != "turn-036" || latest.Title != "Native name" {
		t.Fatalf("turns %d title %q", len(latest.Turns), latest.Title)
	}
	if indexes.Load() != 0 || reads.Load() != 0 {
		t.Fatalf("native paging built a full index or read rollout bodies: indexes=%d reads=%d", indexes.Load(), reads.Load())
	}
	turn := latest.Turns[36]
	parts := latest.Transcript.Parts[turn.PartStart:turn.PartEnd]
	if turn.InputEnd != turn.PartStart+1 || len(parts) != 4 || parts[1].Tool == nil || parts[1].Tool.Status != chatmodel.ToolFailed || *parts[1].Tool.ExitCode != 1 || turn.Reply != "answer-turn-036" || turn.Prompt != "prompt-turn-036" {
		t.Fatalf("turn content: %+v %+v", turn, parts)
	}
	// An unchanged rollout costs no further request.
	pages, threads := fake.pages, fake.threads
	if again, _ := store.Get(chat.ID); len(again.Turns) != 37 || fake.pages != pages || fake.threads != threads {
		t.Fatalf("unchanged history was read again: pages %d→%d", pages, fake.pages)
	}
	if indexes.Load() != 0 || reads.Load() != 0 {
		t.Fatalf("unchanged metadata was parsed again: indexes=%d reads=%d", indexes.Load(), reads.Load())
	}
	// A response may be changed without touching the shared window.
	latest.Transcript.Parts[1].Tool.Status = "changed"
	if again, _ := store.Get(chat.ID); again.Transcript.Parts[1].Tool.Status != chatmodel.ToolFailed {
		t.Fatal("a response shared its tool summary with the cache")
	}
}

func TestCodexHistoryPreservesPerTurnModelAndEffortAcrossPagesAndRestart(t *testing.T) {
	store := testStore(t)
	fake := &fakeCodexHistory{count: 45, turnBytes: 60_000, idPrefix: "native-"}
	chat, path := nativeCodexChat(t, store, fake)
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	efforts := []string{"low", "medium", "high", "xhigh"}
	appendContext := func(i int) {
		t.Helper()
		id := fmt.Sprintf("native-turn-%03d", i)
		if i == 44 {
			// An unrelated record must not supply metadata to this native turn.
			id = "unrelated-turn"
		}
		payload := map[string]any{"turn_id": id, "model": fmt.Sprintf("model-%d", i)}
		key := "effort"
		if i%2 != 0 {
			key = "reasoning_effort"
		}
		payload[key] = efforts[i%len(efforts)]
		appendTitleRecord(t, path, map[string]any{"type": "turn_context", "payload": payload})
	}
	for i := range 45 {
		appendContext(i)
	}
	// Current session settings are not the settings of earlier turns.
	if _, err := store.db.Exec(`UPDATE sessions SET model=?,reasoning_effort=? WHERE id=?`, "current-model", "current-effort", chat.ID); err != nil {
		t.Fatal(err)
	}
	check := func(current *Store) {
		t.Helper()
		latest, ok := current.Get(chat.ID)
		if !ok || latest.History == nil || !latest.History.Native || latest.History.Error != "" {
			t.Fatalf("native history: found=%v history=%+v", ok, latest.History)
		}
		page, seen := latest, 0
		for {
			for _, turn := range page.Turns {
				i, err := strconv.Atoi(strings.TrimPrefix(turn.NativeTurnID, "native-turn-"))
				if err != nil {
					t.Fatal(err)
				}
				model, effort := fmt.Sprintf("model-%d", i), efforts[i%len(efforts)]
				if i == 44 {
					model, effort = "", ""
				}
				if turn.Model != model || turn.ReasoningEffort != effort {
					t.Fatalf("turn %s metadata: model=%q effort=%q, want %q %q", turn.NativeTurnID, turn.Model, turn.ReasoningEffort, model, effort)
				}
				for _, part := range turn.Parts {
					if part.Role == "assistant" && part.Model != model {
						t.Fatalf("turn %s assistant model=%q, want %q", turn.NativeTurnID, part.Model, model)
					}
				}
				seen++
			}
			if !page.History.HasMore {
				break
			}
			var err error
			page, err = current.codexHistoryPage(context.Background(), latest, page.History.Before)
			if err != nil {
				t.Fatal(err)
			}
		}
		if seen != fake.count {
			t.Fatalf("read %d turns, want %d", seen, fake.count)
		}
	}
	indexes, reads := countNativeHistoryReads(store)
	check(store)
	check(store)
	if indexes.Load() != 0 || reads.Load() != 0 {
		t.Fatalf("metadata cache was not reused: indexes=%d reads=%d", indexes.Load(), reads.Load())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopened.codexHistory.reader = fake
	indexes, reads = countNativeHistoryReads(reopened)
	check(reopened)
	// Appended contexts update metadata without building a full session index.
	appendContext(45)
	fake.count++
	check(reopened)
	if indexes.Load() != 0 || reads.Load() != 0 {
		t.Fatalf("appended metadata was not refreshed: indexes=%d reads=%d", indexes.Load(), reads.Load())
	}
}

func TestCodexFinishTurnDoesNotIndexNativeHistory(t *testing.T) {
	for _, announced := range []bool{true, false} {
		t.Run(fmt.Sprintf("announced-%v", announced), func(t *testing.T) {
			store := testStore(t)
			fake := &fakeCodexHistory{count: 2, idPrefix: "native-"}
			chat, path := nativeCodexChat(t, store, fake)
			indexes, reads := countNativeHistoryReads(store)
			turn, err := store.beginTurn(chat.ID, "new input", chat.Backend)
			if err != nil {
				t.Fatal(err)
			}
			appendNativeTurns(t, path, 2, 2, 0)
			fake.count = 4
			result := agent.AgentResult{Model: "model-2", ReasoningEffort: "high"}
			if announced {
				result.TurnID = "native-turn-002"
			}
			if err := store.FinishTurn(chat.ID, turn.ID, result, nil, chatmodel.StatusCompleted); err != nil {
				t.Fatal(err)
			}
			latest, _ := store.Get(chat.ID)
			if len(latest.Turns) != 4 || latest.Turns[2].ID != turn.ID || latest.Turns[2].NativeTurnID != "native-turn-002" {
				t.Fatalf("finished turn lost its exact native identity: %+v", latest.Turns)
			}
			if indexes.Load() != 0 || reads.Load() != 0 {
				t.Fatalf("finishing a native turn indexed history: indexes=%d reads=%d", indexes.Load(), reads.Load())
			}
		})
	}
}

func TestCodexFailedTurnDoesNotIndexExistingNativeHistory(t *testing.T) {
	store := testStore(t)
	chat, _ := nativeCodexChat(t, store, &fakeCodexHistory{count: 2, idPrefix: "native-"})
	indexes, reads := countNativeHistoryReads(store)
	turn, err := store.beginTurn(chat.ID, "failed input", chat.Backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{}, errors.New("provider unavailable"), chatmodel.StatusFailed); err != nil {
		t.Fatal(err)
	}
	latest, _ := store.Get(chat.ID)
	if len(latest.Turns) != 3 || latest.Turns[2].ID != turn.ID || latest.Turns[2].NativeTurnID != "" || latest.Turns[2].Status != chatmodel.StatusFailed {
		t.Fatalf("failed request bound to old history: %+v", latest.Turns)
	}
	if indexes.Load() != 0 || reads.Load() != 0 {
		t.Fatalf("failed request indexed existing history: indexes=%d reads=%d", indexes.Load(), reads.Load())
	}
}

func TestNativeHistoryRecoversActorOffsetsAcrossPages(t *testing.T) {
	store := testStore(t)
	alice, bob := testUser(t, store, "native-alice"), testUser(t, store, "native-bob")
	project := testProject(t, store, alice, bob)
	chat, err := store.CreateChat(alice.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	appendNativeTurns(t, path, 0, 1, 0)
	if err := store.SetNativeSessionPath(chat.ID, "actor-native", path); err != nil {
		t.Fatal(err)
	}
	store.codexHistory.reader = &fakeCodexHistory{count: 1, idPrefix: "native-"}
	turn, err := store.beginTurnBy(chat.ID, "follow-up", chat.Backend, "", nil, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	action, err := store.recordTurnAction(chat.ID, turn.ID, alice.ID, "deny", "approval", "Review approval")
	if err != nil {
		t.Fatal(err)
	}
	appendNativeTurns(t, path, 1, 1, 0)
	// Crash before a native turn event binds Bob's request. Later activity moves
	// his turn outside the newest native page before the chat is reopened.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	appendNativeTurns(t, path, 2, 43, 0)
	reopened, err := OpenStore(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopened.codexHistory.reader = &fakeCodexHistory{count: 45, turnBytes: 60_000, idPrefix: "native-"}
	indexes, reads := countNativeHistoryReads(reopened)
	latest, ok := reopened.Get(chat.ID)
	if !ok || len(latest.Turns) != 10 || latest.History == nil || !latest.History.Native || latest.History.Error != "" {
		t.Fatalf("latest native history: found=%v turns=%d history=%+v", ok, len(latest.Turns), latest.History)
	}
	page, found, seen := latest, false, 0
	for {
		for _, native := range page.Turns {
			seen++
			if native.NativeTurnID == "native-turn-001" {
				found = true
				if native.UserID != bob.ID || !reflect.DeepEqual(native.Actions, []chatmodel.TurnAction{action}) {
					t.Fatalf("unannounced actor/actions lost: user=%s want=%s actions=%+v", native.UserID, bob.ID, native.Actions)
				}
			} else if native.UserID != alice.ID || len(native.Actions) != 0 {
				t.Fatalf("offset assigned to an unrelated turn: turn=%s user=%s actions=%+v", native.NativeTurnID, native.UserID, native.Actions)
			}
		}
		if !page.History.HasMore {
			break
		}
		page, err = reopened.codexHistoryPage(context.Background(), latest, page.History.Before)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !found || seen != 45 {
		t.Fatalf("paging missed recovered turn: found=%v seen=%d", found, seen)
	}
	if indexes.Load() != 0 || reads.Load() != 0 {
		t.Fatalf("offset recovery indexed history or decoded bodies: indexes=%d reads=%d", indexes.Load(), reads.Load())
	}
	other, err := reopened.CreateChat(alice.ID, project.ID, chat.Backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.SetNativeSessionPath(other.ID, "actor-native", path); err != nil {
		t.Fatal(err)
	}
	separate, _ := reopened.Get(other.ID)
	oldest, err := reopened.codexHistoryPage(context.Background(), separate, nativeHistoryCursor(other.ID, "5"))
	if err != nil {
		t.Fatal(err)
	}
	for _, native := range oldest.Turns {
		if native.UserID != alice.ID || len(native.Actions) != 0 {
			t.Fatal("shared native cache leaked another chat's actor/actions")
		}
	}
}

func TestCodexHistoryLoadsOlderPagesOnDemand(t *testing.T) {
	store := testStore(t)
	fake := &fakeCodexHistory{count: 45, turnBytes: 60_000}
	chat, path := nativeCodexChat(t, store, fake)
	app := NewApp(store, nil, "", "")
	t.Cleanup(func() { _ = app.Close() })
	latest, _ := store.Get(chat.ID)
	// 60 KB turns: one native page already fills a 200 KB transport page.
	if len(latest.Turns) != codexWindowTurnPage || latest.History == nil || !latest.History.HasMore || latest.Turns[0].NativeTurnID != "turn-035" {
		t.Fatalf("initial window: %d turns, history %+v", len(latest.Turns), latest.History)
	}
	window := historyWindow(app.snapshot(latest))
	if !window.History.HasMore || window.History.Before == "" || window.Turns[len(window.Turns)-1].NativeTurnID != "turn-044" {
		t.Fatalf("first page: %+v", window.History)
	}
	seen := map[string]bool{}
	for _, turn := range window.Turns {
		seen[turn.NativeTurnID] = true
	}
	before := window.History.Before
	for range 100 {
		// Eviction and new activity must not invalidate the client's cursor.
		store.codexHistory.threads = nil
		if len(seen) == 10 {
			fake.count = 47
		}
		current, _ := store.Get(chat.ID)
		page, err := store.codexHistoryPage(context.Background(), current, before)
		if err != nil {
			t.Fatalf("cursor %q: %v", before, err)
		}
		for _, turn := range page.Turns {
			if seen[turn.NativeTurnID] {
				t.Fatalf("turn %s was served twice", turn.NativeTurnID)
			}
			seen[turn.NativeTurnID] = true
		}
		if !page.History.HasMore {
			break
		}
		before = page.History.Before
	}
	if len(seen) != 45 || !seen["turn-000"] {
		t.Fatalf("paging reached %d of 45 turns", len(seen))
	}
	// Browsing old pages never grows the newest cache window.
	appendNativeTurns(t, path, 2, 1, 1)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	grown, _ := store.Get(chat.ID)
	if len(grown.Turns) != 10 || grown.Turns[0].NativeTurnID != "turn-037" || grown.Turns[9].NativeTurnID != "turn-046" || !grown.History.HasMore {
		t.Fatalf("after growth: %d turns, first %s, history %+v", len(grown.Turns), grown.Turns[0].NativeTurnID, grown.History)
	}
}

func TestToolDetailLoadsTheNativePageHoldingIt(t *testing.T) {
	store := testStore(t)
	fake := &fakeCodexHistory{count: 45, turnBytes: 60_000}
	chat, _ := nativeCodexChat(t, store, fake)
	app := NewApp(store, nil, "", "")
	t.Cleanup(func() { _ = app.Close() })
	latest, _ := store.Get(chat.ID)
	// A client still showing turn 3 after the server forgot its older pages.
	old := chatmodel.Turn{ID: historyTurnID(chat.ID, "turn-003"), NativeTurnID: "turn-003", NativeHistory: true}
	ref := toolPartRef(chat.ID, old, 2, chatmodel.Part{Kind: "tool_result", ID: "call-turn-003"})
	if _, ok := readToolDetailPage(app.snapshot(latest), ref, 0, ""); ok {
		t.Fatal("the old turn was unexpectedly loaded")
	}
	pages := fake.pages
	page, ok := readToolDetailPage(app.snapshotWithTool(context.Background(), latest, ref), ref, 0, "")
	if !ok || page.Total != 60_000 {
		t.Fatalf("old tool detail: %v %+v", ok, page)
	}
	if fake.pages != pages || fake.items != 1 {
		t.Fatal("tool detail walked history instead of locating its item")
	}
	other := latest
	other.ID = "other-chat"
	app.snapshotWithTool(context.Background(), other, ref)
	if fake.items != 1 {
		t.Fatal("a locator from another chat was accepted")
	}
	if page, ok := readToolDetailPage(app.snapshotWithTool(context.Background(), latest, "missing"), "missing", 0, ""); ok {
		t.Fatalf("an unknown ref resolved: %+v", page)
	}
	thought := toolPartRef(chat.ID, old, 2, chatmodel.Part{Kind: "thinking", ID: "thought-old"})
	if page, ok := readToolDetailPage(app.snapshotWithTool(context.Background(), latest, thought), thought, 0, ""); !ok || page.Text != "stored reasoning" {
		t.Fatalf("older reasoning was not addressable: %+v %v", page, ok)
	}
}

func TestNativeSmallPagesReachTheBeginningBeyondTheReadBudget(t *testing.T) {
	store := testStore(t)
	chat, _ := nativeCodexChat(t, store, &fakeCodexHistory{count: 1001, turnBytes: 10})
	latest, _ := store.Get(chat.ID)
	page, seen := latest, map[string]bool{}
	for range 20 {
		for _, turn := range page.Turns {
			if seen[turn.NativeTurnID] {
				t.Fatalf("repeated turn %s", turn.NativeTurnID)
			}
			seen[turn.NativeTurnID] = true
		}
		if !page.History.HasMore {
			break
		}
		var err error
		page, err = store.codexHistoryPage(context.Background(), latest, page.History.Before)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 1001 || !seen["turn-000"] {
		t.Fatalf("only reached %d of 1001 turns", len(seen))
	}
	if again, _ := store.Get(chat.ID); len(again.Turns) != codexWindowMaxPages*codexWindowTurnPage {
		t.Fatal("scrolling grew the cached newest window")
	}
}

func TestCodexHistoryFallsBackToTheRollout(t *testing.T) {
	for name, fake := range map[string]*fakeCodexHistory{
		"legacy thread":     {count: 3, legacy: true},
		"unsupported":       {count: 3, err: fmt.Errorf("%w: thread/read is not supported yet", agent.ErrCodexHistoryUnsupported)},
		"index not built":   {count: 0},
		"codex unavailable": {count: 3, err: errors.New("codex CLI is not installed")},
	} {
		t.Run(name, func(t *testing.T) {
			store := testStore(t)
			chat, _ := nativeCodexChat(t, store, fake)
			latest, _ := store.Get(chat.ID)
			// The rollout holds two turns with their own identities.
			if len(latest.Turns) != 2 || latest.Turns[0].NativeTurnID != "native-turn-000" || latest.History != nil {
				t.Fatalf("fallback: %d turns %+v", len(latest.Turns), latest.History)
			}
			threads := fake.threads
			if again, _ := store.Get(chat.ID); len(again.Turns) != 2 || fake.threads != threads {
				t.Fatalf("a thread known to need its rollout was asked again (%d→%d)", threads, fake.threads)
			}
		})
	}
}

func TestCodexHistoryIsNotUsedForDisabledCodexOrClaude(t *testing.T) {
	store := testStore(t)
	fake := &fakeCodexHistory{count: 3}
	chat, _ := nativeCodexChat(t, store, fake)
	if err := store.ConfigureAgent(chatmodel.BackendCodex, false, ""); err != nil {
		t.Fatal(err)
	}
	if latest, _ := store.Get(chat.ID); len(latest.Turns) != 2 || fake.threads != 0 {
		t.Fatalf("a disabled provider was executed: %d turns, %d requests", len(latest.Turns), fake.threads)
	}
}

func TestNativeHistoryHTTPPagesAndDetailsWhileRunning(t *testing.T) {
	store := testStore(t)
	owner, outsider := testUser(t, store, "native-owner"), testUser(t, store, "native-outsider")
	project := testProject(t, store, owner)
	chat, err := store.CreateChat(owner.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	appendNativeTurns(t, path, 0, 2, 1)
	if err := store.SetNativeSessionPath(chat.ID, "native-http-thread", path); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCodexHistory{count: 45, turnBytes: 60_000}
	store.codexHistory.reader = fake
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = app.Close() })
	cookie, _ := loginRequest(t, app, owner.Username)
	other, _ := loginRequest(t, app, outsider.Username)
	base, _ := store.Get(chat.ID)
	turn, err := store.beginTurn(chat.ID, "new input", chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	app.seedLiveTurn(base, turn)
	get := func(route string) chatmodel.Session {
		t.Helper()
		w := apiRequest(app, "GET", "/api/chats/"+chat.ID+route, "", cookie, "")
		var page chatmodel.Session
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatalf("history HTTP: %d %s", w.Code, w.Body.String())
		}
		return page
	}
	latest := get("?tool_details=lazy")
	if len(latest.Turns) != 11 || latest.Turns[10].ID != turn.ID {
		t.Fatalf("live window: %+v", latest.Turns)
	}
	before := latest.History.Before
	store.codexHistory.threads = nil
	older := get("/history?tool_details=lazy&before=" + url.QueryEscape(before))
	if len(older.Turns) != 10 || older.Turns[0].NativeTurnID != "turn-025" || older.Turns[9].NativeTurnID != "turn-034" {
		t.Fatalf("older: %+v", older.Turns)
	}
	// An item remains addressable when the current/live snapshot does not hold it.
	ref := older.Transcript.Parts[2].Tool.Ref
	pages := fake.pages
	w := apiRequest(app, "GET", "/api/chats/"+chat.ID+"/tool-detail?ref="+ref, "", cookie, "")
	if w.Code != 200 || fake.items != 1 || fake.pages != pages {
		t.Fatalf("targeted detail: %d items=%d pages=%d", w.Code, fake.items, fake.pages)
	}
	for _, route := range []string{"/history?before=" + url.QueryEscape(before), "/tool-detail?ref=" + ref, "/tool-image?index=0&ref=" + ref} {
		w := apiRequest(app, "GET", "/api/chats/"+chat.ID+route, "", other, "")
		if w.Code != 404 {
			t.Fatalf("native read bypassed chat authorization: %s %d", route, w.Code)
		}
	}
	if fake.items != 1 || fake.pages != pages {
		t.Fatal("unauthorized request reached native reader")
	}
	// A later native revision must not change the active base's continuation.
	fake.count = 47
	appendNativeTurns(t, path, 2, 1, 1)
	if again := get("?tool_details=lazy"); again.History.Before != before || len(again.Turns) != 11 {
		t.Fatal("live base and cursor diverged")
	}
}

func TestNativeWindowDoesNotRevokeOlderAttachmentDownloads(t *testing.T) {
	store := testStore(t)
	owner := testUser(t, store, "native-file-owner")
	project := testProject(t, store, owner)
	project.AllowExternalFiles = false
	if _, err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	chat, _ := store.CreateChat(owner.ID, project.ID, chatmodel.BackendCodex)
	file := writeSessionFile(t, t.TempDir(), "old notes.txt", "attached contents")
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	prompt := fmt.Sprintf("# Files mentioned by the user:\n\n## %s: %s\n\n## My request:\nRead the notes.", filepath.Base(file), file)
	appendTitleRecord(t, path, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": prompt}}}})
	if err := store.SetNativeSessionPath(chat.ID, "native-files-thread", path); err != nil {
		t.Fatal(err)
	}
	store.codexHistory.reader = &fakeCodexHistory{count: 45, turnBytes: 60_000}
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = app.Close() })
	cookie, _ := loginRequest(t, app, owner.Username)
	route := "/api/chats/" + chat.ID + "/file?path=" + url.QueryEscape(file) + "&download=1"
	w := apiRequest(app, "GET", route, "", cookie, "")
	if w.Code != 200 || w.Body.String() != "attached contents" {
		t.Fatalf("older attachment: %d %s", w.Code, w.Body.String())
	}
}
