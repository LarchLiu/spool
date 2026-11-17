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

type archiveTestRunner struct {
	fakeRunner
	archive func(context.Context, agent.AgentRequest) error
}

func (r *archiveTestRunner) ArchiveSession(ctx context.Context, request agent.AgentRequest) error {
	return r.archive(ctx, request)
}

func TestArchiveChatUpdatesNavigationOnlyAfterNativeSuccess(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			store := testStore(t)
			owner := testUser(t, store, "owner")
			project := testProject(t, store, owner)
			chat, _ := titleSessionFixture(t, store, backend, owner.ID, project.ID)
			before, err := os.ReadFile(chat.NativeSessionPath)
			if err != nil {
				t.Fatal(err)
			}
			fail, calls := true, 0
			nativeArchived, listings := false, 0
			store.localSources = map[chatmodel.Backend]agent.SessionLister{backend: agent.SessionListerFunc(func(context.Context, agent.ListSessionsOptions) ([]agent.LocalSession, error) {
				listings++
				return []agent.LocalSession{{ID: chat.NativeSessionID, Path: chat.NativeSessionPath, Directory: project.Path, Backend: backend, Archived: nativeArchived}}, nil
			})}
			runner := &archiveTestRunner{archive: func(_ context.Context, request agent.AgentRequest) error {
				calls++
				if request.Backend != backend || request.NativeSessionID != chat.NativeSessionID || request.NativeSessionPath != chat.NativeSessionPath || request.WorkingDirectory != project.Path {
					t.Errorf("wrong archive target: %+v", request)
				}
				if fail {
					return errors.New("native provider failed with a private path")
				}
				nativeArchived = true
				return nil
			}}
			app := NewApp(store, runner, "", "")
			t.Cleanup(func() { app.Close() })
			cookie, csrf := loginRequest(t, app, owner.Username)
			endpoint := "/api/chats/" + chat.ID + "/archive"
			response := apiRequest(app, "POST", endpoint, `{}`, cookie, csrf)
			if response.Code != 502 || store.nativeSessionArchived(backend, chat.NativeSessionID) || !store.CanControlChat(owner.ID, chat.ID) {
				t.Fatalf("failed archive hid chat: %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private path") || !strings.Contains(response.Body.String(), "error") {
				t.Fatalf("unsafe or missing failure message: %s", response.Body.String())
			}
			fail = false
			response = apiRequest(app, "POST", endpoint, `{}`, cookie, csrf)
			if response.Code != 200 || !store.nativeSessionArchived(backend, chat.NativeSessionID) || calls != 2 || listings == 0 {
				t.Fatalf("archive failed: %d %s", response.Code, response.Body.String())
			}
			var navigation Navigation
			if err := json.Unmarshal(response.Body.Bytes(), &navigation); err != nil || len(navigation.Chats) != 0 {
				t.Fatalf("archive did not return refreshed native navigation: %s %v", response.Body.String(), err)
			}
			if store.CanAccessChat(owner.ID, chat.ID) || store.CanControlChat(owner.ID, chat.ID) {
				t.Fatal("archived chat is still active")
			}
			for range 2 {
				nav, err := store.UserNavigation(owner.ID)
				if err != nil || len(nav.Chats) != 0 {
					t.Fatalf("archived chat reappeared: %+v %v", nav, err)
				}
			}
			stored, ok := store.getSession(chat.ID)
			after, err := os.ReadFile(chat.NativeSessionPath)
			if !ok || stored.NativeSessionID != chat.NativeSessionID || stored.UpdatedAt != chat.UpdatedAt || err != nil || string(after) != string(before) {
				t.Fatal("archive destroyed history or changed activity")
			}
			if _, err := store.BeginTurn(chat.ID, "Stale request"); err == nil {
				t.Fatal("archived chat accepted a stale message")
			}
			loaded, err := OpenStore(store.Root())
			if err != nil {
				t.Fatal(err)
			}
			defer loaded.Close()
			loaded.localSources = store.localSources
			nav, err := loaded.UserNavigation(owner.ID)
			if err != nil || len(nav.Chats) != 0 {
				t.Fatal("restart did not reload native archive state", err)
			}
			nativeArchived = false
			if err := store.syncLocalSessions(true); err != nil {
				t.Fatal(err)
			}
			nav, err = store.UserNavigation(owner.ID)
			if err != nil || len(nav.Chats) != 1 || !store.CanAccessChat(owner.ID, chat.ID) {
				t.Fatal("native unarchive was overridden by Spool state", err)
			}
		})
	}
}

func TestArchiveChatPermissionsAndBusySession(t *testing.T) {
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
	app := NewApp(store, &archiveTestRunner{archive: func(context.Context, agent.AgentRequest) error { calls++; return nil }}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, owner.Username)
	viewerCookie, viewerCSRF := loginRequest(t, app, viewer.Username)
	endpoint := "/api/chats/" + chat.ID + "/archive"
	if response := apiRequest(app, "POST", endpoint, `{}`, viewerCookie, viewerCSRF); response.Code != 404 {
		t.Fatal("read-only archive accepted", response.Code)
	}
	if response := apiRequest(app, "POST", endpoint, `{}`, cookie, ""); response.Code != 403 {
		t.Fatal("missing CSRF accepted", response.Code)
	}
	app.queues = map[string][]*queuedMessage{chat.ID: {{id: "queued", input: messageInput{Message: "Keep this draft"}}}}
	if response := apiRequest(app, "POST", endpoint, `{}`, cookie, csrf); response.Code != 409 {
		t.Fatal("queued chat archived", response.Code)
	}
	delete(app.queues, chat.ID)
	turn, err := store.BeginTurn(chat.ID, "Running")
	if err != nil {
		t.Fatal(err)
	}
	if response := apiRequest(app, "POST", endpoint, `{}`, cookie, csrf); response.Code != 409 {
		t.Fatal("running chat archived", response.Code)
	}
	if err := store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{}, nil, chatmodel.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	empty, err := store.CreateChat(owner.ID, project.ID, chatmodel.BackendClaude)
	if err != nil {
		t.Fatal(err)
	}
	if response := apiRequest(app, "POST", "/api/chats/"+empty.ID+"/archive", `{}`, cookie, csrf); response.Code != 409 {
		t.Fatal("empty chat archived", response.Code)
	}
	if calls != 0 {
		t.Fatal("invalid archive reached provider")
	}
}

func TestArchiveChatReservesSessionAndCancelsOnShutdown(t *testing.T) {
	store := testStore(t)
	owner := testUser(t, store, "owner")
	project := testProject(t, store, owner)
	chat, _ := titleSessionFixture(t, store, chatmodel.BackendClaude, owner.ID, project.ID)
	started := make(chan struct{})
	app := NewApp(store, &archiveTestRunner{archive: func(ctx context.Context, _ agent.AgentRequest) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, owner.Username)
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- apiRequest(app, "POST", "/api/chats/"+chat.ID+"/archive", `{}`, cookie, csrf) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("archive did not start")
	}
	if err := app.startTurn(chat, "Concurrent message", chatmodel.CodexOptions{}); err == nil {
		t.Fatal("archive raced a new turn")
	}
	if _, err := app.forkChat(chat.ID, chatmodel.BackendCodex, chatmodel.CodexOptions{}, owner.ID); err == nil {
		t.Fatal("archive raced conversion")
	}
	if _, err := app.queueWhileRunning(chat, messageInput{Message: "Concurrent queue"}, chatmodel.CodexOptions{}, false); err == nil {
		t.Fatal("message queued behind native archive")
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-finished:
		if response.Code != 502 || store.nativeSessionArchived(chat.Backend, chat.NativeSessionID) {
			t.Fatalf("cancelled archive changed chat: %d", response.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel archive")
	}
}

func TestArchiveLocalClaudeSessionRemainsHiddenAfterDiscovery(t *testing.T) {
	localSessionHomes(t)
	store := testStore(t)
	owner := testUser(t, store, "owner")
	project := testProject(t, store, owner)
	project.SharedChats, project.SharedLocalChats = true, true
	if _, err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	path := writeLocalSession(t, chatmodel.BackendClaude, "archive-local", project.Path, 1)
	nav, err := store.UserNavigation(owner.ID)
	if err != nil || len(nav.Chats) != 1 {
		t.Fatalf("native discovery failed: %+v %v", nav, err)
	}
	chat := nav.Chats[0]
	app := NewApp(store, &archiveTestRunner{archive: (&agent.CLIRunner{}).ArchiveSession}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, owner.Username)
	if response := apiRequest(app, "POST", "/api/chats/"+chat.ID+"/archive", `{}`, cookie, csrf); response.Code != 200 {
		t.Fatalf("native archive failed: %d %s", response.Code, response.Body.String())
	}
	if err := store.syncLocalSessions(true); err != nil {
		t.Fatal(err)
	}
	nav, err = store.UserNavigation(owner.ID)
	if err != nil || len(nav.Chats) != 0 {
		t.Fatal("archived local chat returned after refresh", err)
	}
	if body, err := os.ReadFile(path); err != nil || len(body) == 0 {
		t.Fatal("native transcript was removed", err)
	}
}

func TestArchiveChatReportsNativeListRefreshFailure(t *testing.T) {
	store := testStore(t)
	owner := testUser(t, store, "owner")
	project := testProject(t, store, owner)
	chat, _ := titleSessionFixture(t, store, chatmodel.BackendClaude, owner.ID, project.ID)
	listings := 0
	store.localSources = map[chatmodel.Backend]agent.SessionLister{chatmodel.BackendClaude: agent.SessionListerFunc(func(context.Context, agent.ListSessionsOptions) ([]agent.LocalSession, error) {
		listings++
		return nil, errors.New("native list unavailable")
	})}
	app := NewApp(store, &archiveTestRunner{archive: func(context.Context, agent.AgentRequest) error { return nil }}, "", "")
	t.Cleanup(func() { app.Close() })
	cookie, csrf := loginRequest(t, app, owner.Username)
	response := apiRequest(app, "POST", "/api/chats/"+chat.ID+"/archive", `{}`, cookie, csrf)
	if response.Code != 502 || listings == 0 || !strings.Contains(response.Body.String(), "native session list") || !store.CanAccessChat(owner.ID, chat.ID) {
		t.Fatalf("failed refresh was treated as a successful list update: %d %s", response.Code, response.Body.String())
	}
}
