// Package appupdate coordinates signed desktop updates with the chat service.
package appupdate

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Release struct {
	Version string
	Notes   string
	Install func(context.Context, func(int64, int64)) error
}

type Preferences struct {
	AutomaticChecks   bool
	AutomaticInstalls bool
	CheckInterval     time.Duration
}

type Options struct {
	Enabled      bool
	Check        func(context.Context) (*Release, error)
	ReleaseNotes func(context.Context, string, string) ([]ReleaseNotes, error)
	Acquire      func() (func(), error)
	Preferences  func() (Preferences, error)
	Log          func(phase, version string, err error)
}

type State struct {
	Enabled           bool
	Checking          bool
	Installing        bool
	Installed         bool
	Version, Notes    string
	Error             string
	ErrorPhase        string
	LastCheck         time.Time
	Downloaded, Total int64
}

type Controller struct {
	mu              sync.Mutex
	state           State
	opts            Options
	release         *Release
	ctx             context.Context
	cancel          context.CancelFunc
	closed          bool
	started         bool
	workers         sync.WaitGroup
	retryDelays     []time.Duration
	autoInstallHeld bool
}

func New(opts Options) *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	return &Controller{opts: opts, state: State{Enabled: opts.Enabled}, ctx: ctx, cancel: cancel, retryDelays: []time.Duration{2 * time.Second, 4 * time.Second}}
}

func (c *Controller) log(phase, version string, err error) {
	if c.opts.Log != nil {
		c.opts.Log(phase, version, err)
	}
}

func (c *Controller) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *Controller) ReleaseNotes(ctx context.Context, current string) (ReleaseHistory, error) {
	c.mu.Lock()
	if c.closed || !c.state.Enabled || c.state.Version == "" || c.opts.ReleaseNotes == nil {
		c.mu.Unlock()
		return ReleaseHistory{}, errors.New("No update release information is available")
	}
	target := c.state.Version
	c.workers.Add(1)
	c.mu.Unlock()
	defer c.workers.Done()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	var notes []ReleaseNotes
	err := retryNetwork(ctx, c.retryDelays, func() error {
		var err error
		notes, err = c.opts.ReleaseNotes(ctx, current, target)
		return err
	}, nil)
	return ReleaseHistory{CurrentVersion: current, TargetVersion: target, Releases: notes}, err
}

func (c *Controller) begin(install bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || !c.state.Enabled || c.state.Checking || c.state.Installing || c.state.Installed || install && c.release == nil {
		return false
	}
	c.state.Checking = !install
	c.state.Installing = install
	c.state.Error = ""
	c.state.ErrorPhase = ""
	c.workers.Add(1)
	return true
}

func (c *Controller) Check() {
	if !c.begin(false) {
		return
	}
	defer c.workers.Done()
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()
	c.log("check_started", "", nil)
	var release *Release
	err := retryNetwork(ctx, c.retryDelays, func() error {
		var err error
		release, err = c.opts.Check(ctx)
		return err
	}, func(err error) { c.log("check_retry", "", err) })
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Checking = false
	if err != nil {
		c.state.Error, c.state.ErrorPhase = SafeError(err), "check"
		c.log("check_failed", "", err)
		return
	}
	c.state.LastCheck = time.Now()
	if release == nil || c.release == nil || release.Version != c.release.Version {
		c.autoInstallHeld = false
	}
	c.release = release
	c.state.Version, c.state.Notes = "", ""
	if release != nil {
		c.state.Version, c.state.Notes = release.Version, release.Notes
	}
	c.log("check_completed", c.state.Version, nil)
}

func (c *Controller) Install() {
	if !c.begin(true) {
		return
	}
	defer c.workers.Done()
	// Acquire before even downloading; the lease also rejects incoming turns
	// until the verified archive has been installed, or the operation fails.
	release, err := c.opts.Acquire()
	if err == nil {
		defer release()
		ctx, cancel := context.WithTimeout(c.ctx, 30*time.Minute)
		defer cancel()
		c.log("install_started", c.release.Version, nil)
		err = retryNetwork(ctx, c.retryDelays, func() error {
			c.mu.Lock()
			c.state.Downloaded, c.state.Total = 0, 0
			c.mu.Unlock()
			return c.release.Install(ctx, func(downloaded, total int64) {
				c.mu.Lock()
				c.state.Downloaded, c.state.Total = downloaded, total
				c.mu.Unlock()
			})
		}, func(err error) { c.log("install_retry", c.release.Version, err) })
		c.mu.Lock()
		c.autoInstallHeld = err != nil
		c.mu.Unlock()
	} else {
		c.log("install_waiting_for_idle", c.release.Version, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Installing = false
	c.state.Installed = err == nil
	if err != nil {
		c.state.Error, c.state.ErrorPhase = SafeError(err), "install"
		c.log("install_failed", c.release.Version, err)
	} else {
		c.log("install_completed", c.release.Version, nil)
	}
}

// Start checks shortly after launch and periodically afterwards. Automatic
// installs retry when idle, but never force a restart or interrupt a chat.
func (c *Controller) Start() {
	c.mu.Lock()
	if c.closed || c.started || !c.state.Enabled {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		var lastAttempt time.Time
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-timer.C:
			}
			lastAttempt = c.poll(lastAttempt)
			timer.Reset(time.Minute)
		}
	}()
}

func (c *Controller) poll(lastAttempt time.Time) time.Time {
	p, err := c.opts.Preferences()
	if err != nil {
		return lastAttempt
	}
	state := c.State()
	lastCheck := lastAttempt
	if state.LastCheck.After(lastCheck) {
		lastCheck = state.LastCheck
	}
	delay := p.CheckInterval
	if delay <= 0 {
		delay = 12 * time.Hour
	}
	if state.ErrorPhase == "check" {
		delay = 5 * time.Minute
	}
	// Recompute from the latest preference on every poll, so interval changes
	// do not leave a check scheduled at the old interval. Manual checks count too.
	if p.AutomaticChecks && !time.Now().Before(lastCheck.Add(delay)) {
		c.Check()
		lastAttempt = time.Now()
	}
	c.mu.Lock()
	held := c.autoInstallHeld
	c.mu.Unlock()
	if p.AutomaticInstalls && !held {
		c.Install()
	}
	return lastAttempt
}

// PrepareRestart takes the same lease again: chats may have started since
// installation. The caller holds the returned lease until shutdown completes.
func (c *Controller) PrepareRestart() (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || !c.state.Installed {
		return nil, errors.New("No installed update is ready to restart.")
	}
	release, err := c.opts.Acquire()
	c.state.Error = ""
	c.state.ErrorPhase = ""
	if err != nil {
		c.state.Error, c.state.ErrorPhase = SafeError(err), "restart"
		c.log("restart_waiting_for_idle", c.state.Version, err)
	}
	return release, err
}

// RestartFailed keeps an installed update available when restart preparation
// outside the controller (such as persisting its journal) fails.
func (c *Controller) RestartFailed(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Error, c.state.ErrorPhase = SafeError(err), "restart"
	c.log("restart_failed", c.state.Version, err)
}

func (c *Controller) Close() {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.workers.Wait()
}
