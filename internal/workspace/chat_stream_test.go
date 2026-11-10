package workspace

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

// Count SQL preparation through database/sql's normal fallback path. Keeping
// this below the Store catches idle polling even when it sends no SSE frames.
type streamQueryDriver struct {
	driver.Driver
	queries atomic.Int64
}

func (d *streamQueryDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &streamQueryConn{Conn: conn, queries: &d.queries}, nil
}

type streamQueryConn struct {
	driver.Conn
	queries *atomic.Int64
}

func (c *streamQueryConn) Prepare(query string) (driver.Stmt, error) {
	if strings.Contains(query, "FROM sessions WHERE id=?") {
		c.queries.Add(1)
	}
	return c.Conn.Prepare(query)
}

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
				request := localRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
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

func TestChatStreamIdleDoesNotPollDatabase(t *testing.T) {
	store := testStore(t)
	user := testUser(t, store, "viewer")
	project := testProject(t, store, user)
	chat, err := store.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, _ := loginRequest(t, app, "viewer")
	counted := &streamQueryDriver{Driver: &sqlite.Driver{}}
	name := "chat-stream-" + chatmodel.NewID()
	sql.Register(name, counted)
	db, err := sql.Open(name, store.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := store.db.Close(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	store.db = db
	ctx, cancel := context.WithCancel(context.Background())
	request := localRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
	request.AddCookie(cookie)
	stream := newStreamRecorder()
	done := make(chan struct{})
	go func() { defer close(done); app.ServeHTTP(stream, request) }()
	defer func() { cancel(); <-done }()
	if event, _ := readChatStreamFrame(t, stream); event != "snapshot" {
		t.Fatal("missing initial snapshot")
	}
	before := counted.queries.Load()
	// Include several former 100 ms ticks, without reaching the heartbeat.
	// Presence-only updates must not cause conversation queries either.
	for range 4 {
		store.changes.publish(changePresence)
		time.Sleep(100 * time.Millisecond)
	}
	reads := counted.queries.Load() - before
	t.Logf("idle session queries over 400 ms: %d", reads)
	if reads > 1 {
		t.Fatalf("idle stream kept querying its session: %d reads", reads)
	}
}

func TestChatStreamIdleWakesForTurnAndAccessChanges(t *testing.T) {
	store := testStore(t)
	user := testUser(t, store, "viewer")
	project := testProject(t, store, user)
	chat, err := store.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, _ := loginRequest(t, app, "viewer")
	ctx, cancel := context.WithCancel(context.Background())
	request := localRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
	request.AddCookie(cookie)
	stream := newStreamRecorder()
	done := make(chan struct{})
	go func() { defer close(done); app.ServeHTTP(stream, request) }()
	defer func() { cancel(); <-done }()
	if event, _ := readChatStreamFrame(t, stream); event != "snapshot" {
		t.Fatal("missing initial snapshot")
	}
	readTurn := func() chatmodel.Session {
		t.Helper()
		for range 3 {
			if event, value := readChatStreamFrame(t, stream); event == "turn" {
				return value
			} else if event != "metadata" {
				t.Fatalf("expected turn or metadata, got %s", event)
			}
		}
		t.Fatal("stream never delivered the turn")
		return chatmodel.Session{}
	}
	turn, err := store.BeginTurn(chat.ID, "wake the idle stream")
	if err != nil {
		t.Fatal(err)
	}
	if value := readTurn(); value.Status != chatmodel.StatusRunning || len(value.Turns) != 1 || value.Turns[0].ID != turn.ID {
		t.Fatalf("idle stream did not pick up the new turn: %s", value.Status)
	}
	if err := store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{}, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if value := readTurn(); value.Status != chatmodel.StatusCompleted {
		t.Fatalf("stream did not pick up turn completion: %s", value.Status)
	}
	project.Archived = true
	if _, err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle stream did not close after project access was revoked")
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
			request := localRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
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
