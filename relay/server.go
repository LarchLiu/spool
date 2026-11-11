package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type ServerOptions struct {
	AdminToken             string
	DataDir                string
	AllowUnregisteredHosts bool
	MaxHosts               int
	MaxConnections         int
	MaxConnectionsPerHost  int
	// New identities one network may enroll per hour without a token.
	AutomaticRegistrationsPerHour                int
	PublicURL                                    string
	TraefikToken, TraefikResolver, ProxyUpstream string
}

// Server forwards opaque native streams and optional public Web requests.
// It has no Spool database, user accounts or home-server private keys.
type Server struct {
	opts           ServerOptions
	mu             sync.Mutex
	hosts          map[string]*registeredHost
	pending        map[string]*relayPair
	connections    int
	registering    int
	closed         bool
	registrations  map[string]registrationRecord
	tokenIndex     map[string]string
	enrollments    enrollmentLimiter
	trustedProxies []netip.Prefix
	settings       relaySettings
	proxyJob       *proxyDetectionJob
	proxyError     string
	proxyWorkers   sync.WaitGroup
	// takeoverProbe bounds how long a reconnecting host waits for the live
	// duplicate check before it replaces an unreachable control connection.
	takeoverProbe time.Duration
}
type registeredHost struct {
	id string
	// Admission tokens this connection accepts, current first. Guarded by
	// Server.mu: the host replaces them without reconnecting.
	tokens         []string
	publicKey      ed25519.PublicKey
	webOrigin      string // Signed host opt-in, valid only for this connection.
	registrationID string
	control        *websocket.Conn
	ctx            context.Context
	cancel         context.CancelFunc
	open           chan controlMessage
	pairs          map[string]*relayPair
	web            *webPool
}

// A host that changed networks reconnects immediately, while this relay still
// holds the old control socket until its heartbeat times out (up to 30 seconds).
// A short ping separates that case from a second live instance.
const takeoverProbeTimeout = 3 * time.Second

type relayPair struct {
	id, secret string
	host       *registeredHost
	ctx        context.Context
	cancel     context.CancelFunc
	data       chan *websocket.Conn
	done       chan struct{}
	claimed    bool // guarded by Server.mu
	released   sync.Once
}

func NewServer(opts ServerOptions) (*Server, error) {
	if opts.ProxyUpstream == "" {
		opts.ProxyUpstream = "http://relay:28080"
	}
	if !validProxyUpstream(opts.ProxyUpstream) {
		return nil, errors.New("invalid proxy upstream")
	}
	if opts.TraefikResolver == "" {
		opts.TraefikResolver = "letsencrypt"
	}
	if !validTraefikResolver(opts.TraefikResolver) {
		return nil, errors.New("RELAY_TRAEFIK_RESOLVER must contain only letters, numbers, hyphens or underscores")
	}
	if opts.TraefikToken != "" && (len(opts.TraefikToken) < 32 || len(opts.TraefikToken) > 512 || strings.ContainsAny(opts.TraefikToken, "\r\n")) {
		return nil, errors.New("RELAY_TRAEFIK_TOKEN must contain 32–512 characters")
	}
	if len(opts.AdminToken) < 32 || len(opts.AdminToken) > 512 || strings.ContainsAny(opts.AdminToken, "\r\n") {
		return nil, errors.New("SPOOL_RELAY_ADMIN_TOKEN must contain 32–512 characters")
	}
	if opts.TraefikToken == opts.AdminToken {
		return nil, errors.New("RELAY_TRAEFIK_TOKEN must differ from the administrator token")
	}
	if strings.TrimSpace(opts.DataDir) == "" {
		return nil, errors.New("relay data directory is required")
	}
	if opts.MaxHosts <= 0 {
		opts.MaxHosts = 100
	}
	if opts.MaxConnections <= 0 {
		opts.MaxConnections = 4096
	}
	if opts.MaxConnectionsPerHost <= 0 {
		opts.MaxConnectionsPerHost = 128
	}
	if opts.AutomaticRegistrationsPerHour <= 0 {
		opts.AutomaticRegistrationsPerHour = 5
	}
	s := &Server{opts: opts, hosts: make(map[string]*registeredHost), pending: make(map[string]*relayPair), registrations: make(map[string]registrationRecord), tokenIndex: make(map[string]string), takeoverProbe: takeoverProbeTimeout,
		enrollments: enrollmentLimiter{limit: opts.AutomaticRegistrationsPerHour, recent: make(map[string][]time.Time)}}
	if err := s.loadRegistrations(); err != nil {
		return nil, err
	}
	if err := s.loadSettings(); err != nil {
		return nil, err
	}
	return s, nil
}

func authorized(r *http.Request, token string) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
}

// admitted accepts any admission credential the host currently honors.
func admitted(r *http.Request, tokens []string) bool {
	for _, token := range tokens {
		if authorized(r, token) {
			return true
		}
	}
	return false
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.serveTenant(w, r) {
		return
	}
	if strings.HasPrefix(r.URL.Path, proxyProbePath) {
		s.serveProxyProbe(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/web/") {
		// Shared-origin mounts are retired. Never serve tenant-controlled HTML
		// here, including offline pages from the old PWA installation.
		// Cookie clearing spans the registrable domain, including sibling hosts.
		// Retired cookies are inert because new origins only accept __Host- cookies.
		w.Header().Set("Clear-Site-Data", "\"cache\", \"storage\"")
		http.Error(w, "Relay Web has moved to isolated subdomains. Open the new address from Spool settings.", http.StatusGone)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		w.Write([]byte("ok\n"))
		return
	}
	// These are native protocol endpoints. Browser cookies/origins never grant
	// admission, and tokens never occur in URLs or access-log query strings.
	if r.Method != http.MethodGet || r.Header.Get("Origin") != "" || r.URL.RawQuery != "" {
		http.Error(w, "invalid relay request", 400)
		return
	}
	switch {
	case r.URL.Path == "/v1/register":
		s.register(w, r, true)
	case r.URL.Path == "/v1/host":
		s.register(w, r, false)
	case r.URL.Path == "/v1/unregister":
		s.unregister(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/connect/"):
		s.connect(w, r, strings.TrimPrefix(r.URL.Path, "/v1/connect/"))
	case strings.HasPrefix(r.URL.Path, "/v1/data/"):
		s.data(w, r, strings.TrimPrefix(r.URL.Path, "/v1/data/"))
	default:
		http.NotFound(w, r)
	}
}
func accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err == nil {
		c.SetReadLimit(controlLimit)
	}
	return c, err
}

// Caller holds Server.mu. Retires one control connection and its data
// connections; its handler observes the cancellation and returns.
func (s *Server) disconnectHostLocked(host *registeredHost) {
	if s.hosts[host.id] == host {
		delete(s.hosts, host.id)
	}
	host.cancel()
	for _, pair := range host.pairs {
		pair.cancel()
	}
	host.control.CloseNow()
}

// reservePair admits one data connection for h under the global and per-host
// limits. Web tunnels that are only idle for reuse yield to new work before a
// connection is refused. It returns nil when the host is gone or still full.
func (s *Server) reservePair(h *registeredHost, web bool) *relayPair {
	for reclaimed := false; ; reclaimed = true {
		s.mu.Lock()
		if s.closed || s.hosts[h.id] != h || h.ctx.Err() != nil || (web && h.webOrigin == "") {
			s.mu.Unlock()
			return nil
		}
		if s.connections < s.opts.MaxConnections && len(h.pairs) < s.opts.MaxConnectionsPerHost {
			ctx, cancel := context.WithCancel(h.ctx)
			p := &relayPair{id: randomID(), secret: randomID(), host: h, ctx: ctx, cancel: cancel, data: make(chan *websocket.Conn, 1), done: make(chan struct{})}
			s.pending[p.id], h.pairs[p.id] = p, p
			s.connections++
			s.mu.Unlock()
			return p
		}
		s.mu.Unlock()
		if reclaimed {
			return nil
		}
		h.web.closeIdle()
	}
}

// releasePair returns a reservation exactly once, however its connection ends.
func (s *Server) releasePair(p *relayPair) {
	p.released.Do(func() {
		p.cancel()
		s.mu.Lock()
		delete(s.pending, p.id)
		delete(p.host.pairs, p.id)
		s.connections--
		s.mu.Unlock()
		close(p.done)
	})
}

// rejectPair ends a connection the host refused to join, so its client fails
// now instead of waiting for the join timeout. A joined connection is the
// host's to close.
func (s *Server) rejectPair(h *registeredHost, id string) {
	s.mu.Lock()
	if p := h.pairs[id]; p != nil && !p.claimed {
		p.cancel()
	}
	s.mu.Unlock()
}

// setAdmission applies tokens the host still honors, newest first. A host
// connection may only ever widen or narrow its own admission, and every token
// arrives on the control connection that proved the host's identity.
func (s *Server) setAdmission(h *registeredHost, tokens []string) {
	if len(tokens) == 0 || len(tokens) > 1+maxRetiredTokens {
		return
	}
	for _, token := range tokens {
		if !validToken(token) {
			return
		}
	}
	s.mu.Lock()
	if s.hosts[h.id] == h {
		h.tokens = append([]string(nil), tokens...)
	}
	s.mu.Unlock()
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	h := s.hosts[id]
	if h == nil || !admitted(r, h.tokens) {
		s.mu.Unlock()
		http.Error(w, "server unavailable", 404)
		return
	}
	s.mu.Unlock()
	p := s.reservePair(h, false)
	if p == nil {
		http.Error(w, "relay busy", 503)
		return
	}
	defer s.releasePair(p)
	ctx := p.ctx
	client, err := accept(w, r)
	if err != nil {
		return
	}
	defer client.CloseNow()
	timer := time.NewTimer(handshakeTimeout)
	defer timer.Stop()
	// A burst waits its turn on the control connection instead of being dropped.
	select {
	case h.open <- controlMessage{Type: "open", ID: p.id, Secret: p.secret, ClientIP: s.clientIP(r)}:
	case <-ctx.Done():
		return
	case <-timer.C:
		return
	}
	select {
	case agent := <-p.data:
		defer agent.CloseNow()
		bridge(ctx, client, agent)
	case <-ctx.Done():
	case <-timer.C:
	}
}
func (s *Server) data(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	p := s.pending[id]
	if p == nil || p.claimed || p.ctx.Err() != nil || !authorized(r, p.secret) {
		s.mu.Unlock()
		http.Error(w, "unknown connection", 404)
		return
	}
	p.claimed = true
	s.mu.Unlock()
	c, err := accept(w, r)
	if err != nil {
		p.cancel()
		return
	}
	defer c.CloseNow()
	select {
	case p.data <- c:
	case <-p.ctx.Done():
		return
	}
	<-p.done
}
func bridge(ctx context.Context, a, b *websocket.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	x, y := byteStream(ctx, a), byteStream(ctx, b)
	go keepAlive(ctx, a)
	go keepAlive(ctx, b)
	done := make(chan struct{})
	var copies sync.WaitGroup
	copies.Add(2)
	copyStream := func(dst, src io.ReadWriter) {
		defer copies.Done()
		_, _ = io.CopyBuffer(dst, src, make([]byte, 32<<10))
		cancel()
	}
	go copyStream(x, y)
	go copyStream(y, x)
	go func() { copies.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	cancel()
	a.CloseNow()
	b.CloseNow()
	x.Close()
	y.Close()
	<-done
}

func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	if s.proxyJob != nil {
		s.proxyJob.cancel()
	}
	for _, h := range s.hosts {
		h.cancel()
		h.control.CloseNow()
		for _, p := range h.pairs {
			p.cancel()
		}
	}
	s.mu.Unlock()
	s.proxyWorkers.Wait()
	return nil
}
