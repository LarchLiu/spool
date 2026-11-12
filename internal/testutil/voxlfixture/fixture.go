// Package voxlfixture provides a disposable authenticated helper process. It
// never captures a screen, injects input, downloads code or visits real homes.
package voxlfixture

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"spool/internal/voxl/macapp"

	"github.com/coder/websocket"
)

const helperEnv = "SPOOL_TEST_VOXL_HELPER"

// Run must precede TestMain's normal temporary-home setup: subprocesses own no
// untracked test roots and are reaped by the manager that starts them.
func Run() bool {
	if os.Getenv(helperEnv) != "1" {
		return false
	}
	port, shared, transfers, clipboard := "", "", "", ""
	for _, arg := range os.Args[1:] {
		if arg == "--version" {
			version := os.Getenv("SPOOL_TEST_VOXL_VERSION")
			if version == "" {
				version = "voxl 0.3.3"
			}
			fmt.Println(version)
			os.Exit(0)
		}
		if strings.HasPrefix(arg, "--port=") {
			port = strings.TrimPrefix(arg, "--port=")
		}
		if strings.HasPrefix(arg, "--files-root=") {
			shared = strings.TrimPrefix(arg, "--files-root=")
		}
		if strings.HasPrefix(arg, "--file-transfers=") {
			transfers = strings.TrimPrefix(arg, "--file-transfers=")
		}
		if strings.HasPrefix(arg, "--enable-clipboard=") {
			clipboard = strings.TrimPrefix(arg, "--enable-clipboard=")
		}
	}
	if marker := os.Getenv("SPOOL_TEST_VOXL_STARTED"); marker != "" {
		if err := os.WriteFile(marker, []byte("started"), 0600); err != nil {
			os.Exit(3)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil || port == "" {
		os.Exit(2)
	}
	password := os.Getenv("VOXL_PASSWORD")
	_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if password == "" || r.Header.Get("Authorization") != "Bearer "+password || r.Header.Get("Cookie") != "" {
			http.Error(w, "bad helper credentials", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/ws" {
			if r.Header.Get("Origin") != "http://"+r.Host {
				http.Error(w, "bad helper origin", http.StatusForbidden)
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			for {
				kind, data, err := conn.Read(r.Context())
				if err != nil || conn.Write(r.Context(), kind, data) != nil {
					return
				}
			}
		}
		if r.URL.Path == "/stats" {
			if os.Getenv("SPOOL_TEST_VOXL_NOT_READY") == "1" {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"clients": []string{}, "files_root": shared, "transfers": transfers, "clipboard": clipboard})
			return
		}
		if r.URL.Path == "/files/upload" {
			if r.Method != "PUT" || r.Header.Get("X-Voxl-Upload") != "1" || r.Header.Get("X-CSRF-Token") != "" {
				http.Error(w, "bad upload request", http.StatusBadRequest)
				return
			}
			_, _ = io.Copy(w, r.Body)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "fixture:%s", r.URL.Path)
	}))
	os.Exit(0)
	return true
}

func Install(t *testing.T, root string) {
	t.Helper()
	t.Setenv(helperEnv, "1")
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	name := "voxl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	if runtime.GOOS == "darwin" {
		if err := macapp.Install(t.Context(), root, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}
