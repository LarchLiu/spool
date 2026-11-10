package workspace

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func TestLoginClientIPTrustBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, peer, proxies, want string
		forwarded                 []string
		tls                       bool
	}{
		{name: "direct", peer: "198.51.100.1:1234", want: "198.51.100.1"},
		{name: "untrusted forwarding", peer: "198.51.100.1:1234", proxies: "127.0.0.1/32", forwarded: []string{"203.0.113.2"}, want: "198.51.100.1"},
		{name: "empty trust", peer: "127.0.0.1:1234", forwarded: []string{"203.0.113.2"}, want: "127.0.0.1"},
		{name: "invalid saved trust", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32,invalid", forwarded: []string{"203.0.113.2"}, want: "127.0.0.1"},
		{name: "trusted proxy", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32", forwarded: []string{"198.51.100.1"}, want: "198.51.100.1"},
		{name: "forged prefix", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32", forwarded: []string{"203.0.113.2, 198.51.100.1"}, want: "198.51.100.1"},
		{name: "ignore malformed untrusted prefix", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32", forwarded: []string{"garbage, 198.51.100.1"}, want: "198.51.100.1"},
		{name: "multiple trusted hops and header lines", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32,10.20.0.0/24", forwarded: []string{"203.0.113.2, 198.51.100.1", "10.20.0.5"}, want: "198.51.100.1"},
		{name: "stop at untrusted intermediate proxy", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32", forwarded: []string{"198.51.100.1, 10.20.0.5"}, want: "10.20.0.5"},
		{name: "IPv6 proxy", peer: "[2001:db8::20]:1234", proxies: "2001:db8::20/128", forwarded: []string{"2001:0db8::1"}, want: "2001:db8::1"},
		{name: "mapped peer", peer: "[::ffff:127.0.0.1]:1234", proxies: "127.0.0.1/32", forwarded: []string{"198.51.100.1"}, want: "198.51.100.1"},
		{name: "mapped client", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32", forwarded: []string{"::ffff:198.51.100.1"}, want: "198.51.100.1"},
		{name: "missing forwarding", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32", want: "127.0.0.1"},
		{name: "malformed trusted suffix", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32,10.20.0.0/24", forwarded: []string{"garbage, 10.20.0.5"}, want: "127.0.0.1"},
		{name: "empty trusted hop", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32", forwarded: []string{"198.51.100.1,"}, want: "127.0.0.1"},
		{name: "client port is not an IP", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32", forwarded: []string{"198.51.100.1:1234"}, want: "127.0.0.1"},
		{name: "reject forwarded zone", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32", forwarded: []string{"fe80::1%fake"}, want: "127.0.0.1"},
		{name: "long forged prefix", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32", forwarded: []string{strings.Repeat("203.0.113.2,", 64) + "198.51.100.1"}, want: "198.51.100.1"},
		{name: "excessive trusted hops", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32,10.20.0.0/24", forwarded: []string{"198.51.100.1" + strings.Repeat(",10.20.0.5", 64)}, want: "127.0.0.1"},
		{name: "TLS peer ignores Web proxy policy", peer: "127.0.0.1:1234", proxies: "127.0.0.1/32", forwarded: []string{"203.0.113.2"}, tls: true, want: "127.0.0.1"},
		{name: "Relay transport client identity", peer: "relay-client/198.51.100.1", proxies: "127.0.0.1/32", forwarded: []string{"203.0.113.2"}, tls: true, want: "relay-client/198.51.100.1"},
		{name: "Relay IPv6 client identity", peer: "relay-client/2001:db8::1", proxies: "127.0.0.1/32", forwarded: []string{"203.0.113.2"}, tls: true, want: "relay-client/2001:db8::1"},
		{name: "invalid peer", peer: "invalid", proxies: "127.0.0.1/32", forwarded: []string{"203.0.113.2"}, want: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{settings: Settings{WebTrustedProxies: tc.proxies}}
			r := httptest.NewRequest("POST", "http://chat.example.test/api/login", nil)
			r.RemoteAddr = tc.peer
			for _, value := range tc.forwarded {
				r.Header.Add("X-Forwarded-For", value)
			}
			r.Header.Set("X-Real-IP", "203.0.113.99")
			r.Header.Set("Forwarded", "for=203.0.113.99")
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := a.loginClientIP(r); got != tc.want {
				t.Fatalf("client IP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHTTPSProxyLoginLimitsSeparateClientsAndRejectSpoofing(t *testing.T) {
	store := testStore(t)
	testUser(t, store, "proxy")
	a := NewApp(store, &fakeRunner{}, "", "")
	a.settings.PublicURL = "https://chat.example.test"
	t.Cleanup(func() { _ = a.Close() })
	backend := httptest.NewServer(a)
	t.Cleanup(backend.Close)
	target, _ := url.Parse(backend.URL)
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(target)
		// Model a proxy that appends the actual client to existing headers.
		p.Out.Header["X-Forwarded-For"] = p.In.Header.Values("X-Forwarded-For")
		p.SetXForwarded()
	}}
	login := func(peer, spoof, body string, want int) {
		t.Helper()
		// The frontend peer is synthetic; ReverseProxy sends a real HTTP request
		// to the backend with its own TCP peer and the resulting forwarding chain.
		r := httptest.NewRequest("POST", a.settings.PublicURL+"/api/login", strings.NewReader(body))
		r.RemoteAddr = peer
		r.Header.Set("Origin", a.settings.PublicURL)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", spoof)
		r.Header.Set("X-Real-IP", spoof)
		r.Header.Set("Forwarded", "for="+spoof)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("login from %s: %d, want %d: %s", peer, w.Code, want, w.Body)
		}
	}
	for i := 0; i < 20; i++ {
		login(fmt.Sprintf("198.51.100.1:%d", 30000+i), fmt.Sprintf("203.0.113.%d", i+1), "{", http.StatusBadRequest)
	}
	valid := `{"username":"proxy","password":"test-password-2026"}`
	login("198.51.100.1:40000", "203.0.113.99", valid, http.StatusTooManyRequests)
	login("[::ffff:198.51.100.1]:40001", "garbage", valid, http.StatusTooManyRequests)
	login("198.51.100.1:40004", strings.Repeat("203.0.113.99,", 100), valid, http.StatusTooManyRequests)
	login("203.0.113.2:40002", "198.51.100.1", valid, http.StatusOK)
	login("[2001:db8::2]:40003", "198.51.100.1", valid, http.StatusOK)
}

func TestLoginLimitIgnoresUntrustedForwarding(t *testing.T) {
	for _, proxies := range []string{"", "127.0.0.1/32"} {
		t.Run("proxies="+proxies, func(t *testing.T) {
			a := &App{settings: Settings{WebTrustedProxies: proxies}}
			for i := 0; i <= 20; i++ {
				r := httptest.NewRequest("POST", "https://chat.example.test/api/login", nil)
				r.RemoteAddr = fmt.Sprintf("198.51.100.1:%d", 30000+i)
				r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
				w := httptest.NewRecorder()
				a.ServeHTTP(w, r)
				want := http.StatusUnsupportedMediaType
				if i == 20 {
					want = http.StatusTooManyRequests
				}
				if w.Code != want {
					t.Fatalf("attempt %d: status=%d, want %d", i, w.Code, want)
				}
			}
		})
	}
}
