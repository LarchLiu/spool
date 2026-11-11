package workspace

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression probes assert the protections for the original audit findings.
// All accounts, network services, cookies, and browser state are disposable.
func TestAuditPlaintextRemoteLoginRejected(t *testing.T) {
	for _, public := range []string{"", "https://public.example.test"} {
		t.Run(fmt.Sprintf("public_https_%t", public != ""), func(t *testing.T) {
			store := testStore(t)
			user := testUser(t, store, "audit")
			token, _, err := store.login(user.Username, "test-password-2026")
			if err != nil {
				t.Fatal(err)
			}
			settings := DefaultSettings()
			settings.Host, settings.PublicURL = "0.0.0.0", public
			if err := store.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			app := NewApp(store, &fakeRunner{}, "", "")
			t.Cleanup(func() { _ = app.Close() })
			r := httptest.NewRequest("POST", "http://192.0.2.10:8787/api/login", strings.NewReader(`{"username":"audit","password":"test-password-2026"}`))
			r.RemoteAddr = "192.0.2.20:45000"
			r.Header.Set("Origin", "http://192.0.2.10:8787")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatalf("plaintext login status=%d", w.Code)
			}
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("plaintext login issued a session")
			}
			read := httptest.NewRequest("GET", "http://192.0.2.10:8787/api/me", nil)
			read.RemoteAddr = r.RemoteAddr
			read.AddCookie(&http.Cookie{Name: authCookie, Value: token})
			out := httptest.NewRecorder()
			app.ServeHTTP(out, read)
			if out.Code != 403 {
				t.Fatalf("session replay status=%d", out.Code)
			}
			t.Log("remote plaintext login and valid HTTP session replay both rejected with 403")
		})
	}
}

func TestAuditProxyLoginLimitSeparatesClients(t *testing.T) {
	store := testStore(t)
	testUser(t, store, "audit")
	app := NewApp(store, &fakeRunner{}, "", "")
	t.Cleanup(func() { _ = app.Close() })
	app.settings.PublicURL = "https://public.example.test"
	for i := 0; i < 20; i++ {
		r := httptest.NewRequest("POST", "http://public.example.test/api/login", nil)
		r.RemoteAddr = fmt.Sprintf("127.0.0.1:%d", 30000+i)
		r.Header.Set("X-Forwarded-For", "192.0.2.1")
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != 415 {
			t.Fatalf("attempt %d status=%d", i, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "http://public.example.test/api/login", strings.NewReader(`{"username":"audit","password":"test-password-2026"}`))
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("X-Forwarded-For", "198.51.100.2")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("other client's login status=%d", w.Code)
	}
	r = httptest.NewRequest("POST", "http://public.example.test/api/login", nil)
	r.RemoteAddr = "127.0.0.1:40001"
	r.Header.Set("X-Forwarded-For", "203.0.113.99, 192.0.2.1")
	r.Header.Set("X-Forwarded-Proto", "https")
	w = httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatalf("exhausted client's spoofed retry status=%d", w.Code)
	}
	t.Log("20 malformed requests exhaust only their client's quota; another client logs in with 200 and the exhausted client remains at 429 despite a forged prefix and changed port")
}

func TestAuditRelayCrossTenantBrowser(t *testing.T) {
	// Reuse the product regression with two HTTPS sibling origins and an
	// attacker-controlled page without Spool's normal CSP. It checks authenticated
	// reads/writes, WebSockets, cookies, storage, Service Workers and offline PWA.
	TestRelayWebBrowser(t)
}
