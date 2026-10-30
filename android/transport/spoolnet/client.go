// Package spoolnet is the small gomobile boundary used by the Android shell.
package spoolnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"spool/android/transport/internal/connectproxy"
	"tailscale.com/client/local"
	"tailscale.com/tsnet"
)

type Client struct {
	mu     sync.Mutex
	server *tsnet.Server
	local  *local.Client
	proxy  *connectproxy.Proxy
	origin *url.URL
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
}

// NewClient starts one persistent userspace node. Call it on a worker thread.
// dataDir must be an app-private, non-backed-up directory, not a provider home.
func NewClient(dataDir, serverURL string) (*Client, error) {
	origin, target, err := parseEndpoint(serverURL)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(dataDir) {
		return nil, errors.New("tsnet state directory must be absolute")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	if err := prepareLogStorage(dataDir); err != nil {
		return nil, fmt.Errorf("prepare tsnet logs: %w", err)
	}
	server := &tsnet.Server{
		Dir: dataDir, Hostname: "spool-android",
		// Do not leak login URLs or node details into Android's shared log buffer.
		UserLogf: func(string, ...any) {},
	}
	if err := server.Start(); err != nil {
		server.Close()
		return nil, fmt.Errorf("start tsnet: %w", err)
	}
	lc, err := server.LocalClient()
	if err != nil {
		server.Close()
		return nil, err
	}
	proxy, err := connectproxy.Start(target, server.Dial)
	if err != nil {
		server.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{server: server, local: lc, proxy: proxy, origin: origin, ctx: ctx, cancel: cancel}, nil
}

func (c *Client) ServerURL() string     { return c.origin.String() }
func (c *Client) ProxyAddress() string  { return c.proxy.Address }
func (c *Client) ProxyUsername() string { return connectproxy.Username }
func (c *Client) ProxyPassword() string { return c.proxy.Password }
func (c *Client) ProxyRealm() string    { return c.proxy.Realm }

// Status returns only the fields needed by the native connection page.
func (c *Client) Status() (string, error) {
	ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
	defer cancel()
	status, err := c.local.StatusWithoutPeers(ctx)
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(struct {
		State   string `json:"state"`
		AuthURL string `json:"authURL"`
	}{status.BackendState, status.AuthURL})
	return string(result), err
}

func (c *Client) Login() error {
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	return c.local.StartLoginInteractive(ctx)
}

// Probe checks TLS and Spool's unauthenticated health endpoint through tsnet.
func (c *Client) Probe() error {
	ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: c.server.Dial, TLSHandshakeTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("health check must not redirect")
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin.String()+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Spool HTTPS health check failed: %w", err)
	}
	defer resp.Body.Close()
	var health struct {
		OK bool `json:"ok"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&health) != nil || !health.OK {
		return fmt.Errorf("endpoint is not a healthy Spool server (HTTP %d)", resp.StatusCode)
	}
	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	c.cancel()
	return errors.Join(c.proxy.Close(), c.server.Close())
}
