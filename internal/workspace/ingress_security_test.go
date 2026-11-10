package workspace

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func TestWebTransportPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, host, peer, origin, proto, forwardedHost, proxies, public string
		tls, want, customProxies                                        bool
	}{
		{name: "local IPv4", host: "127.0.0.1:8787", peer: "127.0.0.1:1234", want: true},
		{name: "local IPv6", host: "[::1]:8787", peer: "[::1]:1234", want: true},
		{name: "local hostname", host: "localhost:8787", peer: "127.0.0.1:1234", want: true},
		{name: "local IPv4 mapped", host: "127.0.0.1:8787", peer: "[::ffff:127.0.0.1]:1234", want: true},
		{name: "local with public URL", host: "127.0.0.1:8787", peer: "127.0.0.1:1234", public: "https://chat.example.test", want: true},
		{name: "remote TLS", host: "chat.example.test", peer: "192.0.2.20:1234", tls: true, want: true},
		{name: "loopback proxy", host: "chat.example.test", peer: "127.0.0.1:1234", proto: "https", public: "https://chat.example.test", want: true},
		{name: "rewritten proxy host", host: "127.0.0.1:8787", peer: "127.0.0.1:1234", proto: "https", forwardedHost: "chat.example.test", public: "https://chat.example.test", want: true},
		{name: "remote trusted proxy", host: "chat.example.test", peer: "192.0.2.20:1234", proto: "https", public: "https://chat.example.test", customProxies: true, proxies: "192.0.2.20/32", want: true},
		{name: "IPv6 trusted proxy", host: "chat.example.test", peer: "[2001:db8::20]:1234", proto: "https", public: "https://chat.example.test", customProxies: true, proxies: "2001:db8::20/128", want: true},
		{name: "direct remote", host: "192.0.2.10:8787", peer: "192.0.2.20:1234"},
		{name: "remote spoofed local host", host: "localhost:8787", peer: "192.0.2.20:1234"},
		{name: "local peer public host", host: "chat.example.test", peer: "127.0.0.1:1234"},
		{name: "foreign origin on local host", host: "127.0.0.1:8787", peer: "127.0.0.1:1234", origin: "http://foreign.example.test"},
		{name: "spoofed remote proxy", host: "chat.example.test", peer: "192.0.2.20:1234", proto: "https", public: "https://chat.example.test"},
		{name: "missing proto", host: "chat.example.test", peer: "127.0.0.1:1234", public: "https://chat.example.test"},
		{name: "HTTP frontend", host: "chat.example.test", peer: "127.0.0.1:1234", proto: "http", public: "https://chat.example.test"},
		{name: "rewritten HTTP frontend", host: "127.0.0.1:8787", peer: "127.0.0.1:1234", proto: "http", public: "https://chat.example.test"},
		{name: "ambiguous proto", host: "chat.example.test", peer: "127.0.0.1:1234", proto: "https,http", public: "https://chat.example.test"},
		{name: "foreign proxy host", host: "foreign.example.test", peer: "127.0.0.1:1234", proto: "https", public: "https://chat.example.test"},
		{name: "ambiguous forwarded host", host: "127.0.0.1:8787", peer: "127.0.0.1:1234", proto: "https", forwardedHost: "chat.example.test,foreign.example.test", public: "https://chat.example.test"},
		{name: "no public URL", host: "chat.example.test", peer: "127.0.0.1:1234", proto: "https"},
		{name: "HTTP public URL", host: "chat.example.test", peer: "127.0.0.1:1234", proto: "https", public: "http://chat.example.test"},
		{name: "explicit empty trust", host: "chat.example.test", peer: "127.0.0.1:1234", proto: "https", public: "https://chat.example.test", customProxies: true},
		{name: "invalid saved CIDR", host: "chat.example.test", peer: "127.0.0.1:1234", proto: "https", public: "https://chat.example.test", customProxies: true, proxies: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := DefaultSettings()
			settings.PublicURL = tc.public
			if tc.customProxies {
				settings.WebTrustedProxies = tc.proxies
			}
			a := &App{settings: settings}
			r := httptest.NewRequest("GET", "http://"+tc.host+"/api/me", nil)
			r.RemoteAddr = tc.peer
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			if tc.forwardedHost != "" {
				r.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			}
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := a.allowWebTransport(r); got != tc.want {
				t.Fatalf("allowed=%v, want %v", got, tc.want)
			}
			if tc.proto == "https" && tc.want {
				r.Header.Set("Origin", "http://"+r.Host)
				if !a.secureCookie(r) || a.sameOrigin(r) {
					t.Fatal("HTTPS proxy accepted its HTTP upstream origin or lost Secure")
				}
				r.Header.Set("Origin", tc.public)
				if !a.sameOrigin(r) {
					t.Fatal("HTTPS proxy rejected its configured origin")
				}
				r.Header.Add("X-Forwarded-Proto", "http")
				if a.allowWebTransport(r) {
					t.Fatal("accepted duplicate forwarded protocol headers")
				}
			}
		})
	}
}

type unreadableCredentials struct{ t *testing.T }

func (r unreadableCredentials) Read([]byte) (int, error) {
	r.t.Error("blocked ingress read the password body")
	return 0, io.EOF
}

func TestWebRejectsPlaintextBeforeReadingLoginBody(t *testing.T) {
	a := &App{settings: DefaultSettings()}
	r := httptest.NewRequest("POST", "http://chat.example.test/api/login", unreadableCredentials{t})
	r.RemoteAddr = "192.0.2.20:1234"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || len(a.limiter.buckets) != 0 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("plaintext request reached authentication or could be cached", w.Code)
	}
	// Health remains unauthenticated and exposes only availability.
	r = httptest.NewRequest("GET", "http://chat.example.test/healthz", nil)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"ok":true}` || len(w.Result().Cookies()) != 0 {
		t.Fatal("health unexpectedly requires or changes authentication", w.Code)
	}
}

func TestHTTPSReverseProxyRejectsHTTPFrontendAndPreservesLocalLogin(t *testing.T) {
	store := testStore(t)
	testUser(t, store, "proxy")
	a := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	backend := httptest.NewServer(a)
	t.Cleanup(backend.Close)
	target, _ := url.Parse(backend.URL)
	proxy := &httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(target)
		p.SetXForwarded()
	}}
	secure := httptest.NewTLSServer(proxy)
	t.Cleanup(secure.Close)
	insecure := httptest.NewServer(proxy)
	t.Cleanup(insecure.Close)
	a.settings.PublicURL = secure.URL
	client := secure.Client()
	t.Cleanup(client.CloseIdleConnections)
	request := func(base, method, path, origin string, cookie *http.Cookie, want int, spoof bool) *http.Response {
		t.Helper()
		body := ""
		if method == "POST" {
			body = `{"username":"proxy","password":"test-password-2026"}`
		}
		r, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if spoof {
			public, _ := url.Parse(secure.URL)
			r.Host = public.Host
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("X-Forwarded-Host", public.Host)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != want {
			t.Fatalf("%s %s: status=%d want=%d: %s (%v)", method, base+path, resp.StatusCode, want, data, err)
		}
		return resp
	}
	resp := request(secure.URL, "POST", "/api/login", secure.URL, nil, 200, false)
	if len(resp.Cookies()) != 1 || !resp.Cookies()[0].Secure {
		t.Fatal("HTTPS proxy lost Secure session cookie")
	}
	cookie := resp.Cookies()[0]
	request(secure.URL, "GET", "/api/me", "", cookie, 200, false)
	request(insecure.URL, "POST", "/api/login", secure.URL, nil, 403, true)
	request(insecure.URL, "GET", "/api/me", "", cookie, 403, true)
	request(insecure.URL, "GET", "/", "", nil, 403, true)
	request(backend.URL, "POST", "/api/login", backend.URL, nil, 200, false)
}

func TestWebRejectsRemotePlaintextCredentialsAndSessions(t *testing.T) {
	for _, publicURL := range []string{"", "https://chat.example.test"} {
		t.Run("public="+publicURL, func(t *testing.T) {
			store := testStore(t)
			user := testUser(t, store, "plaintext")
			token, id, err := store.login(user.Username, "test-password-2026")
			if err != nil {
				t.Fatal(err)
			}
			a := NewApp(store, &fakeRunner{}, "", "")
			t.Cleanup(func() { a.Close() })
			a.settings.PublicURL = publicURL
			for _, path := range []string{"/", "/api/login", "/api/me", "/api/preferences", "/api/events", "/api/chats/fixture/events"} {
				for _, spoof := range []bool{false, true} {
					method, body := "GET", ""
					if path == "/api/login" || path == "/api/preferences" {
						method, body = "POST", `{"username":"plaintext","password":"test-password-2026"}`
					}
					r := httptest.NewRequest(method, "http://192.0.2.10:8787"+path, strings.NewReader(body))
					r.RemoteAddr = "192.0.2.20:45000"
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("Origin", "http://192.0.2.10:8787")
					r.Header.Set("X-CSRF-Token", id.CSRF)
					r.AddCookie(&http.Cookie{Name: authCookie, Value: token})
					if spoof {
						r.Host = "chat.example.test"
						r.Header.Set("Origin", "https://chat.example.test")
						r.Header.Set("X-Forwarded-Proto", "https")
						r.Header.Set("X-Forwarded-For", "127.0.0.1")
						r.Header.Set("Forwarded", "for=127.0.0.1;proto=https")
					}
					w := httptest.NewRecorder()
					a.ServeHTTP(w, r)
					if w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 {
						t.Errorf("%s spoof=%v: status=%d, want 403 without a session", path, spoof, w.Code)
					}
				}
			}
		})
	}
}
