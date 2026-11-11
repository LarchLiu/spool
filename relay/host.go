package relay

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

var ErrRegistrationRejected = errors.New("relay registration rejected")

// A relay that does not announce its per-host limit uses this default.
const defaultHostConnections = 128

// The relay bounds outstanding connections. This cap also bounds local dial
// and handshake work if an operator runs a malicious or misconfigured relay.
const maxHostConnections = 1024

func hostConnectionLimit(announced int) int {
	if announced <= 0 {
		return defaultHostConnections
	}
	return min(announced, maxHostConnections)
}

// Host is a listener fed by outbound relay connections. Spool wraps it with
// Devices.Listener and Devices.Handler to enforce device and Web admission.
type Host struct {
	URL        string
	Identity   *Identity
	HTTPClient *http.Client
	OnStatus   func(string)
	// Set before Run. Opts into a public Web route for this host connection.
	WebEnabled bool
	OnWebURL   func(string)
	// Set before Run. Automatically issued credentials must be saved before
	// accepting traffic; returning an error causes enrollment to retry.
	OnRegistrationToken func(string) error
	// Called after every successful handshake, before accepting traffic.
	OnRegistered      func(string) error
	ctx               context.Context
	cancel            context.CancelFunc
	accepted          chan net.Conn
	workers           sync.WaitGroup
	mu                sync.Mutex
	registrationToken string
	webURL            string
	connections       map[*ownedConn]struct{}
	// Connections being opened or established, bounded by limit.
	reserved, limit int
	// The admission token in use and replaced ones still honored. They change
	// with SetAdmission while Identity keeps the values the host started with.
	access  string
	retired []RetiredToken
	// What the current control connection registered with, and whether its
	// relay applies admission changes without a new registration.
	sessionAccess  string
	sessionUpdates bool
	// Wakes the session after the admission tokens change.
	admissionChanged chan struct{}
}

func NewHost(ctx context.Context, url, registrationToken string, id *Identity, client *http.Client) (*Host, error) {
	if _, err := ParseRelayURL(url); err != nil {
		return nil, err
	}
	if id == nil || !id.valid() {
		return nil, errors.New("invalid relay identity")
	}
	if registrationToken != "" && (len(registrationToken) < 32 || len(registrationToken) > 512 || strings.ContainsAny(registrationToken, "\r\n")) {
		return nil, errors.New("relay registration token must be empty or contain 32–512 characters")
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Host{URL: url, registrationToken: registrationToken, Identity: id, HTTPClient: client, ctx: ctx, cancel: cancel, accepted: make(chan net.Conn), connections: make(map[*ownedConn]struct{}), limit: defaultHostConnections,
		access: id.AccessToken, retired: id.RetiredTokens(time.Now()), admissionChanged: make(chan struct{}, 1)}, nil
}

// AccessToken is the admission token for new pairing codes and connections.
func (h *Host) AccessToken() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.access
}

// SetAdmission applies a rotated identity. Established connections continue;
// the relay learns the change on the existing control connection.
func (h *Host) SetAdmission(id *Identity) {
	h.mu.Lock()
	h.access, h.retired = id.AccessToken, id.RetiredTokens(time.Now())
	h.mu.Unlock()
	h.notifyAdmission()
}

// ExpireAdmission stops honoring retired tokens whose grace period has ended.
func (h *Host) ExpireAdmission(now time.Time) {
	h.mu.Lock()
	current := h.retired[:0:0]
	for _, retired := range h.retired {
		if now.Unix() < retired.ExpiresAt {
			current = append(current, retired)
		}
	}
	changed := len(current) != len(h.retired)
	h.retired = current
	h.mu.Unlock()
	if changed {
		h.notifyAdmission()
	}
}

func (h *Host) notifyAdmission() {
	select {
	case h.admissionChanged <- struct{}{}:
	default:
	}
}

func (h *Host) admissionTokens() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	tokens := []string{h.access}
	for _, retired := range h.retired {
		tokens = append(tokens, retired.Token)
	}
	return tokens
}

// maintainAdmission keeps one control connection's relay informed of the
// tokens this host honors, starting from what the handshake registered.
func (h *Host) maintainAdmission(ctx context.Context, c *websocket.Conn) {
	h.mu.Lock()
	known, updates := []string{h.sessionAccess}, h.sessionUpdates
	h.mu.Unlock()
	for {
		tokens := h.admissionTokens()
		switch {
		case slices.Equal(tokens, known):
		case updates:
			if writeControl(ctx, c, controlMessage{Type: "admission", Tokens: tokens}) != nil {
				c.CloseNow()
				return
			}
		case tokens[0] != known[0]:
			// An older relay only learns the admission token at registration,
			// and cannot honor retired ones. Register again with the new token.
			c.CloseNow()
			return
		}
		known = tokens
		select {
		case <-ctx.Done():
			return
		case <-h.admissionChanged:
		}
	}
}

func (h *Host) RegistrationToken() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.registrationToken
}

func (h *Host) Run() {
	backoff := time.Second
	for h.ctx.Err() == nil {
		start := time.Now()
		err := h.session()
		if h.ctx.Err() != nil {
			break
		}
		if h.OnStatus != nil {
			if errors.Is(err, ErrRegistrationRejected) {
				h.OnStatus("registration_required")
			} else {
				h.OnStatus("reconnecting")
			}
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-h.ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
	h.Close()
}

// Register verifies and persists enrollment without opening an ingress listener.
// A new token retires this identity's previous credential and disconnects its
// host; retrying the same token preserves it. Store the returned credential.
func Register(ctx context.Context, url, token string, id *Identity, client *http.Client) (string, error) {
	return RegisterNamed(ctx, url, token, "", id, client)
}

// RegisterNamed optionally labels the server in the relay's management records.
// An empty name preserves the existing label or uses an automatic default.
func RegisterNamed(ctx context.Context, url, token, name string, id *Identity, client *http.Client) (string, error) {
	name = strings.TrimSpace(name)
	if name != "" && !validRegistrationName(name) {
		return "", ErrInvalidRegistration
	}
	h, err := NewHost(ctx, url, token, id, client)
	if err != nil {
		return "", err
	}
	defer h.Close()
	c, credential, err := h.handshakeNamed("/v1/register", name)
	if err != nil {
		return "", err
	}
	c.CloseNow()
	return credential, nil
}

func (h *Host) handshake(path string) (*websocket.Conn, string, error) {
	return h.handshakeNamed(path, "")
}

func (h *Host) handshakeNamed(path, name string) (_ *websocket.Conn, credential string, result error) {
	dialCtx, cancel := context.WithTimeout(h.ctx, handshakeTimeout)
	token := h.RegistrationToken()
	c, err := dialWebsocket(dialCtx, h.HTTPClient, h.URL, path, token)
	cancel()
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if result != nil {
			c.CloseNow()
		}
	}()
	authCtx, authCancel := context.WithTimeout(h.ctx, handshakeTimeout)
	defer authCancel()
	m, err := readControl(authCtx, c)
	if err != nil || m.Type != "challenge" || !validToken(m.Challenge) {
		return nil, "", fmt.Errorf("relay challenge failed: %w", errors.Join(err, errors.New("invalid registration challenge")))
	}
	webOrigin := ""
	webVersion := 0
	if h.WebEnabled && path == "/v1/host" {
		u, _ := ParseRelayURL(h.URL)
		webOrigin = u.String()
		webVersion = 2
	}
	access := h.AccessToken()
	if err := writeControl(authCtx, c, controlMessage{Type: "register", PublicKey: h.Identity.PublicKey(), AccessToken: access, WebOrigin: webOrigin, WebVersion: webVersion, Name: name,
		Signature: ed25519.Sign(h.Identity.PrivateKey(), hostRegistrationProof(m.Challenge, access, webOrigin, name, webVersion))}); err != nil {
		return nil, "", err
	}
	m, err = readControl(authCtx, c)
	if err == nil && m.Type == "registration_rejected" {
		return nil, "", ErrRegistrationRejected
	}
	if err == nil && m.Type == "registration_limited" {
		return nil, "", ErrRegistrationLimited
	}
	if err == nil && m.Type == "invalid_registration" {
		return nil, "", ErrInvalidRegistration
	}
	if err != nil || m.Type != "ready" {
		return nil, "", fmt.Errorf("relay did not confirm registration: %w", errors.Join(err, errors.New("registration failed")))
	}
	if m.RegistrationToken != "" {
		if token != "" || !validToken(m.RegistrationToken) {
			return nil, "", errors.New("invalid issued registration token")
		}
		token = m.RegistrationToken
	}
	if token == "" {
		return nil, "", errors.New("relay did not issue a registration token")
	}
	if m.WebURL != "" && (webOrigin == "" || !ValidateWebURL(m.WebURL, h.Identity.ID())) {
		return nil, "", errors.New("invalid relay Web address")
	}
	h.mu.Lock()
	h.webURL = m.WebURL
	h.limit = hostConnectionLimit(m.MaxConnections)
	h.sessionAccess, h.sessionUpdates = access, m.AdmissionUpdates
	h.mu.Unlock()
	return c, token, nil
}

func (h *Host) session() error {
	c, token, err := h.handshake("/v1/host")
	if err != nil {
		return err
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	if token != h.RegistrationToken() {
		if h.OnRegistrationToken != nil {
			if err := h.OnRegistrationToken(token); err != nil {
				return err
			}
		}
		h.mu.Lock()
		h.registrationToken = token
		h.mu.Unlock()
	}
	if h.OnRegistered != nil {
		if err := h.OnRegistered(token); err != nil {
			return err
		}
	}
	if h.OnStatus != nil {
		h.OnStatus("connected")
	}
	if h.OnWebURL != nil {
		h.mu.Lock()
		webURL := h.webURL
		h.mu.Unlock()
		h.OnWebURL(webURL)
	}
	go keepAlive(ctx, c)
	h.workers.Add(1)
	go func() { defer h.workers.Done(); h.maintainAdmission(ctx, c) }()
	for {
		m, err := readControl(ctx, c)
		if err != nil {
			return err
		}
		if m.Type != "open" || !validToken(m.ID) || !validToken(m.Secret) {
			return errors.New("invalid relay connection request")
		}
		if !h.reserve() {
			// Refuse now; otherwise the client waits for the relay's join timeout.
			if err := writeControl(ctx, c, controlMessage{Type: "reject", ID: m.ID}); err != nil {
				return err
			}
			continue
		}
		h.workers.Add(1)
		go func(m controlMessage) { defer h.workers.Done(); h.open(ctx, m) }(m)
	}
}

// reserve admits one connection before any dial or handshake work starts.
func (h *Host) reserve() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ctx.Err() != nil || h.reserved >= h.limit {
		return false
	}
	h.reserved++
	return true
}

// open joins one reserved connection. The reservation is returned when the
// join fails or, once established, when the stream closes.
func (h *Host) open(ctx context.Context, m controlMessage) {
	established := false
	defer func() {
		if !established {
			h.mu.Lock()
			h.reserved--
			h.mu.Unlock()
		}
	}()
	var remote net.Addr
	if m.ClientIP != "" {
		ip, err := netip.ParseAddr(m.ClientIP)
		if err != nil || ip.Zone() != "" {
			return
		}
		// The authenticated control channel supplies this address. HTTP headers
		// inside native or Web streams cannot change the login limiter's key.
		remote = relayAddr("relay-client/" + ip.Unmap().String())
	}
	dialCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	c, err := dialWebsocket(dialCtx, h.HTTPClient, h.URL, "/v1/data/"+m.ID, m.Secret)
	cancel()
	if err != nil {
		return
	}
	stream := &ownedConn{Conn: byteStream(ctx, c), remote: remote}
	stream.release = func() { h.mu.Lock(); delete(h.connections, stream); h.reserved--; h.mu.Unlock() }
	h.mu.Lock()
	if h.ctx.Err() != nil {
		h.mu.Unlock()
		c.CloseNow()
		return
	}
	h.connections[stream] = struct{}{}
	h.mu.Unlock()
	established = true
	select {
	case h.accepted <- stream:
	case <-ctx.Done():
		stream.Close()
	}
}

type ownedConn struct {
	net.Conn
	remote  net.Addr
	once    sync.Once
	release func()
}

func (c *ownedConn) RemoteAddr() net.Addr {
	if c.remote != nil {
		return c.remote
	}
	return c.Conn.RemoteAddr()
}

func (c *ownedConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
func (h *Host) Accept() (net.Conn, error) {
	select {
	case c := <-h.accepted:
		return c, nil
	case <-h.ctx.Done():
		return nil, net.ErrClosed
	}
}
func (h *Host) Addr() net.Addr { return relayAddr(h.Identity.ID()) }

type relayAddr string

func (a relayAddr) Network() string { return "relay" }
func (a relayAddr) String() string  { return string(a) }
func (h *Host) Close() error {
	h.cancel()
	h.mu.Lock()
	connections := make([]*ownedConn, 0, len(h.connections))
	for c := range h.connections {
		connections = append(connections, c)
	}
	h.mu.Unlock()
	for _, c := range connections {
		c.Close()
	}
	return nil
}

// Wait is called after Run has returned and Close has stopped accepted streams.
func (h *Host) Wait() { h.workers.Wait() }
