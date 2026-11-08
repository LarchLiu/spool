// Package spoolnet provides native Android HTTPS transport over the system network or embedded tsnet.
package spoolnet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

const maxResponseBytes = 16 << 20

type Client struct {
	mu               sync.Mutex
	server           *tsnet.Server
	local            *local.Client
	node             *nodeStatus
	origin           *url.URL
	http             *http.Client
	transport        *http.Transport
	closed           bool
	direct           bool
	ctx              context.Context
	cancel           context.CancelFunc
	responseActivity atomic.Pointer[time.Time]
}

// NewDirectClient connects to one HTTPS origin over the system network without
// starting or enrolling a Tailscale node. Sessions remain private and origin-scoped.
func NewDirectClient(dataDir, serverURL string) (*Client, error) {
	origin, authority, err := parseEndpoint(serverURL)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(dataDir) {
		return nil, errors.New("native state directory must be absolute")
	}
	ctx, cancel := context.WithCancel(context.Background())
	dialer := directDialer{authority: authority, lifetime: ctx, dialTCP: (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext}
	httpClient, transport := nativeHTTPClient(dialer.DialContext)
	httpClient.Jar, err = newSessionJar(dataDir, origin)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("prepare native session: %w", err)
	}
	return &Client{direct: true, origin: origin, ctx: ctx, cancel: cancel, transport: transport, http: httpClient}, nil
}

// NewClient starts one app-private userspace node. No WebView or local proxy is used.
func NewClient(dataDir, serverURL string) (*Client, error) {
	origin, authority, err := parseEndpoint(serverURL)
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
	server := &tsnet.Server{Dir: dataDir, Hostname: "spool-android", UserLogf: func(string, ...any) {}}
	if err := server.Start(); err != nil {
		server.Close()
		return nil, fmt.Errorf("start tsnet: %w", err)
	}
	lc, err := server.LocalClient()
	if err != nil {
		server.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Server.Dial can fall back to system DNS/dialing on a MagicDNS miss.
	// Use the pinned SDK's netstack-only TCP hook, even if a peer disappears.
	dialer := tailnetDialer{api: lc, authority: authority, lifetime: ctx, dialTCP: server.Sys().Dialer.Get().NetstackDialTCP}
	httpClient, transport := nativeHTTPClient(dialer.DialContext)
	httpClient.Jar, err = newSessionJar(dataDir, origin)
	if err != nil {
		cancel()
		server.Close()
		return nil, fmt.Errorf("prepare native session: %w", err)
	}
	state := newNodeStatus(ctx, func(ctx context.Context) (nodeStatusStream, error) {
		return lc.WatchIPNBus(ctx, ipn.NotifyInitialState)
	})
	return &Client{server: server, local: lc, node: state, origin: origin, ctx: ctx, cancel: cancel, transport: transport, http: httpClient}, nil
}

func nativeHTTPClient(dial func(context.Context, string, string) (net.Conn, error)) (*http.Client, *http.Transport) {
	// No HTTP proxy is inherited. Each dialer enforces the selected origin and
	// routing mode; TLS verifies the original URL hostname, not the dialed IP.
	transport := &http.Transport{DialContext: dial, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 25 * time.Second, IdleConnTimeout: 60 * time.Second}
	jar, _ := cookiejar.New(nil)
	return &http.Client{Transport: transport, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("redirects are not allowed")
	}}, transport
}

func (c *Client) ServerURL() string { return c.origin.String() }

func (c *Client) Status() (string, error) {
	if c.direct {
		return `{"state":"Running","authURL":""}`, nil
	}
	ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
	defer cancel()
	status, err := c.node.StatusWithoutPeers(ctx)
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
	if c.direct {
		return errors.New("Tailscale login is unavailable for direct HTTPS connections")
	}
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	return c.local.StartLoginInteractive(ctx)
}

// apiURL confines every native request, including encoded paths, to this server's API.
func (c *Client) apiURL(path string) (string, error) {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/api/") || strings.ContainsAny(u.Path, "\\\r\n") {
		return "", errors.New("only relative Spool API paths are allowed")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == ".." || part == "." {
			return "", errors.New("invalid API path")
		}
	}
	return c.origin.String() + u.String(), nil
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader, contentType, csrf, language string) (*http.Response, error) {
	address, err := c.apiURL(path)
	if err != nil {
		return nil, err
	}
	if method != http.MethodGet && method != http.MethodPost {
		return nil, errors.New("unsupported API method")
	}
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", c.origin.String())
	req.Header.Set("X-UI-Language", language)
	req.Header.Set("Accept", "application/json")
	if strings.HasPrefix(path, "/api/chats/") {
		req.Header.Set("X-Tool-Details", "lazy")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if method == http.MethodPost {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil || resp.StatusCode >= 500 {
		c.responseActivity.Store(nil)
	}
	return resp, err
}

type apiResponse struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	MediaType string `json:"mediaType,omitempty"`
	Base64    bool   `json:"base64,omitempty"`
}

func encodeResponse(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxResponseBytes {
		return "", errors.New("API response is too large")
	}
	mediaType := resp.Header.Get("Content-Type")
	result := apiResponse{Status: resp.StatusCode, Body: string(data), MediaType: mediaType}
	// JSON strings cannot preserve invalid UTF-8 in downloaded text files.
	if !utf8.Valid(data) || !strings.Contains(mediaType, "json") && !strings.HasPrefix(mediaType, "text/") {
		result.Body, result.Base64 = base64.StdEncoding.EncodeToString(data), true
	}
	encoded, err := json.Marshal(result)
	return string(encoded), err
}

// Request retains session cookies privately; credentials never cross the Go boundary.
func (c *Client) Request(method, path, body, csrf, language string) (string, error) {
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()
	contentType := ""
	if method == http.MethodPost {
		contentType = "application/json"
	}
	resp, err := c.request(ctx, method, path, strings.NewReader(body), contentType, csrf, language)
	if err != nil {
		return "", err
	}
	return c.encodeResponse(resp)
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if jar, ok := c.http.Jar.(*serverSessionJar); ok {
		jar.close()
	}
	c.cancel()
	if c.node != nil {
		<-c.node.done
	}
	c.transport.CloseIdleConnections()
	if c.server != nil {
		return c.server.Close()
	}
	return nil
}
