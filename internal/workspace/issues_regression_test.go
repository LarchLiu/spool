package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestIssueRoundPreservesIssueTitle(t *testing.T) {
	a, p, _, runner := issueFixture(t)
	want := "Issues #1 \u00b7 Fix fixture"
	runner.work = func(_ context.Context, request agent.AgentRequest) (agent.AgentResult, error) {
		chat, ok := a.store.Get(request.ChatID)
		if !ok || chat.Title != want {
			t.Errorf("title at agent start = %q, want %q", chat.Title, want)
		}
		return agent.AgentResult{}, errors.New("verification unavailable")
	}
	if err := a.runIssueRound(context.Background(), p); err == nil {
		t.Fatal("expected an unfinished claim")
	}
	chats, err := a.store.UserSessions(p.Issues.UserID, p.ID)
	if err != nil || len(chats) != 1 {
		t.Fatalf("chats = %#v, error = %v", chats, err)
	}
	if chats[0].Title != want {
		t.Fatalf("persisted title = %q, want %q", chats[0].Title, want)
	}
}

func TestFirstTurnOnlyGeneratesTitlesForUnnamedChats(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	for _, title := range []string{"New chat", "", "Custom title"} {
		t.Run(title, func(t *testing.T) {
			chat, err := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE sessions SET title=? WHERE id=?`, title, chat.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.BeginTurn(chat.ID, "First message"); err != nil {
				t.Fatal(err)
			}
			want := title
			if title == "" || title == "New chat" {
				want = "First message"
			}
			latest, _ := s.Get(chat.ID)
			if latest.Title != want {
				t.Fatalf("title = %q, want %q", latest.Title, want)
			}
		})
	}
}

func issueStreamEvent(t *testing.T, stream *streamRecorder, event string) chatmodel.Session {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	prefix := "event: " + event + "\ndata: "
	for {
		select {
		case chunk := <-stream.writes:
			if !strings.HasPrefix(string(chunk), prefix) {
				continue
			}
			var value chatmodel.Session
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(string(chunk), prefix))), &value); err != nil {
				t.Fatal(err)
			}
			return value
		case <-deadline.C:
			t.Fatalf("missing SSE %s event", event)
		}
	}
}

func TestIssueChatStreamPushesArtifactsAndAssignees(t *testing.T) {
	for _, status := range []chatmodel.SessionStatus{chatmodel.StatusReady, chatmodel.StatusRunning, chatmodel.StatusCompleted} {
		t.Run(string(status), func(t *testing.T) {
			a, p, _, runner := issueFixture(t)
			bob := testUser(t, a.store, "bob")
			p.Members = append(p.Members, bob.ID)
			if _, err := a.store.SaveProject(p); err != nil {
				t.Fatal(err)
			}
			chat, err := a.store.createAutomationChat(p.Issues, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status == chatmodel.StatusRunning {
				runner.work = func(ctx context.Context, _ agent.AgentRequest) (agent.AgentResult, error) {
					<-ctx.Done()
					return agent.AgentResult{}, ctx.Err()
				}
				if err = a.startTurnWithCallback(chat, messageInput{Message: "fixture"}, chatmodel.CodexOptions{}, nil); err != nil {
					t.Fatal(err)
				}
			} else if status == chatmodel.StatusCompleted {
				turn, err := a.store.BeginTurn(chat.ID, "fixture")
				if err != nil {
					t.Fatal(err)
				}
				if err = a.store.FinishTurn(chat.ID, turn.ID, agent.AgentResult{}, nil, status); err != nil {
					t.Fatal(err)
				}
			}
			cookie, _ := loginRequest(t, a, "automation")
			ctx, cancel := context.WithCancel(context.Background())
			request := httptest.NewRequest("GET", "/api/chats/"+chat.ID+"/events", nil).WithContext(ctx)
			request.AddCookie(cookie)
			stream := newStreamRecorder()
			done := make(chan struct{})
			go func() { a.ServeHTTP(stream, request); close(done) }()
			defer func() { cancel(); <-done }()
			initial := issueStreamEvent(t, stream, "snapshot")
			if initial.Status != status || len(initial.Artifacts) != 0 {
				t.Fatalf("unexpected initial snapshot: %#v", initial)
			}
			if status != chatmodel.StatusReady {
				issueStreamEvent(t, stream, "turn")
			}
			link := "https://github.com/fixture/issues/pull/20"
			if err = a.store.attachPR(chat.ID, link); err != nil {
				t.Fatal(err)
			}
			metadata := issueStreamEvent(t, stream, "metadata")
			if metadata.ID != chat.ID || metadata.UpdatedAt <= initial.UpdatedAt || len(metadata.Artifacts) != 1 || metadata.Artifacts[0].URL != link {
				t.Fatalf("artifact missing from metadata: %#v", metadata)
			}
			if status != chatmodel.StatusReady {
				turn := issueStreamEvent(t, stream, "turn")
				if turn.UpdatedAt < metadata.UpdatedAt || turn.Status != status {
					t.Fatalf("turn revision lagged behind metadata: %#v", turn)
				}
			}
			p.Issues.UserID, p.Issues.AssigneeIDs = bob.ID, []string{bob.ID}
			if _, err = a.store.SaveProject(p); err != nil {
				t.Fatal(err)
			}
			updated := issueStreamEvent(t, stream, "metadata")
			if !updated.AutomationChat || len(updated.CollaboratorIDs) != 1 || updated.CollaboratorIDs[0] != bob.ID || updated.UpdatedAt <= metadata.UpdatedAt || len(updated.Artifacts) != 1 {
				t.Fatalf("assignee update missing from metadata: %#v", updated)
			}
			if a.store.CanControlChat(chat.UserID, chat.ID) {
				t.Fatal("previous assignee retained control")
			}
			p.Issues.Enabled, p.Issues.UserID, p.Issues.AssigneeIDs = false, "", nil
			if _, err = a.store.SaveProject(p); err != nil {
				t.Fatal(err)
			}
			cleared := issueStreamEvent(t, stream, "metadata")
			if len(cleared.CollaboratorIDs) != 0 || !cleared.AutomationChat || cleared.UpdatedAt <= updated.UpdatedAt {
				t.Fatalf("empty assignee list not propagated: %#v", cleared)
			}
		})
	}
}

func TestIssueFollowupPreflightCancelsOnShutdown(t *testing.T) {
	a, p, commands, runner := issueFixture(t)
	chat, err := a.store.createAutomationChat(p.Issues, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner.work = func(context.Context, agent.AgentRequest) (agent.AgentResult, error) {
		t.Error("agent started after canceled preflight")
		return agent.AgentResult{}, nil
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	a.automation.run = func(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) > 0 && args[0] == "status" {
			close(started)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return nil, errors.New("preflight manually unblocked")
			}
		}
		return commands.command(ctx, directory, name, args...)
	}
	result := make(chan error, 1)
	go func() {
		result <- a.startIssueFollowup(chat, messageInput{Message: "continue"}, chatmodel.CodexOptions{})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("preflight did not start")
	}
	closed := make(chan struct{})
	go func() { _ = a.Close(); close(closed) }()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("preflight error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		unblock()
		<-result
		t.Error("shutdown did not cancel Git preflight")
	}
	<-closed
	var count int
	if err = a.store.db.QueryRow(`SELECT count(*) FROM automation_runs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("canceled preflight admitted a run: %d, %v", count, err)
	}
}
