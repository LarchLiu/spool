package appupdate

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestReleaseHistoryIsReadOnlyAndUsesAvailableRange(t *testing.T) {
	var calls int
	fail := false
	c := New(Options{
		Enabled: true,
		Check: func(context.Context) (*Release, error) {
			return &Release{Version: "1.3.0", Notes: "signed notes"}, nil
		},
		ReleaseNotes: func(_ context.Context, current, target string) ([]ReleaseNotes, error) {
			calls++
			if current != "1.0.0" || target != "1.3.0" {
				t.Fatalf("incorrect release range: %s - %s", current, target)
			}
			if fail {
				return nil, errors.New("GitHub unavailable")
			}
			return []ReleaseNotes{{Version: "1.3.0", Body: "untrusted description"}}, nil
		},
	})
	t.Cleanup(c.Close)
	if _, err := c.ReleaseNotes(context.Background(), "1.0.0"); err == nil || calls != 0 {
		t.Fatal("history loaded without available update")
	}
	c.Check()
	before := c.State()
	history, err := c.ReleaseNotes(context.Background(), "1.0.0")
	if err != nil || history.CurrentVersion != "1.0.0" || history.TargetVersion != "1.3.0" || len(history.Releases) != 1 || c.State() != before {
		t.Fatalf("history changed updater state: %+v, %v", history, err)
	}
	fail = true
	if _, err := c.ReleaseNotes(context.Background(), "1.0.0"); err == nil || c.State() != before {
		t.Fatal("history error affected signed updater state")
	}
	c.Close()
	if _, err := c.ReleaseNotes(context.Background(), "1.0.0"); err == nil {
		t.Fatal("history loaded after shutdown")
	}
}

func TestReleaseHistoryCancelsOnCloseAndRetriesOnlyNetworkErrors(t *testing.T) {
	started := make(chan struct{})
	c := New(Options{
		Enabled: true,
		Check:   func(context.Context) (*Release, error) { return &Release{Version: "2.0.0"}, nil },
		ReleaseNotes: func(ctx context.Context, _, _ string) ([]ReleaseNotes, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	c.Check()
	done := make(chan error, 1)
	go func() { _, err := c.ReleaseNotes(context.Background(), "1.0.0"); done <- err }()
	<-started
	c.Close()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("shutdown did not cancel release request:", err)
	}
	var calls int
	c = New(Options{
		Enabled: true,
		Check:   func(context.Context) (*Release, error) { return &Release{Version: "2.0.0"}, nil },
		ReleaseNotes: func(context.Context, string, string) ([]ReleaseNotes, error) {
			calls++
			if calls == 1 {
				return nil, &NetworkError{errors.New("temporary failure")}
			}
			return []ReleaseNotes{}, nil
		},
	})
	t.Cleanup(c.Close)
	c.retryDelays = []time.Duration{0}
	c.Check()
	if _, err := c.ReleaseNotes(context.Background(), "1.0.0"); err != nil || calls != 2 {
		t.Fatalf("transient release request not retried: %d, %v", calls, err)
	}
}

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
		Preferences: func() (Preferences, error) { return Preferences{AutomaticChecks: true, AutomaticInstalls: true}, nil },
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

func TestAutomaticCheckUsesEachConfiguredInterval(t *testing.T) {
	for _, interval := range []time.Duration{30 * time.Minute, time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour, 0} {
		t.Run(interval.String(), func(t *testing.T) {
			checks := 0
			c := New(Options{
				Enabled: true,
				Preferences: func() (Preferences, error) {
					return Preferences{AutomaticChecks: true, CheckInterval: interval}, nil
				},
				Check: func(context.Context) (*Release, error) { checks++; return nil, nil },
			})
			t.Cleanup(c.Close)
			delay := interval
			if delay == 0 {
				delay = 12 * time.Hour
			}
			recent := time.Now().Add(-delay + time.Minute)
			if got := c.poll(recent); checks != 0 || got != recent {
				t.Fatal("checked before the configured interval elapsed")
			}
			if got := c.poll(time.Now().Add(-delay - time.Minute)); checks != 1 || time.Since(got) > time.Second {
				t.Fatal("did not check after the configured interval elapsed")
			}
		})
	}
}

func TestIntervalChangesAndManualChecksRescheduleAutomaticChecks(t *testing.T) {
	p := Preferences{AutomaticChecks: true, CheckInterval: 24 * time.Hour}
	checks := 0
	c := New(Options{
		Enabled:     true,
		Preferences: func() (Preferences, error) { return p, nil },
		Check:       func(context.Context) (*Release, error) { checks++; return nil, nil },
	})
	t.Cleanup(c.Close)
	last := time.Now().Add(-2 * time.Hour)
	c.poll(last)
	if checks != 0 {
		t.Fatal("checked before the initial interval")
	}
	p.CheckInterval = time.Hour
	c.poll(last)
	if checks != 1 {
		t.Fatal("shortening the interval did not reschedule the next check")
	}
	c.mu.Lock()
	c.state.LastCheck = last
	c.mu.Unlock()
	p.CheckInterval = 6 * time.Hour
	c.poll(last)
	if checks != 1 {
		t.Fatal("lengthening the interval did not defer the next check")
	}
	c.Check()
	c.poll(time.Time{})
	if checks != 2 {
		t.Fatal("automatic check ignored a recent manual check")
	}
	p.AutomaticChecks = false
	c.mu.Lock()
	c.state.LastCheck = time.Now().Add(-48 * time.Hour)
	c.mu.Unlock()
	c.poll(time.Time{})
	if checks != 2 {
		t.Fatal("disabled automatic checks still ran")
	}
}

func TestAutomaticCheckFailureKeepsFiveMinuteRetry(t *testing.T) {
	checks := 0
	c := New(Options{
		Enabled: true,
		Preferences: func() (Preferences, error) {
			return Preferences{AutomaticChecks: true, CheckInterval: 24 * time.Hour}, nil
		},
		Check: func(context.Context) (*Release, error) {
			checks++
			return nil, errors.New("offline")
		},
	})
	t.Cleanup(c.Close)
	last := c.poll(time.Time{})
	c.poll(last.Add(-time.Minute))
	if checks != 1 {
		t.Fatal("failed check retried too early")
	}
	c.poll(last.Add(-6 * time.Minute))
	if checks != 2 {
		t.Fatal("failed check waited for the full configured interval")
	}
}
