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

func TestAutomaticUpdatesRetryWhenIdleThenRestartWithoutRechecking(t *testing.T) {
	checks, installs, restarts := 0, 0, 0
	busy := true
	var c *Controller
	c = New(Options{
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
		Restart: func() {
			release, err := c.PrepareRestart()
			if err != nil {
				t.Fatal(err)
			}
			release()
			restarts++
		},
	})
	t.Cleanup(c.Close)
	c.advance(time.Now())
	if checks != 1 || installs != 0 || restarts != 0 || c.State().Error == "" {
		t.Fatal("automatic update ignored active chats")
	}
	busy = false
	c.advance(time.Now())
	if checks != 1 || installs != 1 || restarts != 1 || !c.State().Installed {
		t.Fatal("idle retry did not install and restart the pending update")
	}
}

func TestAutomaticRestartWaitsForChatsStartedAfterInstallation(t *testing.T) {
	checks, installs, restarts, attempts := 0, 0, 0, 0
	busy := false
	var c *Controller
	c = New(Options{
		Enabled: true,
		Preferences: func() (Preferences, error) {
			return Preferences{AutomaticChecks: true, AutomaticInstalls: true}, nil
		},
		Check: func(context.Context) (*Release, error) {
			checks++
			return &Release{Version: "2.0.0", Install: func(context.Context, func(int64, int64)) error {
				installs++
				return nil
			}}, nil
		},
		Acquire: func() (func(), error) {
			if busy {
				return nil, errors.New("chats running")
			}
			return func() { busy = true }, nil
		},
		Restart: func() {
			attempts++
			if release, err := c.PrepareRestart(); err == nil {
				restarts++
				release()
			}
		},
	})
	t.Cleanup(c.Close)
	c.advance(time.Now())
	if installs != 1 || restarts != 0 || attempts != 1 || c.State().ErrorPhase != "restart" {
		t.Fatalf("new chat did not defer restart: %+v", c.State())
	}
	busy = false
	c.advance(time.Now())
	if checks != 1 || installs != 1 || restarts != 1 || attempts != 2 || c.State().Error != "" {
		t.Fatalf("idle restart did not retry without reinstalling: %+v", c.State())
	}
}

func TestAutomaticInstallSwitchAlsoControlsRestartOfManuallyInstalledUpdates(t *testing.T) {
	p := Preferences{}
	checks, installs, restarts := 0, 0, 0
	c := New(Options{
		Enabled:     true,
		Preferences: func() (Preferences, error) { return p, nil },
		Check: func(context.Context) (*Release, error) {
			checks++
			return &Release{Version: "2.0.0", Install: func(context.Context, func(int64, int64)) error {
				installs++
				return nil
			}}, nil
		},
		Acquire: func() (func(), error) { return func() {}, nil },
		Restart: func() { restarts++ },
	})
	t.Cleanup(c.Close)
	c.Check()
	c.advance(time.Now())
	if installs != 0 || restarts != 0 {
		t.Fatal("disabled automatic installation still ran")
	}
	c.Install()
	c.advance(time.Now())
	if installs != 1 || restarts != 0 || !c.State().Installed {
		t.Fatal("manual installation with the switch off must wait for a manual restart")
	}
	p.AutomaticInstalls = true
	c.advance(time.Now())
	if checks != 1 || installs != 1 || restarts != 1 {
		t.Fatal("enabling automatic installation did not restart the installed update")
	}
	p.AutomaticInstalls = false
	c.advance(time.Now())
	if restarts != 1 {
		t.Fatal("disabling automatic installation did not cancel the pending restart")
	}
	p.AutomaticInstalls = true
	c.Close()
	c.advance(time.Now())
	if restarts != 1 {
		t.Fatal("controller requested an automatic restart after closing")
	}
}

func TestAutomaticRestartRechecksPreferencesAfterInstallation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "unavailable"}[fail], func(t *testing.T) {
			installed, restarted := false, false
			c := New(Options{
				Enabled: true,
				Preferences: func() (Preferences, error) {
					if installed && fail {
						return Preferences{}, errors.New("preferences unavailable")
					}
					return Preferences{AutomaticChecks: true, AutomaticInstalls: !installed}, nil
				},
				Check: func(context.Context) (*Release, error) {
					return &Release{Version: "2.0.0", Install: func(context.Context, func(int64, int64)) error {
						installed = true
						return nil
					}}, nil
				},
				Acquire: func() (func(), error) { return func() {}, nil },
				Restart: func() { restarted = true },
			})
			t.Cleanup(c.Close)
			c.advance(time.Now())
			if !installed || restarted {
				t.Fatal("restart ignored preferences changed during installation")
			}
		})
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

