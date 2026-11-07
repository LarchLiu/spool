package spoolnet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDirectClientNeedsNoNodeAndRetainsOnlyItsPrivateSession(t *testing.T) {
	directory := t.TempDir()
	client, err := NewDirectClient(directory, "https://chat.example.com:8443/")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	if !client.direct || client.server != nil || client.local != nil || client.transport.Proxy != nil || client.ServerURL() != "https://chat.example.com:8443" {
		t.Fatal("direct connection started a node or changed origin")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "sessions" {
		t.Fatalf("direct mode wrote node state: %v, %v", entries, err)
	}
	if err := client.Login(); err == nil {
		t.Fatal("direct mode offered Tailscale enrollment")
	}
	status, err := client.Status()
	if err != nil || !strings.Contains(status, `"state":"Running"`) {
		t.Fatal(status, err)
	}
	info, err := client.ConnectionInfo()
	if err != nil || !strings.Contains(info, `"path":"https"`) || strings.Contains(info, "targetIP") {
		t.Fatal(info, err)
	}
	client.http.Jar.SetCookies(client.origin, []*http.Cookie{{Name: "spool_session", Value: "saved", Path: "/", Secure: true, HttpOnly: true, MaxAge: 3600}})
	client.NetworkChanged()
	if err := client.Reconnect(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	client.NetworkChanged()
	if err := client.Reconnect(); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed direct connection recovered: %v", err)
	}
	restored, err := NewDirectClient(directory, client.ServerURL())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if cookies := restored.http.Jar.Cookies(restored.origin); len(cookies) != 1 || cookies[0].Value != "saved" {
		t.Fatal("session did not survive reconnect/restart", cookies)
	}
	other, err := NewDirectClient(directory, "https://other.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if len(other.http.Jar.Cookies(other.origin)) != 0 {
		t.Fatal("session crossed origins")
	}
	for _, address := range []string{"http://chat.example.com", "https://user:password@chat.example.com", "https://chat.example.com/path"} {
		if c, err := NewDirectClient(filepath.Join(directory, "invalid"), address); err == nil {
			c.Close()
			t.Fatalf("accepted %s", address)
		}
	}
	if c, err := NewDirectClient("relative", "https://chat.example.com"); err == nil {
		c.Close()
		t.Fatal("accepted relative session storage")
	}
}

func TestDirectDialerConfinesAuthorityAndBoundsCancellation(t *testing.T) {
	lifetime, stop := context.WithCancel(context.Background())
	defer stop()
	called := false
	dialer := directDialer{authority: "chat.example.com:8443", lifetime: lifetime, dialTCP: func(ctx context.Context, network, address string) (net.Conn, error) {
		called = true
		if network != "tcp" || address != "chat.example.com:8443" {
			t.Fatalf("unexpected destination %s %s", network, address)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 20*time.Second {
			t.Fatal("unbounded direct dial")
		}
		stop()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("client close did not cancel direct dial")
		}
		return nil, ctx.Err()
	}}
	for _, target := range []struct{ network, address string }{{"udp", dialer.authority}, {"tcp", "attacker.example:8443"}, {"tcp", "chat.example.com:443"}} {
		if _, err := dialer.DialContext(context.Background(), target.network, target.address); err == nil || called {
			t.Fatal("invalid destination reached the network", target)
		}
	}
	if _, err := dialer.DialContext(context.Background(), "tcp", "CHAT.EXAMPLE.COM:8443"); !errors.Is(err, context.Canceled) || !called {
		t.Fatal("direct dial failed to honor lifetime", err)
	}
	called = false
	if _, err := dialer.DialContext(context.Background(), "tcp", dialer.authority); !errors.Is(err, context.Canceled) || called {
		t.Fatal("closed client dialed", err)
	}
}

func TestDirectHTTPSLoginHealthSSEAndWorkspaceShareTheSession(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			io.WriteString(w, `{"ok":true}`)
			return
		}
		if r.TLS == nil || r.TLS.ServerName != "example.com" || r.Host != "example.com" || r.Header.Get("Origin") != "https://example.com" {
			t.Error("direct connection lost TLS/SNI or exact origin")
		}
		if r.URL.Path == "/api/login" {
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "direct-session", Path: "/", Secure: true, HttpOnly: true, MaxAge: 3600})
			io.WriteString(w, `{}`)
			return
		}
		cookie, err := r.Cookie("spool_session")
		if err != nil || cookie.Value != "direct-session" {
			t.Error("direct stream or API lost its login", err)
		}
		switch r.URL.Path {
		case "/api/preferences":
			if r.Header.Get("X-CSRF-Token") != "direct-csrf" {
				t.Error("direct mutation lost CSRF")
			}
			io.WriteString(w, `{}`)
		case "/api/chats/chat/events":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: snapshot\ndata: {\"id\":\"chat\"}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/api/events":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.CloseNow()
			_, data, err := conn.Read(r.Context())
			if err != nil || !strings.Contains(string(data), `"csrf":"direct-csrf"`) {
				t.Error("direct workspace lost CSRF hello", err)
				return
			}
			conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"ready","data":true}`))
			conn.Read(r.Context())
		case "/api/redirect":
			http.Redirect(w, r, "https://attacker.example/api/me", http.StatusFound)
		default:
			t.Error("unexpected path", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewDirectClient(t.TempDir(), "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	// Replace only DNS/destination resolution with an isolated TLS fixture. Use
	// the production direct dial policy, HTTP client, cookie jar and stream code.
	dialer := directDialer{authority: "example.com:443", lifetime: client.ctx, dialTCP: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}
	client.transport.DialContext = dialer.DialContext
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client.transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	for _, path := range []string{"/api/login", "/api/preferences"} {
		if _, err := client.Request("POST", path, `{}`, "direct-csrf", "en"); err != nil {
			t.Fatal(err)
		}
	}
	health, err := client.Health()
	var info healthSnapshot
	if err != nil || json.Unmarshal([]byte(health), &info) != nil || info.HTTPS != "Verified" || info.Problem != "" || info.AuthURL != "" {
		t.Fatal("direct health required a node", health, err)
	}
	events := make(eventRecorder, 8)
	chat, err := client.SubscribeChat("chat", "en", events)
	if err != nil {
		t.Fatal(err)
	}
	defer chat.Close()
	select {
	case event := <-events:
		if event[0] != "snapshot" {
			t.Fatal(event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("direct SSE did not connect")
	}
	workspace, err := client.SubscribeWorkspace("direct-csrf", "en", events)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	select {
	case event := <-events:
		if event[0] != "ready" {
			t.Fatal(event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("direct workspace did not connect")
	}
	if _, err := client.Request("GET", "/api/redirect", "", "", "en"); err == nil {
		t.Fatal("direct connection followed a redirect")
	}
	client.transport.CloseIdleConnections()
	client.transport.TLSClientConfig = &tls.Config{RootCAs: x509.NewCertPool()}
	health, err = client.Health()
	if err != nil || json.Unmarshal([]byte(health), &info) != nil || info.Problem != "endpoint" || info.HTTPS != "Failed" {
		t.Fatal("direct health accepted an untrusted certificate", health, err)
	}
}
