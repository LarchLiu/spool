package workspace

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"spool/internal/agent"
	chatmodel "spool/internal/model"
)

type Service struct {
	Store          *Store
	Registry       *Registry
	mu             sync.Mutex
	server         *http.Server
	app            *App
	runner         *agent.CLIRunner
	ctx            context.Context
	cancel         context.CancelFunc
	workers        sync.WaitGroup
	lease          string
	lastError      string
	closed         bool
	runningAddress string
}
type ServiceStatus struct {
	Running bool
	Address string
	Error   string
	Online  []PresenceUser
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
					s.lastError = "database lease renewal failed; service stopped"
					s.mu.Unlock()
					s.Stop()
				}
			}
		}
	}()
	return s, nil
}
func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("service is closed")
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
	listener, err := net.Listen("tcp", settings.Address())
	if err != nil {
		s.lastError = err.Error()
		return err
	}
	agents, err := s.Store.Agents()
	if err != nil {
		listener.Close()
		return err
	}
	runner := agent.NewCLIRunner()
	runner.ConfigureBinaries(agentBinaries(agents))
	runner.IdleTimeout = time.Duration(settings.IdleMinutes) * time.Minute
	app := NewApp(s.Store, runner, "", "")
	server := &http.Server{Handler: app, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	s.server = server
	s.app = app
	s.runner = runner
	s.lastError = ""
	s.runningAddress = listener.Addr().String()
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.mu.Lock()
			s.lastError = err.Error()
			s.mu.Unlock()
		}
	}()
	return nil
}
func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server == nil {
		return nil
	}
	server, app := s.server, s.app
	s.server = nil
	s.app = nil
	s.runner = nil
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
	if s.server != nil {
		address = s.runningAddress
	}
	return ServiceStatus{Running: s.server != nil, Address: address, Error: message, Online: online}
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
	host := settings.Host
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, fmt.Sprint(settings.Port))
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
func DefaultDataDir() string {
	if path := strings.TrimSpace(os.Getenv("SPOOL_HOME")); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "spool")
	}
	return filepath.Join(home, ".spool")
}
