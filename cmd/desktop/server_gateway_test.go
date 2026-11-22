package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/bcrypt"
	chatmodel "spool/internal/model"
	"spool/internal/workspace"
)

func newGatewayTestUser(t *testing.T, store *workspace.Store, username, nickname string) {
	t.Helper()
	// Seed only this test's disposable database with a cheap hash. Login still
	// verifies bcrypt and creates real sessions through the workspace API.
	hash, err := bcrypt.GenerateFromPassword([]byte("fixture-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", store.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users(id,username,nickname,password_hash,disabled,created_at) VALUES(?,?,?,?,0,?)`, chatmodel.NewID(), username, nickname, string(hash), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func newTestServerGateway(t *testing.T, profiles desktopServers) *desktopServerGateway {
	t.Helper()
	gateway, err := startDesktopServerGateway(nil, profiles)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.close)
	return gateway
}

func TestDesktopServerGatewayIsolatesAccountsOnTheSameHost(t *testing.T) {
	profiles := defaultDesktopServers()
	for _, name := range []string{"First", "Second"} {
		store, err := workspace.OpenStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		newGatewayTestUser(t, store, "alice", name)
		app := workspace.NewApp(store, nil, "", "")
		t.Cleanup(func() { _ = app.Close() })
		server := httptest.NewServer(app)
		t.Cleanup(server.Close)
		profiles, err = profiles.save(desktopServer{Name: name, Address: server.URL, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	gateway := newTestServerGateway(t, profiles)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	for _, server := range profiles.Entries[1:] {
		request, _ := http.NewRequest("POST", gateway.url+desktopServerMount(server)+"api/login", strings.NewReader(`{"username":"alice","password":"fixture-password"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", gateway.url)
		status, body := devRequest(t, client, request)
		if status != 200 {
			t.Fatalf("login %s: %d %s", server.Name, status, body)
		}
	}
	for _, server := range profiles.Entries[1:] {
		request, _ := http.NewRequest("GET", gateway.url+desktopServerMount(server)+"api/me", nil)
		status, body := devRequest(t, client, request)
		var me struct {
			User workspace.User `json:"user"`
			CSRF string         `json:"csrf"`
		}
		if json.Unmarshal([]byte(body), &me) != nil || status != 200 || me.User.Nickname != server.Name {
			t.Fatalf("account leaked across profiles: %d %s", status, body)
		}
		// A CSRF-protected write must still succeed after origin translation.
		request, _ = http.NewRequest("POST", gateway.url+desktopServerMount(server)+"api/preferences", strings.NewReader(`{"theme":"dark","language":"en"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", me.CSRF)
		request.Header.Set("Origin", gateway.url)
		if status, body := devRequest(t, client, request); status != 200 {
			t.Fatalf("remote CSRF contract failed: %d %s", status, body)
		}
	}
	// Editing a destination must start without the old destination's cookie.
	changed := profiles.Entries[1]
	changed.Direct = profiles.Entries[2].Address
	next, err := profiles.save(changed)
	if err != nil {
		t.Fatal(err)
	}
	changed, _ = next.find(changed.ID)
	gateway.update(next)
	request, _ := http.NewRequest("GET", gateway.url+desktopServerMount(changed)+"api/me", nil)
	if status, _ := devRequest(t, client, request); status != 401 {
		t.Fatal("editing destination reused old credentials", status)
	}
	request, _ = http.NewRequest("GET", gateway.url+desktopServerMount(profiles.Entries[1])+"api/me", nil)
	if status, _ := devRequest(t, client, request); status != 404 {
		t.Fatal("old destination stayed routable after edit", status)
	}
}

func TestDesktopServerGatewayKeepsSeparateLoginsForEachConnectionMode(t *testing.T) {
	store, err := workspace.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, name := range []string{"direct", "tailscale", "relay"} {
		newGatewayTestUser(t, store, name, name)
	}
	app := workspace.NewApp(store, nil, "", "")
	t.Cleanup(func() { _ = app.Close() })
	upstream := httptest.NewServer(app)
	t.Cleanup(upstream.Close)
	// All modes intentionally terminate at one fixture, replacing only network
	// routing. Account/session handling runs through the production workspace API.
	profile := desktopServer{ID: "server-1234", Address: upstream.URL + "/", Enabled: true}
	profiles := defaultDesktopServers()
	profiles.Entries = append(profiles.Entries, profile)
	gateway := newTestServerGateway(t, profiles)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second}
	selectMode := func(mode string) string {
		t.Helper()
		profiles.Entries[1].Mode = mode
		gateway.update(profiles)
		return gateway.url + desktopServerMount(profiles.Entries[1]) + "api/"
	}
	for _, mode := range []string{"direct", "tailscale", "relay"} {
		base := selectMode(mode)
		request, _ := http.NewRequest("GET", base+"me", nil)
		if status, _ := devRequest(t, client, request); status != 401 {
			t.Fatalf("%s inherited another mode's login: %d", mode, status)
		}
		request, _ = http.NewRequest("POST", base+"login", strings.NewReader(`{"username":"`+mode+`","password":"wrong-password"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", gateway.url)
		if status, body := devRequest(t, client, request); status != 401 {
			t.Fatalf("%s accepted an incorrect password: %d %s", mode, status, body)
		}
		request, _ = http.NewRequest("GET", base+"me", nil)
		if status, _ := devRequest(t, client, request); status != 401 {
			t.Fatalf("%s created a session for an incorrect password: %d", mode, status)
		}
		request, _ = http.NewRequest("POST", base+"login", strings.NewReader(`{"username":"`+mode+`","password":"fixture-password"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", gateway.url)
		if status, body := devRequest(t, client, request); status != 200 {
			t.Fatal(mode, status, body)
		}
	}
	// Restarting the loopback gateway changes its port, but each browser cookie
	// still belongs to the same stable profile/mode/destination namespace.
	gateway.close()
	gateway = newTestServerGateway(t, profiles)
	for _, mode := range []string{"relay", "direct", "tailscale", "direct"} {
		request, _ := http.NewRequest("GET", selectMode(mode)+"me", nil)
		status, body := devRequest(t, client, request)
		var me struct {
			User workspace.User `json:"user"`
		}
		if json.Unmarshal([]byte(body), &me) != nil || status != 200 || me.User.Username != mode {
			t.Fatalf("%s did not restore its own login: %d %s", mode, status, body)
		}
	}
	request, _ := http.NewRequest("GET", selectMode("direct")+"me", nil)
	_, body := devRequest(t, client, request)
	var me struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal([]byte(body), &me); err != nil {
		t.Fatal(err)
	}
	request, _ = http.NewRequest("POST", selectMode("direct")+"logout", strings.NewReader(`{}`))
	request.Header.Set("Origin", gateway.url)
	request.Header.Set("X-CSRF-Token", me.CSRF)
	if status, body := devRequest(t, client, request); status != 200 {
		t.Fatal("logout", status, body)
	}
	for _, mode := range []string{"direct", "tailscale", "relay"} {
		request, _ := http.NewRequest("GET", selectMode(mode)+"me", nil)
		status, _ := devRequest(t, client, request)
		if mode == "direct" && status != 401 || mode != "direct" && status != 200 {
			t.Fatal("logout affected a different connection mode", mode, status)
		}
	}
}

func TestDesktopServerGatewayMountsRelayPathsAndFiltersCookies(t *testing.T) {
	relayPath := "/web/" + strings.Repeat("a", 52) + "/"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == relayPath+"api/login" {
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "remote-token", Path: relayPath, Secure: true, HttpOnly: true})
			w.Header().Set("Location", relayPath+"api/me")
			w.WriteHeader(http.StatusCreated)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"cookie": r.Header.Get("Cookie"), "path": r.URL.RequestURI(), "origin": r.Header.Get("Origin"), "authorization": r.Header.Get("Authorization")})
	}))
	t.Cleanup(upstream.Close)
	profiles, err := defaultDesktopServers().save(desktopServer{Name: "Relay", Address: upstream.URL + relayPath, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	server := profiles.Entries[1]
	mount := desktopServerMount(server)
	gateway := newTestServerGateway(t, profiles)
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(gateway.url + mount + "api/login")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name == "spool_session" || cookies[0].Path != mount || cookies[0].Domain != "" || cookies[0].Secure || !cookies[0].HttpOnly || response.Header.Get("Location") != mount+"api/me" {
		t.Fatal("cookie or redirect was not scoped to the desktop mount", cookies, response.Header)
	}
	request, _ := http.NewRequest("POST", gateway.url+mount+"api/files?path=a%2Fb", nil)
	request.Header.Set("Origin", gateway.url)
	request.Header.Set("Authorization", "Bearer local-secret")
	request.AddCookie(&http.Cookie{Name: "spool_session", Value: "local-token"})
	request.AddCookie(&http.Cookie{Name: "spool_remote_other_spool_session", Value: "other-token"})
	request.AddCookie(cookies[0])
	status, body := devRequest(t, client, request)
	var got map[string]string
	if json.Unmarshal([]byte(body), &got) != nil || status != 200 || got["cookie"] != "spool_session=remote-token" || got["authorization"] != "" || got["path"] != relayPath+"api/files?path=a%2Fb" || got["origin"] != upstream.URL {
		t.Fatalf("proxy changed mount or leaked identity: %d %+v", status, got)
	}
	for _, kind := range []string{"host", "origin", "cross-site", "forwarded"} {
		request, _ := http.NewRequest("POST", gateway.url+mount+"api/login", nil)
		switch kind {
		case "host":
			request.Host = "foreign.test"
		case "origin":
			request.Header.Set("Origin", "https://foreign.test")
		case "cross-site":
			request.Header.Set("Sec-Fetch-Site", "cross-site")
		case "forwarded":
			request.Header.Set("X-Forwarded-For", "203.0.113.1")
		}
		if status, _ := devRequest(t, client, request); status != 403 {
			t.Fatal("foreign request accepted", kind, status)
		}
	}
	server.Enabled = false
	profiles, _ = profiles.save(server)
	gateway.update(profiles)
	request, _ = http.NewRequest("GET", gateway.url+mount+"api/me", nil)
	if status, _ := devRequest(t, client, request); status != 503 {
		t.Fatal("disabled server still routable", status)
	}
}

func TestDesktopServerGatewayServesDisconnectedSidebarsWithoutContactingDisabledServers(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("disabled profile contacted its server")
	}))
	defer upstream.Close()
	profiles := defaultDesktopServers()
	var err error
	for _, address := range []string{upstream.URL, ""} {
		profiles, err = profiles.save(desktopServer{Direct: address, Enabled: false})
		if err != nil {
			t.Fatal(err)
		}
	}
	gateway := newTestServerGateway(t, profiles)
	client := &http.Client{Timeout: 5 * time.Second}
	for _, server := range profiles.Entries[1:] {
		for _, endpoint := range []string{"", "api/me", "api/login"} {
			request, _ := http.NewRequest("GET", gateway.url+desktopServerMount(server)+endpoint, nil)
			status, body := devRequest(t, client, request)
			if endpoint == "" {
				if status != 200 || !strings.Contains(body, `<div id="root">`) {
					t.Fatal("disabled profile lost its workspace shell", status, body)
				}
			} else if status != 503 || !json.Valid([]byte(body)) {
				t.Fatal("disabled API was not rejected locally", status, body)
			}
		}
	}
}

func TestDesktopServerGatewayServesMermaidScriptsToOpaqueSandbox(t *testing.T) {
	const mount = "/web/fixture/"
	files := fstest.MapFS{}
	for _, name := range []string{"assets/mermaid.min-fixture.js", "assets/mermaid-frame-fixture.js", "assets/chat-fixture.js", "src/mermaid-frame.js", "node_modules/mermaid/dist/mermaid.min.js"} {
		files[name] = &fstest.MapFile{Data: []byte("// " + name)}
	}
	gateway := &desktopServerGateway{
		url: "http://127.0.0.1:12345", assets: http.FileServer(http.FS(files)), development: true,
		routes: map[string]http.Handler{mount: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("sandbox request reached the workspace API")
		})},
	}
	for _, test := range []struct {
		name, path, method, dest, origin string
		status                           int
	}{
		{name: "library", path: mount + "assets/mermaid.min-fixture.js", status: 200},
		{name: "renderer", path: mount + "assets/mermaid-frame-fixture.js", status: 200},
		{name: "head", method: "HEAD", path: mount + "assets/mermaid-frame-fixture.js", status: 200},
		{name: "development renderer", path: "/src/mermaid-frame.js?no-inline", status: 200},
		{name: "development library", path: "/node_modules/mermaid/dist/mermaid.min.js?no-inline", status: 200},
		{name: "workspace API", path: mount + "api/me", status: 403},
		{name: "workspace document", path: mount, dest: "document", status: 403},
		{name: "unrelated script", path: mount + "assets/chat-fixture.js", status: 403},
		{name: "post", method: "POST", path: mount + "assets/mermaid-frame-fixture.js", status: 403},
		{name: "fetch", dest: "empty", path: mount + "assets/mermaid-frame-fixture.js", status: 403},
		{name: "foreign origin", origin: "https://foreign.test", path: mount + "assets/mermaid-frame-fixture.js", status: 403},
		{name: "null origin", origin: "null", path: mount + "assets/mermaid-frame-fixture.js", status: 403},
		{name: "nested path", path: mount + "assets/mermaid-frame-fixture/other.js", status: 403},
		{name: "encoded path", path: mount + "assets/mermaid%2dframe-fixture.js", status: 403},
		{name: "unknown workspace", path: "/web/unknown/assets/mermaid-frame-fixture.js", status: 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = "GET"
			}
			dest := test.dest
			if dest == "" {
				dest = "script"
			}
			request := httptest.NewRequest(method, gateway.url+test.path, nil)
			request.Header.Set("Sec-Fetch-Site", "cross-site")
			request.Header.Set("Sec-Fetch-Dest", dest)
			request.Header.Set("Origin", test.origin)
			response := httptest.NewRecorder()
			gateway.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
			if test.status == 200 && method == "GET" && !strings.HasPrefix(response.Body.String(), "// ") {
				t.Fatal("script body was not served", response.Body.String())
			}
		})
	}
}

func TestDesktopServerGatewayStreamsSSEAndWebSockets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: connected\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		kind, data, err := conn.Read(ctx)
		if err == nil {
			_ = conn.Write(ctx, kind, data)
		}
	}))
	t.Cleanup(upstream.Close)
	profiles, _ := defaultDesktopServers().save(desktopServer{Name: "Stream", Address: upstream.URL, Enabled: true})
	gateway := newTestServerGateway(t, profiles)
	base := gateway.url + desktopServerMount(profiles.Entries[1])
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(base + "api/stream")
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	response.Body.Close()
	if err != nil || line != "data: connected\n" {
		t.Fatal("SSE buffered", line, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, base+"api/events", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {gateway.url}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err = conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != "hello" {
		t.Fatal("WebSocket stopped streaming", string(data), err)
	}
}

func TestDesktopServerGatewayUpdatesRoutesDuringRequests(t *testing.T) {
	profiles, _ := defaultDesktopServers().save(desktopServer{Name: "Offline", Address: "http://127.0.0.1:1", Enabled: true})
	gateway := newTestServerGateway(t, profiles)
	var work sync.WaitGroup
	for range 3 {
		work.Go(func() {
			for range 10 {
				request := httptest.NewRequest("GET", gateway.url+desktopServerMount(profiles.Entries[1])+"logo.png", nil)
				response := httptest.NewRecorder()
				gateway.ServeHTTP(response, request)
				if response.Code != 200 {
					t.Error("offline server lost local client assets", response.Code)
				}
			}
		})
	}
	for range 10 {
		gateway.update(profiles)
	}
	work.Wait()
}

func TestDesktopServerGatewayDevAssetsUseTheFrontendHost(t *testing.T) {
	vite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer conn.CloseNow()
			_ = conn.Write(r.Context(), websocket.MessageText, []byte("vite-hmr:"+r.URL.RequestURI()))
			_, _, _ = conn.Read(r.Context())
			return
		}
		io.WriteString(w, "vite:"+r.URL.RequestURI())
	}))
	t.Cleanup(vite.Close)
	frontend := newTestDevFrontend(t, vite.URL, func() (string, error) {
		t.Error("remote asset requests reached the local service API")
		return "http://127.0.0.1:1", nil
	})
	profiles, err := defaultDesktopServers().save(desktopServer{Direct: "https://remote.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := startDesktopServerGateway(frontend, profiles)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.close)
	mount := desktopServerMount(profiles.Entries[1])
	client := &http.Client{Timeout: 3 * time.Second}
	for _, asset := range []struct{ address, body string }{
		{gateway.url + mount, "vite:/"},
		{gateway.url + mount + "logo.png", "vite:/logo.png"},
		{gateway.url + "/@vite/client", "vite:/@vite/client"},
		{gateway.url + "/src/main.tsx?t=1", "vite:/src/main.tsx?t=1"},
		{frontend.url + "/servers.html", "vite:/servers.html"},
	} {
		request, _ := http.NewRequest("GET", asset.address, nil)
		status, body := devRequest(t, client, request)
		if status != 200 || body != asset.body {
			t.Fatalf("development asset failed: %s: %d %s", asset.address, status, body)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, gateway.url+"/?token=fixture", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {gateway.url}}})
	if err != nil {
		t.Fatal("development HMR upgrade", err)
	}
	defer conn.CloseNow()
	_, message, err := conn.Read(ctx)
	if err != nil || string(message) != "vite-hmr:/?token=fixture" {
		t.Fatal("development HMR did not reach Vite", string(message), err)
	}
}
