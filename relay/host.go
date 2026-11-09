package relay

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

var ErrRegistrationRejected = errors.New("relay registration rejected")

// Host is a listener fed by outbound relay connections. Wrap it in a TLS
// listener using Identity.TLSConfig and serve the ordinary Spool HTTP handler.
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
}

func NewHost(ctx context.Context, url, registrationToken string, id *Identity, client *http.Client) (*Host, error) {
	if _, err := ParseRelayURL(url); err != nil {
		return nil, err
	}
	if id == nil || len(id.Seed) != ed25519.SeedSize || !validToken(id.AccessToken) {
		return nil, errors.New("invalid relay identity")
	}
	if registrationToken != "" && (len(registrationToken) < 32 || len(registrationToken) > 512 || strings.ContainsAny(registrationToken, "\r\n")) {
		return nil, errors.New("relay registration token must be empty or contain 32–512 characters")
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Host{URL: url, registrationToken: registrationToken, Identity: id, HTTPClient: client, ctx: ctx, cancel: cancel, accepted: make(chan net.Conn), connections: make(map[*ownedConn]struct{})}, nil
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

// Register verifies and persists enrollment without opening an ingress listener
// or replacing a running host. The returned credential must be stored locally.
func Register(ctx context.Context, url, token string, id *Identity, client *http.Client) (string, error) {
	h, err := NewHost(ctx, url, token, id, client)
	if err != nil {
		return "", err
	}
	defer h.Close()
	c, credential, err := h.handshake("/v1/register")
	if err != nil {
		return "", err
	}
	c.CloseNow()
	return credential, nil
}

func (h *Host) handshake(path string) (_ *websocket.Conn, credential string, result error) {
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
	if h.WebEnabled && path == "/v1/host" {
		u, _ := ParseRelayURL(h.URL)
		webOrigin = u.String()
	}
	if err := writeControl(authCtx, c, controlMessage{Type: "register", PublicKey: h.Identity.PublicKey(), AccessToken: h.Identity.AccessToken, WebOrigin: webOrigin,
		Signature: ed25519.Sign(h.Identity.PrivateKey(), registrationProof(m.Challenge, h.Identity.AccessToken, webOrigin))}); err != nil {
		return nil, "", err
	}
	m, err = readControl(authCtx, c)
	if err == nil && m.Type == "registration_rejected" {
		return nil, "", ErrRegistrationRejected
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
	if m.WebURL != "" && (webOrigin == "" || m.WebURL != webOrigin+WebPath(h.Identity.ID())) {
		return nil, "", errors.New("invalid relay Web address")
	}
	h.mu.Lock()
	h.webURL = m.WebURL
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
	// The relay bounds outstanding requests. Also bound local dial/handshake
	// work in case an operator runs a malicious or misconfigured relay.
	slots := make(chan struct{}, 128)
	for {
		m, err := readControl(ctx, c)
		if err != nil {
			return err
		}
		if m.Type != "open" || !validToken(m.ID) || !validToken(m.Secret) {
			return errors.New("invalid relay connection request")
		}
		select {
		case slots <- struct{}{}:
		default:
			continue
		}
		h.workers.Add(1)
		go func(m controlMessage) { defer h.workers.Done(); defer func() { <-slots }(); h.open(ctx, m) }(m)
	}
}

func (h *Host) open(ctx context.Context, m controlMessage) {
	dialCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	c, err := dialWebsocket(dialCtx, h.HTTPClient, h.URL, "/v1/data/"+m.ID, m.Secret)
	cancel()
	if err != nil {
		return
	}
	stream := &ownedConn{Conn: byteStream(ctx, c)}
	stream.release = func() { h.mu.Lock(); delete(h.connections, stream); h.mu.Unlock() }
	h.mu.Lock()
	if h.ctx.Err() != nil || len(h.connections) >= 128 {
		h.mu.Unlock()
		c.CloseNow()
		return
	}
	h.connections[stream] = struct{}{}
	h.mu.Unlock()
	select {
	case h.accepted <- stream:
	case <-ctx.Done():
		stream.Close()
	}
}

type ownedConn struct {
	net.Conn
	once    sync.Once
	release func()
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
