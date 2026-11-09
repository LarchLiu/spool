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
	// Only these proxy peers may supply X-Forwarded-For client addresses.
	TrustedProxies []string
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
}
type relayPair struct {
	id, secret string
	host       *registeredHost
	ctx        context.Context
	cancel     context.CancelFunc
	data       chan *websocket.Conn
	done       chan struct{}
	claimed    bool // guarded by Server.mu
}

func NewServer(opts ServerOptions) (*Server, error) {
	if len(opts.AdminToken) < 32 || len(opts.AdminToken) > 512 || strings.ContainsAny(opts.AdminToken, "\r\n") {
		return nil, errors.New("SPOOL_RELAY_ADMIN_TOKEN must contain 32–512 characters")
	}
	if strings.TrimSpace(opts.DataDir) == "" {
		return nil, errors.New("relay data directory is required")
	}
	opts.TrustedProxies = append([]string(nil), opts.TrustedProxies...)
	for _, value := range opts.TrustedProxies {
		if strings.TrimSpace(value) == "" || strings.Contains(value, ",") {
			return nil, errors.New("trusted proxies must be IP CIDR ranges")
		}
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
	s := &Server{opts: opts, hosts: make(map[string]*registeredHost), pending: make(map[string]*relayPair), registrations: make(map[string]registrationRecord), tokenIndex: make(map[string]string)}
	var err error
	s.trustedProxies, err = ParseTrustedProxies(strings.Join(opts.TrustedProxies, ","))
	if err != nil {
		return nil, err
	}
	if err := s.loadRegistrations(); err != nil {
		return nil, err
	}
	return s, nil
}

func authorized(r *http.Request, token string) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, "/web/") {
		s.serveWeb(w, r)
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
	full := s.closed || (!registrationOnly && len(s.hosts) >= s.opts.MaxHosts) || s.registering >= s.opts.MaxHosts
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
		!ed25519.Verify(m.PublicKey, namedRegistrationProof(challenge, m.AccessToken, m.WebOrigin, m.Name), m.Signature) {
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
	metadata := registrationMetadata{name: name, ip: s.clientIP(r), enrollment: registrationOnly}
	h := &registeredHost{id: ServerID(m.PublicKey), token: m.AccessToken, publicKey: append(ed25519.PublicKey(nil), m.PublicKey...), webOrigin: m.WebOrigin, registrationID: registration.ID, control: c, ctx: ctx, cancel: cancel, open: make(chan controlMessage, 32), pairs: make(map[string]*relayPair)}
	s.mu.Lock()
	current, allowed := s.registrationForRequestLocked(r)
	if !withoutToken && (!allowed || current.ID != registration.ID) {
		s.mu.Unlock()
		writeControl(ctx, c, controlMessage{Type: "registration_rejected"})
		return
	}
	if s.closed || (!registrationOnly && len(s.hosts) >= s.opts.MaxHosts) || s.hosts[h.id] != nil && (!registrationOnly || withoutToken) {
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
	if registrationOnly {
		s.mu.Unlock()
		writeControl(ctx, c, controlMessage{Type: "ready", RegistrationToken: issuedToken})
		return
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
	}()
	webURL := ""
	if h.webOrigin != "" {
		webURL = h.webOrigin + WebPath(h.id)
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
func (s *Server) connect(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	h := s.hosts[id]
	if h == nil || !authorized(r, h.token) {
		s.mu.Unlock()
		http.Error(w, "server unavailable", 404)
		return
	}
	if s.closed || h.ctx.Err() != nil || s.connections >= s.opts.MaxConnections || len(h.pairs) >= s.opts.MaxConnectionsPerHost {
		s.mu.Unlock()
		http.Error(w, "relay busy", 503)
		return
	}
	ctx, cancel := context.WithCancel(h.ctx)
	p := &relayPair{id: randomID(), secret: randomID(), host: h, ctx: ctx, cancel: cancel, data: make(chan *websocket.Conn, 1), done: make(chan struct{})}
	s.pending[p.id] = p
	h.pairs[p.id] = p
	s.connections++
	s.mu.Unlock()
	defer func() {
		cancel()
		close(p.done)
		s.mu.Lock()
		delete(s.pending, p.id)
		delete(h.pairs, p.id)
		s.connections--
		s.mu.Unlock()
	}()
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
	defer s.mu.Unlock()
	s.closed = true
	for _, h := range s.hosts {
		h.cancel()
		h.control.CloseNow()
		for _, p := range h.pairs {
			p.cancel()
		}
	}
	return nil
}
