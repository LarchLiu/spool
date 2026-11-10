package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Opt-in real backend for test/admin-smoke.mjs. The parent owns all scratch
// space, closes stdin to stop the fixture, and never uses deployment data.
func TestAdminBrowserFixture(t *testing.T) {
	if os.Getenv("SPOOL_RELAY_BROWSER_FIXTURE") != "1" {
		t.Skip("opt-in browser fixture")
	}
	s := testBroker(t)
	public := trustedProbeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, proxyProbePath) || r.URL.Path == "/healthz" ||
			r.URL.Path == "/v1/host" || r.URL.Path == "/v1/register" || r.URL.Path == "/v1/unregister" ||
			strings.HasPrefix(r.URL.Path, "/v1/connect/") || strings.HasPrefix(r.URL.Path, "/v1/data/") {
			r.TLS = nil
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("X-Forwarded-For", "203.0.113.9")
			s.ServeHTTP(w, r)
			return
		}
		s.AdminHandler().ServeHTTP(w, r)
	}))
	// Exercise online server metadata against an actual native connection.
	_, token := createViaAPI(t, s, "home-mac")
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	startAdminTestHost(t, public, token, id, make(chan struct{}))
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"fixture_url": public.URL}); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
