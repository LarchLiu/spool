package appupdate

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestInstallAndRestartBothRequireIdleLease(t *testing.T) {
	busy := true
	installs, leases, releases := 0, 0, 0
	busyErr := errors.New("chats running")
	c := New(Options{
		Enabled: true,
		Check: func(context.Context) (*Release, error) {
			return &Release{Version: "1.2.0", Notes: "Fixes", Install: func(_ context.Context, progress func(int64, int64)) error {
				installs++
				progress(50, 100)
				return nil
			}}, nil
		},
		Acquire: func() (func(), error) {
			if busy {
				return nil, busyErr
			}
			leases++
			return func() { releases++ }, nil
		},
	})
	t.Cleanup(c.Close)
	c.Check()
	c.Install()
	if installs != 0 || c.State().Installed || c.State().Error != busyErr.Error() {
		t.Fatalf("busy chat did not block installation: %+v", c.State())
	}
	busy = false
	c.Install()
	state := c.State()
	if installs != 1 || leases != 1 || releases != 1 || !state.Installed || state.Downloaded != 50 || state.Notes != "Fixes" {
		t.Fatalf("unexpected install: %+v, %d/%d/%d", state, installs, leases, releases)
	}
	busy = true
	if _, err := c.PrepareRestart(); !errors.Is(err, busyErr) {
		t.Fatal("a chat started after installation did not block restart")
	}
	busy = false
	release, err := c.PrepareRestart()
	if err != nil {
		t.Fatal(err)
	}
	if leases != 2 || releases != 1 {
		t.Fatal("restart did not hold a fresh lease")
	}
	release()
	c.RestartFailed(errors.New("cannot persist restart journal"))
	if state = c.State(); state.ErrorPhase != "restart" || !state.Installed || state.Error == "" {
		t.Fatalf("restart failure lost the installed update or its failure stage: %+v", state)
	}
}

func TestFailedInstallReleasesLeaseAndCanRetry(t *testing.T) {
	failed, released := true, 0
	c := New(Options{
		Enabled: true,
		Check: func(context.Context) (*Release, error) {
			return &Release{Version: "2.0.0", Install: func(context.Context, func(int64, int64)) error {
				if failed {
					return errors.New("invalid signature")
				}
				return nil
			}}, nil
		},
		Acquire: func() (func(), error) { return func() { released++ }, nil },
	})
	t.Cleanup(c.Close)
	c.Check()
	c.Install()
	if c.State().Installing || c.State().Installed || c.State().Error == "" || released != 1 {
		t.Fatal("failed installation leaked a lease or succeeded")
	}
	failed = false
	c.Install()
	if !c.State().Installed || released != 2 {
		t.Fatal("retry failed")
	}
}

func TestAutomaticUpdatesRetryWithoutRecheckingOrRestarting(t *testing.T) {
	checks, installs := 0, 0
	busy := true
	c := New(Options{
		Enabled:     true,
		Preferences: func() (Preferences, error) { return Preferences{true, true}, nil },
		Check: func(context.Context) (*Release, error) {
			checks++
			return &Release{Version: "2.0.0", Install: func(context.Context, func(int64, int64)) error { installs++; return nil }}, nil
		},
		Acquire: func() (func(), error) {
			if busy {
				return nil, errors.New("chats running")
			}
			return func() {}, nil
		},
	})
	t.Cleanup(c.Close)
	next := c.poll(time.Time{})
	if checks != 1 || installs != 0 || c.State().Error == "" {
		t.Fatal("automatic update ignored active chats")
	}
	busy = false
	c.poll(next)
	if checks != 1 || installs != 1 || !c.State().Installed {
		t.Fatal("idle retry did not install the pending update")
	}
}

func TestCloseCancelsInstallAndRejectsConcurrentOperations(t *testing.T) {
	started, done := make(chan struct{}), make(chan struct{})
	var installs, releases atomic.Int32
	c := New(Options{
		Enabled: true,
		Check: func(context.Context) (*Release, error) {
			return &Release{Version: "2.0.0", Install: func(ctx context.Context, _ func(int64, int64)) error {
				installs.Add(1)
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}}, nil
		},
		Acquire: func() (func(), error) { return func() { releases.Add(1) }, nil },
	})
	c.Check()
	go func() { c.Install(); close(done) }()
	<-started
	c.Install()
	c.Check()
	c.Close()
	<-done
	c.Install()
	if installs.Load() != 1 || releases.Load() != 1 || c.State().Installed {
		t.Fatal("close or concurrency handling failed")
	}
}

func TestDisabledAndFailedChecksNeverInstall(t *testing.T) {
	c := New(Options{})
	c.Check()
	c.Install()
	c.Start()
	c.Close()
	if c.State().Enabled {
		t.Fatal("development updater enabled")
	}
	c = New(Options{Enabled: true, Check: func(context.Context) (*Release, error) { return nil, errors.New("offline") }})
	defer c.Close()
	c.Check()
	c.Install()
	if !c.State().LastCheck.IsZero() || c.State().Error != "offline" {
		t.Fatal("failed check was reported as successful")
	}
}
