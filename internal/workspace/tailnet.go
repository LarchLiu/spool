package workspace

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"spool/internal/tailnet"
)

const tsNetAuthKeyPrefix = "tskey-auth-"

type tailnetConnection interface {
	Listener() net.Listener
	Origin() string
	Close() error
}

type tailnetOpener func(context.Context, string, tailnet.Options, func(tailnet.Status)) (tailnetConnection, error)

type tailnetRuntime struct {
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	status       tailnet.Status
	authRevision uint64
	loginURL     func(context.Context) (string, error)
	enrolling    bool
	settings     Settings
}

func (r *tailnetRuntime) setLogin(login func(context.Context) (string, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loginURL = login
}

func (r *tailnetRuntime) snapshot() tailnet.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func (r *tailnetRuntime) report(status tailnet.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if status.URL == "" {
		status.URL = r.status.URL
	}
	r.status = status
}

func sameTailnetSettings(a, b Settings) bool {
	return a.TSNetEnabled == b.TSNetEnabled && (!a.TSNetEnabled || a.TSNetHostname == b.TSNetHostname && a.TSNetPort == b.TSNetPort)
}

func (s *Service) startTailnetLocked(settings Settings) {
	if s.tailnet != nil {
		s.tailnet.cancel()
	}
	ctx, cancel := context.WithCancel(s.ctx)
	runtime := &tailnetRuntime{ctx: ctx, cancel: cancel, status: tailnet.Status{State: "Starting"}, authRevision: s.tailnetAuthRevision}
	authKey := s.tailnetAuthKey
	s.tailnet = runtime
	server := s.server
	open := s.openTailnet
	if open == nil {
		open = func(ctx context.Context, root string, options tailnet.Options, report func(tailnet.Status)) (tailnetConnection, error) {
			endpoint, err := tailnet.Start(ctx, root, options, report)
			if err != nil {
				return nil, err
			}
			runtime.setLogin(endpoint.LoginURL)
			if err := endpoint.Up(ctx); err != nil {
				_ = endpoint.Close()
				return nil, err
			}
			return endpoint, nil
		}
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		// A restart may already have returned while the old node is closing.
		// Never open two nodes against the same persistent state directory.
		s.tailnetMu.Lock()
		defer s.tailnetMu.Unlock()
		if ctx.Err() != nil {
			return
		}
		connection, err := open(ctx, s.Store.Root(), tailnet.Options{Hostname: settings.TSNetHostname, Port: settings.TSNetPort, AuthKey: authKey}, runtime.report)
		if err != nil {
			if ctx.Err() == nil {
				runtime.report(tailnet.Status{State: "Error", Error: tailnet.RedactSecrets(err.Error(), authKey)})
			}
			return
		}
		defer func() {
			if err := connection.Close(); err != nil {
				log.Printf("event=tsnet_shutdown_failed error=%q", tailnet.RedactSecrets(err.Error(), authKey))
			}
		}()
		if !s.attachTailnet(runtime, server, connection, ctx) {
			return
		}
		<-ctx.Done()
	}()
}

// RegisterTSNetAuthKey runs enrollment now, without stopping the local HTTP
// service or attaching a new ingress. Restart explicitly activates the saved
// identity later. Only one node may touch the persistent state at a time.
func (s *Service) RegisterTSNetAuthKey(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if err := validateTSNetAuthKey(key); err != nil || key == "" {
		return fmt.Errorf("invalid Tailscale Auth Key")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("service is closed")
	}
	settings, err := s.Store.Settings()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if !settings.TSNetEnabled {
		s.mu.Unlock()
		return fmt.Errorf("Enable embedded Tailscale before using an Auth Key.")
	}
	if s.tailnet != nil {
		state := s.tailnet.snapshot()
		if state.State == "Registered" || state.State == "Running" && state.URL != "" {
			s.mu.Unlock()
			return fmt.Errorf("This Tailscale node is already registered. No Auth Key is required.")
		}
		if s.tailnet.enrolling && s.tailnet.ctx.Err() == nil {
			s.mu.Unlock()
			return fmt.Errorf("Tailscale registration is already in progress.")
		}
		s.tailnet.cancel()
	}
	enrollCtx, cancel := context.WithCancel(s.ctx)
	stop := context.AfterFunc(ctx, cancel)
	runtime := &tailnetRuntime{ctx: enrollCtx, cancel: cancel, enrolling: true, settings: settings, status: tailnet.Status{State: "Registering"}, authRevision: s.tailnetAuthRevision}
	s.tailnet = runtime
	enroll := s.enrollTailnet
	if enroll == nil {
		enroll = tailnet.Enroll
	}
	s.workers.Add(1)
	s.mu.Unlock()
	defer s.workers.Done()
	defer cancel()
	defer stop()
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()
	if enrollCtx.Err() == nil {
		err = enroll(enrollCtx, s.Store.Root(), tailnet.Options{Hostname: settings.TSNetHostname, Port: settings.TSNetPort, AuthKey: key}, func(status tailnet.Status) {
			status.Error = tailnet.RedactSecrets(status.Error, key)
			if status.State != "NeedsMachineAuth" && status.State != "Error" {
				status.State = "Registering"
			}
			status.AuthURL, status.URL = "", ""
			runtime.report(status)
		})
	}
	if enrollCtx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("Tailscale registration timed out. Check network access or device approval, then retry.")
		} else {
			err = fmt.Errorf("Tailscale registration was canceled. Try again when the service is ready.")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tailnet != runtime {
		return fmt.Errorf("Tailscale registration was canceled. Try again when the service is ready.")
	}
	if err != nil {
		err = errors.New(tailnet.RedactSecrets(err.Error(), key))
		runtime.report(tailnet.Status{State: "Error", Error: err.Error()})
		return err
	}
	s.tailnetAuthKey = ""
	runtime.report(tailnet.Status{State: "Registered"})
	return nil
}

// TSNetLoginURL works while Up is still waiting for enrollment. The native
// bridge calls it on a worker, never on the UI thread or under the service lock.
func (s *Service) TSNetLoginURL(ctx context.Context) (string, error) {
	s.mu.Lock()
	runtime, authKey := s.tailnet, s.tailnetAuthKey
	s.mu.Unlock()
	if runtime == nil {
		return "", fmt.Errorf("Tailscale is not running. Start the service first.")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(runtime.ctx, cancel)
	defer stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if runtime.ctx.Err() != nil {
			return "", fmt.Errorf("Tailscale service was restarted or stopped. Try signing in again.")
		}
		runtime.mu.Lock()
		status, login := runtime.status, runtime.loginURL
		runtime.mu.Unlock()
		if tailnet.IsLoginURL(status.AuthURL) {
			return status.AuthURL, nil
		}
		if status.State == "Error" {
			return "", errors.New(status.Error)
		}
		if login != nil {
			address, err := login(ctx)
			if runtime.ctx.Err() != nil {
				return "", fmt.Errorf("Tailscale service was restarted or stopped. Try signing in again.")
			}
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			if err != nil {
				return "", errors.New(tailnet.RedactSecrets(err.Error(), authKey))
			}
			if !tailnet.IsLoginURL(address) {
				return "", fmt.Errorf("Tailscale returned an invalid login link.")
			}
			return address, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("Tailscale did not provide a login link. Check network access to Tailscale and try again, or use an Auth Key.")
		case <-ticker.C:
		}
	}
	return "", fmt.Errorf("Tailscale did not provide a login link. Check network access to Tailscale and try again, or use an Auth Key.")
}

func (s *Service) attachTailnet(runtime *tailnetRuntime, server *http.Server, connection tailnetConnection, ctx context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil || s.server != server || s.tailnet != runtime {
		return false
	}
	origin := connection.Origin()
	// TLS requests already pass the app's same-origin check against their Host.
	// Publish this ingress separately; never replace the configured proxy origin.
	if runtime.authRevision == s.tailnetAuthRevision {
		s.tailnetAuthKey = ""
	}
	s.serveLocked(server, connection.Listener())
	runtime.report(tailnet.Status{State: "Running", URL: origin})
	log.Printf("Spool Tailnet HTTPS: %s (Spool account login is still required)", origin)
	return true
}

// SetTSNetAuthKey stages an enrollment key in memory, not in Settings or SQLite.
// Applying it is an explicit service start/restart, so active turns are not
// silently interrupted when the user edits credentials.
func (s *Service) SetTSNetAuthKey(key string) error {
	key = strings.TrimSpace(key)
	if err := validateTSNetAuthKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("service is closed")
	}
	if s.tailnetAuthKey != key {
		s.tailnetAuthKey = key
		s.tailnetAuthRevision++
	}
	return nil
}

func validateTSNetAuthKey(key string) error {
	if key != "" && (!strings.HasPrefix(key, tsNetAuthKeyPrefix) || len(key) <= len(tsNetAuthKeyPrefix) || len(key) > 512 || strings.ContainsAny(key, " \t\r\n")) {
		return fmt.Errorf("invalid Tailscale Auth Key")
	}
	return nil
}
