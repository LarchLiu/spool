package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// Dialer selects a verified LAN endpoint first and otherwise creates an opaque
// relay stream. It never retries HTTP requests: only connection establishment
// is retried, before any application bytes have been written.
type Dialer struct {
	Pairing    Pairing
	HTTPClient *http.Client
	// Configure before dialing. Device and invitation transports use distinct
	// TLS names; the public Web gateway retains the original server name.
	TLSConfig   *tls.Config
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	lan         []string
	path        string
	connections map[*clientConn]struct{}
}

func NewDialer(ctx context.Context, p Pairing, client *http.Client) (*Dialer, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Dialer{Pairing: p, HTTPClient: client, ctx: ctx, cancel: cancel, lan: append([]string(nil), p.LAN...), connections: make(map[*clientConn]struct{})}, nil
}

func (d *Dialer) SetLANAddresses(addresses []string) error {
	if len(addresses) > 16 {
		return errors.New("too many LAN candidates")
	}
	for _, address := range addresses {
		if !ValidLANAddress(address) {
			return errors.New("invalid LAN candidate")
		}
	}
	d.mu.Lock()
	d.lan = append([]string(nil), addresses...)
	d.mu.Unlock()
	return nil
}
func (d *Dialer) Path() string { d.mu.Lock(); defer d.mu.Unlock(); return d.path }

func (d *Dialer) DialTLSContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" || address != net.JoinHostPort(d.Pairing.ID()+".spool.invalid", "443") {
		return nil, errors.New("connection outside paired Spool server")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	stop := context.AfterFunc(d.ctx, cancel)
	defer stop()
	d.mu.Lock()
	addresses := append([]string(nil), d.lan...)
	d.mu.Unlock()
	if len(addresses) > 0 {
		lanCtx, lanCancel := context.WithTimeout(ctx, 1200*time.Millisecond)
		conn := d.tryLAN(lanCtx, addresses)
		lanCancel()
		if conn != nil {
			return d.own(conn, "lan")
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	c, err := dialWebsocket(ctx, d.HTTPClient, d.Pairing.RelayURL, "/v1/connect/"+d.Pairing.ID(), d.Pairing.AccessToken)
	if err != nil {
		return nil, errors.New("Spool relay unavailable")
	}
	streamCtx, streamCancel := context.WithCancel(d.ctx)
	stream := byteStream(streamCtx, c)
	tlsConn := tls.Client(stream, d.tlsConfig())
	if err = tlsConn.HandshakeContext(ctx); err != nil {
		streamCancel()
		c.CloseNow()
		return nil, err
	}
	go keepAlive(streamCtx, c)
	return d.own(&cancelConn{Conn: tlsConn, cancel: streamCancel}, "relay")
}
func (d *Dialer) tryLAN(ctx context.Context, addresses []string) net.Conn {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan net.Conn)
	var workers sync.WaitGroup
	for _, address := range addresses {
		workers.Add(1)
		go func(address string) {
			defer workers.Done()
			conn, err := (&tls.Dialer{NetDialer: &net.Dialer{KeepAlive: 30 * time.Second}, Config: d.tlsConfig()}).DialContext(ctx, "tcp", address)
			if err != nil {
				return
			}
			select {
			case result <- conn:
			case <-ctx.Done():
				conn.Close()
			}
		}(address)
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case c := <-result:
		return c
	case <-done:
		return nil
	case <-ctx.Done():
		return nil
	}
}

func (d *Dialer) tlsConfig() *tls.Config {
	if d.TLSConfig != nil {
		return d.TLSConfig
	}
	return d.Pairing.TLSConfig()
}

type cancelConn struct {
	net.Conn
	cancel context.CancelFunc
}

func (c *cancelConn) Close() error { c.cancel(); return c.Conn.Close() }

type clientConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *clientConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
func (d *Dialer) own(conn net.Conn, path string) (net.Conn, error) {
	c := &clientConn{Conn: conn}
	c.release = func() { d.mu.Lock(); delete(d.connections, c); d.mu.Unlock() }
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx.Err() != nil {
		conn.Close()
		return nil, d.ctx.Err()
	}
	d.path = path
	d.connections[c] = struct{}{}
	return c, nil
}

// NetworkChanged retires existing transports without replaying any request.
// SSE/workspace consumers resubscribe through their existing recovery logic;
// mutations and uploads can fail, and desktop views require a new explicit open.
func (d *Dialer) NetworkChanged() {
	d.mu.Lock()
	connections := make([]*clientConn, 0, len(d.connections))
	for c := range d.connections {
		connections = append(connections, c)
	}
	d.path = ""
	d.mu.Unlock()
	for _, c := range connections {
		c.Close()
	}
}
func (d *Dialer) Close() error { d.cancel(); d.NetworkChanged(); return nil }
