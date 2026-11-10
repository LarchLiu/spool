package spoolnet

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

type auditRoundTripper func(*http.Request) (*http.Response, error)

func (fn auditRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestAuditChangedRouteReceivesExistingSession(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "session")
	direct, tail := "https://original.example.test", "https://original.tail.example.test"
	origin := sessionOrigin(t, direct)
	jar, err := newServerSessionJar(directory, origin, direct, tail, true)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(origin, []*http.Cookie{sessionCookie("synthetic-audit-secret")})
	jar.close()
	replacement := "https://replacement.example.test"
	changed, err := newServerSessionJar(directory, sessionOrigin(t, replacement), replacement, tail, true)
	if err != nil {
		t.Fatal(err)
	}
	observed := false
	var paths []string
	client := &http.Client{Jar: changed, Transport: auditRoundTripper(func(r *http.Request) (*http.Response, error) {
		cookie, err := r.Cookie("spool_session")
		observed = err == nil && cookie.Value == "synthetic-audit-secret" && r.URL.Host == "replacement.example.test"
		if !observed {
			t.Error("expected the synthetic old session at the replacement route")
		}
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/healthz" {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}, nil
		}
		return &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"wrong server"}`)), Request: r}, nil
	})}
	// Exercise the real health path: it runs before /me and already uses the jar.
	candidate := &Client{direct: true, origin: sessionOrigin(t, replacement), http: client}
	if err := candidate.probeOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(replacement + "/api/me")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !observed {
		t.Fatal("new route did not receive existing session")
	}
	if strings.Join(paths, ",") != "/healthz,/api/me" {
		t.Fatalf("unexpected request sequence: %v", paths)
	}
	t.Log("replacement HTTPS origin receives the old session on /healthz and /api/me before any account comparison")
}
