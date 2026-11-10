package workspace

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func detectionProxy(t *testing.T, service *Service, rewrite func(*httputil.ProxyRequest)) *httptest.Server {
	t.Helper()
	target, _ := url.Parse("http://" + service.Status().Address)
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(target)
		p.SetXForwarded()
		if rewrite != nil {
			rewrite(p)
		}
	}}
	server := httptest.NewTLSServer(proxy)
	t.Cleanup(server.Close)
	return server
}

func TestDetectWebProxyVerifiesTLSAndAppliesWithoutRestart(t *testing.T) {
	service, settings := settingsService(t)
	settings.WebTrustedProxies = ""
	settings.IdleMinutes++
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("web"); err != nil {
		t.Fatal(err)
	}
	app, runner, ingress := service.app, service.runner, service.web
	localURL, _ := service.LocalWebURL()
	proxy := detectionProxy(t, service, func(p *httputil.ProxyRequest) {
		if strings.HasPrefix(p.In.URL.Path, webProxyProbePath) && (p.In.Header.Get("Cookie") != "" || p.In.Header.Get("Authorization") != "" || p.In.URL.RawQuery != "") {
			t.Error("detection sent credentials or a query")
		}
	})
	// The production client must reject a self-signed public certificate.
	if err := service.DetectWebProxy(t.Context(), proxy.URL); err == nil {
		t.Fatal("detection disabled TLS certificate verification")
	}
	if err := service.detectWebProxy(t.Context(), proxy.URL+"/", proxy.Client().Transport); err != nil {
		t.Fatal(err)
	}
	saved, _ := service.Store.Settings()
	if saved.PublicURL != proxy.URL || saved.WebTrustedProxies != "" || saved.WebDetectedProxy != "127.0.0.1/32" || saved.WebDetectedOrigin != proxy.URL {
		t.Fatal("detected origin/peer were not saved exactly", saved)
	}
	if service.app != app || service.runner != runner || service.web != ingress {
		t.Fatal("detection restarted the service or Web listener")
	}
	if address, _ := service.LocalWebURL(); address != localURL || service.Status().RestartRequired {
		t.Fatal("detection changed the desktop endpoint or left pending runtime changes", service.Status())
	}
	if app.activeSettings().PublicURL != saved.PublicURL || app.activeSettings().WebDetectedProxy != saved.WebDetectedProxy {
		t.Fatal("detection needs another Apply action")
	}
	user := testUser(t, service.Store, "detected")
	r, _ := http.NewRequest("POST", proxy.URL+"/api/login", strings.NewReader(fmt.Sprintf(`{"username":%q,"password":"test-password-2026"}`, user.Username)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", proxy.URL)
	response, err := proxy.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 || !response.Cookies()[0].Secure {
		t.Fatal("detected public entry cannot sign in securely", response.StatusCode)
	}
	if err := service.detectWebProxy(t.Context(), proxy.URL, proxy.Client().Transport); err != nil {
		t.Fatal("repeat detection failed", err)
	}
	again, _ := service.Store.Settings()
	if again.WebTrustedProxies != saved.WebTrustedProxies || again.WebDetectedProxy != saved.WebDetectedProxy {
		t.Fatal("repeat detection duplicated trust")
	}
}

func TestDetectWebProxyFailuresKeepSettings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rewrite func(*httputil.ProxyRequest)
	}{
		{"missing proto", func(p *httputil.ProxyRequest) { p.Out.Header.Del("X-Forwarded-Proto") }},
		{"HTTP frontend", func(p *httputil.ProxyRequest) { p.Out.Header.Set("X-Forwarded-Proto", "http") }},
		{"ambiguous proto", func(p *httputil.ProxyRequest) { p.Out.Header.Add("X-Forwarded-Proto", "https") }},
		{"wrong host", func(p *httputil.ProxyRequest) { p.Out.Header.Set("X-Forwarded-Host", "foreign.example.test") }},
		{"missing client IP", func(p *httputil.ProxyRequest) { p.Out.Header.Del("X-Forwarded-For") }},
		{"invalid client IP", func(p *httputil.ProxyRequest) { p.Out.Header.Set("X-Forwarded-For", "invalid") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, before := settingsService(t)
			proxy := detectionProxy(t, service, tc.rewrite)
			if err := service.detectWebProxy(t.Context(), proxy.URL, proxy.Client().Transport); err == nil || err.Error() != webProxyHeaders {
				t.Fatal("detection accepted incomplete forwarding", err)
			}
			after, _ := service.Store.Settings()
			if after != before || service.app.webProxyProbe != nil {
				t.Fatal("failed detection changed settings or left a probe active")
			}
		})
	}
}

func TestDetectWebProxyAddsOnlyTheConnectingPeer(t *testing.T) {
	for _, tc := range []struct {
		name, remote, trusted, want string
	}{
		{"IPv4 proxy", "172.18.0.3:2345", "127.0.0.0/8,::1/128", "172.18.0.3/32"},
		{"IPv6 proxy", "[2001:db8::10]:2345", "127.0.0.0/8", "2001:db8::10/128"},
		{"mapped IPv4", "[::ffff:172.18.0.3]:2345", "", "172.18.0.3/32"},
		{"already covered", "172.18.0.3:2345", "172.18.0.0/24", "172.18.0.3/32"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, settings := settingsService(t)
			activeManual := service.app.activeSettings().WebTrustedProxies
			settings.WebTrustedProxies = tc.trusted
			if err := service.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			transport := probeRoundTripper(func(r *http.Request) (*http.Response, error) {
				incoming := httptest.NewRequest("GET", "http://chat.example.test"+r.URL.Path, nil)
				incoming.RemoteAddr = tc.remote
				incoming.Header.Set("X-Forwarded-Proto", "https")
				incoming.Header.Set("X-Forwarded-For", "198.51.100.2")
				out := httptest.NewRecorder()
				ordinaryWebHandler(service.app).ServeHTTP(out, incoming)
				return out.Result(), nil
			})
			if err := service.detectWebProxy(t.Context(), "https://chat.example.test", transport); err != nil {
				t.Fatal(err)
			}
			saved, _ := service.Store.Settings()
			if saved.WebTrustedProxies != tc.trusted || saved.WebDetectedProxy != tc.want || service.app.activeSettings().WebDetectedProxy != tc.want {
				t.Fatal("detection changed the wrong proxy network", saved)
			}
			if service.app.activeSettings().WebTrustedProxies != activeManual {
				t.Fatal("detection applied an unrelated pending manual trust edit")
			}
		})
	}
}

func waitWebProxyDetection(t *testing.T, service *Service, ready func(Settings, ServiceStatus) bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		settings, err := service.Store.Settings()
		if err != nil {
			t.Fatal(err)
		}
		if ready(settings, service.Status()) {
			return
		}
		select {
		case <-deadline:
			t.Fatal("startup detection did not finish", service.Status())
		case <-ticker.C:
		}
	}
}

func TestWebProxyStartupReplacesOnlyAutomaticallyDetectedPeer(t *testing.T) {
	service, settings := settingsService(t)
	settings.PublicURL = "https://chat.example.test"
	settings.WebTrustedProxies = "198.51.100.0/24"
	settings.WebDetectedOrigin, settings.WebDetectedProxy = settings.PublicURL, "192.0.2.10/32"
	if err := service.Store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	service.webProxyTransport = probeRoundTripper(func(r *http.Request) (*http.Response, error) {
		peer := fmt.Sprintf("192.0.2.%d:1234", 10+calls.Add(1))
		incoming := httptest.NewRequest("GET", "http://chat.example.test"+r.URL.Path, nil)
		incoming.RemoteAddr = peer
		incoming.Header.Set("X-Forwarded-Proto", "https")
		incoming.Header.Set("X-Forwarded-For", "203.0.113.2")
		out := httptest.NewRecorder()
		ordinaryWebHandler(service.app).ServeHTTP(out, incoming)
		return out.Result(), nil
	})
	for _, want := range []string{"192.0.2.11/32", "192.0.2.12/32"} {
		if err := service.Restart(); err != nil {
			t.Fatal(err)
		}
		waitWebProxyDetection(t, service, func(s Settings, _ ServiceStatus) bool { return s.WebDetectedProxy == want })
		for _, peer := range []string{"192.0.2.10", "192.0.2.11", "192.0.2.12", "198.51.100.7"} {
			r := httptest.NewRequest("GET", "http://chat.example.test/api/me", nil)
			r.RemoteAddr = peer + ":1234"
			r.Header.Set("X-Forwarded-Proto", "https")
			if service.app.allowWebTransport(r) != (want == peer+"/32" || peer == "198.51.100.7") {
				t.Fatal("startup retained a stale auto peer or lost manual trust", peer, want)
			}
		}
	}
	// A stale UI save preserves machine-owned metadata and does not start a
	// third probe. Subsequent checks require an explicit action or entry start.
	settings.WebDetectedProxy = "203.0.113.1/32"
	settings.IdleMinutes++
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	saved, _ := service.Store.Settings()
	if saved.WebDetectedProxy != "192.0.2.12/32" || saved.WebTrustedProxies != settings.WebTrustedProxies || calls.Load() != 2 {
		t.Fatal("settings save overwrote verified trust or started another probe", saved, calls.Load())
	}
	// The auto peer is not valid for a different public origin.
	saved.PublicURL = "https://other.example.test"
	networks, err := webProxyNetworks(saved)
	if err != nil || trustsWebProxy(networks, netip.MustParseAddr("192.0.2.12")) {
		t.Fatal("detected trust escaped its verified origin", err)
	}
}

func TestWebProxyStartupFailureKeepsLocalServiceAndSavedTrust(t *testing.T) {
	service, settings := settingsService(t)
	settings.PublicURL = "https://chat.example.test"
	settings.WebDetectedOrigin, settings.WebDetectedProxy = settings.PublicURL, "192.0.2.10/32"
	if err := service.Store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	service.webProxyTransport = probeRoundTripper(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("unavailable test tunnel")
	})
	if err := service.Restart(); err != nil {
		t.Fatal("optional detection blocked local startup", err)
	}
	waitWebProxyDetection(t, service, func(_ Settings, status ServiceStatus) bool { return status.WebError == webProxyFailed })
	saved, _ := service.Store.Settings()
	if saved != settings {
		t.Fatal("failed startup detection changed saved configuration")
	}
	local, _ := service.LocalWebURL()
	requireServiceHealth(t, local)
}

func TestWebProxyStartupProbeStopsWithItsEntry(t *testing.T) {
	service, settings := settingsService(t)
	settings.PublicURL = "https://chat.example.test"
	if err := service.Store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	entered, finished := make(chan struct{}), make(chan struct{})
	service.webProxyTransport = probeRoundTripper(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		close(finished)
		return nil, r.Context().Err()
	})
	if err := service.Restart(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not begin detection")
	}
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("stopping Web left its detection request running")
	}
}

func TestSavingPublicURLAutomaticallyDetectsAndCancelsOlderAddress(t *testing.T) {
	service, settings := settingsService(t)
	first := make(chan struct{})
	var calls atomic.Int64
	service.webProxyTransport = probeRoundTripper(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(first)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		incoming := httptest.NewRequest("GET", "http://"+r.URL.Host+r.URL.Path, nil)
		incoming.RemoteAddr = "192.0.2.10:1234"
		incoming.Header.Set("X-Forwarded-Proto", "https")
		incoming.Header.Set("X-Forwarded-For", "203.0.113.2")
		out := httptest.NewRecorder()
		ordinaryWebHandler(service.app).ServeHTTP(out, incoming)
		return out.Result(), nil
	})
	app, ingress := service.app, service.web
	settings.PublicURL = "https://old.example.test"
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
	case <-time.After(3 * time.Second):
		t.Fatal("saving the address did not begin detection")
	}
	if !service.Status().WebDetecting {
		t.Fatal("automatic detection does not report progress")
	}
	settings.PublicURL = "https://new.example.test"
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	waitWebProxyDetection(t, service, func(s Settings, status ServiceStatus) bool {
		return s.WebDetectedOrigin == settings.PublicURL && !status.WebDetecting
	})
	if service.app != app || service.web != ingress || calls.Load() != 2 || len(service.Status().PendingChanges) != 0 {
		t.Fatal("address detection restarted the service or applied a stale result", service.Status())
	}
}

func TestSavingPublicURLWhileStoppedWaitsForStart(t *testing.T) {
	service, settings := settingsService(t)
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	service.webProxyTransport = probeRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		incoming := httptest.NewRequest("GET", "http://"+r.URL.Host+r.URL.Path, nil)
		incoming.RemoteAddr = "192.0.2.10:1234"
		incoming.Header.Set("X-Forwarded-Proto", "https")
		incoming.Header.Set("X-Forwarded-For", "203.0.113.2")
		out := httptest.NewRecorder()
		ordinaryWebHandler(service.app).ServeHTTP(out, incoming)
		return out.Result(), nil
	})
	settings.PublicURL = "https://chat.example.test"
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || service.Status().WebDetecting {
		t.Fatal("offline configuration started a listener or attempted detection")
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	waitWebProxyDetection(t, service, func(s Settings, status ServiceStatus) bool {
		return s.WebDetectedOrigin == settings.PublicURL && !status.WebDetecting
	})
	if calls.Load() != 1 {
		t.Fatal("startup did not perform exactly one detection", calls.Load())
	}
}

func TestDetectWebProxyRejectsRedirectReflectionAndWrongServer(t *testing.T) {
	for _, mode := range []string{"redirect", "reflect", "wrong-server", "alter-response"} {
		t.Run(mode, func(t *testing.T) {
			service, before := settingsService(t)
			var requests int
			proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				switch mode {
				case "redirect":
					http.Redirect(w, r, "/elsewhere", http.StatusFound)
				case "reflect":
					_, _ = io.WriteString(w, strings.TrimPrefix(r.URL.Path, webProxyProbePath))
				case "alter-response":
					incoming := httptest.NewRequest("GET", "http://"+r.Host+r.URL.Path, nil)
					incoming.Host = r.Host
					incoming.RemoteAddr = "192.0.2.10:1234"
					incoming.Header = http.Header{"X-Forwarded-Proto": {"https"}, "X-Forwarded-For": {"198.51.100.2"}}
					out := httptest.NewRecorder()
					ordinaryWebHandler(service.app).ServeHTTP(out, incoming)
					if out.Code != 200 {
						t.Error("probe did not reach its handler", out.Code)
					}
					_, _ = io.WriteString(w, "replaced")
				default:
					_, _ = io.WriteString(w, "ok")
				}
			}))
			t.Cleanup(proxy.Close)
			if err := service.detectWebProxy(t.Context(), proxy.URL, proxy.Client().Transport); err == nil {
				t.Fatal("unverified address accepted")
			}
			if requests != 1 {
				t.Fatal("detection followed a redirect", requests)
			}
			after, _ := service.Store.Settings()
			if after != before || service.app.webProxyProbe != nil {
				t.Fatal("unverified address changed configuration")
			}
		})
	}
}

type probeRoundTripper func(*http.Request) (*http.Response, error)

func (f probeRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDetectWebProxyConcurrentSettingsAndLifecycle(t *testing.T) {
	for _, change := range []string{"public URL", "trust", "listener", "stop", "restart", "cancel", "concurrent", "unrelated"} {
		t.Run(change, func(t *testing.T) {
			service, _ := settingsService(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			transport := probeRoundTripper(func(r *http.Request) (*http.Response, error) {
				incoming := httptest.NewRequest("GET", "http://chat.example.test"+r.URL.Path, nil)
				incoming.RemoteAddr = "192.0.2.10:1234"
				incoming.Header.Set("X-Forwarded-Proto", "https")
				incoming.Header.Set("X-Forwarded-For", "198.51.100.2")
				out := httptest.NewRecorder()
				ordinaryWebHandler(service.app).ServeHTTP(out, incoming)
				close(entered)
				<-release
				return out.Result(), nil
			})
			done := make(chan error, 1)
			go func() { done <- service.detectWebProxy(ctx, "https://chat.example.test", transport) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("probe not started")
			}
			settings, _ := service.Store.Settings()
			switch change {
			case "public URL":
				settings.PublicURL = "https://new.example.test"
			case "trust":
				settings.WebTrustedProxies = "198.51.100.2/32"
			case "listener":
				settings.Port = availableServicePort(t)
			case "unrelated":
				settings.IdleMinutes++
			case "stop":
				if err := service.Stop(); err != nil {
					t.Fatal(err)
				}
			case "restart":
				if err := service.ApplyServiceChanges("web"); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			case "concurrent":
				if err := service.detectWebProxy(t.Context(), "https://chat.example.test", transport); err == nil || err.Error() != webProxyBusy {
					t.Fatal("concurrent detection was allowed", err)
				}
			}
			if err := service.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			unblock()
			err := <-done
			if (err == nil) != (change == "concurrent" || change == "unrelated") {
				t.Fatal("stale detection result applied", err)
			}
			if change == "unrelated" {
				saved, _ := service.Store.Settings()
				if saved.IdleMinutes != settings.IdleMinutes || service.runner.IdleTimeout != time.Duration(settings.IdleMinutes)*time.Minute || service.Status().RestartRequired {
					t.Fatal("detection discarded an immediately applied concurrent edit")
				}
			}
			if err != nil {
				saved, _ := service.Store.Settings()
				if saved != settings {
					t.Fatal("detection overwrote a newer setting")
				}
			}
		})
	}
}

func TestWebProxyProbeOnlyPermitsOneActiveOrdinaryEntryCallback(t *testing.T) {
	a := &App{settings: DefaultSettings()}
	probe := &webProxyProbe{path: webProxyProbePath + "random", origin: "https://chat.example.test", proof: "independent-proof", expires: time.Now().Add(time.Minute)}
	a.webProxyProbe = probe
	request := func(method, path string, tlsEntry bool, handler http.Handler, want int) {
		t.Helper()
		r := httptest.NewRequest(method, "http://chat.example.test"+path, nil)
		r.RemoteAddr = "192.0.2.10:1234"
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "198.51.100.2")
		if tlsEntry {
			r.TLS = &tls.ConnectionState{}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want || len(w.Result().Cookies()) != 0 {
			t.Fatalf("%s %s: got %d, want %d", method, path, w.Code, want)
		}
	}
	web := ordinaryWebHandler(a)
	request("GET", probe.path, false, a, 404)
	request("GET", probe.path, true, web, 404)
	request("POST", probe.path, false, web, 404)
	request("GET", probe.path+"?x=1", false, web, 404)
	request("GET", webProxyProbePath+"wrong", false, web, 404)
	request("GET", "/api/me", false, web, 403)
	request("GET", probe.path, false, web, 200)
	if probe.peer != netip.MustParseAddr("192.0.2.10") {
		t.Fatal("probe trusted the forwarded client rather than its socket peer")
	}
	request("GET", probe.path, false, web, 404)
	probe.completed = false
	probe.expires = time.Now().Add(-time.Second)
	request("GET", probe.path, false, web, 404)
	a.webProxyProbe = nil
	request("GET", probe.path, false, web, 404)
}

func TestDetectWebProxyRequiresRunningWebAndHTTPSOrigin(t *testing.T) {
	service, _ := settingsService(t)
	for _, address := range []string{"", "http://chat.example.test", "https://user:password@chat.example.test", "https://chat.example.test/path", "https://chat.example.test?", "https://chat.example.test#fragment"} {
		if err := service.DetectWebProxy(t.Context(), address); err == nil || err.Error() != webProxyNeedsHTTPS {
			t.Fatal("invalid origin accepted", address, err)
		}
	}
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := service.DetectWebProxy(t.Context(), "https://chat.example.test"); err == nil || err.Error() != webProxyNeedsWeb {
		t.Fatal("detection ran without Web access", err)
	}
}
