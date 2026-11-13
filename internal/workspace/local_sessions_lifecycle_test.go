package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

func TestLocalSessionSyncCloseCancelsDiscovery(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "owner")
	p := testProject(t, s, u)
	p.SharedChats, p.SharedLocalChats = true, true
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	s.localSources = map[chatmodel.Backend]agent.SessionLister{
		chatmodel.BackendCodex: agent.SessionListerFunc(func(ctx context.Context, _ agent.ListSessionsOptions) ([]agent.LocalSession, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	}
	result := make(chan error, 1)
	go func() { result <- s.syncLocalSessions(true) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("discovery did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("store shutdown did not cancel discovery")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery cancellation: %v", err)
	}
}
