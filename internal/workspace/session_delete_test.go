package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type deleteTestRunner struct {
	fakeRunner
	remove func(context.Context, agent.AgentRequest) error
}

func (r *deleteTestRunner) DeleteSession(ctx context.Context, request agent.AgentRequest) error {
	return r.remove(ctx, request)
}

func TestDeleteChatRefreshesNativeNavigationAfterSuccess(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			store := testStore(t)
			owner := testUser(t, store, "owner")
			project := testProject(t, store, owner)
			chat, _ := titleSessionFixture(t, store, backend, owner.ID, project.ID)
			other, err := store.CreateChat(owner.ID, project.ID, backend)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(`INSERT INTO chat_artifacts(session_id,type,url,created_at) VALUES(?,'pull_request','https://example.com/pull/1',1)`, chat.ID); err != nil {
				t.Fatal(err)
			}
			fail, deleted, listings, calls := true, false, 0, 0
			store.localSources = map[chatmodel.Backend]agent.SessionLister{backend: agent.SessionListerFunc(func(context.Context, agent.ListSessionsOptions) ([]agent.LocalSession, error) {
				listings++
				if deleted {
					return nil, nil
				}
				return []agent.LocalSession{{ID: chat.NativeSessionID, Path: chat.NativeSessionPath, Directory: project.Path}}, nil
			})}
			app := NewApp(store, &deleteTestRunner{remove: func(_ context.Context, request agent.AgentRequest) error {
				calls++
				if request.ChatID != chat.ID || request.Backend != backend || request.NativeSessionID != chat.NativeSessionID || request.NativeSessionPath != chat.NativeSessionPath {
					t.Errorf("incorrect delete target: %+v", request)
				}
				if fail {
					return errors.New("native failure contains a private path")
				}
				deleted = true
				return nil
			}}, "", "")
			t.Cleanup(func() { app.Close() })
			cookie, csrf := loginRequest(t, app, owner.Username)
			endpoint := "/api/chats/" + chat.ID + "/delete"
			response := apiRequest(app, "POST", endpoint, `{}`, cookie, csrf)
			if response.Code != 502 || !store.CanAccessChat(owner.ID, chat.ID) || strings.Contains(response.Body.String(), "private path") {
				t.Fatalf("native failure removed chat or exposed details: %d %s", response.Code, response.Body.String())
			}
			fail = false
			before := listings
			response = apiRequest(app, "POST", endpoint, `{}`, cookie, csrf)
			var navigation Navigation
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &navigation) != nil || calls != 2 || listings <= before {
				t.Fatalf("delete did not refresh native list: %d %s calls=%d lists=%d", response.Code, response.Body.String(), calls, listings)
			}
			if len(navigation.Chats) != 1 || navigation.Chats[0].ID != other.ID {
				t.Fatalf("wrong navigation after deletion: %+v", navigation)
			}
			if _, ok := store.getSession(chat.ID); ok || store.CanAccessChat(owner.ID, chat.ID) {
				t.Fatal("deleted session remains accessible")
			}
			for _, table := range []string{"chat_artifacts", "turn_actors", "session_workspaces", "chat_collaborators"} {
				var count int
				if err := store.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE session_id=?`, chat.ID).Scan(&count); err != nil || count != 0 {
					t.Fatal("deleted session retained related records", table, count, err)
				}
			}
			loaded, err := OpenStore(store.Root())
			if err != nil {
				t.Fatal(err)
			}
			defer loaded.Close()
			loaded.localSources = store.localSources
			nav, err := loaded.UserNavigation(owner.ID)
			if err != nil || len(nav.Chats) != 1 || nav.Chats[0].ID != other.ID {
				t.Fatal("deleted chat reappeared after restart", err)
			}
		})
	}
}

func TestDeleteChatPermissionsBusyAndEmptySession(t *testing.T) {
	store := testStore(t)
	owner, viewer := testUser(t, store, "owner"), testUser(t, store, "viewer")
	project := testProject(t, store, owner, viewer)
	project.SharedChats = true
	project.MemberAccess = map[string]string{viewer.ID: "read-only"}
	if _, err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	chat, _ := titleSessionFixture(t, store, chatmodel.BackendClaude, owner.ID, project.ID)
	calls := 0
	app := NewApp(store, &deleteTestRunner{remove: func(context.Context, agent.AgentRequest) error { calls++; return nil }}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, owner.Username)
	viewerCookie, viewerCSRF := loginRequest(t, app, viewer.Username)
	endpoint := "/api/chats/" + chat.ID + "/delete"
	if response := apiRequest(app, "POST", endpoint, `{}`, viewerCookie, viewerCSRF); response.Code != 404 {
		t.Fatal("read-only deletion accepted", response.Code)
	}
	if response := apiRequest(app, "POST", endpoint, `{}`, cookie, ""); response.Code != 403 {
		t.Fatal("missing CSRF accepted", response.Code)
	}
	app.queues = map[string][]*queuedMessage{chat.ID: {{id: "queued", input: messageInput{Message: "Keep this draft"}}}}
	if response := apiRequest(app, "POST", endpoint, `{}`, cookie, csrf); response.Code != 409 {
		t.Fatal("queued session deleted", response.Code)
	}
	delete(app.queues, chat.ID)
	turn, err := store.BeginTurn(chat.ID, "Running")
	if err != nil {
		t.Fatal(err)
	}
	if response := apiRequest(app, "POST", endpoint, `{}`, cookie, csrf); response.Code != 409 {
		t.Fatal("running session deleted", response.Code)
	}
	if err := store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{}, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	empty, err := store.CreateChat(owner.ID, project.ID, chatmodel.BackendClaude)
	if err != nil {
		t.Fatal(err)
	}
	if response := apiRequest(app, "POST", "/api/chats/"+empty.ID+"/delete", `{}`, cookie, csrf); response.Code != 200 {
		t.Fatalf("empty session could not be deleted: %d %s", response.Code, response.Body.String())
	}
	if calls != 0 {
		t.Fatal("invalid or empty deletion reached provider")
	}
}

func TestDeleteChatReservesSessionAndCancelsOnShutdown(t *testing.T) {
	store := testStore(t)
	owner := testUser(t, store, "owner")
	project := testProject(t, store, owner)
	chat, _ := titleSessionFixture(t, store, chatmodel.BackendClaude, owner.ID, project.ID)
	started := make(chan struct{})
	app := NewApp(store, &deleteTestRunner{remove: func(ctx context.Context, _ agent.AgentRequest) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, owner.Username)
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- apiRequest(app, "POST", "/api/chats/"+chat.ID+"/delete", `{}`, cookie, csrf) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("delete did not start")
	}
	if err := app.startTurn(chat, "Concurrent message", chatmodel.CodexOptions{}); err == nil {
		t.Fatal("delete raced a new turn")
	}
	if _, err := app.forkChat(chat.ID, chatmodel.BackendCodex, chatmodel.CodexOptions{}, owner.ID); err == nil {
		t.Fatal("delete raced conversion")
	}
	if _, err := app.queueWhileRunning(chat, messageInput{Message: "Concurrent queue"}, chatmodel.CodexOptions{}, false); err == nil {
		t.Fatal("message queued behind deletion")
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-finished:
		if response.Code != 502 {
			t.Fatal("cancelled deletion succeeded", response.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel deletion")
	}
	if _, ok := store.getSession(chat.ID); !ok {
		t.Fatal("cancelled deletion removed records")
	}
}

func TestDeleteLocalClaudeSessionDoesNotReturnFromCachedDiscovery(t *testing.T) {
	for _, listFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "list-failure"}[listFailure], func(t *testing.T) {
			localSessionHomes(t)
			store := testStore(t)
			owner := testUser(t, store, "owner")
			project := testProject(t, store, owner)
			project.SharedChats, project.SharedLocalChats = true, true
			if _, err := store.SaveProject(project); err != nil {
				t.Fatal(err)
			}
			path := writeLocalSession(t, chatmodel.BackendClaude, "delete-local", project.Path, 1)
			broken := false
			lister := agent.NewClaudeSessionLister()
			store.localSources = map[chatmodel.Backend]agent.SessionLister{chatmodel.BackendClaude: agent.SessionListerFunc(func(ctx context.Context, options agent.ListSessionsOptions) ([]agent.LocalSession, error) {
				if broken {
					return nil, errors.New("native list unavailable")
				}
				return lister.ListSessions(ctx, options)
			})}
			nav, err := store.UserNavigation(owner.ID)
			if err != nil || len(nav.Chats) != 1 {
				t.Fatalf("native discovery failed: %+v %v", nav, err)
			}
			chat := nav.Chats[0]
			app := NewApp(store, &deleteTestRunner{remove: func(ctx context.Context, request agent.AgentRequest) error {
				if err := (&agent.CLIRunner{}).DeleteSession(ctx, request); err != nil {
					return err
				}
				broken = listFailure
				return nil
			}}, "", "")
			t.Cleanup(func() { app.Close() })
			cookie, csrf := loginRequest(t, app, owner.Username)
			response := apiRequest(app, "POST", "/api/chats/"+chat.ID+"/delete", `{}`, cookie, csrf)
			if listFailure {
				if response.Code != 502 || !strings.Contains(response.Body.String(), "native session list") {
					t.Fatalf("missing refresh failure: %d %s", response.Code, response.Body.String())
				}
			} else if response.Code != 200 {
				t.Fatalf("native delete failed: %d %s", response.Code, response.Body.String())
			}
			if err := store.syncLocalSessions(true); err != nil {
				t.Fatal(err)
			}
			nav, err = store.UserNavigation(owner.ID)
			if err != nil || len(nav.Chats) != 0 {
				t.Fatal("deleted chat returned from cached native metadata", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("native history retained", err)
			}
		})
	}
}
