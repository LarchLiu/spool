// Package connectproxy tunnels HTTPS without terminating TLS or rewriting origins.
package connectproxy

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type DialFunc func(context.Context, string, string) (net.Conn, error)

type Proxy struct {
	Address  string
	Password string
	Realm    string
	target   string
	dial     DialFunc
	auth     string
	server   *http.Server
	ctx      context.Context
	cancel   context.CancelFunc
	slots    chan struct{}
	mu       sync.Mutex
	closed   bool
	active   sync.WaitGroup
}

const Username = "spool"

func Start(target string, dial DialFunc) (*Proxy, error) {
	if _, _, err := net.SplitHostPort(target); err != nil {
		return nil, fmt.Errorf("invalid proxy target: %w", err)
	}
	if dial == nil {
		return nil, errors.New("missing proxy dialer")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Proxy{
		Address: listener.Addr().String(), Password: base64.RawURLEncoding.EncodeToString(secret),
		Realm:  "Spool-" + base64.RawURLEncoding.EncodeToString(secret[:9]),
		target: target, dial: dial, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 32),
	}
	p.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(Username+":"+p.Password))
	p.server = &http.Server{
		Handler: p, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	go p.server.Serve(listener) // Closing the owned listener is the only expected exit.
	return p, nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		http.Error(w, "proxy stopped", http.StatusServiceUnavailable)
		return
	}
	p.active.Add(1)
	p.mu.Unlock()
	defer p.active.Done()
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(p.auth)) != 1 {
		w.Header().Set("Proxy-Authenticate", `Basic realm="`+p.Realm+`"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	// No forwarding of ordinary HTTP, arbitrary ports, or other tailnet services.
	if r.Method != http.MethodConnect || !strings.EqualFold(r.Host, p.target) ||
		!strings.EqualFold(r.URL.Host, p.target) || r.URL.Path != "" || r.ContentLength > 0 {
		http.Error(w, "target not allowed", http.StatusForbidden)
		return
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		http.Error(w, "too many tunnels", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()
	defer cancel()
	upstream, err := p.dial(ctx, "tcp", p.target)
	if err != nil {
		http.Error(w, "tailnet connection failed", http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "tunneling unavailable", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	// http.Server.Close does not own hijacked sockets; cancellation must close both.
	stopTunnel := context.AfterFunc(p.ctx, func() { client.Close(); upstream.Close() })
	defer stopTunnel()
	client.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buffered.Flush(); err != nil {
		return
	}
	client.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() { io.Copy(upstream, buffered); done <- struct{}{} }()
	go func() { io.Copy(client, upstream); done <- struct{}{} }()
	<-done
	client.Close()
	upstream.Close()
	<-done
}

func (p *Proxy) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.cancel()
	err := p.server.Close()
	p.mu.Unlock()
	p.active.Wait()
	return err
}
