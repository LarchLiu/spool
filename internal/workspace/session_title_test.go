package workspace

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func appendTitleRecord(t *testing.T, path string, record map[string]any) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(record); err != nil {
		t.Fatal(err)
	}
}

func titleSessionFixture(t *testing.T, store *Store, backend chatmodel.Backend, user, project string) (chatmodel.Session, func(string)) {
	t.Helper()
	chat, err := store.createSession(user, project, backend)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "native.jsonl")
	indexPath := filepath.Join(root, "session_index.jsonl")
	if backend == chatmodel.BackendCodex {
		path = filepath.Join(root, "sessions", "2026", "10", "28", "native.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		appendNativeTurns(t, path, 0, 1, 0)
	} else {
		appendTitleRecord(t, path, map[string]any{"type": "user", "uuid": "user-one", "message": map[string]any{"role": "user", "content": "First prompt"}})
		appendTitleRecord(t, path, map[string]any{"type": "assistant", "uuid": "reply-one", "parentUuid": "user-one", "message": map[string]any{"role": "assistant", "content": "Full answer"}})
	}
	if err := store.SetNativeSessionPath(chat.ID, "native-session-title", path); err != nil {
		t.Fatal(err)
	}
	chat, _ = store.getSession(chat.ID)
	return chat, func(title string) {
		if backend == chatmodel.BackendCodex {
			appendTitleRecord(t, indexPath, map[string]any{"id": chat.NativeSessionID, "thread_name": title})
		} else {
			appendTitleRecord(t, path, map[string]any{"type": "ai-title", "aiTitle": title, "sessionId": chat.NativeSessionID})
		}
	}
}

func TestSessionNativeTitlePersistsWithoutChangingActivityOrder(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			store := testStore(t)
			chat, rename := titleSessionFixture(t, store, backend, "", "")
			indexes, reads := countNativeHistoryReads(store)
			latest, ok := store.Get(chat.ID)
			if !ok || latest.Title != "New chat" {
				t.Fatalf("missing native title erased the existing title: %q", latest.Title)
			}
			rename("  Provider-generated title  ")
			latest, ok = store.Get(chat.ID)
			if !ok || latest.Title != "Provider-generated title" || len(latest.Turns) != 1 || len(latest.Transcript.Parts) != 2 {
				t.Fatalf("native title or history missing: %+v", latest)
			}
			stored, ok := store.getSession(chat.ID)
			if !ok || stored.Title != latest.Title || stored.UpdatedAt != chat.UpdatedAt || stored.NativeSessionID != chat.NativeSessionID {
				t.Fatalf("native-title sync changed unrelated session metadata: %+v", stored)
			}
			beforeIndexes, beforeReads := indexes.Load(), reads.Load()
			for range 3 {
				if cached, _ := store.Get(chat.ID); cached.Title != latest.Title {
					t.Fatalf("cached read lost title: %q", cached.Title)
				}
			}
			if indexes.Load() != beforeIndexes || reads.Load() != beforeReads {
				t.Fatal("title extraction bypassed the parsed-history cache")
			}
			rename("Renamed in native session")
			latest, _ = store.Get(chat.ID)
			if latest.Title != "Renamed in native session" || latest.UpdatedAt != chat.UpdatedAt {
				t.Fatalf("title did not refresh independently of activity: %q", latest.Title)
			}
			if backend == chatmodel.BackendCodex && (indexes.Load() != beforeIndexes || reads.Load() != beforeReads) {
				t.Fatal("changing the Codex title index forced history to be parsed again")
			}
			rename(" \t ")
			if latest, _ := store.Get(chat.ID); latest.Title != "Renamed in native session" {
				t.Fatal("empty native title erased the saved title")
			}
			if err := os.Rename(chat.NativeSessionPath, chat.NativeSessionPath+".missing"); err != nil {
				t.Fatal(err)
			}
			if latest, _ := store.Get(chat.ID); latest.Title != "Renamed in native session" || latest.History == nil || latest.History.Error == "" {
				t.Fatal("unavailable native history erased the saved title")
			}
		})
	}
}

func TestSessionNativeTitleReachesAPIAndNavigation(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			store := testStore(t)
			user := testUser(t, store, "title-viewer")
			project := testProject(t, store, user)
			chat, rename := titleSessionFixture(t, store, backend, user.ID, project.ID)
			rename("Native title for sidebar")
			app := NewApp(store, &fakeRunner{}, "", "")
			t.Cleanup(func() { app.Close() })
			cookie, csrf := loginRequest(t, app, user.Username)
			response := apiRequest(app, "GET", "/api/chats/"+chat.ID, "", cookie, csrf)
			var value chatmodel.Session
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &value) != nil || value.Title != "Native title for sidebar" {
				t.Fatalf("API did not return the native title: %s", response.Body.String())
			}
			navigation, err := store.UserNavigation(user.ID)
			if err != nil || len(navigation.Chats) != 1 || navigation.Chats[0].Title != value.Title || len(navigation.Chats[0].Transcript.Parts) != 0 {
				t.Fatalf("navigation did not retain metadata-only native title: %+v, %v", navigation, err)
			}
		})
	}
}

func TestSessionNativeTitlePreservesIssueLabel(t *testing.T) {
	app, project, _, _ := issueFixture(t)
	chat, rename := titleSessionFixture(t, app.store, chatmodel.BackendClaude, project.Issues.UserID, project.ID)
	const want = "Issues #1 - Fix fixture"
	if _, err := app.store.db.Exec(`INSERT INTO automation_chats(session_id,project_id) VALUES(?,?)`, chat.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.db.Exec(`UPDATE sessions SET title=? WHERE id=?`, want, chat.ID); err != nil {
		t.Fatal(err)
	}
	rename("Provider-generated title for a task")
	if latest, ok := app.store.Get(chat.ID); !ok || !latest.AutomationChat || latest.Title != want {
		t.Fatalf("native title replaced the issue label: %+v", latest)
	}
}

func TestSessionNativeTitleMetadataDoesNotResendHistory(t *testing.T) {
	store := testStore(t)
	user := testUser(t, store, "title-viewer")
	project := testProject(t, store, user)
	chat, rename := titleSessionFixture(t, store, chatmodel.BackendCodex, user.ID, project.ID)
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { app.Close() })
	app.streamHeartbeat = 250 * time.Millisecond
	cookie, _ := loginRequest(t, app, user.Username)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
	request.AddCookie(cookie)
	stream := newStreamRecorder()
	done := make(chan struct{})
	go func() { defer close(done); app.ServeHTTP(stream, request) }()
	defer func() { cancel(); <-done }()
	if event, initial := readChatStreamFrame(t, stream); event != "snapshot" || initial.Title != "New chat" {
		t.Fatal("unexpected initial snapshot")
	}
	rename("Native rename while connected")
	if latest, _ := store.Get(chat.ID); latest.Title != "Native rename while connected" {
		t.Fatal("reading history did not persist the native rename")
	}
	for range 10 {
		event, update := readChatStreamFrame(t, stream)
		if event == "ping" {
			continue
		}
		if event != "metadata" || update.Title != "Native rename while connected" || update.UpdatedAt != chat.UpdatedAt || len(update.Transcript.Parts) != 0 {
			t.Fatalf("title-only update replayed history or changed revision: %s, %+v", event, update)
		}
		if event, _ = readChatStreamFrame(t, stream); event != "ping" {
			t.Fatalf("title-only metadata was followed by redundant history: %s", event)
		}
		return
	}
	t.Fatal("title metadata never reached the connected viewer")
}
