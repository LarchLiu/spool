package relay

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests trust only their own TLS fixture certificate; production detection
// always uses ordinary certificate validation and the standard proxy policy.
func trustedProbeServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	pool, _ := x509.SystemCertPool()
	if pool == nil {
		pool = x509.NewCertPool()
	}
	pool.AddCert(server.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = original
		transport.CloseIdleConnections()
		server.Close()
	})
	return server
}

func awaitDetection(t *testing.T, s *Server) settingsStatus {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		status := s.settingsStatus()
		if !status.Detecting {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("detection did not complete")
	return settingsStatus{}
}

func TestProxyDetectionFromBrowserOriginAndRestart(t *testing.T) {
	s := testBroker(t)
	broker := s
	peer := "172.18.0.3:41000"
	var path string
	public := trustedProbeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" {
			t.Error("probe sent credentials")
		}
		path = r.URL.Path
		r.RemoteAddr = peer
		r.TLS = nil
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
		broker.ServeHTTP(w, r)
	}))
	if err := s.updateSettings(settingsInput{BrowserOrigin: public.URL, TrustedProxies: []string{"10.10.0.0/24"}}); err != nil {
		t.Fatal(err)
	}
	status := awaitDetection(t, s)
	if status.PublicURL != public.URL || status.PublicURLOverride != "" || status.AdminOrigin != public.URL || status.DetectionError != "" || status.DetectedProxy != "172.18.0.3/32" || status.DetectedAt == nil {
		t.Fatalf("automatic discovery failed: %+v", status)
	}
	r := httptest.NewRequest("GET", "/v1/host", nil)
	r.RemoteAddr = peer
	r.Header.Set("X-Forwarded-For", "203.0.113.44")
	if s.clientIP(r) != "203.0.113.44" {
		t.Fatal("detected proxy not applied live")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", public.URL+path, nil))
	if w.Code != 404 {
		t.Fatal("one-use callback remained active")
	}
	file := filepath.Join(s.opts.DataDir, "settings.json")
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("settings are not private", err)
	}
	s.Close()
	restored, err := NewServer(s.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	broker, peer = restored, "172.18.0.4:42000"
	restored.StartProxyDetection()
	status = awaitDetection(t, restored)
	if status.DetectedProxy != "172.18.0.4/32" || strings.Join(status.TrustedProxies, ",") != "10.10.0.0/24" {
		t.Fatal("restart did not replace only automatic peer", status)
	}
	r.RemoteAddr = "172.18.0.3:41000"
	if restored.clientIP(r) != "172.18.0.3" {
		t.Fatal("old automatic proxy remained trusted")
	}
	r.Host = strings.TrimPrefix(public.URL, "https://")
	r.URL.Path = WebPath(strings.Repeat("a", 52))
	w = httptest.NewRecorder()
	restored.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("tenant Web allowed on management origin", w.Code)
	}
}

func TestProxyDetectionRejectsUnverifiedResponses(t *testing.T) {
	for _, mode := range []string{"reflection", "redirect", "missing-headers", "multiple-proto", "wrong-host", "wrong-proof", "admin-listener"} {
		t.Run(mode, func(t *testing.T) {
			s := testBroker(t)
			redirectFollowed := false
			public := trustedProbeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "reflection":
					io.WriteString(w, strings.TrimPrefix(r.URL.Path, proxyProbePath))
					return
				case "redirect":
					if r.URL.Path == "/followed" {
						redirectFollowed = true
					}
					http.Redirect(w, r, "/followed", 302)
					return
				case "admin-listener":
					s.AdminHandler().ServeHTTP(w, r)
					return
				}
				r.TLS = nil
				r.RemoteAddr = "172.18.0.7:1234"
				r.Header.Set("X-Forwarded-Proto", "https")
				r.Header.Set("X-Forwarded-For", "203.0.113.1")
				switch mode {
				case "missing-headers":
					r.Header.Del("X-Forwarded-For")
				case "multiple-proto":
					r.Header.Add("X-Forwarded-Proto", "https")
				case "wrong-host":
					r.Host = "attacker.example"
				}
				if mode == "wrong-proof" {
					capture := httptest.NewRecorder()
					s.ServeHTTP(capture, r)
					io.WriteString(w, "not-the-response-proof")
					return
				}
				s.ServeHTTP(w, r)
			}))
			if err := s.updateSettings(settingsInput{BrowserOrigin: public.URL}); err != nil {
				t.Fatal(err)
			}
			status := awaitDetection(t, s)
			if status.DetectionError == "" || status.DetectedAt != nil || status.DetectedProxy != "" || redirectFollowed {
				t.Fatal("unverified proxy accepted", status)
			}
		})
	}
}

func TestProxyDetectionRequiresTrustedTLSAndSupportsDirectTLS(t *testing.T) {
	s := testBroker(t)
	untrusted := httptest.NewTLSServer(s)
	defer untrusted.Close()
	if err := s.updateSettings(settingsInput{BrowserOrigin: untrusted.URL}); err != nil {
		t.Fatal(err)
	}
	if status := awaitDetection(t, s); status.DetectionError == "" || status.DetectedAt != nil {
		t.Fatal("untrusted certificate accepted")
	}
	public := trustedProbeServer(t, s)
	if err := s.updateSettings(settingsInput{BrowserOrigin: public.URL}); err != nil {
		t.Fatal(err)
	}
	if status := awaitDetection(t, s); status.DetectionError != "" || status.DetectedAt == nil || status.DetectedProxy != "" {
		t.Fatal("direct TLS should need no proxy", status)
	}
}

func TestProxyDetectionCancellationAndConcurrentPolicy(t *testing.T) {
	s := testBroker(t)
	started := make(chan struct{}, 1)
	public := trustedProbeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	if err := s.updateSettings(settingsInput{BrowserOrigin: public.URL}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("probe never started")
	}
	var workers sync.WaitGroup
	for i := 0; i < 5; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 30; j++ {
				r := httptest.NewRequest("GET", "/", nil)
				r.RemoteAddr = "127.0.0.1:1234"
				r.Header.Set("X-Forwarded-For", "203.0.113.10")
				s.clientIP(r)
				s.settingsStatus()
			}
		}()
	}
	if err := s.updateSettings(settingsInput{TrustedProxies: []string{"127.0.0.1/32"}}); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not cancel and join verifier")
	}
	if status := s.settingsStatus(); status.DetectedAt != nil || status.PublicURL != "" || status.Detecting {
		t.Fatal("cancelled detection changed policy", status)
	}
}

func TestSettingsValidationPersistenceAndFailures(t *testing.T) {
	s := testBroker(t)
	for _, address := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/path", "https://example.com?", "https://example.com#", "https://example.com:999999"} {
		if err := s.updateSettings(settingsInput{PublicURLOverride: address}); err == nil {
			t.Fatal("accepted unsafe origin", address)
		}
	}
	if err := s.updateSettings(settingsInput{BrowserOrigin: "http://localhost:28081", TrustedProxies: []string{"10.0.0.0/24"}}); err != nil {
		t.Fatal(err)
	}
	if s.settingsStatus().PublicURL != "" {
		t.Fatal("local management invented a public address")
	}
	file := filepath.Join(s.opts.DataDir, "settings.json")
	data, _ := os.ReadFile(file)
	if strings.Contains(string(data), testAdminToken) {
		t.Fatal("administrator credential persisted")
	}
	if err := os.Rename(file, file+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.updateSettings(settingsInput{TrustedProxies: []string{"192.0.2.2/32"}}); err == nil {
		t.Fatal("failed persistence reported success")
	}
	if got := s.settingsStatus().TrustedProxies; len(got) != 1 || got[0] != "10.0.0.0/24" {
		t.Fatal("failed persistence changed runtime policy")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"version":1,"detected_proxy":"0.0.0.0/0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if other, err := NewServer(s.opts); err == nil {
		other.Close()
		t.Fatal("invalid stored automatic trust accepted")
	}
}

func TestAdminDashboardAndSameOriginAPI(t *testing.T) {
	s := testBroker(t)
	for path, contentType := range map[string]string{"/": "text/html", "/admin.js": "text/javascript", "/admin.css": "text/css"} {
		w := adminRequest(t, s, "GET", path, "", "")
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), contentType) || w.Header().Get("Content-Security-Policy") == "" || strings.Contains(w.Body.String(), testAdminToken) {
			t.Fatal("invalid dashboard asset", path, w.Code)
		}
		public := httptest.NewRecorder()
		s.ServeHTTP(public, httptest.NewRequest("GET", path, nil))
		if public.Code != 404 {
			t.Fatal("public listener exposed dashboard", path)
		}
	}
	r := httptest.NewRequest("POST", "https://admin.example/v1/registrations", strings.NewReader(`{"name":"browser"}`))
	r.Header.Set("Origin", "https://admin.example")
	r.Header.Set("Authorization", "Bearer "+testAdminToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal("same-origin browser API rejected", w.Code)
	}
	for _, origin := range []string{"https://attacker.example", "null", "https://admin.example.attacker"} {
		r.Header.Set("Origin", origin)
		w = httptest.NewRecorder()
		s.AdminHandler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("cross-origin administration accepted", origin)
		}
	}
	if w := adminRequest(t, s, "PUT", "/v1/settings", testAdminToken, `{"browser_origin":"http://localhost:28081","trusted_proxies":["127.0.0.1/32"]}`); w.Code != 200 {
		t.Fatal("runtime settings endpoint", w.Code, w.Body)
	}
	w = adminRequest(t, s, "GET", "/v1/settings", testAdminToken, "")
	var status settingsStatus
	if json.Unmarshal(w.Body.Bytes(), &status) != nil || len(status.TrustedProxies) != 1 {
		t.Fatal("settings not returned", w.Body)
	}
	if w := adminRequest(t, s, "POST", "/v1/proxy-detection", testAdminToken, ""); w.Code != 400 {
		t.Fatal("detection without address accepted")
	}
	// A known public origin can be protected even when its dashboard uses an
	// independent private listener and TLS terminates before reaching Go.
	s.mu.Lock()
	s.settings.AdminOrigin = "https://admin.example"
	s.mu.Unlock()
	request := httptest.NewRequest("GET", "https://admin.example"+WebPath(strings.Repeat("a", 52)), nil)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, request)
	if w.Code != 403 {
		t.Fatal("management origin allowed tenant content")
	}
}

func TestFailedRetryPreservesVerifiedPolicyAndManualEdits(t *testing.T) {
	s := testBroker(t)
	var mu sync.Mutex
	fail := false
	public := trustedProbeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		broken := fail
		mu.Unlock()
		if broken {
			http.Error(w, "unavailable", 503)
			return
		}
		r.TLS = nil
		r.RemoteAddr = "172.18.0.8:1234"
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
		s.ServeHTTP(w, r)
	}))
	if err := s.updateSettings(settingsInput{BrowserOrigin: public.URL}); err != nil {
		t.Fatal(err)
	}
	before := awaitDetection(t, s)
	mu.Lock()
	fail = true
	mu.Unlock()
	s.StartProxyDetection()
	if err := s.updateSettings(settingsInput{BrowserOrigin: public.URL, TrustedProxies: []string{"10.10.0.0/24"}}); err != nil {
		t.Fatal(err)
	}
	after := awaitDetection(t, s)
	if before.DetectedProxy != "172.18.0.8/32" || after.DetectedProxy != before.DetectedProxy || after.DetectionError == "" || len(after.TrustedProxies) != 1 {
		t.Fatal("failed retry lost verified or manually edited policy", after)
	}
}
