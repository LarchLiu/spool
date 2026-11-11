package relay

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	// Shorter than the home server's own idle timeout, so the gateway never
	// reuses a tunnel the home has already closed.
	webIdleTimeout = 30 * time.Second
	// Idle tunnels kept for one client address between requests.
	webIdleTunnels = 4
	// Client addresses with reusable tunnels per host. Further addresses still
	// work, with a tunnel per request.
	webClientLimit = 256
)

// webPool reuses tunnels between requests instead of opening one, with its
// outbound host connection and TLS handshake, for every browser request.
//
// A tunnel carries the client address that the home server's login limiter
// keys on, so requests from different addresses never share one. The pool
// belongs to a single host connection and ends with it.
type webPool struct {
	dial    func(ctx context.Context, clientIP string) (net.Conn, error)
	mu      sync.Mutex
	closed  bool
	clients map[string]*webClient
	swept   time.Time
}

type webClient struct {
	transport *http.Transport
	active    int
	used      time.Time
}

func newWebPool(dial func(ctx context.Context, clientIP string) (net.Conn, error)) *webPool {
	return &webPool{dial: dial, clients: make(map[string]*webClient)}
}

func (p *webPool) transport(clientIP string, reuse bool) *http.Transport {
	return &http.Transport{
		Proxy: nil, DisableKeepAlives: !reuse, DisableCompression: true,
		ResponseHeaderTimeout: 30 * time.Second, MaxResponseHeaderBytes: 1 << 20,
		MaxIdleConnsPerHost: webIdleTunnels, IdleConnTimeout: webIdleTimeout,
		DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return p.dial(ctx, clientIP)
		},
	}
}

// acquire returns the transport for one request and a function to call when
// that request, including a streamed or upgraded response, has finished.
func (p *webPool) acquire(clientIP string) (http.RoundTripper, func()) {
	now := time.Now()
	p.mu.Lock()
	var expired []*http.Transport
	if now.Sub(p.swept) >= webIdleTimeout || len(p.clients) >= webClientLimit {
		p.swept = now
		for ip, client := range p.clients {
			if client.active == 0 && now.Sub(client.used) >= webIdleTimeout {
				delete(p.clients, ip)
				expired = append(expired, client.transport)
			}
		}
	}
	client := p.clients[clientIP]
	if client == nil && !p.closed && len(p.clients) < webClientLimit {
		client = &webClient{transport: p.transport(clientIP, true)}
		p.clients[clientIP] = client
	}
	if client != nil {
		client.active++
	}
	p.mu.Unlock()
	for _, transport := range expired {
		transport.CloseIdleConnections()
	}
	if client == nil {
		transport := p.transport(clientIP, false)
		return transport, transport.CloseIdleConnections
	}
	return client.transport, func() {
		p.mu.Lock()
		client.active--
		client.used = time.Now()
		p.mu.Unlock()
	}
}

// closeIdle releases tunnels that are only held for reuse. A transport also
// stops keeping the tunnels of requests in flight until its next request, so
// a host at its limit does not immediately refill with idle ones. Closing a
// tunnel takes Server.mu, so the caller must not hold it.
func (p *webPool) closeIdle() {
	p.mu.Lock()
	transports := make([]*http.Transport, 0, len(p.clients))
	for _, client := range p.clients {
		transports = append(transports, client.transport)
	}
	p.mu.Unlock()
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}

// close ends reuse after the host connection is gone. Requests in flight are
// canceled through the host context. The caller must not hold Server.mu.
func (p *webPool) close() {
	p.mu.Lock()
	p.closed = true
	transports := make([]*http.Transport, 0, len(p.clients))
	for _, client := range p.clients {
		transports = append(transports, client.transport)
	}
	clear(p.clients)
	p.mu.Unlock()
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}
