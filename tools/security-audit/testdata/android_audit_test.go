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

func TestAuditChangedRouteDoesNotReceiveExistingSession(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "session")
	direct, tail := "https://original.example.test", "https://original.tail.example.test"
	origin := sessionOrigin(t, direct)
	jar, err := newServerSessionJar(directory, origin, direct, tail, "https")
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(origin, []*http.Cookie{sessionCookie("synthetic-audit-secret")})
	jar.close()
	replacement := "https://replacement.example.test"
	changed, err := newServerSessionJar(directory, sessionOrigin(t, replacement), replacement, tail, "https")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	client := &http.Client{Jar: changed, Transport: auditRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "" || r.URL.Host != "replacement.example.test" {
			t.Error("replacement route received credentials or request escaped its origin")
		}
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/healthz" {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}, nil
		}
		return &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"wrong server"}`)), Request: r}, nil
	})}
	// Exercise health and the first API read before any account comparison.
	candidate := &Client{direct: true, origin: sessionOrigin(t, replacement), http: client}
	if err := candidate.probeOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(replacement + "/api/me")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if strings.Join(paths, ",") != "/healthz,/api/me" {
		t.Fatalf("unexpected request sequence: %v", paths)
	}
	t.Log("replacement HTTPS origin receives no old session on /healthz or /api/me")
}
