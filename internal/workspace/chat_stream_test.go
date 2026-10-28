package workspace

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func readChatStreamFrame(t *testing.T, stream *streamRecorder) (string, chatmodel.Session) {
	t.Helper()
	select {
	case chunk := <-stream.writes:
		frame := string(chunk)
		event, _, _ := strings.Cut(strings.TrimPrefix(frame, "event: "), "\n")
		_, data, _ := strings.Cut(frame, "data: ")
		var value chatmodel.Session
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &value); err != nil {
			t.Fatal(err)
		}
		return event, value
	case <-time.After(5 * time.Second):
		t.Fatal("missing SSE frame")
		return "", chatmodel.Session{}
	}
}

func TestChatStreamIdleHistoryIsSentOnlyOnce(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "large-history", true: "empty"}[empty], func(t *testing.T) {
			store := testStore(t)
			user := testUser(t, store, "viewer")
			project := testProject(t, store, user)
			chat, _ := store.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
			if !empty {
				path := filepath.Join(t.TempDir(), "native.jsonl")
				appendNativeTurns(t, path, 0, 3, 80)
				if err := store.SetNativeSessionPath(chat.ID, "sse-native", path); err != nil {
					t.Fatal(err)
				}
			}
			app := NewApp(store, &fakeRunner{}, "", "")
			t.Cleanup(func() { app.Close() })
			app.streamHeartbeat = 250 * time.Millisecond
			cookie, _ := loginRequest(t, app, "viewer")
			// Reopening a stream must get a fresh snapshot, not depend on the
			// previous connection having received any particular event.
			for range 2 {
				ctx, cancel := context.WithCancel(context.Background())
				request := httptest.NewRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
				request.AddCookie(cookie)
				stream := newStreamRecorder()
				done := make(chan struct{})
				go func() { defer close(done); app.ServeHTTP(stream, request) }()
				func() {
					defer func() { cancel(); <-done }()
					if event, value := readChatStreamFrame(t, stream); event != "snapshot" || value.ID != chat.ID || (!empty && len(value.Turns) != 1) {
						t.Fatalf("missing initial history: %s %#v", event, value.History)
					}
					if event, _ := readChatStreamFrame(t, stream); event != "ping" {
						t.Fatalf("unchanged history was resent after the snapshot: %s", event)
					}
				}()
			}
		})
	}
}

type snapshotHookWriter struct {
	*streamRecorder
	once sync.Once
	hook func()
}

func (w *snapshotHookWriter) Flush() { w.once.Do(w.hook) }

func TestChatStreamDoesNotLoseLiveChangesAroundInitialSnapshot(t *testing.T) {
	for _, duringSnapshot := range []bool{false, true} {
		t.Run(map[bool]string{false: "after-initialization", true: "during-snapshot"}[duringSnapshot], func(t *testing.T) {
			store := testStore(t)
			user := testUser(t, store, "viewer")
			project := testProject(t, store, user)
			chat, _ := store.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
			app := NewApp(store, &fakeRunner{}, "", "")
			t.Cleanup(func() { app.Close() })
			turn, err := store.BeginTurn(chat.ID, "live input")
			if err != nil {
				t.Fatal(err)
			}
			turn = app.seedLiveTurn(chat, turn)
			appendTurnText(&turn, chatmodel.Part{Role: "assistant", Kind: "text", Text: "initial"})
			app.liveMu.Lock()
			app.live[chat.ID] = turn
			app.liveMu.Unlock()
			app.streamHeartbeat = 250 * time.Millisecond
			change := func() {
				app.liveMu.Lock()
				defer app.liveMu.Unlock()
				current := app.live[chat.ID]
				appendTurnText(&current, chatmodel.Part{Role: "assistant", Kind: "text", Text: " changed"})
				app.live[chat.ID] = current
			}
			cookie, _ := loginRequest(t, app, "viewer")
			ctx, cancel := context.WithCancel(context.Background())
			request := httptest.NewRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
			request.AddCookie(cookie)
			stream := newStreamRecorder()
			writer := &snapshotHookWriter{streamRecorder: stream, hook: func() {
				if duringSnapshot {
					change()
				}
			}}
			done := make(chan struct{})
			go func() { defer close(done); app.ServeHTTP(writer, request) }()
			defer func() { cancel(); <-done }()
			if event, initial := readChatStreamFrame(t, stream); event != "snapshot" || initial.Transcript.Parts[1].Text != "initial" {
				t.Fatal("incorrect initial live snapshot")
			}
			if !duringSnapshot {
				if event, _ := readChatStreamFrame(t, stream); event != "ping" {
					t.Fatalf("unchanged live turn was resent: %s", event)
				}
				change()
			}
			for range 8 {
				event, updated := readChatStreamFrame(t, stream)
				if event == "ping" {
					continue
				}
				if event != "turn" || len(updated.Transcript.Parts) != 2 || updated.Transcript.Parts[1].Text != "initial changed" {
					t.Fatalf("live delta after snapshot was lost: %s", event)
				}
				return
			}
			t.Fatal("live delta after snapshot was never delivered")
		})
	}
}
