package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestSessionIndexPreservesProviderSnapshotsAndDetectsReplacement(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(100, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	snapshot := []LocalSession{{ID: "native-session", Path: path, Directory: dir, Title: "First", UpdatedAt: 100}}
	var failure error
	source := SessionListerFunc(func(context.Context, ListSessionsOptions) ([]LocalSession, error) { return snapshot, failure })
	sources := map[chatmodel.Backend]SessionLister{chatmodel.BackendCodex: source}
	now := time.Unix(1000, 0)
	index := LocalSessionIndex{now: func() time.Time { return now }}
	first, err := index.Discover(ctx, sources)
	if err != nil || len(first) != 1 {
		t.Fatalf("initial snapshot: %+v %v", first, err)
	}
	snapshot[0].Title = "Renamed"
	renamed, err := index.Discover(ctx, sources)
	if err != nil || renamed[0].Title != "Renamed" || renamed[0].Generation != first[0].Generation {
		t.Fatalf("title-only refresh: %+v %v", renamed, err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	replaced, err := index.Discover(ctx, sources)
	if err != nil || replaced[0].Generation == first[0].Generation {
		t.Fatalf("replacement was not detected: %+v %v", replaced, err)
	}
	failure = errors.New("temporary provider failure")
	retained, err := index.Discover(ctx, sources)
	if err != nil || len(retained) != 1 || retained[0].Generation != replaced[0].Generation {
		t.Fatalf("failed provider withdrew sessions: %+v %v", retained, err)
	}
	if failing := index.Failing(); len(failing) != 1 || failing[0] != chatmodel.BackendCodex {
		t.Fatalf("failure was not reported: %+v", failing)
	}
	now = now.Add(localSessionRetryInterval)
	failure, snapshot = nil, nil
	empty, err := index.Discover(ctx, sources)
	if err != nil || len(empty) != 0 {
		t.Fatalf("successful deletion was not reflected: %+v %v", empty, err)
	}
}

func TestSessionIndexBoundsFailedAndDisabledProviders(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	calls := 0
	var failure error
	var requested ListSessionsOptions
	source := SessionListerFunc(func(_ context.Context, options ListSessionsOptions) ([]LocalSession, error) {
		calls++
		requested = options
		return []LocalSession{{ID: "native-session", Directory: dir, UpdatedAt: 100, TitleAuthoritative: true}}, failure
	})
	sources := map[chatmodel.Backend]SessionLister{chatmodel.BackendCodex: source}
	now := time.Unix(1000, 0)
	index := LocalSessionIndex{now: func() time.Time { return now }}
	sessions, err := index.Discover(ctx, sources)
	if err != nil || len(sessions) != 1 || sessions[0].Title != "Codex" || sessions[0].TitleAuthoritative {
		t.Fatalf("untitled session was not listed under a provisional provider label: %+v %v", sessions, err)
	}
	if requested.Limit != localSessionDiscoveryLimit || !requested.IncludeUntitled {
		t.Fatalf("discovery was not bounded: %+v", requested)
	}
	failure = errors.New("provider unavailable")
	for range 3 {
		if sessions, err = index.Discover(ctx, sources); err != nil || len(sessions) != 1 {
			t.Fatalf("recent snapshot was withdrawn: %+v %v", sessions, err)
		}
	}
	if calls != 2 {
		t.Fatalf("failing provider was retried on every refresh: %d calls", calls)
	}
	now = now.Add(localSessionStaleAfter)
	if sessions, err = index.Discover(ctx, sources); err != nil || len(sessions) != 0 || calls != 3 {
		t.Fatalf("stale snapshot outlived its bound: %+v %v calls=%d", sessions, err, calls)
	}
	failure = nil
	now = now.Add(localSessionRetryInterval)
	if sessions, err = index.Discover(ctx, sources); err != nil || len(sessions) != 1 || len(index.Failing()) != 0 {
		t.Fatalf("provider did not recover: %+v %v", sessions, err)
	}
	if sessions, err = index.Discover(ctx, nil); err != nil || len(sessions) != 0 {
		t.Fatalf("disabled provider kept its snapshot: %+v %v", sessions, err)
	}
}

func TestSessionIndexCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var index LocalSessionIndex
	_, err := index.Discover(ctx, map[chatmodel.Backend]SessionLister{chatmodel.BackendClaude: SessionListerFunc(func(ctx context.Context, _ ListSessionsOptions) ([]LocalSession, error) { return nil, ctx.Err() })})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
