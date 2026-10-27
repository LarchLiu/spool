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
}

type Options struct {
	Enabled     bool
	Check       func(context.Context) (*Release, error)
	Acquire     func() (func(), error)
	Preferences func() (Preferences, error)
}

type State struct {
	Enabled           bool
	Checking          bool
	Installing        bool
	Installed         bool
	Version, Notes    string
	Error             string
	LastCheck         time.Time
	Downloaded, Total int64
}

type Controller struct {
	mu      sync.Mutex
	state   State
	opts    Options
	release *Release
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	started bool
	workers sync.WaitGroup
}

func New(opts Options) *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	return &Controller{opts: opts, state: State{Enabled: opts.Enabled}, ctx: ctx, cancel: cancel}
}

func (c *Controller) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
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
	release, err := c.opts.Check(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Checking = false
	if err != nil {
		c.state.Error = err.Error()
		return
	}
	c.state.LastCheck = time.Now()
	c.release = release
	c.state.Version, c.state.Notes = "", ""
	if release != nil {
		c.state.Version, c.state.Notes = release.Version, release.Notes
	}
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
		err = c.release.Install(ctx, func(downloaded, total int64) {
			c.mu.Lock()
			c.state.Downloaded, c.state.Total = downloaded, total
			c.mu.Unlock()
		})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Installing = false
	c.state.Installed = err == nil
	if err != nil {
		c.state.Error = err.Error()
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
		var nextCheck time.Time
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-timer.C:
			}
			nextCheck = c.poll(nextCheck)
			timer.Reset(time.Minute)
		}
	}()
}

func (c *Controller) poll(nextCheck time.Time) time.Time {
	p, err := c.opts.Preferences()
	if err != nil {
		return nextCheck
	}
	if p.AutomaticChecks && !time.Now().Before(nextCheck) {
		c.Check()
		delay := 12 * time.Hour
		if c.State().Error != "" {
			delay = 5 * time.Minute
		}
		nextCheck = time.Now().Add(delay)
	}
	if p.AutomaticInstalls {
		c.Install()
	}
	return nextCheck
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
	if err != nil {
		c.state.Error = err.Error()
	}
	return release, err
}

func (c *Controller) Close() {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.workers.Wait()
}
