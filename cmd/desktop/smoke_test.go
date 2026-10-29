package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSmokeReleaseHistoryFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	for _, body := range []string{`{`, `{}`, `{"current_version":"1.0.0","target_version":"2.0.0","releases":[]}`} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSmokeReleaseHistory(path); err == nil {
			t.Fatal("invalid smoke fixture accepted:", body)
		}
	}
	if err := os.WriteFile(path, []byte(`{"current_version":"1.0.0","target_version":"2.0.0","releases":[{"version":"2.0.0"},{"version":"1.1.0"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	history, err := loadSmokeReleaseHistory(path)
	if err != nil || history.CurrentVersion != "1.0.0" || len(history.Releases) != 2 {
		t.Fatalf("smoke fixture lost release history: %+v, %v", history, err)
	}
}

func TestReleaseProbeChecksHealthAndEmbeddedPWA(t *testing.T) {
	var broken, missingVersion atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.Write([]byte(`{"ok":true}`))
		case "/":
			if !broken.Load() {
				w.Write([]byte("<title>Spool · Chat</title>"))
			}
		case "/manifest.webmanifest":
			w.Write([]byte(`{"name":"Spool"}`))
		case "/api/version":
			if missingVersion.Load() {
				http.NotFound(w, r)
			} else {
				w.Write([]byte(`{"version":"9.8.7"}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	if err := probeWeb(context.Background(), address); err != nil {
		t.Fatal(err)
	}
	missingVersion.Store(true)
	if err := probeWeb(context.Background(), address); err == nil {
		t.Fatal("missing current version passed release probe")
	}
	missingVersion.Store(false)
	broken.Store(true)
	if err := probeWeb(context.Background(), address); err == nil {
		t.Fatal("empty embedded frontend passed release probe")
	}
}
