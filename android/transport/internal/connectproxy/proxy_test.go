package connectproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func auth(p *Proxy) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(Username+":"+p.Password))
}

func newProxy(t *testing.T, dial DialFunc) *Proxy {
	t.Helper()
	p, err := Start("spool.example.ts.net:443", dial)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestRejectsUnauthorizedAndOtherTargets(t *testing.T) {
	var dials atomic.Int32
	p := newProxy(t, func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("unexpected dial")
	})
	for _, test := range []struct {
		method, target, credential string
		code                       int
	}{
		{"CONNECT", "spool.example.ts.net:443", "", 407},
		{"CONNECT", "spool.example.ts.net:443", "Basic wrong", 407},
		{"CONNECT", "other.example.ts.net:443", auth(p), 403},
		{"CONNECT", "spool.example.ts.net:22", auth(p), 403},
		{"CONNECT", "127.0.0.1:443", auth(p), 403},
		{"GET", "https://spool.example.ts.net/", auth(p), 403},
	} {
		t.Run(test.method+test.target+test.credential, func(t *testing.T) {
			conn, err := net.DialTimeout("tcp", p.Address, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			fmt.Fprintf(conn, "%s %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\nConnection: close\r\n\r\n",
				test.method, test.target, "spool.example.ts.net:443", test.credential)
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != test.code {
				t.Fatalf("status = %d, want %d", resp.StatusCode, test.code)
			}
			if test.code == 407 && resp.Header.Get("Proxy-Authenticate") != `Basic realm="`+p.Realm+`"` {
				t.Fatal("missing authentication challenge")
			}
		})
	}
	if dials.Load() != 0 {
		t.Fatal("rejected requests reached tailnet")
	}
}

func TestTunnelPreservesTLSCookiesAndOrigin(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "spool.example.ts.net" || r.Header.Get("Origin") != "https://spool.example.ts.net" ||
			r.Header.Get("Cookie") != "spool_session=fixture" || r.Header.Get("Proxy-Authorization") != "" {
			t.Errorf("tunnel changed request metadata")
		}
		w.Header().Set("Set-Cookie", "spool_session=fixture; Secure; HttpOnly; SameSite=Strict")
		w.Write([]byte("data: streamed\n\n"))
	}))
	t.Cleanup(upstream.Close)
	p := newProxy(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "spool.example.ts.net:443" {
			t.Errorf("unexpected dial %s %s", network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	proxyURL, _ := url.Parse("http://" + p.Address)
	proxyURL.User = url.UserPassword(Username, p.Password)
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		// Trust only the test server's certificate; production never overrides TLS validation.
		TLSClientConfig: &tls.Config{RootCAs: upstream.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs},
	}
	// httptest's certificate is for example.com, not the tailnet fixture name.
	transport.TLSClientConfig.ServerName = "example.com"
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", "https://spool.example.ts.net/api/events", nil)
	req.Header.Set("Origin", "https://spool.example.ts.net")
	req.Header.Set("Cookie", "spool_session=fixture")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "data: streamed\n\n" || !strings.Contains(resp.Header.Get("Set-Cookie"), "Secure") {
		t.Fatalf("unexpected response: %q, %v", body, err)
	}
}

func TestBufferedTunnelAndShutdown(t *testing.T) {
	upstream, echo := net.Pipe()
	echoDone := make(chan struct{})
	go func() { defer close(echoDone); defer echo.Close(); io.Copy(echo, echo) }()
	p := newProxy(t, func(context.Context, string, string) (net.Conn, error) { return upstream, nil })
	conn, err := net.DialTimeout("tcp", p.Address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	// A TLS ClientHello can already be buffered behind the CONNECT request.
	fmt.Fprintf(conn, "CONNECT spool.example.ts.net:443 HTTP/1.1\r\nHost: spool.example.ts.net:443\r\nProxy-Authorization: %s\r\n\r\nhello", auth(p))
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("CONNECT: %v, %v", resp, err)
	}
	data := make([]byte, 5)
	if _, err := io.ReadFull(reader, data); err != nil || string(data) != "hello" {
		t.Fatalf("buffered data lost: %q, %v", data, err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("hijacked tunnel survived Close")
	}
	select {
	case <-echoDone:
	case <-time.After(time.Second):
		t.Fatal("upstream socket leaked")
	}
}

func TestCloseCancelsPendingDial(t *testing.T) {
	started := make(chan struct{})
	p := newProxy(t, func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	conn, err := net.DialTimeout("tcp", p.Address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "CONNECT spool.example.ts.net:443 HTTP/1.1\r\nHost: spool.example.ts.net:443\r\nProxy-Authorization: %s\r\n\r\n", auth(p))
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("dial did not start")
	}
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not cancel pending dial")
	}
}

func TestWebSocketUpgradeAndBidirectionalFrames(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		kind, message, err := conn.Read(ctx)
		if err == nil {
			conn.Write(ctx, kind, message)
		}
	}))
	t.Cleanup(upstream.Close)
	p := newProxy(t, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	proxyURL, _ := url.Parse("http://" + p.Address)
	proxyURL.User = url.UserPassword(Username, p.Password)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{
		RootCAs: upstream.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, ServerName: "example.com",
	}}
	t.Cleanup(transport.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "wss://spool.example.ts.net/api/events", &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport}, HTTPHeader: http.Header{"Origin": {"https://spool.example.ts.net"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","csrf":"fixture"}`)); err != nil {
		t.Fatal(err)
	}
	kind, data, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageText || string(data) != `{"type":"hello","csrf":"fixture"}` {
		t.Fatalf("WebSocket frame lost: %s %v", data, err)
	}
}

func TestSSEDoesNotWaitForResponseToFinish(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() { close(release) })
	p := newProxy(t, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	proxyURL, _ := url.Parse("http://" + p.Address)
	proxyURL.User = url.UserPassword(Username, p.Password)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{
		RootCAs: upstream.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, ServerName: "example.com",
	}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	resp, err := client.Get("https://spool.example.ts.net/api/chats/fixture/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("SSE buffered until completion: %q %v", line, err)
	}
}
