package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReleaseProbeChecksHealthAndEmbeddedPWA(t *testing.T) {
	var broken atomic.Bool
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
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	if err := probeWeb(context.Background(), address); err != nil {
		t.Fatal(err)
	}
	broken.Store(true)
	if err := probeWeb(context.Background(), address); err == nil {
		t.Fatal("empty embedded frontend passed release probe")
	}
}
