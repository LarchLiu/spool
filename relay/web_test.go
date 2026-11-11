package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func webFixture(t *testing.T, enabled bool, handler http.Handler) (*Server, *Host, *httptest.Server, string) {
	t.Helper()
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewServer(ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	public := webPublicServer(t, broker)
	t.Cleanup(func() { broker.Close(); public.Close() })
	_, credential, err := broker.CreateRegistration("web test")
	if err != nil {
		t.Fatal(err)
	}
	host, _ := startWebTestHost(t, public, credential, id, enabled, handler)
	return broker, host, public, testTenantURL(t, public.URL, id.ID())
}

func startWebTestHost(t *testing.T, public *httptest.Server, credential string, id *Identity, enabled bool, handler http.Handler) (*Host, func()) {
	t.Helper()
	host, err := NewHost(t.Context(), public.URL, credential, id, public.Client())
	if err != nil {
		t.Fatal(err)
	}
	host.WebEnabled = enabled
	ready := make(chan string, 1)
	host.OnWebURL = func(address string) {
		select {
		case ready <- address:
		default:
		}
	}
	cfg, _ := id.TLSConfig()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	served := make(chan struct{})
	go func() { server.Serve(tls.NewListener(host, cfg)); close(served) }()
	done := make(chan struct{})
	go func() { host.Run(); host.Wait(); close(done) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			host.Close()
			server.Close()
			for _, stopped := range []chan struct{}{served, done} {
				select {
				case <-stopped:
				case <-time.After(5 * time.Second):
					t.Error("host did not stop")
				}
			}
		})
	}
	t.Cleanup(stop)
	select {
	case address := <-ready:
		want := ""
		if enabled {
			want = testTenantURL(t, public.URL, id.ID())
		}
		if address != want {
			t.Fatalf("Web URL %q, want %q", address, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host did not register")
	}
	return host, stop
}

func TestWebAccessRequiresHostOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		_, _, server, address := webFixture(t, enabled, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("workspace")) }))
		resp, err := server.Client().Get(address)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		want := http.StatusServiceUnavailable
		if enabled {
			want = 200
		}
		if resp.StatusCode != want {
			t.Fatalf("enabled=%t: %d", enabled, resp.StatusCode)
		}
	}
}

func TestWebRoutesFollowHostLifecycleWithoutRelayRestart(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "token"
		if automatic {
			name = "automatic"
		}
		t.Run(name, func(t *testing.T) {
			broker := testBrokerMode(t, automatic)
			public := webPublicServer(t, broker)
			t.Cleanup(func() { broker.Close(); public.Close() })
			client := public.Client()
			client.Timeout = 5 * time.Second
			check := func(id *Identity, want int) {
				t.Helper()
				resp, err := client.Get(testTenantURL(t, public.URL, id.ID()))
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != want {
					t.Fatalf("Web route status %d, want %d", resp.StatusCode, want)
				}
			}
			enroll := func() (*Identity, string) {
				t.Helper()
				// The ID is first created after relay startup: no static list exists.
				id, _ := NewIdentity()
				check(id, 503)
				var token string
				if !automatic {
					_, token = createViaAPI(t, broker, "dynamic web")
				}
				credential, err := Register(t.Context(), public.URL, token, id, client)
				if err != nil {
					t.Fatal(err)
				}
				check(id, 503) // Enrollment alone never publishes Web.
				return id, credential
			}
			id, token := enroll()
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("workspace")) })
			var other *Identity
			for _, enabled := range []bool{false, true, false, true} {
				_, stop := startWebTestHost(t, public, token, id, enabled, handler)
				want := 503
				if enabled {
					want = 200
				}
				check(id, want)
				if enabled && other == nil {
					var otherToken string
					other, otherToken = enroll()
					startWebTestHost(t, public, otherToken, other, true, handler)
				}
				stop()
				deadline := time.Now().Add(5 * time.Second)
				for {
					broker.mu.Lock()
					removed := broker.hosts[id.ID()] == nil
					broker.mu.Unlock()
					if removed {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("disconnected host retained its route")
					}
					time.Sleep(5 * time.Millisecond)
				}
				check(id, 503)
				if other != nil {
					check(other, 200) // Changing one host never restarts the relay.
				}
			}
		})
	}
}

func TestWebOptInMustBeAuthenticatedAndSigned(t *testing.T) {
	broker := testBroker(t)
	public := webPublicServer(t, broker)
	t.Cleanup(func() { broker.Close(); public.Close() })
	id, _ := NewIdentity()
	_, token := createViaAPI(t, broker, "signed web")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if socket, err := dialWebsocket(ctx, public.Client(), public.URL, "/v1/host", randomID()); err == nil {
		socket.CloseNow()
		t.Fatal("unknown credential opened a host connection")
	}
	socket, err := dialWebsocket(ctx, public.Client(), public.URL, "/v1/host", token)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	challenge, err := readControl(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	// A native-only signature cannot be upgraded by adding a Web origin.
	message := controlMessage{Type: "register", PublicKey: id.PublicKey(), AccessToken: id.AccessToken, WebOrigin: public.URL,
		Signature: ed25519.Sign(id.PrivateKey(), proof(challenge.Challenge, id.AccessToken))}
	if err = writeControl(ctx, socket, message); err != nil {
		t.Fatal(err)
	}
	if _, err = readControl(ctx, socket); err == nil || ctx.Err() != nil {
		t.Fatal("unsigned Web opt-in was not rejected promptly", err)
	}
	resp, err := public.Client().Get(testTenantURL(t, public.URL, id.ID()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatal("invalid registration published a Web route")
	}
}

func TestWebProtocolVersionCannotBeUpgradedOrDowngraded(t *testing.T) {
	broker := testBroker(t)
	public := webPublicServer(t, broker)
	id, _ := NewIdentity()
	_, token := createViaAPI(t, broker, "version signature")
	for _, versions := range [][2]int{{0, 2}, {2, 0}, {2, 3}} {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		socket, err := dialWebsocket(ctx, public.Client(), public.URL, "/v1/host", token)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		challenge, err := readControl(ctx, socket)
		if err != nil {
			t.Fatal(err)
		}
		message := controlMessage{Type: "register", PublicKey: id.PublicKey(), AccessToken: id.AccessToken, WebOrigin: public.URL, WebVersion: versions[1],
			Signature: ed25519.Sign(id.PrivateKey(), hostRegistrationProof(challenge.Challenge, id.AccessToken, public.URL, "", versions[0]))}
		if err := writeControl(ctx, socket, message); err != nil {
			t.Fatal(err)
		}
		_, err = readControl(ctx, socket)
		socket.CloseNow()
		if err == nil || ctx.Err() != nil {
			cancel()
			t.Fatal("tampered Web version was not promptly rejected", versions, err)
		}
		cancel()
	}
	if statuses := broker.registrationStatuses(""); len(statuses) != 1 || statuses[0].WebPublished || statuses[0].ServerID != "" {
		t.Fatal("invalid signature/version persisted publication", statuses)
	}
}

func TestWebProxyCookiesOriginsStreamingAndRevocation(t *testing.T) {
	broker, host, server, address := webFixture(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			t.Error("missing pinned TLS")
		}
		_, path, ok := ParseWebPath(r.URL)
		if !ok {
			t.Error("lost server prefix")
		}
		if r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
			t.Error("forwarded client headers")
		}
		switch path {
		case "/login":
			w.Header().Set("Clear-Site-Data", `"cookies", "*"`)
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "synthetic", Path: "/", Domain: "example.com", HttpOnly: true, Secure: true})
		case "/file":
			if cookie, err := r.Cookie("spool_session"); err != nil || cookie.Value != "synthetic" {
				t.Error("lost cookie")
			}
			if r.URL.Query().Get("path") != "a/b & 中文" || r.Header.Get("X-CSRF-Token") != "csrf" {
				t.Error("lost request metadata")
			}
			io.Copy(w, r.Body)
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: ready\n\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/ws":
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			c.SetReadLimit(2 << 20)
			for {
				kind, payload, err := c.Read(r.Context())
				if err != nil {
					return
				}
				if c.Write(r.Context(), kind, payload) != nil {
					return
				}
			}
		case "/sw.js":
			w.Header().Set("Service-Worker-Allowed", "/")
		case "/assets/app.js":
			w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
		case "/redirect":
			http.Redirect(w, r, "/login", http.StatusFound)
		case "/logout":
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Path: "/", MaxAge: -1})
		}
	}))
	client := server.Client()
	client.Timeout = 5 * time.Second
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	get := func(path string) *http.Response {
		t.Helper()
		resp, err := client.Get(address + path)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := get("login")
	resp.Body.Close()
	cookies := resp.Cookies()
	if len(cookies) != 1 || cookies[0].Path != "/" || cookies[0].Domain != "" || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("unscoped login cookie", cookies)
	}
	if resp.Header.Get("Clear-Site-Data") != "" {
		t.Fatal("tenant may clear sibling domains' cookies")
	}
	other, _ := url.Parse(strings.Replace(address, host.Identity.ID(), strings.Repeat("a", 52), 1))
	if len(client.Jar.Cookies(other)) != 0 {
		t.Fatal("cookie escaped server path")
	}
	payload := bytes.Repeat([]byte{0, 255, 128, 1}, 200000)
	req, _ := http.NewRequest("POST", address+"file?path="+url.QueryEscape("a/b & 中文"), bytes.NewReader(payload))
	req.Header.Set("Origin", strings.TrimSuffix(address, "/"))
	req.Header.Set("X-CSRF-Token", "csrf")
	req.Header.Set("X-Forwarded-Host", "evil.test")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "1.2.3.4")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("binary upload/download failed", err, resp.StatusCode)
	}
	req, _ = http.NewRequest("POST", address+"login", nil)
	req.Header.Set("Origin", "https://evil.test")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("foreign origin accepted")
	}
	for path, header := range map[string]string{"sw.js": "Service-Worker-Allowed", "redirect": "Location"} {
		resp = get(path)
		resp.Body.Close()
		if !strings.HasPrefix(resp.Header.Get(header), "/") {
			t.Fatal("unscoped response", header)
		}
	}
	asset := get("assets/app.js")
	asset.Body.Close()
	if values := asset.Header.Values("Cache-Control"); len(values) != 1 || values[0] != "public,max-age=31536000,immutable" {
		t.Fatal("relay overrode asset cache policy", values)
	}
	resp = get("events")
	defer resp.Body.Close()
	first := make([]byte, len("data: ready\n\n"))
	if _, err = io.ReadFull(resp.Body, first); err != nil || string(first) != "data: ready\n\n" {
		t.Fatal("SSE buffered", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, strings.Replace(address, "https:", "wss:", 1)+"ws", &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Origin": {strings.TrimSuffix(address, "/")}}})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	socket.SetReadLimit(2 << 20)
	if err = socket.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatal(err)
	}
	kind, data, err := socket.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || !bytes.Equal(data, payload) {
		t.Fatal("WebSocket corrupted", err)
	}
	logout := get("logout")
	logout.Body.Close()
	u, _ := url.Parse(address)
	if len(client.Jar.Cookies(u)) != 0 {
		t.Fatal("logout did not clear scoped cookie")
	}
	registration := broker.registrationStatuses("")[0]
	if err = broker.RevokeRegistration(registration.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = socket.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatal("revocation did not close WebSocket", err)
	}
	if _, err = resp.Body.Read(make([]byte, 1)); err == nil {
		t.Fatal("revocation did not close SSE")
	}
}

func TestWebPathValidation(t *testing.T) {
	id, _ := NewIdentity()
	for _, suffix := range []string{"/../api/me", "/%2e%2e/api/me", "//api/me", "/a\\b"} {
		u, _ := url.Parse("/web/" + id.ID() + suffix)
		if _, _, ok := ParseWebPath(u); ok {
			t.Fatal("accepted ambiguous path", suffix)
		}
	}
	u, _ := url.Parse(WebPath(id.ID()) + "api/file?path=a%2Fb")
	if got, path, ok := ParseWebPath(u); !ok || got != id.ID() || path != "/api/file" {
		t.Fatal("valid path rejected")
	}
}
