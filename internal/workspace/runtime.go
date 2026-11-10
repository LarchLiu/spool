package workspace

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
	"spool/internal/netproxy"
	"spool/internal/tailnet"
	"spool/internal/voxl"
	"spool/relay"
)

type Service struct {
	Store               *Store
	Registry            *Registry
	mu                  sync.Mutex
	server              *http.Server
	web                 *httpIngress
	activeSettings      Settings
	webError            string
	relayError          string
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
	openTailnetLogin    func(context.Context, string, tailnet.Options, func(tailnet.Status)) (tailnetLoginConnection, error)
	enrollTailnet       func(context.Context, string, tailnet.Options, func(tailnet.Status)) error
	tailnetAuthKey      string
	tailnetAuthRevision uint64
	networkProxy        netproxy.Options
	networkProxyReady   bool
	desktop             *voxl.Manager
	relay               *relayRuntime
	relayRegistering    bool
	relayDevices        *relay.Devices
}
type ServiceStatus struct {
	Running                  bool
	RestartRequired          bool
	PendingChanges           []string
	WebError                 string
	Address                  string
	Error                    string
	Online                   []PresenceUser
	TSNet                    tailnet.Status
	TSNetAuthKeySet          bool
	Voxl                     voxl.Status
	RemoteDesktopConnections map[string]int
	Relay                    RelayStatus
}

func OpenService(root string) (*Service, error) {
	return openService(root, serviceRuntime)
}

// OpenAdministration acquires the same exclusive runtime lease without probing
// provider CLIs in the background. Local one-shot commands should not start
// agents or scan native sessions just to edit saved configuration.
func OpenAdministration(root string) (*Service, error) {
	return openService(root, serviceAdministration)
}

type serviceMode int

const (
	serviceRuntime serviceMode = iota
	serviceAdministration
)

func openService(root string, mode serviceMode) (*Service, error) {
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
	s.desktop = voxl.New(filepath.Join(store.Root(), "voxl"), func() { store.changes.publish(changeRemoteDesktop) })
	settings, err := store.Settings()
	if err == nil {
		_, err = s.loadRelayRegistrationsLocked()
	}
	if err == nil {
		s.applyNetworkSettingsLocked(settings)
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	if err = store.SetInterrupted(); err != nil {
		s.Close()
		return nil, err
	}
	if mode == serviceRuntime {
		s.workers.Add(1)
		go func() { defer s.workers.Done(); s.Registry.Run(ctx) }()
	}
	s.workers.Add(1)
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
	return s.startWithSettingsLocked(settings)
}

func (s *Service) startWithSettingsLocked(settings Settings) error {
	if err := settings.validateLocal(); err != nil {
		return err
	}
	s.applyNetworkSettingsLocked(settings)
	// The desktop keeps a stable private entry when other listeners restart.
	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.lastError = err.Error()
		return err
	}
	agents, err := s.Store.Agents()
	if err != nil {
		localListener.Close()
		return err
	}
	runner := agent.NewCLIRunner()
	runner.ConfigureBinaries(agentBinaries(agents))
	runner.IdleTimeout = time.Duration(settings.IdleMinutes) * time.Minute
	app := NewApp(s.Store, runner, "", "")
	app.settings = settings
	app.configureRelayWeb(settings)
	app.desktop = s.desktop
	app.startLocalSessionSync()
	app.startIssueScheduler(s.lease)
	server := &http.Server{Handler: app, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	s.server = server
	s.app = app
	s.runner = runner
	s.lastError = ""
	s.activeSettings = settings
	s.localAddress = localListener.Addr().String()
	s.serveLocked(server, localListener)
	if err := s.startWebLocked(settings); err != nil {
		_ = s.stopLocked()
		s.lastError = err.Error()
		return err
	}
	s.webError, s.relayError = "", ""
	if settings.RelayEnabled {
		if err := s.startRelayLocked(settings); err != nil {
			s.stopLocked()
			s.lastError = err.Error()
			return err
		}
	}
	if settings.TSNetEnabled {
		s.startTailnetLocked(settings)
	}
	s.desktop.SetEnabled(s.ctx, settings.RemoteDesktopEnabled)
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
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed && !errors.Is(err, net.ErrClosed) {
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
	if s.relay != nil {
		s.relay.close()
		s.relay = nil
	}
	if s.desktop != nil {
		s.desktop.SetEnabled(s.ctx, false)
	}
	if s.tailnet != nil {
		s.tailnet.cancel()
		s.tailnet.ingress.close()
		s.tailnet = nil
	}
	if s.server == nil {
		return nil
	}
	s.web.close()
	s.web = nil
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
	connections := map[string]int{}
	if s.app != nil {
		online = s.app.presence.List(s.Store)
		connections = s.app.remoteDesktopConnections()
	}
	address := ""
	if settings.WebEnabled {
		address = settings.Address()
	}
	pending := s.pendingChangesLocked(settings)
	if s.server != nil {
		address = s.runningAddress
	}
	tailnetStatus := tailnet.Status{State: "Disabled"}
	if settings.TSNetEnabled {
		tailnetStatus.State = "Stopped"
	}
	if s.tailnet != nil {
		tailnetStatus = s.tailnet.snapshot()
	}
	var desktopStatus voxl.Status
	if s.desktop != nil {
		desktopStatus = s.desktop.Status()
	}
	relayStatus := RelayStatus{State: "disabled"}
	if settings.RelayEnabled {
		relayStatus.State = "stopped"
	}
	if s.relay != nil {
		relayStatus = s.relay.snapshot()
	}
	if s.relayError != "" {
		relayStatus.Error = s.relayError
		if s.relay == nil {
			relayStatus.State = "error"
		}
	}
	relayStatus.RegistrationTokenSet = s.relayRegistrationToken(settings.RelayURL) != ""
	relayStatus.Registered = s.relayRegisteredLocked(settings.RelayURL)
	relayStatus.Registering = s.relayRegistering
	return ServiceStatus{Running: s.server != nil, RestartRequired: len(pending) > 0, PendingChanges: pending, WebError: s.webError, Address: address, Error: message, Online: online, TSNet: tailnetStatus, TSNetAuthKeySet: s.tailnetAuthKey != "", Voxl: desktopStatus, RemoteDesktopConnections: connections, Relay: relayStatus}
}
func (s *Service) WebURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, _ := s.Store.Settings()
	if s.app != nil {
		settings = s.app.activeSettings()
	}
	if settings.WebEnabled && settings.PublicURL != "" {
		return settings.PublicURL
	}
	if s.tailnet != nil {
		if origin := s.tailnet.snapshot().URL; origin != "" {
			return origin
		}
	}
	if !settings.WebEnabled {
		if s.relay != nil {
			if address := s.relay.snapshot().WebURL; address != "" {
				return address
			}
		}
		if s.server != nil {
			return "http://" + s.localAddress
		}
		return ""
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
