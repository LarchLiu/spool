package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"spool/internal/workspace"
)

func TestDesktopDevFrontendIsOptInAndNeverEnabledInProduction(t *testing.T) {
	for _, tc := range []struct {
		development bool
		address     string
	}{{false, "http://127.0.0.1:5173"}, {false, "%"}, {true, ""}} {
		frontend, err := startDesktopDevFrontend(tc.development, tc.address, nil)
		if err != nil || frontend != nil {
			t.Fatalf("unexpected dev frontend: %+v, %v", frontend, err)
		}
		frontend.close()
	}
	for _, address := range []string{
		"%", "/", "//localhost:5173", "file:///tmp/index.html", "javascript:alert(1)",
		"http://example.com:5173", "http://127.0.0.1.evil.example:5173", "http://0.0.0.0:5173",
		"http://user@localhost:5173", "http://localhost:5173/path", "http://localhost:5173/%2f",
		"http://localhost:5173?", "http://localhost:5173?dev=1", "http://localhost:5173#", "http://localhost:5173#dev",
	} {
		frontend, err := startDesktopDevFrontend(true, address, nil)
		if err == nil || frontend != nil {
			frontend.close()
			t.Errorf("unsafe dev origin accepted: %q", address)
		}
	}
	for _, address := range []string{"http://127.0.0.1:5173", "http://localhost:5173/", "https://[::1]:5173"} {
		frontend, err := startDesktopDevFrontend(true, address, nil)
		if err != nil {
			t.Fatal(err)
		}
		frontend.close()
	}
}

func newTestDevFrontend(t *testing.T, vite string, serviceURL func() (string, error)) *desktopDevFrontend {
	t.Helper()
	frontend, err := startDesktopDevFrontend(true, vite, serviceURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(frontend.close)
	return frontend
}

func devRequest(t *testing.T, client *http.Client, request *http.Request) (int, string) {
	t.Helper()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

func TestDesktopDevFrontendRoutesAssetsAndUsesTheActiveService(t *testing.T) {
	vite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "vite:"+r.URL.RequestURI())
	}))
	t.Cleanup(vite.Close)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"host": r.Host, "origin": r.Header.Get("Origin"), "csrf": r.Header.Get("X-CSRF-Token"),
			"cookie": r.Header.Get("Cookie"), "site": r.Header.Get("Sec-Fetch-Site"), "path": r.URL.RequestURI(),
		})
	}))
	t.Cleanup(backend.Close)
	var active atomic.Value
	active.Store(backend.URL)
	frontend := newTestDevFrontend(t, vite.URL, func() (string, error) {
		if address := active.Load().(string); address != "" {
			return address, nil
		}
		return "", fmt.Errorf("service stopped")
	})
	client := &http.Client{Timeout: 3 * time.Second}
	for _, path := range []string{"/", "/desktop.html", "/@vite/client", "/src/settings-main.tsx?t=1", "/src/style.css"} {
		request, _ := http.NewRequest("GET", frontend.url+path, nil)
		status, body := devRequest(t, client, request)
		if status != 200 || body != "vite:"+path {
			t.Fatalf("asset %q: %d %s", path, status, body)
		}
	}
	for _, path := range []string{"/api", "/api/preferences?test=1", "/healthz"} {
		request, _ := http.NewRequest("POST", frontend.url+path, strings.NewReader(`{"theme":"system"}`))
		request.Header.Set("Origin", frontend.url)
		request.Header.Set("X-CSRF-Token", "fixture-csrf")
		request.Header.Set("Cookie", "spool_session=fixture-session")
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		status, body := devRequest(t, client, request)
		var headers map[string]string
		if err := json.Unmarshal([]byte(body), &headers); err != nil {
			t.Fatal(err, body)
		}
		if status != 200 || headers["host"] != strings.TrimPrefix(frontend.url, "http://") ||
			headers["origin"] != frontend.url || headers["csrf"] != "fixture-csrf" ||
			headers["cookie"] != "spool_session=fixture-session" || headers["site"] != "same-origin" || headers["path"] != path {
			t.Fatalf("API proxy changed auth/CSRF headers or path: %d %+v", status, headers)
		}
	}
	request, _ := http.NewRequest("GET", frontend.url+"/api/me", nil)
	request.Host = "foreign.example"
	if status, _ := devRequest(t, client, request); status != http.StatusForbidden {
		t.Fatal("foreign Host accepted", status)
	}
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "new service")
	}))
	t.Cleanup(second.Close)
	active.Store(second.URL)
	request, _ = http.NewRequest("GET", frontend.url+"/api/me", nil)
	if status, body := devRequest(t, client, request); status != 200 || body != "new service" {
		t.Fatal("API stayed on the previous service port", status, body)
	}
	active.Store("")
	if status, body := devRequest(t, client, request); status != 503 || strings.Contains(body, "vite:") {
		t.Fatal("stopped API fell through to Vite", status, body)
	}
	frontend.close()
	if response, err := client.Get(frontend.url); err == nil {
		response.Body.Close()
		t.Fatal("desktop dev listener survived shutdown")
	}
}

func TestDesktopDevFrontendStreamsSSEWithoutWaitingForCompletion(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: fixture\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(backend.Close)
	frontend := newTestDevFrontend(t, backend.URL, func() (string, error) { return backend.URL, nil })
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(frontend.url + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: fixture\n" {
		t.Fatalf("SSE not flushed: %q, %v", line, err)
	}
}

func TestDesktopDevFrontendTunnelsViteHMRWebSocket(t *testing.T) {
	vite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/?token=fixture" || r.Header.Get("Sec-WebSocket-Protocol") != "vite-hmr" {
			http.Error(w, "invalid HMR request", 400)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nSec-WebSocket-Protocol: vite-hmr\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw)
	}))
	t.Cleanup(vite.Close)
	frontend := newTestDevFrontend(t, vite.URL, nil)
	u, _ := url.Parse(frontend.url)
	conn, err := net.DialTimeout("tcp", u.Host, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = fmt.Fprintf(conn, "GET /?token=fixture HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Protocol: vite-hmr\r\n\r\n", u.Host, frontend.url)
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 101 || response.Header.Get("Sec-WebSocket-Protocol") != "vite-hmr" {
		t.Fatal("HMR upgrade rejected", response.Status)
	}
	// The tunnel forwards bytes in both directions after upgrading.
	_, _ = io.WriteString(conn, "hot update\n")
	line, err := reader.ReadString('\n')
	if err != nil || line != "hot update\n" {
		t.Fatalf("HMR tunnel failed: %q, %v", line, err)
	}
}

func TestDesktopDevFrontendTunnelsWorkspaceWebSocket(t *testing.T) {
	headers := make(chan http.Header, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		copy := r.Header.Clone()
		copy.Set("Host", r.Host)
		copy.Set("Path", r.URL.Path)
		headers <- copy
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		kind, data, err := conn.Read(ctx)
		if err == nil {
			conn.Write(ctx, kind, data)
		}
	}))
	t.Cleanup(backend.Close)
	frontend := newTestDevFrontend(t, backend.URL, func() (string, error) { return backend.URL, nil })
	ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	conn, _, err := websocket.Dial(ctx, frontend.url+"/api/events", &websocket.DialOptions{HTTPHeader: http.Header{
		"Origin": {frontend.url}, "Cookie": {"spool_session=fixture-session"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","csrf":"fixture"}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != `{"type":"hello","csrf":"fixture"}` {
		t.Fatal("workspace socket did not tunnel bidirectionally", string(data), err)
	}
	got := <-headers
	if got.Get("Host") != strings.TrimPrefix(frontend.url, "http://") || got.Get("Origin") != frontend.url || got.Get("Cookie") != "spool_session=fixture-session" || got.Get("Path") != "/api/events" {
		t.Fatal("workspace WebSocket proxy changed auth/origin/path:", got)
	}
}

func TestDesktopDevSettingsTrustOnlyItsExactDocument(t *testing.T) {
	p := &panel{devFrontend: &desktopDevFrontend{url: "http://127.0.0.1:12345"}}
	if p.settingsURL() != "http://127.0.0.1:12345/desktop.html" || !p.settingsDocumentAllowed(p.settingsURL()) {
		t.Fatal("dev settings document rejected")
	}
	for _, address := range []string{
		settingsPageURL, "http://mygo.localhost/desktop.html", "http://127.0.0.1:5173/desktop.html",
		"http://127.0.0.1:12346/desktop.html", "http://localhost:12345/desktop.html",
		p.devFrontend.url + "/", p.devFrontend.url + "/index.html", p.devFrontend.url + "/__desktop/settings",
		p.settingsURL() + "?", p.settingsURL() + "?admin=true", p.settingsURL() + "#", p.settingsURL() + "#settings",
		p.devFrontend.url + "/%64esktop.html", p.settingsURL() + "/",
	} {
		if p.settingsDocumentAllowed(address) {
			t.Errorf("non-settings document accepted: %q", address)
		}
	}
	p.devFrontend = nil
	if p.settingsURL() != settingsPageURL || !p.settingsDocumentAllowed(settingsPageURL) || p.settingsDocumentAllowed("http://127.0.0.1:12345/desktop.html") {
		t.Fatal("embedded settings trust changed outside dev mode")
	}
}

func TestDesktopBindingsGenerationDoesNotOpenWorkspace(t *testing.T) {
	if os.Getenv("SPOOL_TEST_GENERATE") == "1" {
		if err := runDesktop(); err != nil {
			t.Fatal(err)
		}
		return
	}
	root := t.TempDir()
	// The running app already owns the lease when MyGo regenerates bindings.
	service, err := workspace.OpenService(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	client := filepath.Join(t.TempDir(), "mygo.ts")
	command := exec.Command(os.Args[0], "-test.run=^TestDesktopBindingsGenerationDoesNotOpenWorkspace$")
	command.Env = append(os.Environ(), "SPOOL_TEST_GENERATE=1", "SPOOL_HOME="+root, "MYGO_GENERATE="+client)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generation acquired a database lease or started native services: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(client)
	if err != nil || !strings.Contains(string(contents), "DesktopSettings") || !strings.Contains(string(contents), "spool:settings-section") {
		t.Fatal("bindings missing desktop service/events", err, string(contents))
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "db" {
		t.Fatal("generation created logs or other runtime state", entries, err)
	}
}
