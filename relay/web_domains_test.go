package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebDomainAssignmentsAndValidation(t *testing.T) {
	id, _ := NewIdentity()
	address, err := WebURL("https://relay.example:8443", id.ID())
	if err != nil || address != "https://"+id.ID()+".web.relay.example:8443/" || !ValidateWebURL(address, id.ID()) {
		t.Fatal(address, err)
	}
	for _, origin := range []string{"http://relay.example", "https://example", "https://*.example.com", "https://bad.example/path", "https://127.0.0.1", "https://[::1]", "https://example..com", "https://-bad.example", "https://" + strings.Repeat("a.", 97) + "com"} {
		if _, err := WebURL(origin, id.ID()); err == nil {
			t.Fatal("accepted origin for Web publishing", origin)
		}
	}
	for _, bad := range []string{strings.Replace(address, "https:", "http:", 1), address + "api/me", strings.Replace(address, id.ID(), strings.Repeat("a", 52), 1), address + "?token=x", strings.Replace(address, "https://", "https://user:pass@", 1), strings.Replace(address, ".web.", ".", 1), strings.Replace(address, ".web.", ".custom.", 1)} {
		if ValidateWebURL(bad, id.ID()) {
			t.Fatal("accepted assignment", bad)
		}
	}
}

func TestWebPublishingProxyPolicyPersistsAndRevokes(t *testing.T) {
	s := testBroker(t)
	public := webPublicServer(t, s)
	s.opts.TraefikToken = strings.Repeat("p", 32)
	registration, token := createViaAPI(t, s, "proxy policy")
	id, _ := NewIdentity()
	request := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.ProxyHandler().ServeHTTP(w, r)
		return w
	}
	domain, _ := url.Parse(testTenantURL(t, public.URL, id.ID()))
	ask := "/v1/tls/allow?domain=" + domain.Hostname()
	if w := request(ask, ""); w.Code != 403 {
		t.Fatal("unpublished hostname approved", w.Code)
	}
	_, stop := startWebTestHost(t, public, token, id, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("tenant")) }))
	if w := request(ask, ""); w.Code != 200 {
		t.Fatal("published hostname rejected", w.Code, w.Body)
	}
	for _, path := range []string{ask + "&domain=evil.example", ask + "&extra=1", "/v1/tls/allow?domain=evil.example", "/v1/settings"} {
		if w := request(path, ""); w.Code == 200 {
			t.Fatal("approved unknown request", path)
		}
	}
	stop()
	if w := request(ask, ""); w.Code != 200 {
		t.Fatal("offline server lost certificate authorization")
	}
	if err := s.setWebBlocked(registration.ID, true); err != nil {
		t.Fatal(err)
	}
	if w := request(ask, ""); w.Code != 403 {
		t.Fatal("administratively blocked domain approved")
	}
	if err := s.setWebBlocked(registration.ID, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewServer(s.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	w := httptest.NewRecorder()
	loaded.ProxyHandler().ServeHTTP(w, httptest.NewRequest("GET", ask, nil))
	if w.Code != 200 {
		t.Fatal("restart lost publishing intent", w.Code)
	}
	if w := request("/v1/traefik", ""); w.Code != 401 {
		t.Fatal("config exposed without credential")
	}
	w = request("/v1/traefik", s.opts.TraefikToken)
	var config struct {
		HTTP struct {
			Routers map[string]struct {
				Rule string
				TLS  map[string]any
			}
		}
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &config) != nil || len(config.HTTP.Routers) != 2 {
		t.Fatal(w.Code, w.Body)
	}
	for name, router := range config.HTTP.Routers {
		if !strings.Contains(router.Rule, domain.Hostname()) {
			t.Fatal("wrong host", router.Rule)
		}
		if strings.HasSuffix(name, "-http") {
			if router.TLS != nil {
				t.Fatal("HTTP redirect router must not enable TLS", name, router)
			}
		} else if router.TLS["certResolver"] != "letsencrypt" {
			t.Fatal("individual router must explicitly select its own resolver", name, router)
		}
	}
	opts := s.opts
	opts.TraefikResolver = "spool_dns-01"
	custom, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer custom.Close()
	w = httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/traefik", nil)
	r.Header.Set("Authorization", "Bearer "+opts.TraefikToken)
	custom.ProxyHandler().ServeHTTP(w, r)
	config.HTTP.Routers = nil
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &config) != nil || len(config.HTTP.Routers) != 2 {
		t.Fatal(w.Code, w.Body)
	}
	for name, router := range config.HTTP.Routers {
		if !strings.HasSuffix(name, "-http") && router.TLS["certResolver"] != opts.TraefikResolver {
			t.Fatal("custom resolver not applied", name, router)
		}
	}
	opts.TraefikResolver = "none"
	existing, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer existing.Close()
	w = httptest.NewRecorder()
	existing.ProxyHandler().ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "certResolver") {
		t.Fatal("existing certificate mode requests individual certificates")
	}
	config.HTTP.Routers = nil
	if err := json.Unmarshal(w.Body.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	for name, router := range config.HTTP.Routers {
		if !strings.HasSuffix(name, "-http") && (router.TLS == nil || len(router.TLS) != 0) {
			t.Fatal("existing certificate router must suppress entrypoint resolver", name, router)
		}
	}
	if err := s.RevokeRegistration(registration.ID); err != nil {
		t.Fatal(err)
	}
	w = request("/v1/traefik", s.opts.TraefikToken)
	json.Unmarshal(w.Body.Bytes(), &config)
	var revoked map[string]any
	json.Unmarshal(w.Body.Bytes(), &revoked)
	if strings.Contains(w.Body.String(), domain.Hostname()) {
		t.Fatal("revocation retained route")
	}
}

func TestDeploymentSettingsOverrideLegacyStore(t *testing.T) {
	for _, origin := range []string{"https://new.example.com", ""} {
		t.Run("origin="+origin, func(t *testing.T) {
			s := testBroker(t)
			saved := map[string]any{"version": 3, "public_url": "https://old.example.com", "web_domain": "old.example.com", "certificate_mode": "wildcard", "trusted_proxies": []string{"10.0.0.0/24"}, "detected_origin": "https://old.example.com", "detected_proxy": "172.18.0.3/32", "detected_at": time.Now().UTC()}
			data, _ := json.Marshal(saved)
			path := filepath.Join(s.opts.DataDir, "settings.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			opts := s.opts
			opts.PublicURL = origin
			loaded, err := NewServer(opts)
			if err != nil {
				t.Fatal(err)
			}
			defer loaded.Close()
			status := loaded.settingsStatus()
			wantDomain := ""
			if origin != "" {
				wantDomain = "web.new.example.com"
			}
			if status.PublicURL != origin || status.WebDomain != wantDomain || status.DetectedAt != nil || status.DetectedProxy != "" || len(status.TrustedProxies) != 1 {
				t.Fatal("deployment did not replace old choices", status)
			}
			for _, input := range []string{`{"public_url":"https://attacker.example"}`, `{"web_domain":"other.example"}`, `{"certificate_mode":"individual"}`} {
				w := adminRequest(t, loaded, "PUT", "/v1/settings", testAdminToken, input)
				if w.Code != 400 {
					t.Fatal("API changed deployment setting", w.Code, w.Body)
				}
			}
			if err := loaded.updateSettings(settingsInput{TrustedProxies: proxiesptr([]string{"192.0.2.1/32"})}); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var store map[string]any
			if json.Unmarshal(data, &store) != nil || store["version"] != float64(4) {
				t.Fatal("bad runtime store", string(data))
			}
			for _, key := range []string{"public_url", "web_domain", "certificate_mode"} {
				if _, ok := store[key]; ok {
					t.Fatal("deployment persisted in runtime store", key)
				}
			}
			for _, invalid := range []ServerOptions{{PublicURL: "http://invalid.example"}, {PublicURL: "https://invalid.example/path"}} {
				invalid.AdminToken, invalid.DataDir = opts.AdminToken, opts.DataDir
				if server, err := NewServer(invalid); err == nil {
					server.Close()
					t.Fatal("stored state hid invalid deployment")
				}
			}
		})
	}
}

func TestTraefikCredentialCannotUseAdministratorToken(t *testing.T) {
	if server, err := NewServer(ServerOptions{AdminToken: strings.Repeat("a", 32), TraefikToken: strings.Repeat("a", 32), DataDir: t.TempDir()}); err == nil {
		server.Close()
		t.Fatal("administrator credential reused for proxy discovery")
	}
}

func TestTraefikResolverValidation(t *testing.T) {
	for _, name := range []string{"spool.dns", "resolver@file", "space name", "bad\nname", "通配"} {
		if server, err := NewServer(ServerOptions{AdminToken: strings.Repeat("a", 32), TraefikResolver: name, DataDir: t.TempDir()}); err == nil {
			server.Close()
			t.Fatal("invalid resolver accepted", name)
		}
	}
}

func TestOldSharedMountAndHostConfusionRejected(t *testing.T) {
	broker, host, server, address := webFixture(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("tenant")) }))
	for _, target := range []string{server.URL + WebPath(host.Identity.ID()), address + "web/" + host.Identity.ID() + "/", strings.Replace(address, host.Identity.ID(), "unknown", 1)} {
		response, err := server.Client().Get(target)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == 200 {
			t.Fatal("shared/unknown route served content", target)
		}
		if strings.Contains(response.Header.Get("Clear-Site-Data"), "cookies") {
			t.Fatal("legacy migration may clear sibling hosts' session cookies")
		}
	}
	r := httptest.NewRequest("POST", address+"api/login", nil)
	r.Header.Set("Origin", strings.Replace(strings.TrimSuffix(address, "/"), host.Identity.ID(), strings.Repeat("a", 52), 1))
	r.Header.Set("Sec-Fetch-Site", "same-site")
	w := httptest.NewRecorder()
	broker.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("sibling-origin request accepted", w.Code)
	}
	r = httptest.NewRequest("GET", address, nil)
	r.Header.Add("Origin", strings.TrimSuffix(address, "/"))
	r.Header.Add("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	broker.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("duplicate Origin accepted")
	}
}

func TestDeploymentWebDomainDerivedFromPublicHost(t *testing.T) {
	for _, tc := range []struct {
		name, origin, domain string
	}{
		{"standard", "https://relay.example.com", "web.relay.example.com"},
		{"with-port", "https://relay.example.com:8443", "web.relay.example.com"},
		{"nested", "https://relay.region.example.com", "web.relay.region.example.com"},
		{"normalized", " HTTPS://Relay.Example.COM:8443/ ", "web.relay.example.com"},
		{"web-prefix", "https://web.example.com", "web.web.example.com"},
		{"native-only", "", ""},
		{"IPv4", "https://127.0.0.1:8443", ""},
		{"IPv6", "https://[::1]:8443", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewServer(ServerOptions{AdminToken: testAdminToken, DataDir: t.TempDir(), PublicURL: tc.origin})
			if err != nil {
				t.Fatal("unexpected deployment error", err)
			}
			defer s.Close()
			if status := s.settingsStatus(); status.WebDomain != tc.domain {
				t.Fatal("unexpected derived Web domain", status.WebDomain, tc.domain)
			}
			origin := s.settingsStatus().PublicURL
			if origin == "" {
				return
			}
			for path, want := range map[string]int{"/healthz": 200, "/v1/register": 401, "/v1/host": 401, "/v1/unregister": 401} {
				w := httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest("GET", origin+path, nil))
				if w.Code != want {
					t.Fatal("tenant suffix intercepted native entry", path, w.Code, w.Body)
				}
			}
			id, _ := NewIdentity()
			address, err := WebURL(origin, id.ID())
			if tc.domain == "" {
				if err == nil {
					t.Fatal("published a Web URL without a DNS domain", address)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", address+"healthz", nil))
			if w.Code != 503 {
				t.Fatal("tenant domain escaped into native health check", w.Code)
			}
		})
	}
}

func TestWebBlockSurvivesReenrollmentAndCredentialReplacement(t *testing.T) {
	for _, automatic := range []bool{true, false} {
		mode := "replacement-token"
		if automatic {
			mode = "automatic-reenrollment"
		}
		t.Run(mode, func(t *testing.T) {
			s := testBrokerMode(t, automatic)
			public := webPublicServer(t, s)
			id, _ := NewIdentity()
			var token string
			if !automatic {
				_, token = createViaAPI(t, s, "original")
			}
			token, err := Register(t.Context(), public.URL, token, id, public.Client())
			if err != nil {
				t.Fatal(err)
			}
			if err := s.setWebBlocked(s.registrationStatuses("")[0].ID, true); err != nil {
				t.Fatal(err)
			}
			reenroll := func(broker *Server, server *httptest.Server) {
				t.Helper()
				if automatic {
					if err := Unregister(t.Context(), server.URL, token, id, server.Client()); err != nil {
						t.Fatal(err)
					}
					token = ""
				} else {
					_, token = createViaAPI(t, broker, "replacement")
				}
				var err error
				token, err = Register(t.Context(), server.URL, token, id, server.Client())
				if err != nil {
					t.Fatal(err)
				}
			}
			reenroll(s, public)
			s.Close()
			restored, err := NewServer(s.opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { restored.Close() })
			server := webPublicServer(t, restored)
			var current registrationStatus
			for _, record := range restored.registrationStatuses("") {
				if record.RevokedAt == nil && record.UnregisteredAt == nil {
					current = record
				}
			}
			if !current.WebBlocked {
				t.Fatal("reenrollment or restart cleared the identity restriction", current)
			}
			host, err := NewHost(t.Context(), server.URL, token, id, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close()
			host.WebEnabled = true
			connection, _, err := host.handshake("/v1/host")
			if err != nil {
				t.Fatal("Web restriction blocked native connectivity", err)
			}
			defer connection.CloseNow()
			if host.webURL != "" {
				t.Fatal("blocked identity republished Web", host.webURL)
			}
			address := testTenantURL(t, server.URL, id.ID())
			u, _ := url.Parse(address)
			ask := "/v1/tls/allow?domain=" + u.Hostname()
			w := httptest.NewRecorder()
			restored.ProxyHandler().ServeHTTP(w, httptest.NewRequest("GET", ask, nil))
			if w.Code != 403 {
				t.Fatal("blocked identity obtained certificate authorization", w.Code)
			}
			if err := restored.setWebBlocked(current.ID, false); err != nil {
				t.Fatal(err)
			}
			connection.CloseNow()
			reenroll(restored, server)
			for _, record := range restored.registrationStatuses("") {
				if record.WebBlocked {
					t.Fatal("administrator allow left a historical identity restriction", record)
				}
			}
			_, stop := startWebTestHost(t, server, token, id, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "published") }))
			defer stop()
			response, err := server.Client().Get(address)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("administrator could not restore publishing", response.StatusCode)
			}
		})
	}
}
