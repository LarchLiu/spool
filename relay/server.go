package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type ServerOptions struct {
	AdminToken                                   string
	DataDir                                      string
	AllowUnregisteredHosts                       bool
	MaxHosts                                     int
	MaxConnections                               int
	MaxConnectionsPerHost                        int
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
	id, token      string
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
	s := &Server{opts: opts, hosts: make(map[string]*registeredHost), pending: make(map[string]*relayPair), registrations: make(map[string]registrationRecord), tokenIndex: make(map[string]string), takeoverProbe: takeoverProbeTimeout}
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
func (s *Server) register(w http.ResponseWriter, r *http.Request, registrationOnly bool) {
	s.mu.Lock()
	registration, allowed := s.registrationForRequestLocked(r)
	withoutToken := s.opts.AllowUnregisteredHosts && r.Header.Get("Authorization") == ""
	// A credential whose identity is already connected can only replace that
	// entry, so it never counts as an additional host.
	reconnecting := !registrationOnly && allowed && registration.ServerID != "" && s.hosts[registration.ServerID] != nil
	full := s.closed || (!registrationOnly && !reconnecting && len(s.hosts) >= s.opts.MaxHosts) || s.registering >= s.opts.MaxHosts
	if (allowed || withoutToken) && !full {
		s.registering++
	}
	s.mu.Unlock()
	if !allowed && !withoutToken {
		http.Error(w, "unauthorized", 401)
		return
	}
	if full {
		http.Error(w, "relay unavailable", 503)
		return
	}
	reserved := true
	defer func() {
		s.mu.Lock()
		if reserved {
			s.registering--
		}
		s.mu.Unlock()
	}()
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	challenge := randomID()
	if writeControl(ctx, c, controlMessage{Type: "challenge", Challenge: challenge}) != nil {
		return
	}
	authCtx, authCancel := context.WithTimeout(ctx, handshakeTimeout)
	m, err := readControl(authCtx, c)
	authCancel()
	if err != nil || m.Type != "register" || len(m.PublicKey) != ed25519.PublicKeySize || !validToken(m.AccessToken) ||
		!ed25519.Verify(m.PublicKey, hostRegistrationProof(challenge, m.AccessToken, m.WebOrigin, m.Name, m.WebVersion), m.Signature) {
		return
	}
	name := strings.TrimSpace(m.Name)
	if name != "" && (!registrationOnly || !validRegistrationName(name)) {
		writeControl(ctx, c, controlMessage{Type: "invalid_registration"})
		return
	}
	if m.WebOrigin != "" {
		if u, e := ParseRelayURL(m.WebOrigin); e != nil || u.String() != m.WebOrigin {
			return
		}
	}
	if m.WebVersion != 0 && m.WebVersion != 2 {
		return
	}
	metadata := registrationMetadata{name: name, ip: s.clientIP(r), enrollment: registrationOnly}
	h := &registeredHost{id: ServerID(m.PublicKey), token: m.AccessToken, publicKey: append(ed25519.PublicKey(nil), m.PublicKey...), webOrigin: m.WebOrigin, registrationID: registration.ID, control: c, ctx: ctx, cancel: cancel, open: make(chan controlMessage, 32), pairs: make(map[string]*relayPair)}
	h.web = newWebPool(func(ctx context.Context, clientIP string) (net.Conn, error) { return s.dialWeb(ctx, h, clientIP) })
	var unreachable *registeredHost
	if reconnecting {
		unreachable = s.unreachableHost(ctx, h.id, registration.ID)
	}
	s.mu.Lock()
	current, allowed := s.registrationForRequestLocked(r)
	if !withoutToken && (!allowed || current.ID != registration.ID) {
		s.mu.Unlock()
		writeControl(ctx, c, controlMessage{Type: "registration_rejected"})
		return
	}
	// A live duplicate keeps its connection: two instances sharing one identity
	// must not disconnect each other in turn.
	existing := s.hosts[h.id]
	replaces := existing != nil && existing == unreachable
	conflicts := existing != nil && !replaces && (!registrationOnly || withoutToken)
	if s.closed || conflicts || (!registrationOnly && !replaces && len(s.hosts) >= s.opts.MaxHosts) {
		s.mu.Unlock()
		return
	}
	var issuedToken string
	if withoutToken {
		current, issuedToken, err = s.claimIdentityLocked(h.id, metadata)
	} else {
		err = s.claimRegistrationLocked(current, h.id, metadata)
	}
	if err != nil {
		s.mu.Unlock()
		if errors.Is(err, ErrRegistrationExists) || errors.Is(err, ErrRegistrationRevoked) {
			writeControl(ctx, c, controlMessage{Type: "registration_rejected"})
		}
		return
	}
	// Only the signed v2 opt-in may publish isolated Web. Old clients keep
	// native connectivity but cannot reintroduce shared-origin Web mounts.
	h.webOrigin = ""
	if m.WebOrigin != "" && m.WebVersion == 2 && !s.registrations[current.ID].WebBlocked {
		h.webOrigin = strings.TrimSuffix(s.webURLLocked(h.id), "/")
	}
	if !registrationOnly {
		if err := s.setWebPublishedLocked(current.ID, m.WebOrigin != "" && m.WebVersion == 2); err != nil {
			s.mu.Unlock()
			return
		}
	}
	if registrationOnly {
		s.mu.Unlock()
		writeControl(ctx, c, controlMessage{Type: "ready", RegistrationToken: issuedToken})
		return
	}
	if replaces {
		s.disconnectHostLocked(existing)
	}
	h.registrationID = current.ID
	s.hosts[h.id] = h
	s.registering--
	reserved = false
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		if s.hosts[h.id] == h {
			delete(s.hosts, h.id)
		}
		for _, p := range h.pairs {
			p.cancel()
		}
		s.mu.Unlock()
		h.web.close()
	}()
	webURL := ""
	if h.webOrigin != "" {
		webURL = h.webOrigin + "/"
	}
	if writeControl(ctx, c, controlMessage{Type: "ready", RegistrationToken: issuedToken, WebURL: webURL}) != nil {
		return
	}
	go keepAlive(ctx, c)
	// Control is server-to-host after registration. A reader processes pongs
	// and detects dead agents even when no client is currently connected.
	go func() {
		defer cancel()
		for {
			if _, err := readControl(ctx, c); err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-h.open:
			if writeControl(ctx, c, msg) != nil {
				return
			}
		}
	}
}

// unreachableHost returns the connected host for id only when its control
// connection fails a liveness probe. The caller has verified the identity's
// signature; the credential must also be the one that connection registered.
func (s *Server) unreachableHost(ctx context.Context, id, registrationID string) *registeredHost {
	s.mu.Lock()
	existing := s.hosts[id]
	s.mu.Unlock()
	if existing == nil || existing.registrationID != registrationID {
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, s.takeoverProbe)
	defer cancel()
	// A canceled newcomer proves nothing about the existing connection.
	if existing.control.Ping(probe) == nil || ctx.Err() != nil {
		return nil
	}
	return existing
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

func (s *Server) connect(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	h := s.hosts[id]
	if h == nil || !authorized(r, h.token) {
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
	select {
	case h.open <- controlMessage{Type: "open", ID: p.id, Secret: p.secret, ClientIP: s.clientIP(r)}:
	default:
		return
	}
	timer := time.NewTimer(handshakeTimeout)
	defer timer.Stop()
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
