package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
	"spool/internal/testutil/agentfixture"
)

func waitLiveTurn(t *testing.T, a *App, chatID string, ready func(chatmodel.Turn) bool) chatmodel.Turn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		chat, _ := a.store.Get(chatID)
		chat = a.snapshot(chat)
		if len(chat.Turns) > 0 {
			turn := chat.Turns[len(chat.Turns)-1]
			if ready(turn) {
				return turn
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("turn did not reach expected state")
	return chatmodel.Turn{}
}

func TestStopResponseCancelsOnlyOwnedTurnAndPreservesResume(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			fixture := newAgentFixture(t, backend)
			s := testStore(t)
			alice, bob := testUser(t, s, "alice"), testUser(t, s, "bob")
			project, err := s.SaveProject(Project{Name: "Stop test", Path: fixture.WorkingDirectory, Members: []string{alice.ID, bob.ID}})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.saveAgent(AgentInfo{Backend: backend, Enabled: true, Installed: true, Executable: fixture.Binary}); err != nil {
				t.Fatal(err)
			}
			runner := newTestRunner(t)
			runner.ConfigureBinaries(map[chatmodel.Backend]string{backend: fixture.Binary})
			a := NewApp(s, runner, "", "")
			t.Cleanup(func() { _ = a.Close() })
			cookie, csrf := loginRequest(t, a, "alice")
			bobCookie, bobCSRF := loginRequest(t, a, "bob")
			chat, _ := s.CreateChat(alice.ID, project.ID, backend)
			other, _ := s.CreateChat(alice.ID, project.ID, backend)
			start := func(id, message string) chatmodel.Turn {
				t.Helper()
				w := apiRequest(a, "POST", "/api/chats/"+id+"/messages", `{"message":"`+message+`"}`, cookie, csrf)
				if w.Code != 202 {
					t.Fatalf("send: %d %s", w.Code, w.Body.String())
				}
				return waitLiveTurn(t, a, id, func(turn chatmodel.Turn) bool { return turn.Reply == "Partial reply" })
			}
			turn := start(chat.ID, "block-task")
			pid := agentfixture.PID(t, fixture.WorkingDirectory)
			otherTurn := start(other.ID, "block-task")
			path := "/api/chats/" + chat.ID + "/stop"
			body := `{"turn_id":"` + turn.ID + `"}`
			if w := apiRequest(a, "POST", path, body, cookie, "wrong-csrf"); w.Code != 403 {
				t.Fatal("stop accepted invalid CSRF")
			}
			if w := apiRequest(a, "POST", path, body, bobCookie, bobCSRF); w.Code != 404 {
				t.Fatal("another project member stopped a private chat")
			}
			r := httptest.NewRequest("POST", path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://evil.example")
			r.Header.Set("X-CSRF-Token", csrf)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatal("stop accepted cross-origin request")
			}
			if w := apiRequest(a, "POST", path, `{"turn_id":"invalid/id"}`, cookie, csrf); w.Code != 400 {
				t.Fatal("stop accepted invalid turn ID")
			}
			if w := apiRequest(a, "POST", path, `{"turn_id":"`+otherTurn.ID+`"}`, cookie, csrf); w.Code != 409 {
				t.Fatal("stop accepted another chat's turn ID")
			}
			w = apiRequest(a, "POST", path, body, cookie, csrf)
			if w.Code != 200 {
				t.Fatalf("stop: %d %s", w.Code, w.Body.String())
			}
			var stopped chatmodel.Session
			if json.Unmarshal(w.Body.Bytes(), &stopped) != nil || stopped.Status != chatmodel.StatusStopped || len(stopped.Transcript.Parts) == 0 || stopped.Transcript.Parts[len(stopped.Transcript.Parts)-1].Text != "Partial reply" || stopped.Turns[0].FinishedAt == 0 || stopped.NativeSessionID != "" {
				t.Fatalf("partial response/status lost or native ID exposed: %s", w.Body.String())
			}
			agentfixture.WaitExit(t, pid)
			if other, _ := s.Get(other.ID); other.Status != chatmodel.StatusRunning {
				t.Fatal("stopping one chat affected another")
			}
			if w := apiRequest(a, "POST", path, body, cookie, csrf); w.Code != 200 {
				t.Fatal("repeating a stop was not idempotent")
			}
			saved, _ := s.Get(chat.ID)
			if saved.NativeSessionID == "" {
				t.Fatal("stop discarded provider resume data")
			}
			if w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/messages", `{"message":"continue"}`, cookie, csrf); w.Code != 202 {
				t.Fatalf("resume: %s", w.Body.String())
			}
			resumed := waitChat(t, s, chat.ID)
			// Completed response text is intentionally not persisted in SQLite;
			// history hydration reads it from the provider-owned transcript. This
			// CLI fixture writes no native file, so only the latest runtime turn exists.
			if resumed.Status != chatmodel.StatusCompleted || resumed.NativeSessionID != saved.NativeSessionID || len(resumed.Turns) != 1 || resumed.Turns[0].Status != chatmodel.StatusCompleted {
				t.Fatalf("could not continue stopped chat: %#v", resumed)
			}
			newTurn := start(chat.ID, "block-task")
			if w := apiRequest(a, "POST", path, body, cookie, csrf); w.Code != 409 {
				t.Fatal("a stale stop could target a new turn")
			}
			if current, _ := s.Get(chat.ID); current.Status != chatmodel.StatusRunning {
				t.Fatal("stale stop cancelled a new turn")
			}
			for id, turnID := range map[string]string{chat.ID: newTurn.ID, other.ID: otherTurn.ID} {
				if w := apiRequest(a, "POST", "/api/chats/"+id+"/stop", `{"turn_id":"`+turnID+`"}`, cookie, csrf); w.Code != 200 {
					t.Fatal(w.Body.String())
				}
			}
		})
	}
}

func TestStopResponseCancelsPendingApproval(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			fixture := newAgentFixture(t, backend)
			s := testStore(t)
			user := testUser(t, s, "alice")
			project, _ := s.SaveProject(Project{Name: "Approval", Path: fixture.WorkingDirectory, Members: []string{user.ID}})
			chat, _ := s.CreateChat(user.ID, project.ID, backend)
			runner := newTestRunner(t)
			runner.ConfigureBinaries(map[chatmodel.Backend]string{backend: fixture.Binary})
			a := NewApp(s, runner, "", "")
			t.Cleanup(func() { _ = a.Close() })
			if err := a.startTurnWithInput(chat, messageInput{Message: "permission-test"}, chatmodel.CodexOptions{}); err != nil {
				t.Fatal(err)
			}
			turn := waitLiveTurn(t, a, chat.ID, func(turn chatmodel.Turn) bool { return turn.PendingApproval != nil })
			cookie, csrf := loginRequest(t, a, "alice")
			w := apiRequest(a, "POST", "/api/chats/"+chat.ID+"/stop", `{"turn_id":"`+turn.ID+`"}`, cookie, csrf)
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			latest, _ := s.Get(chat.ID)
			if latest.Status != chatmodel.StatusStopped || latest.Turns[0].PendingApproval != nil {
				t.Fatal("stop left an actionable approval")
			}
			w = apiRequest(a, "POST", "/api/chats/"+chat.ID+"/approvals", `{"id":"`+turn.PendingApproval.ID+`","turn_id":"`+turn.ID+`","allow":true}`, cookie, csrf)
			if w.Code != 409 {
				t.Fatal("stopped approval could still be granted")
			}
		})
	}
}
