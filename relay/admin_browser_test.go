package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

// Opt-in real backend for test/admin-smoke.mjs. The parent owns all scratch
// space, closes stdin to stop the fixture, and never uses deployment data.
func TestAdminBrowserFixture(t *testing.T) {
	if os.Getenv("SPOOL_RELAY_BROWSER_FIXTURE") != "1" {
		t.Skip("opt-in browser fixture")
	}
	s := testBroker(t)
	var proxyReady atomic.Bool
	public := webPublicServerWithHandler(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" && !proxyReady.Load() {
			http.Error(w, "proxy is starting", http.StatusServiceUnavailable)
			return
		}
		r.TLS = nil
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
		s.ServeHTTP(w, r)
	}))
	s.StartProxyDetectionWhenReady()
	// Management and public traffic have independent listeners, matching the
	// normal deployment instead of relying on per-path management URL routing.
	admin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/fixture/proxy-ready" && authorized(r, testAdminToken) {
			proxyReady.Store(true)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.AdminHandler().ServeHTTP(w, r)
	}))
	t.Cleanup(admin.Close)
	// Exercise online server metadata and Web links against a real host.
	_, token := createViaAPI(t, s, "home-mac")
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	startWebTestHost(t, public, token, id, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<!doctype html><title>Spool browser fixture</title>Workspace ready")
	}))
	// Registered tokens remain registered while offline. Self-unregistration
	// permanently retires a token and must never make it look unused again.
	for _, name := range []string{"offline-server", "retired-server"} {
		_, token := createViaAPI(t, s, name)
		identity, err := NewIdentity()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Register(t.Context(), public.URL, token, identity, public.Client()); err != nil {
			t.Fatal(err)
		}
		if name == "retired-server" {
			if err := Unregister(t.Context(), public.URL, token, identity, public.Client()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"fixture_url": admin.URL, "public_url": public.URL}); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
