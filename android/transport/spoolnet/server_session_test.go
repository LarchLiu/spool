package spoolnet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"spool/relay"
)

func sessionOrigin(t *testing.T, address string) *url.URL {
	t.Helper()
	origin, _, err := parseEndpoint(address)
	if err != nil {
		t.Fatal(err)
	}
	return origin
}

func sessionCookie(value string) *http.Cookie {
	return &http.Cookie{Name: "spool_session", Value: value, Path: "/", Secure: true, HttpOnly: true, MaxAge: 3600}
}

func savedLegacyLogin(t *testing.T, root, mode, address, value string) {
	t.Helper()
	origin := sessionOrigin(t, address)
	jar, err := newSessionJar(filepath.Join(root, mode), origin)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(origin, []*http.Cookie{sessionCookie(value)})
}

func requireSession(t *testing.T, jar http.CookieJar, origin *url.URL, want string) {
	t.Helper()
	cookies := jar.Cookies(origin)
	if want == "" {
		if len(cookies) != 0 {
			t.Fatal("unexpected login was retained")
		}
	} else if len(cookies) != 1 || cookies[0].Name != "spool_session" || cookies[0].Value != want {
		t.Fatal("expected the connection's exact-origin login")
	}
}

func TestServerSessionMigratesEachRouteOnlyToItsOwnOrigin(t *testing.T) {
	direct, tail := "https://public.example.com", "https://private.example.ts.net"
	for _, selected := range []bool{true, false} {
		root := t.TempDir()
		savedLegacyLogin(t, root, "https", direct, "public-login")
		savedLegacyLogin(t, root, "tsnet", tail, "tailnet-login")
		address, mode, want := direct, "https", "public-login"
		if !selected {
			address, mode, want = tail, "tsnet", "tailnet-login"
		}
		jar, err := newServerSessionJar(filepath.Join(root, "session"), sessionOrigin(t, address), direct, tail, mode)
		if err != nil {
			t.Fatal(err)
		}
		requireSession(t, jar, sessionOrigin(t, address), want)
		jar.close()
		for _, next := range []string{direct, tail, direct} {
			mode, want = "https", "public-login"
			if next == tail {
				mode, want = "tsnet", "tailnet-login"
			}
			jar, err = newServerSessionJar(filepath.Join(root, "session"), sessionOrigin(t, next), direct, tail, mode)
			if err != nil {
				t.Fatal(err)
			}
			requireSession(t, jar, sessionOrigin(t, next), want)
			jar.close()
		}
	}
}

func TestServerSessionDoesNotMigrateAnotherRoutesLogin(t *testing.T) {
	root := t.TempDir()
	direct, tail := "https://public.example.com", "https://private.example.ts.net"
	savedLegacyLogin(t, root, "tsnet", tail, "existing-login")
	jar, err := newServerSessionJar(filepath.Join(root, "session"), sessionOrigin(t, direct), direct, tail, "https")
	if err != nil {
		t.Fatal(err)
	}
	requireSession(t, jar, sessionOrigin(t, direct), "")
	info, err := os.Stat(jar.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("profile session storage must remain private", err)
	}
}

func TestServerSessionDoesNotCrossProfilesOrEscapeTheCurrentExactHTTPSOrigin(t *testing.T) {
	direct, tail := "https://public.example.com", "https://private.example.ts.net"
	origin := sessionOrigin(t, direct)
	jar, err := newServerSessionJar(filepath.Join(t.TempDir(), "session"), origin, direct, tail, "https")
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(origin, []*http.Cookie{sessionCookie("private-login")})
	for _, address := range []string{tail, "http://public.example.com", "https://public.example.com:8443", "https://other.example.com"} {
		other, _ := url.Parse(address)
		requireSession(t, jar, other, "")
		jar.SetCookies(other, []*http.Cookie{sessionCookie("foreign-login")})
	}
	requireSession(t, jar, origin, "private-login")
	otherProfile, err := newServerSessionJar(filepath.Join(t.TempDir(), "session"), origin, direct, tail, "https")
	if err != nil {
		t.Fatal(err)
	}
	requireSession(t, otherProfile, origin, "")
	if _, err := newServerSessionJar("relative", origin, direct, tail, "https"); err == nil {
		t.Fatal("accepted relative private state directory")
	}
	if _, err := newServerSessionJar(filepath.Join(t.TempDir(), "session"), sessionOrigin(t, "https://foreign.example.com"), direct, tail, "https"); err == nil {
		t.Fatal("accepted a transport outside the configured server")
	}
}

func TestServerSessionIsIndependentOfRelayAdmission(t *testing.T) {
	id, err := relay.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	encode := func(version int, token string) string {
		data, err := json.Marshal(relay.Pairing{Version: version, PublicKey: id.PublicKey(), AccessToken: token, RelayURL: "https://relay.example.com"})
		if err != nil {
			t.Fatal(err)
		}
		return "spool-pair:" + base64.RawURLEncoding.EncodeToString(data)
	}
	for name, code := range map[string]string{
		"legacy":            encode(1, id.AccessToken),
		"invalid-admission": encode(2, "invalid-token"),
		"malformed":         "spool-pair:broken",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			direct, tail := "https://public.example.com", "https://private.example.ts.net"
			savedLegacyLogin(t, root, "https", direct, "existing-login")
			for _, address := range []string{direct, tail} {
				// Session binding depends only on the client's selected HTTPS
				// origin; no Tailscale node or live network is needed here.
				client, err := NewDirectClient(filepath.Join(root, "transport"), address)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { client.direct = true; client.Close() })
				client.direct = address == direct
				if err := client.UsePairedServerSession(filepath.Join(root, "session"), direct, tail, code); err != nil {
					t.Fatal("Relay metadata blocked an independent transport", err)
				}
				want := ""
				if address == direct {
					want = "existing-login"
				}
				requireSession(t, client.http.Jar, client.origin, want)
				requireSession(t, client.http.Jar, sessionOrigin(t, "https://foreign.example.com"), "")
				client.direct = true // Close the synthetic client without a tsnet node.
				client.Close()
			}
			if client, err := NewRelayClient(filepath.Join(root, "relay"), code); err == nil {
				client.Close()
				t.Fatal("profile session handling weakened Relay admission")
			}
		})
	}
}

func TestLegacyRelayIdentityDoesNotAuthorizeAnotherNetwork(t *testing.T) {
	id, err := relay.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(relay.Pairing{Version: 1, PublicKey: id.PublicKey(), AccessToken: id.AccessToken, RelayURL: "https://relay.example.com"})
	code := "spool-pair:" + base64.RawURLEncoding.EncodeToString(data)
	root := t.TempDir()
	origin := sessionOrigin(t, relay.Origin(id.ID()))
	previous, err := newServerSessionJar(filepath.Join(root, "session"), origin, "", "", "relay", origin.String())
	if err != nil {
		t.Fatal(err)
	}
	previous.SetCookies(origin, []*http.Cookie{sessionCookie("relay-login")})
	previous.close()
	client, err := NewDirectClient(filepath.Join(root, "https"), "https://public.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.UsePairedServerSession(filepath.Join(root, "session"), client.origin.String(), "", code); err != nil {
		t.Fatal(err)
	}
	requireSession(t, client.http.Jar, client.origin, "")
	client.Close()
	restored, err := newServerSessionJar(filepath.Join(root, "session"), origin, "https://public.example.com", "", "relay", origin.String())
	if err != nil {
		t.Fatal(err)
	}
	requireSession(t, restored, origin, "relay-login")
}

func TestServerSessionLogoutAndRetiredResponsesCannotReviveEitherLegacyLogin(t *testing.T) {
	root := t.TempDir()
	direct, tail := "https://public.example.com", "https://private.example.ts.net"
	savedLegacyLogin(t, root, "https", direct, "old-public-login")
	savedLegacyLogin(t, root, "tsnet", tail, "old-tailnet-login")
	old, err := newServerSessionJar(filepath.Join(root, "session"), sessionOrigin(t, direct), direct, tail, "https")
	if err != nil {
		t.Fatal(err)
	}
	old.close()
	current, err := newServerSessionJar(filepath.Join(root, "session"), sessionOrigin(t, tail), direct, tail, "tsnet")
	if err != nil {
		t.Fatal(err)
	}
	current.SetCookies(sessionOrigin(t, tail), []*http.Cookie{{Name: "spool_session", Path: "/", MaxAge: -1}})
	old.SetCookies(sessionOrigin(t, direct), []*http.Cookie{sessionCookie("late-login")})
	current.close()
	for _, address := range []string{direct, tail} {
		mode := "https"
		if address == tail {
			mode = "tsnet"
		}
		jar, err := newServerSessionJar(filepath.Join(root, "session"), sessionOrigin(t, address), direct, tail, mode)
		if err != nil {
			t.Fatal(err)
		}
		requireSession(t, jar, sessionOrigin(t, address), "")
	}
}

func TestServerSessionRejectsExpiredCookiesAndACompletelyReplacedServer(t *testing.T) {
	root := t.TempDir()
	direct, tail := "https://public.example.com", "https://private.example.ts.net"
	origin := sessionOrigin(t, direct)
	jar, err := newServerSessionJar(filepath.Join(root, "session"), origin, direct, tail, "https")
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []*http.Cookie{
		{Name: "spool_session", Value: "insecure", Path: "/", HttpOnly: true, MaxAge: 3600},
		{Name: "spool_session", Value: "readable", Path: "/", Secure: true, MaxAge: 3600},
		{Name: "spool_session", Value: "foreign-domain", Path: "/", Domain: "elsewhere.example.com", Secure: true, HttpOnly: true, MaxAge: 3600},
	} {
		jar.SetCookies(origin, []*http.Cookie{invalid})
		requireSession(t, jar, origin, "")
	}
	jar.SetCookies(origin, []*http.Cookie{sessionCookie("private-login")})
	jar.cookie.Expires = time.Now().Add(-time.Minute)
	if err := jar.persist(); err != nil {
		t.Fatal(err)
	}
	jar.close()
	jar, err = newServerSessionJar(filepath.Join(root, "session"), origin, direct, tail, "https")
	if err != nil {
		t.Fatal(err)
	}
	requireSession(t, jar, origin, "")
	jar.close()
	jar, err = newServerSessionJar(filepath.Join(root, "session"), sessionOrigin(t, tail), direct, tail, "tsnet")
	if err != nil {
		t.Fatal(err)
	}
	requireSession(t, jar, sessionOrigin(t, tail), "")
	jar.SetCookies(sessionOrigin(t, tail), []*http.Cookie{sessionCookie("fresh-login")})
	jar.close()
	changed := "https://replacement.example.com"
	jar, err = newServerSessionJar(filepath.Join(root, "session"), sessionOrigin(t, changed), changed, "", "https")
	if err != nil {
		t.Fatal(err)
	}
	requireSession(t, jar, sessionOrigin(t, changed), "")
}

func TestServerSessionIndependentLoginsWebSocketAndProfileLogout(t *testing.T) {
	direct, tail := "https://example.com", "https://example.com:8443"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "https://"+r.Host || r.TLS == nil || r.TLS.ServerName != "example.com" {
			t.Error("profile login changed exact HTTPS origin or TLS identity")
		}
		wantCookie := "public-login"
		if r.Host == "example.com:8443" {
			wantCookie = "tailnet-login"
		}
		if r.URL.Path == "/api/login" {
			http.SetCookie(w, sessionCookie(wantCookie))
			io.WriteString(w, `{"user":{"id":"alice"},"csrf":"shared-csrf"}`)
			return
		}
		cookie, err := r.Cookie("spool_session")
		if err != nil || cookie.Value != wantCookie {
			if err == nil {
				t.Error("another connection's cookie reached this HTTPS origin")
			}
			http.Error(w, "sign in required", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/me":
			io.WriteString(w, `{"user":{"id":"alice"},"csrf":"shared-csrf"}`)
		case "/api/preferences", "/api/logout":
			if r.Header.Get("X-CSRF-Token") != "shared-csrf" {
				t.Error("network handoff did not restore this route's CSRF")
			}
			if r.URL.Path == "/api/logout" {
				http.SetCookie(w, &http.Cookie{Name: "spool_session", Path: "/", MaxAge: -1})
			}
			io.WriteString(w, `{}`)
		case "/api/events":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.CloseNow()
			_, hello, err := conn.Read(r.Context())
			if err != nil || !strings.Contains(string(hello), `"csrf":"shared-csrf"`) {
				t.Error("WebSocket did not use this route's login")
				return
			}
			conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"ready","data":true}`))
			conn.Read(r.Context())
		default:
			t.Error("unexpected fixture path", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	open := func(address string, directMode bool) *Client {
		t.Helper()
		mode := "https"
		if !directMode {
			mode = "tsnet"
		}
		client, err := NewDirectClient(filepath.Join(root, mode), address)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { client.Close() })
		client.direct = directMode
		client.transport.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
			if target != client.origin.Hostname()+":443" && target != client.origin.Host {
				t.Error("request escaped the transport's authority")
			}
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		}
		roots := x509.NewCertPool()
		roots.AddCert(server.Certificate())
		client.transport.TLSClientConfig = &tls.Config{RootCAs: roots}
		if err := client.UseServerSession(filepath.Join(root, "session"), direct, tail); err != nil {
			t.Fatal(err)
		}
		return client
	}
	request := func(client *Client, method, path, wantBody string, wantStatus int) {
		t.Helper()
		raw, err := client.Request(method, path, `{}`, "shared-csrf", "en")
		var response apiResponse
		if err != nil || json.Unmarshal([]byte(raw), &response) != nil || response.Status != wantStatus || !strings.Contains(response.Body, wantBody) {
			t.Fatalf("request %s failed: status=%d error=%v", path, response.Status, err)
		}
	}
	public := open(direct, true)
	request(public, "POST", "/api/login", "alice", 200)
	public.Close()
	private := open(tail, false)
	request(private, "GET", "/api/me", "sign in required", 401)
	request(private, "POST", "/api/login", "alice", 200)
	request(private, "GET", "/api/me", "alice", 200)
	request(private, "POST", "/api/preferences", "{}", 200)
	events := make(eventRecorder, 4)
	stream, err := private.SubscribeWorkspace("shared-csrf", "en", events)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	select {
	case event := <-events:
		if event[0] != "ready" {
			t.Fatal("route session WebSocket did not authenticate")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("route session WebSocket timed out")
	}
	stream.Close()
	private.Close()
	public = open(direct, true)
	request(public, "GET", "/api/me", "alice", 200)
	public.Close()
	private = open(tail, false)
	request(private, "POST", "/api/logout", "{}", 200)
	private.Close()
	public = open(direct, true)
	request(public, "GET", "/api/me", "sign in required", 401)
	public.Close()
	private = open(tail, false)
	request(private, "GET", "/api/me", "sign in required", 401)
}
