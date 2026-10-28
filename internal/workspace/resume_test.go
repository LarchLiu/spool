package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestInterruptedChatResumesPersistedSessionAfterReload(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			fixture := newAgentFixture(t, backend)
			variable := "SPOOL_CODEX_BIN"
			if backend == chatmodel.BackendClaude {
				variable = "SPOOL_CLAUDE_BIN"
			}
			t.Setenv(variable, fixture.Binary)
			store := testStore(t)
			session, err := store.createSession("", "", backend)
			if err != nil {
				t.Fatal(err)
			}
			runner := newTestRunner(t)
			ctx, cancel := context.WithCancel(context.Background())
			timer := time.AfterFunc(8*time.Second, cancel)
			defer timer.Stop()
			defer cancel()
			_, err = NewApp(store, runner, "", "").runTurn(ctx, session.ID, "block-task", func(event agent.AgentEvent) error {
				if event.Type == "delta" {
					cancel()
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reloaded, err := OpenStore(store.Root())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reloaded.Close() })
			saved, _ := reloaded.Get(session.ID)
			if saved.NativeSessionID != "native-session-123" || saved.Status == chatmodel.StatusRunning || len(saved.Turns) != 0 || len(saved.Transcript.Parts) != 0 {
				t.Fatalf("lost interrupted turn: %#v", saved)
			}
			ctx, cancel2 := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel2()
			result, err := NewApp(reloaded, newTestRunner(t), "", "").runTurn(ctx, session.ID, "Continue", nil)
			if err != nil || result.Text != "Hello world" || result.SessionID != saved.NativeSessionID {
				t.Fatalf("chat was not resumed: result=%#v, error=%v", result, err)
			}
		})
	}
}
