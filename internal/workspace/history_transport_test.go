package workspace

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestHistoryTransportFinishesCrossingTurnWithoutChangingFullSnapshot(t *testing.T) {
	store := testStore(t)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 1, 1)
	appendNativeTurns(t, path, 1, 1, 80)
	appendNativeTurns(t, path, 2, 1, 6)
	full, err := store.readHistory(chatmodel.Session{ID: "transport", Backend: chatmodel.BackendCodex, NativeSessionPath: path})
	if err != nil {
		t.Fatal(err)
	}
	page := historyWindow(full)
	if len(page.Turns) != 2 || page.Turns[0].Prompt != "prompt-001" || len(page.Turns[0].Parts) != 82 || len(page.Transcript.Parts) != 90 || !page.History.HasMore || page.Transcript.Cut {
		t.Fatal("200 KB soft target split or excluded the crossing turn")
	}
	if len(full.Turns) != 3 || full.Turns[1].PartStart != 3 || full.Turns[2].PartStart != 85 || len(full.Transcript.Parts) != 93 {
		t.Fatal("transport slicing mutated the full in-memory history")
	}
	older, err := historyPage(full, page.History.Before)
	if err != nil || len(older.Turns) != 1 || older.Turns[0].Prompt != "prompt-000" || older.History.HasMore {
		t.Fatal("transport cursor skipped the turn before the crossing turn", err)
	}
}

func TestHistoryTransportHasNoTurnOrPartContentLimit(t *testing.T) {
	store := testStore(t)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 100, 0)
	full, err := store.readHistory(chatmodel.Session{ID: "many", Backend: chatmodel.BackendCodex, NativeSessionPath: path})
	if err != nil {
		t.Fatal(err)
	}
	page := historyWindow(full)
	if len(page.Turns) != 100 || page.History.HasMore {
		t.Fatal("page has a turn count limit")
	}
	last := &full.Turns[99]
	large := strings.Repeat("x", 900_000)
	full.Transcript.Parts[last.PartEnd-1].Text = large
	page = historyWindow(full)
	if len(page.Turns) != 1 || page.Transcript.Parts[1].Text != large || page.Transcript.Cut || !page.History.HasMore {
		t.Fatal("oversized single part/turn was truncated")
	}
}

func TestHistoryTransportCursorSurvivesAppendAndLiveNativeIdentityChange(t *testing.T) {
	store := testStore(t)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 3, 18)
	chat := chatmodel.Session{ID: "append", Backend: chatmodel.BackendCodex, NativeSessionPath: path}
	full, _ := store.readHistory(chat)
	page := historyWindow(full)
	appendNativeTurns(t, path, 3, 1, 18)
	updated, _ := store.readHistory(chat)
	older, err := historyPage(updated, page.History.Before)
	if err != nil || len(older.Turns) != 1 || older.Turns[0].Prompt != "prompt-000" {
		t.Fatal("append shifted the history boundary", err)
	}
	start := len(full.Transcript.Parts)
	live := chatmodel.Turn{ID: "runtime-only", PartStart: start, InputEnd: start + 1, PartEnd: start + 2, Status: chatmodel.StatusRunning}
	full.Turns = append(full.Turns, live)
	full.Transcript.Parts = append(full.Transcript.Parts, chatmodel.Part{Role: "user", Kind: "text", Text: "live input"}, chatmodel.Part{Role: "assistant", Kind: "text", Text: strings.Repeat("z", 900_000)})
	livePage := historyWindow(full)
	if len(livePage.Turns) != 1 || len(livePage.Transcript.Parts[1].Text) != 900_000 {
		t.Fatal("live turn was limited")
	}
	// The native session now owns the live turn, with a completely different ID.
	older, err = historyPage(updated, livePage.History.Before)
	if err != nil || len(older.Turns) != 2 || older.Turns[0].Prompt != "prompt-001" || older.Turns[1].Prompt != "prompt-002" {
		t.Fatal("history cursor relied on the unwritten live identity", err)
	}
}

func TestHistoryTransportSSEDoesNotBypassPagination(t *testing.T) {
	store := testStore(t)
	user := testUser(t, store, "viewer")
	project := testProject(t, store, user)
	chat, _ := store.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	path := filepath.Join(t.TempDir(), "native.jsonl")
	appendNativeTurns(t, path, 0, 25, 18)
	_ = store.SetNativeSessionPath(chat.ID, "sse-native", path)
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, _ := loginRequest(t, app, "viewer")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
	request.AddCookie(cookie)
	stream := newStreamRecorder()
	done := make(chan struct{})
	go func() { defer close(done); app.ServeHTTP(stream, request) }()
	defer func() { cancel(); <-done }()
	seen := map[string]bool{}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !seen["snapshot"] {
		select {
		case chunk := <-stream.writes:
			frame := string(chunk)
			event, _, _ := strings.Cut(strings.TrimPrefix(frame, "event: "), "\n")
			if event != "snapshot" && event != "turn" {
				continue
			}
			_, data, ok := strings.Cut(frame, "data: ")
			if !ok {
				continue
			}
			var page chatmodel.Session
			if json.Unmarshal([]byte(strings.TrimSpace(data)), &page) != nil || len(page.Turns) != 2 || len(page.Transcript.Parts) != 40 || page.History == nil || !page.History.HasMore || page.Transcript.Parts[0].Text != "prompt-023" {
				t.Fatalf("SSE %s bypassed the history window", event)
			}
			seen[event] = true
		case <-deadline.C:
			t.Fatal("SSE did not deliver the initial snapshot")
		}
	}
	base, _ := store.Get(chat.ID)
	turn, err := store.BeginTurn(chat.ID, "live input")
	if err != nil {
		t.Fatal(err)
	}
	turn = app.seedLiveTurn(base, turn)
	turn.Reply = strings.Repeat("z", 900_000)
	appendTurnText(&turn, chatmodel.Part{Role: "assistant", Kind: "text", Text: turn.Reply})
	app.liveMu.Lock()
	app.live[chat.ID] = turn
	app.liveMu.Unlock()
	if err := store.SaveRunningTurn(chat.ID, turn); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case chunk := <-stream.writes:
			frame := string(chunk)
			if !strings.HasPrefix(frame, "event: turn\n") {
				continue
			}
			_, data, _ := strings.Cut(frame, "data: ")
			var page chatmodel.Session
			if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Turns) == 0 || page.Turns[len(page.Turns)-1].ID != turn.ID {
				continue
			}
			if len(page.Turns) != 1 || len(page.Transcript.Parts) != 2 || page.Transcript.Parts[1].Text != turn.Reply || !page.History.HasMore || page.Turns[0].PartStart != 0 || page.Turns[0].PartEnd != 2 {
				t.Fatal("SSE truncated live content or used global part ranges")
			}
			return
		case <-deadline.C:
			t.Fatal("SSE did not deliver the oversized live turn")
		}
	}
}
