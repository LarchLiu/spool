package workspace

import (
	"context"
	"errors"
	"net/http"
	"testing"

	chatmodel "spool/internal/model"
)

func TestUpdatePreferences(t *testing.T) {
	s := testStore(t)
	p, err := s.UpdatePreferences()
	if err != nil || !p.AutomaticChecks || p.AutomaticInstalls {
		t.Fatalf("unsafe defaults: %+v %v", p, err)
	}
	p = UpdatePreferences{AutomaticChecks: false, AutomaticInstalls: true}
	if err := s.SaveUpdatePreferences(p); err != nil {
		t.Fatal(err)
	}
	if got, err := s.UpdatePreferences(); err != nil || got != p {
		t.Fatalf("preferences not saved: %+v %v", got, err)
	}
}

func TestUpdateLeaseCountsStoppedAndSupersededTurns(t *testing.T) {
	a := &App{}
	s := &Service{app: a}
	a.turnMu.Lock()
	_, old := a.registerTurn(context.Background(), "chat", "old")
	_, next := a.registerTurn(context.Background(), "chat", "next")
	a.turnMu.Unlock()
	old.cancel()
	a.releaseTurn("chat", next)
	if _, err := s.AcquireUpdate(); !errors.Is(err, ErrChatsRunning) {
		t.Fatal("stopped/superseded turn cleanup did not block update")
	}
	a.releaseTurn("chat", old)
	release, err := s.AcquireUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatal("service started while update lease held")
	}
	if _, err := s.AcquireUpdate(); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatal("overlapping updates allowed")
	}
	if err := a.startTurn(chatmodel.Session{}, "test", chatmodel.CodexOptions{}); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatal("new turn admitted during update")
	}
	if _, err := a.runTurn(context.Background(), "chat", "test", nil); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatal("synchronous turn admitted during update")
	}
	release()
	release()
	if s.updating || a.updating {
		t.Fatal("lease did not release")
	}
}

func TestTurnAdmissionRacesUpdateLease(t *testing.T) {
	store := testStore(t)
	user := testUser(t, store, "alice")
	project := testProject(t, store, user)
	for range 20 {
		chat, err := store.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
		if err != nil {
			t.Fatal(err)
		}
		runner := &gatedRunner{started: make(chan struct{}), release: make(chan struct{})}
		a := NewApp(store, runner, "", "")
		s := &Service{app: a}
		start := make(chan struct{})
		turnResult := make(chan error, 1)
		type leaseResult struct {
			release func()
			err     error
		}
		lease := make(chan leaseResult, 1)
		go func() { <-start; turnResult <- a.startTurn(chat, "test", chatmodel.CodexOptions{}) }()
		go func() { <-start; release, err := s.AcquireUpdate(); lease <- leaseResult{release, err} }()
		close(start)
		turnErr, result := <-turnResult, <-lease
		if turnErr == nil && result.err == nil {
			t.Fatal("turn and update were admitted together")
		}
		if turnErr != nil && !errors.Is(turnErr, ErrUpdateInProgress) || result.err != nil && !errors.Is(result.err, ErrChatsRunning) {
			t.Fatalf("unexpected errors: %v / %v", turnErr, result.err)
		}
		if result.release != nil {
			result.release()
		}
		close(runner.release)
		a.turns.Wait()
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMessageDuringUpdateReturnsRetryableError(t *testing.T) {
	store := testStore(t)
	user := testUser(t, store, "alice")
	project := testProject(t, store, user)
	testAgent(t, store)
	chat, _ := store.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	a := NewApp(store, &fakeRunner{text: "reply"}, "", "")
	t.Cleanup(func() { _ = a.Close() })
	cookie, csrf := loginRequest(t, a, "alice")
	s := &Service{app: a}
	release, err := s.AcquireUpdate()
	if err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"test"}`, cookie, csrf)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatalf("expected retryable update error, got %d %s", w.Code, w.Body.String())
	}
	release()
	w = apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"test"}`, cookie, csrf)
	if w.Code != http.StatusAccepted {
		t.Fatalf("turn rejected after update: %d %s", w.Code, w.Body.String())
	}
	a.turns.Wait()
}
