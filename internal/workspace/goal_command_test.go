package workspace

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestNativeGoalControlsAreAuthorizedAndDoNotCreateTurns(t *testing.T) {
	s := testStore(t)
	alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
	p := testProject(t, s, alice, bob)
	testAgent(t, s)
	chat, err := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNativeSession(chat.ID, "native-session-123"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{text: "native goal state"}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	bobCookie, bobCSRF := loginRequest(t, a, "bob")
	path := "/api/chats/" + chat.ID + "/goal"
	if w := apiRequest(a, "POST", path, `{"action":"clear"}`, nil, ""); w.Code != 401 {
		t.Fatalf("unauthenticated goal: %d", w.Code)
	}
	if w := apiRequest(a, "POST", path, `{"action":"clear"}`, cookie, "wrong"); w.Code != 403 {
		t.Fatalf("goal without CSRF: %d", w.Code)
	}
	if w := apiRequest(a, "POST", path, `{"action":"clear"}`, bobCookie, bobCSRF); w.Code != 404 {
		t.Fatalf("other member changed goal: %d", w.Code)
	}
	if w := apiRequest(a, "POST", path, `{"action":"set"}`, cookie, csrf); w.Code != 400 {
		t.Fatalf("invalid action: %d", w.Code)
	}
	for _, action := range []string{"get", "clear"} {
		w := apiRequest(a, "POST", path, `{"action":"`+action+`"}`, cookie, csrf)
		if w.Code != 200 || !strings.Contains(w.Body.String(), runner.text) {
			t.Fatalf("goal %s: %d %s", action, w.Code, w.Body.String())
		}
	}
	runner.mu.Lock()
	requests := append([]string(nil), runner.requests[0].Prompt, runner.requests[1].Prompt)
	for _, request := range runner.requests {
		if request.NativeSessionID != "native-session-123" || request.WorkingDirectory != p.Path || request.Backend != chatmodel.BackendCodex {
			t.Fatalf("wrong native target: %#v", request)
		}
	}
	runner.mu.Unlock()
	if strings.Join(requests, "|") != "/goal|/goal clear" {
		t.Fatal(requests)
	}
	saved, _ := s.Get(chat.ID)
	if len(saved.Turns) != 0 || saved.NativeSessionID != "native-session-123" || saved.UpdatedAt != chat.UpdatedAt {
		t.Fatalf("control changed chat metadata: %#v", saved)
	}
}

func TestGoalControlOnEmptyChatDoesNotStartCLI(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	testAgent(t, s)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	runner := &fakeRunner{}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/goal", `{"action":"get"}`, cookie, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No goal set.") {
		t.Fatal(w.Body.String())
	}
	if len(runner.requests) != 0 {
		t.Fatal("empty goal started a CLI")
	}
}

func TestGoalControlAndModelTurnsCannotOverlap(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	testAgent(t, s)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	if err := s.SetNativeSession(chat.ID, "native-session-123"); err != nil {
		t.Fatal(err)
	}
	runner := &gatedRunner{started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(runner.release) }) }
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	path := "/api/chats/" + chat.ID
	finished := make(chan int, 1)
	go func() { finished <- apiRequest(a, "POST", path+"/goal", `{"action":"get"}`, cookie, csrf).Code }()
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("goal control did not start")
	}
	defer release()
	if w := apiRequest(a, "POST", path+"/goal", `{"action":"clear"}`, cookie, csrf); w.Code != 409 {
		t.Fatalf("overlapping controls: %d", w.Code)
	}
	if w := apiRequest(a, "POST", path+"/messages", `{"message":"work"}`, cookie, csrf); w.Code != 409 {
		t.Fatalf("overlapping model turn: %d %s", w.Code, w.Body.String())
	}
	if _, err := a.runTurn(context.Background(), chat.ID, "work", nil); err == nil {
		t.Fatal("synchronous turn overlapped native control")
	}
	release()
	select {
	case code := <-finished:
		if code != 200 {
			t.Fatalf("goal control returned %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("goal control did not finish")
	}
	if _, err := s.BeginTurn(chat.ID, "already running"); err != nil {
		t.Fatal(err)
	}
	if w := apiRequest(a, "POST", path+"/goal", `{"action":"get"}`, cookie, csrf); w.Code != 409 {
		t.Fatalf("goal accepted during running turn: %d", w.Code)
	}
}

func TestInvalidGoalInputsNeverCreateTurns(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	testAgent(t, s)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	runner := &fakeRunner{}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	for _, message := range []string{"/goal", "/goal clear", "/goal " + strings.Repeat("x", 4001)} {
		w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"`+message+`"}`, cookie, csrf)
		if w.Code != 400 {
			t.Fatalf("invalid message %q: %d", message[:min(len(message), 30)], w.Code)
		}
	}
	file := uploadedAttachment{Attachment: chatmodel.Attachment{Name: "note.txt", MediaType: "text/plain"}, Data: []byte("test")}
	w := uploadRequest(t, a, chat.ID, cookie, csrf, map[string]string{"message": "/goal fix tests"}, file)
	if w.Code != 400 {
		t.Fatalf("goal with attachment: %d", w.Code)
	}
	saved, _ := s.Get(chat.ID)
	if len(saved.Turns) != 0 || len(runner.requests) != 0 {
		t.Fatal("invalid input started work")
	}
}

func TestGoalControlFailurePreservesSession(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	testAgent(t, s)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	if err := s.SetNativeSession(chat.ID, "native-session-123"); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{err: errors.New("native goal unavailable")}
	a := NewApp(s, runner, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/goal", `{"action":"clear"}`, cookie, csrf)
	if w.Code != 409 {
		t.Fatalf("got %d", w.Code)
	}
	saved, _ := s.Get(chat.ID)
	if len(saved.Turns) != 0 || saved.NativeSessionID != "native-session-123" {
		t.Fatalf("failed control changed chat: %#v", saved)
	}
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.activeTurns[chat.ID] != nil || a.runningTurns != 0 {
		t.Fatal("native control lock not released")
	}
}

func TestGoalSubmissionAndControlsUseTheSameNativeSession(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			fixture := newAgentFixture(t, backend)
			s := testStore(t)
			u := testUser(t, s, "alice")
			project, err := s.SaveProject(Project{Name: "Goals", Path: fixture.WorkingDirectory, Members: []string{u.ID}})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.saveAgent(AgentInfo{Backend: backend, Enabled: true, Installed: true, Executable: fixture.Binary, Catalog: fixtureCatalog()}); err != nil {
				t.Fatal(err)
			}
			runner := newTestRunner(t)
			runner.ConfigureBinaries(map[chatmodel.Backend]string{backend: fixture.Binary})
			a := NewApp(s, runner, "", "")
			t.Cleanup(func() { _ = a.Close() })
			chat, err := s.CreateChat(u.ID, project.ID, backend)
			if err != nil {
				t.Fatal(err)
			}
			cookie, csrf := loginRequest(t, a, "alice")
			path := "/api/chats/" + chat.ID
			w := apiRequest(a, "POST", path+"/messages", `{"message":"/goal fix tests"}`, cookie, csrf)
			if w.Code != 202 {
				t.Fatalf("submit goal: %d %s", w.Code, w.Body.String())
			}
			saved := waitChat(t, s, chat.ID)
			if saved.Status != chatmodel.StatusCompleted || saved.NativeSessionID != "native-session-123" {
				t.Fatalf("goal did not use native session: %#v", saved)
			}
			// FinishTurn commits before the provider control is released.
			a.turnMu.Lock()
			control := a.activeTurns[chat.ID]
			a.turnMu.Unlock()
			if control != nil {
				select {
				case <-control.done:
				case <-time.After(5 * time.Second):
					t.Fatal("goal turn did not release its process")
				}
			}
			w = apiRequest(a, "POST", path+"/goal", `{"action":"get"}`, cookie, csrf)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "fix tests") {
				t.Fatalf("get goal: %d %s", w.Code, w.Body.String())
			}
			w = apiRequest(a, "POST", path+"/goal", `{"action":"clear"}`, cookie, csrf)
			if w.Code != 200 {
				t.Fatalf("clear goal: %d %s", w.Code, w.Body.String())
			}
			after, _ := s.Get(chat.ID)
			if len(after.Turns) != 1 || after.NativeSessionID != saved.NativeSessionID || after.UpdatedAt != saved.UpdatedAt {
				t.Fatalf("goal controls created a turn or changed the session: %#v", after)
			}
		})
	}
}
