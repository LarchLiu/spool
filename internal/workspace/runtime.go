package workspace

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
	"spool/internal/netproxy"
	"spool/internal/tailnet"
)

type Service struct {
	Store               *Store
	Registry            *Registry
	mu                  sync.Mutex
	server              *http.Server
	app                 *App
	runner              *agent.CLIRunner
	ctx                 context.Context
	cancel              context.CancelFunc
	workers             sync.WaitGroup
	lease               string
	lastError           string
	closed              bool
	updating            bool
	runningAddress      string
	localAddress        string
	tailnet             *tailnetRuntime
	tailnetMu           sync.Mutex
	openTailnet         tailnetOpener
	enrollTailnet       func(context.Context, string, tailnet.Options, func(tailnet.Status)) error
	tailnetAuthKey      string
	tailnetAuthRevision uint64
	networkProxy        netproxy.Options
}
type ServiceStatus struct {
	Running         bool
	RestartRequired bool
	Address         string
	Error           string
	Online          []PresenceUser
	TSNet           tailnet.Status
	TSNetAuthKeySet bool
}

func OpenService(root string) (*Service, error) {
	store, err := OpenStore(root)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*Service, error) { store.Close(); return nil, e }
	if _, err = store.db.Exec(`CREATE TABLE IF NOT EXISTS runtime_lease(id INTEGER PRIMARY KEY CHECK(id=1),token TEXT NOT NULL,expires_at INTEGER NOT NULL)`); err != nil {
		return fail(err)
	}
	lease := randomToken()
	now := time.Now()
	res, err := store.db.Exec(`INSERT INTO runtime_lease VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET token=excluded.token,expires_at=excluded.expires_at WHERE expires_at<?`, lease, now.Add(30*time.Second).UnixMilli(), now.UnixMilli())
	if err != nil {
		return fail(err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fail(fmt.Errorf("this database is already managed by another running instance; after a crash wait up to 30 seconds"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{Store: store, Registry: NewRegistry(store), ctx: ctx, cancel: cancel, lease: lease}
	settings, err := store.Settings()
	if err == nil {
		err = s.applyNetworkSettingsLocked(settings)
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	if err = store.SetInterrupted(); err != nil {
		s.Close()
		return nil, err
	}
	s.workers.Add(2)
	go func() { defer s.workers.Done(); s.Registry.Run(ctx) }()
	go func() {
		defer s.workers.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				res, e := store.db.Exec(`UPDATE runtime_lease SET expires_at=? WHERE id=1 AND token=?`, time.Now().Add(30*time.Second).UnixMilli(), lease)
				var affected int64
				if e == nil {
					affected, e = res.RowsAffected()
				}
				if e != nil || affected != 1 {
					s.mu.Lock()
					firstFailure := s.lastError != "database lease renewal failed; service stopped"
					s.lastError = "database lease renewal failed; service stopped"
					s.mu.Unlock()
					if firstFailure {
						log.Printf("event=database_lease_lost affected=%d error=%v", affected, e)
					}
					if err := s.Stop(); err != nil {
						log.Printf("event=service_stop_failed error=%q", err)
					}
				}
			}
		}
	}()
	return s, nil
}
func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startLocked()
}

func (s *Service) startLocked() error {
	if s.closed {
		return fmt.Errorf("service is closed")
	}
	if s.updating {
		return ErrUpdateInProgress
	}
	if s.server != nil {
		return nil
	}
	settings, err := s.Store.Settings()
	if err != nil {
		return err
	}
	if err = settings.Validate(); err != nil {
		return err
	}
	if err = s.applyNetworkSettingsLocked(settings); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", settings.Address())
	if err != nil {
		s.lastError = err.Error()
		return err
	}
	localAddress := loopbackAddress(listener.Addr().String())
	var localListener net.Listener
	if localAddress == "" {
		// A LAN-only binding cannot serve a secure-context desktop page. Share
		// this server and App through a loopback listener, not a second backend.
		localListener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			listener.Close()
			s.lastError = err.Error()
			return err
		}
		localAddress = localListener.Addr().String()
	}
	agents, err := s.Store.Agents()
	if err != nil {
		listener.Close()
		if localListener != nil {
			localListener.Close()
		}
		return err
	}
	runner := agent.NewCLIRunner()
	runner.ConfigureBinaries(agentBinaries(agents))
	runner.IdleTimeout = time.Duration(settings.IdleMinutes) * time.Minute
	app := NewApp(s.Store, runner, "", "")
	app.startLocalSessionSync()
	app.startIssueScheduler(s.lease)
	server := &http.Server{Handler: app, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	s.server = server
	s.app = app
	s.runner = runner
	s.lastError = ""
	s.runningAddress = listener.Addr().String()
	s.localAddress = localAddress
	listeners := []net.Listener{listener}
	if localListener != nil {
		listeners = append(listeners, localListener)
	}
	for _, listener := range listeners {
		s.serveLocked(server, listener)
	}
	if settings.TSNetEnabled {
		s.startTailnetLocked(settings)
	}
	log.Printf("event=service_started address=%q", s.runningAddress)
	return nil
}

// ServeListener adds an ingress to the running service, sharing its App and
// shutdown lifecycle. Ownership transfers only on success; Stop closes it.
// A subsequent Start/Restart does not recreate caller-supplied listeners.
func (s *Service) ServeListener(listener net.Listener) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.server == nil {
		return fmt.Errorf("chat service is not running")
	}
	if listener == nil {
		return fmt.Errorf("listener is required")
	}
	s.serveLocked(s.server, listener)
	return nil
}

func (s *Service) serveLocked(server *http.Server, listener net.Listener) {
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("event=http_server_failed error=%q", err)
			s.mu.Lock()
			s.lastError = err.Error()
			s.mu.Unlock()
		}
	}()
}

func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked()
}

// Restart is explicit: saving settings alone never interrupts running tasks.
func (s *Service) Restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updating {
		return ErrUpdateInProgress
	}
	if err := s.stopLocked(); err != nil {
		return err
	}
	return s.startLocked()
}

func (s *Service) stopLocked() error {
	if s.tailnet != nil {
		s.tailnet.cancel()
		s.tailnet = nil
	}
	if s.server == nil {
		return nil
	}
	server, app := s.server, s.app
	s.server = nil
	s.app = nil
	s.runner = nil
	s.localAddress = ""
	_ = server.Close()
	return app.Close()
}
func (s *Service) Status() ServiceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.Store.Settings()
	message := s.lastError
	if err != nil {
		message = err.Error()
	}
	online := []PresenceUser{}
	if s.app != nil {
		online = s.app.presence.List(s.Store)
	}
	address := settings.Address()
	restartRequired := false
	if s.server != nil {
		address = s.runningAddress
		if s.app != nil {
			active := s.app.settings
			restartRequired = active.Host != settings.Host || active.Port != settings.Port ||
				s.app.publicURL() != settings.PublicURL || active.IdleMinutes != settings.IdleMinutes ||
				!sameTailnetSettings(active, settings) || active.TSNetEnabled && active.proxyOptions() != settings.proxyOptions()
		}
	}
	tailnetStatus := tailnet.Status{State: "Disabled"}
	if settings.TSNetEnabled {
		tailnetStatus.State = "Stopped"
	}
	if s.tailnet != nil {
		tailnetStatus = s.tailnet.snapshot()
		restartRequired = restartRequired || s.tailnet.authRevision != s.tailnetAuthRevision || tailnetStatus.State == "Registered"
	}
	return ServiceStatus{Running: s.server != nil, RestartRequired: restartRequired, Address: address, Error: message, Online: online, TSNet: tailnetStatus, TSNetAuthKeySet: s.tailnetAuthKey != ""}
}
func (s *Service) WebURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, _ := s.Store.Settings()
	if s.app != nil {
		settings = s.app.settings
	}
	if settings.PublicURL != "" {
		return settings.PublicURL
	}
	if s.tailnet != nil {
		if origin := s.tailnet.snapshot().URL; origin != "" {
			return origin
		}
	}
	host := settings.Host
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, fmt.Sprint(settings.Port))
}

// LocalWebURL is the active service's same-origin desktop endpoint. Saved
// listener settings and a public reverse-proxy URL must not redirect it.
func (s *Service) LocalWebURL() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server == nil {
		return "", fmt.Errorf("chat service is not running")
	}
	return "http://" + s.localAddress, nil
}

func loopbackAddress(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if ip.IsUnspecified() {
		host = "::1"
		if ip.To4() != nil {
			host = "127.0.0.1"
		}
	} else if !ip.IsLoopback() {
		return ""
	}
	return net.JoinHostPort(host, port)
}
func (s *Service) SaveAgent(backend chatmodel.Backend, enabled bool, path string) error {
	if err := s.Store.ConfigureAgent(backend, enabled, path); err != nil {
		return err
	}
	all, err := s.Store.Agents()
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.runner != nil {
		s.runner.ConfigureBinaries(agentBinaries(all))
	}
	s.mu.Unlock()
	return s.Registry.Refresh(s.ctx)
}
func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	err := s.Stop()
	s.workers.Wait()
	_, e := s.Store.db.Exec(`DELETE FROM runtime_lease WHERE token=?`, s.lease)
	if err == nil {
		err = e
	}
	e = s.Store.Close()
	if err == nil {
		err = e
	}
	return err
}
