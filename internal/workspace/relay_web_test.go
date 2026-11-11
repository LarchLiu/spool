package workspace

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	chatmodel "spool/internal/model"
	"spool/relay"
)

type relayWebNode struct {
	store   *Store
	app     *App
	id      *relay.Identity
	host    *relay.Host
	user    User
	project Project
	url     string
}

// Two real Spool applications share a broker origin, with separate identities,
// databases, cookies and PWA mounts. All host traffic uses outbound TLS tunnels.
func relayWebFixture(t *testing.T, count int, trustedProxies ...string) (*relay.Server, *httptest.Server, []*relayWebNode) {
	t.Helper()
	localSessionHomes(t)
	nodes := make([]*relayWebNode, count)
	for i := range nodes {
		store := testStore(t)
		id, err := relay.LoadIdentity(filepath.Join(store.Root(), "relay"))
		if err != nil {
			t.Fatal(err)
		}
		user := testUser(t, store, "alice")
		nodes[i] = &relayWebNode{store: store, id: id, user: user, project: testProject(t, store, user)}
		testAgent(t, store)
	}
	broker, err := relay.NewServer(relay.ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir(), TrustedProxies: trustedProxies})
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(broker)
	t.Cleanup(func() { broker.Close(); public.Close() })
	for _, node := range nodes {
		settings := DefaultSettings()
		settings.RelayURL, settings.RelayEnabled, settings.RelayWebEnabled = public.URL, true, true
		if err := node.store.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		node.app = NewApp(node.store, &fakeRunner{}, "", "")
		t.Cleanup(func() { node.app.Close() })
		_, token, err := broker.CreateRegistration("Web integration")
		if err != nil {
			t.Fatal(err)
		}
		node.host, err = relay.NewHost(t.Context(), public.URL, token, node.id, nil)
		if err != nil {
			t.Fatal(err)
		}
		node.host.WebEnabled = true
		ready := make(chan string, 1)
		node.host.OnWebURL = func(address string) {
			select {
			case ready <- address:
			default:
			}
		}
		cfg, _ := node.id.TLSConfig()
		server := &http.Server{Handler: node.app, ReadHeaderTimeout: 5 * time.Second}
		served, done := make(chan struct{}), make(chan struct{})
		go func() { server.Serve(tls.NewListener(node.host, cfg)); close(served) }()
		go func() { node.host.Run(); node.host.Wait(); close(done) }()
		t.Cleanup(func() {
			node.host.Close()
			server.Close()
			for _, stopped := range []chan struct{}{served, done} {
				select {
				case <-stopped:
				case <-time.After(5 * time.Second):
					t.Error("Web host did not stop")
				}
			}
		})
		select {
		case node.url = <-ready:
			if node.url != public.URL+relay.WebPath(node.id.ID()) {
				t.Fatal("invalid Web URL", node.url)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Web host did not register")
		}
	}
	return broker, public, nodes
}

func TestRelayWebAuthenticationPathsAndLiveTraffic(t *testing.T) {
	_, public, nodes := relayWebFixture(t, 2)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	request := func(node *relayWebNode, method, path, body, csrf, origin string, status int) (*http.Response, []byte) {
		t.Helper()
		r, _ := http.NewRequest(method, node.url+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != status {
			t.Fatalf("%s %s: %d, want %d: %s (%v)", method, path, response.StatusCode, status, data, err)
		}
		return response, data
	}
	csrf := make([]string, 2)
	for i, node := range nodes {
		request(node, "GET", "api/me", "", "", "", 401)
		request(node, "POST", "api/login", `{"username":"alice","password":"test-password-2026"}`, "", "https://foreign.test", 403)
		resp, data := request(node, "POST", "api/login", `{"username":"alice","password":"test-password-2026"}`, "", public.URL, 200)
		var signedIn struct{ CSRF string }
		if err := json.Unmarshal(data, &signedIn); err != nil || signedIn.CSRF == "" {
			t.Fatal("missing CSRF", err)
		}
		csrf[i] = signedIn.CSRF
		if cookies := resp.Cookies(); len(cookies) != 1 || cookies[0].Path != relay.WebPath(node.id.ID()) || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
			t.Fatal("incorrect session cookie", cookies)
		}
		_, data = request(node, "GET", "api/me", "", "", "", 200)
		if !strings.Contains(string(data), node.user.ID) {
			t.Fatal("wrong server session")
		}
		request(node, "POST", "api/logout", `{}`, "wrong", public.URL, 403)
		request(node, "POST", "api/logout", `{}`, csrf[i], "https://foreign.test", 403)
	}
	root, _ := url.Parse(public.URL + "/")
	if len(jar.Cookies(root)) != 0 {
		t.Fatal("relay sessions escaped their paths")
	}
	node := nodes[0]
	chat, err := node.store.CreateChat(node.user.ID, node.project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	// File query encoding survives both proxy layers, and project grants remain
	// enforced by the application rather than by possession of a server ID.
	name := "hello & 中文.txt"
	if err = os.WriteFile(filepath.Join(node.project.Path, name), []byte("relay file"), 0600); err != nil {
		t.Fatal(err)
	}
	_, data := request(node, "GET", "api/projects/"+node.project.ID+"/file?download=1&path="+url.QueryEscape(name), "", "", "", 200)
	if string(data) != "relay file" {
		t.Fatal("file path or payload changed")
	}
	testUser(t, node.store, "bob")
	bob, bobCSRF := loginRequest(t, node.app, "bob")
	r, _ := http.NewRequest("POST", node.url+"api/chats", strings.NewReader(fmt.Sprintf(`{"project_id":%q,"backend":"codex"}`, node.project.ID)))
	r.Header.Set("Origin", public.URL)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", bobCSRF)
	r.AddCookie(bob)
	denied, err := (&http.Client{Timeout: 5 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	denied.Body.Close()
	if denied.StatusCode != 403 {
		t.Fatal("relay bypassed project membership", denied.StatusCode)
	}
	sse, err := client.Get(node.url + "api/chats/" + chat.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer sse.Body.Close()
	line, err := bufio.NewReader(sse.Body).ReadString('\n')
	if err != nil || line != "event: snapshot\n" {
		t.Fatal("chat SSE did not stream", line, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, strings.Replace(node.url, "http:", "ws:", 1)+"api/events", &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Origin": {public.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	socket.SetReadLimit(4 << 20)
	realtimeWrite(t, socket, realtimeInput{Type: "hello", CSRF: csrf[0], Visible: true})
	realtimeReady(t, socket)
	resp, _ := request(node, "POST", "api/logout", `{}`, csrf[0], public.URL, 200)
	if cookies := resp.Cookies(); len(cookies) != 1 || cookies[0].MaxAge != -1 || cookies[0].Path != relay.WebPath(node.id.ID()) {
		t.Fatal("logout did not remove scoped cookie")
	}
	for {
		if _, _, err := socket.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("logout did not close live connection")
			}
			break
		}
	}
	request(node, "GET", "api/me", "", "", "", 401)
	request(nodes[1], "GET", "api/me", "", "", "", 200)
}

func TestRelayWebLoginLimitsSeparateClientsAndRejectSpoofing(t *testing.T) {
	for _, proxied := range []bool{false, true} {
		t.Run(fmt.Sprintf("proxied=%t", proxied), func(t *testing.T) {
			var proxies []string
			if proxied {
				proxies = []string{"127.0.0.1/32"}
			}
			broker, public, nodes := relayWebFixture(t, 1, proxies...)
			node := nodes[0]
			login := func(ip, port, spoof, body string, want int) {
				t.Helper()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				r := httptest.NewRequest(http.MethodPost, node.url+"api/login", strings.NewReader(body)).WithContext(ctx)
				r.RemoteAddr = net.JoinHostPort(ip, port)
				r.Header.Set("X-Forwarded-For", spoof)
				if proxied {
					r.RemoteAddr = "127.0.0.1:" + port
					// The trusted proxy appends the actual client after any
					// attacker-supplied entries.
					r.Header.Add("X-Forwarded-For", ip)
				}
				r.Header.Set("X-Real-IP", spoof)
				r.Header.Set("Forwarded", "for="+spoof)
				r.Header.Set("X-Spool-Relay-Client", spoof)
				r.AddCookie(&http.Cookie{Name: "spool_relay_client", Value: spoof})
				r.Header.Set("Origin", public.URL)
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				broker.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("login from %s: %d, want %d: %s", ip, w.Code, want, w.Body)
				}
			}
			// Malformed JSON still consumes login quota, without bcrypt work.
			for i := 0; i < 20; i++ {
				login("198.51.100.1", fmt.Sprint(10000+i), fmt.Sprintf("203.0.113.%d", i+1), "{", http.StatusBadRequest)
			}
			valid := `{"username":"alice","password":"test-password-2026"}`
			login("198.51.100.1", "20000", "203.0.113.99", valid, http.StatusTooManyRequests)
			login("203.0.113.2", "20001", "198.51.100.1", valid, http.StatusOK)
			login("2001:db8::2", "20002", "198.51.100.1", valid, http.StatusOK)

			// Native relay traffic must not share the public attacker's bucket.
			dialer, err := relay.NewDialer(t.Context(), relay.Pairing{Version: 2, RelayURL: public.URL, PublicKey: node.id.PublicKey(), AccessToken: node.id.AccessToken}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dialer.Close()
			transport := &http.Transport{DialTLSContext: dialer.DialTLSContext}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			response, err := client.Post(relay.Origin(node.id.ID())+"/api/login", "application/json", strings.NewReader(valid))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatal("public login attempts blocked the native client", response.StatusCode)
			}
		})
	}
}

func TestRelayWebDetectedProxySeparatesLoginLimitsWithoutRestart(t *testing.T) {
	// Start with no manually trusted proxies. The real reverse proxy connects
	// to the real broker, which opens pinned TLS tunnels to a real Spool app.
	broker, public, nodes := relayWebFixture(t, 1)
	node := nodes[0]
	target, _ := url.Parse(public.URL)
	upstream := &http.Transport{}
	t.Cleanup(upstream.CloseIdleConnections)
	proxy := &httputil.ReverseProxy{Transport: upstream, Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(target)
		p.Out.Header["X-Forwarded-For"] = p.In.Header.Values("X-Forwarded-For")
		p.SetXForwarded()
	}}
	front := httptest.NewTLSServer(proxy)
	t.Cleanup(front.Close)
	// Trust only this fixture's certificate for the production HTTPS verifier.
	// No TLS verification bypass is used for the automatic callback.
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	pool, _ := x509.SystemCertPool()
	if pool == nil {
		pool = x509.NewCertPool()
	}
	pool.AddCert(front.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	http.DefaultTransport = transport
	t.Cleanup(func() {
		broker.Close()
		http.DefaultTransport = original
		transport.CloseIdleConnections()
	})
	login := func(ip, spoof, body string, want int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		r := httptest.NewRequest(http.MethodPost, node.url+"api/login", strings.NewReader(body)).WithContext(ctx)
		// Only the frontend client IP is synthetic. The actual proxy-to-broker
		// TCP peer and all Relay host/data streams are real local connections.
		r.RemoteAddr = net.JoinHostPort(ip, "41000")
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("Origin", public.URL)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", spoof)
		r.Header.Set("X-Real-IP", spoof)
		r.Header.Set("Forwarded", "for="+spoof)
		r.Header.Set("X-Spool-Relay-Client", spoof)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("login from %s: %d, want %d: %s", ip, w.Code, want, w.Body)
		}
	}
	valid := `{"username":"alice","password":"test-password-2026"}`
	wrong := `{"username":"alice","password":"wrong-password"}`
	for i := range 20 {
		login("198.51.100.1", fmt.Sprintf("203.0.113.%d", i+1), wrong, http.StatusUnauthorized)
	}
	// Without trust, the proxy is the only known client: another person is
	// blocked by the shared quota. This is the pre-detection control case.
	login("203.0.113.2", "198.51.100.1", valid, http.StatusTooManyRequests)

	admin := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		broker.AdminHandler().ServeHTTP(w, r)
		return w
	}
	data, _ := json.Marshal(map[string]any{"browser_origin": "https://relay-admin.example", "public_url": front.URL, "trusted_proxies": []string{}})
	if w := admin(http.MethodPut, "/v1/settings", string(data)); w.Code != 200 {
		t.Fatal("could not save Relay public URL", w.Code, w.Body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var status struct {
			Detecting      bool   `json:"detecting"`
			DetectedProxy  string `json:"detected_proxy"`
			DetectionError string `json:"detection_error"`
		}
		w := admin(http.MethodGet, "/v1/settings", "")
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil {
			t.Fatal("invalid detection status", w.Code, w.Body)
		}
		if !status.Detecting {
			if status.DetectionError != "" || status.DetectedProxy != "127.0.0.1/32" {
				t.Fatal("automatic proxy verification failed", w.Body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("proxy detection timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The same running broker and app must now assign separate client quotas,
	// despite the old shared proxy bucket still being exhausted.
	for i := range 20 {
		login("198.51.100.1", fmt.Sprintf("203.0.113.%d", i+1), wrong, http.StatusUnauthorized)
	}
	login("198.51.100.1", "203.0.113.99", valid, http.StatusTooManyRequests)
	login("203.0.113.2", "198.51.100.1", valid, http.StatusOK)
	login("2001:db8::2", "198.51.100.1", valid, http.StatusOK)
	// A long attacker-supplied prefix must neither change an exhausted client's
	// bucket nor put an unrelated client back into the shared proxy bucket.
	longPrefix := strings.Repeat("203.0.113.99,", 100)
	login("198.51.100.1", longPrefix, valid, http.StatusTooManyRequests)
	login("203.0.113.3", longPrefix, valid, http.StatusOK)
}

func TestRelayWebStaticAssetsAndHostValidation(t *testing.T) {
	_, public, nodes := relayWebFixture(t, 1)
	node := nodes[0]
	for _, path := range []string{"", "manifest.webmanifest", "sw.js", "offline.html", "offline.js", "logo.png"} {
		request, _ := http.NewRequest("GET", node.url+path, nil)
		// Opening a shared link from another site must still show the login page.
		request.Header.Set("Sec-Fetch-Site", "cross-site")
		response, err := public.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal(path, response.StatusCode)
		}
		if path == "sw.js" && response.Header.Get("Service-Worker-Allowed") != relay.WebPath(node.id.ID()) {
			t.Fatal("worker can escape its server path")
		}
		if path == "" && (!strings.Contains(string(data), `src="./assets/`) || strings.Contains(string(data), `href="/`)) {
			t.Fatal("frontend build is not relative to its mount; run bun run --cwd web build")
		}
		if path == "manifest.webmanifest" {
			var manifest struct{ ID, Scope, StartURL string }
			var values map[string]any
			json.Unmarshal(data, &manifest)
			json.Unmarshal(data, &values)
			if manifest.ID != "./" || manifest.Scope != "./" || values["start_url"] != "./" {
				t.Fatal("unscoped PWA manifest")
			}
		}
	}
	for _, address := range []string{public.URL + relay.WebPath(strings.Repeat("a", 52)), "http://foreign.test" + relay.WebPath(node.id.ID())} {
		response := httptest.NewRecorder()
		node.app.ServeHTTP(response, httptest.NewRequest("GET", address, nil))
		if response.Code != 404 {
			t.Fatal("host accepted a foreign mount", address)
		}
	}
	response := httptest.NewRecorder()
	node.app.ServeHTTP(response, httptest.NewRequest("GET", "/healthz", nil))
	if response.Code != 200 {
		t.Fatal("direct routes no longer work")
	}
}

func TestRelayWebServiceEnableDisable(t *testing.T) {
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
	service, settings := settingsService(t)
	id, err := relay.LoadIdentity(filepath.Join(service.Store.Root(), "relay"))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := relay.NewServer(relay.ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir(), AllowUnregisteredHosts: true})
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(broker)
	defer public.Close()
	defer broker.Close()
	port, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	settings.RelayLANPort = port.Addr().(*net.TCPAddr).Port
	port.Close()
	settings.RelayURL = public.URL
	if err = service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err = service.RegisterRelay(t.Context(), public.URL, ""); err != nil {
		t.Fatal(err)
	}
	settings.RelayEnabled, settings.RelayWebEnabled = true, true
	if err = service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err = service.Restart(); err != nil {
		t.Fatal(err)
	}
	address := public.URL + relay.WebPath(id.ID())
	waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.WebURL == address })
	client := &http.Client{Timeout: 5 * time.Second}
	check := func(want int) {
		t.Helper()
		response, err := client.Get(address + "api/me")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatal("Web enablement", response.StatusCode, "want", want)
		}
	}
	check(401)
	settings.RelayWebEnabled = false
	if err = service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if !service.Status().RestartRequired {
		t.Fatal("Web setting did not require restart")
	}
	if err = service.Restart(); err != nil {
		t.Fatal(err)
	}
	status := waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.State == "connected" })
	if status.Relay.WebURL != "" || status.Relay.ServerID != id.ID() || status.RestartRequired {
		t.Fatal("disabled Web retained its address or changed native identity", status)
	}
	check(503)
	code, err := service.RelayPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	pairing, _ := relay.ParsePairing(code)
	pairing.LAN = nil
	dialer := pairServiceDevice(t, service, pairing)
	defer dialer.Close()
	transport := &http.Transport{DialTLSContext: dialer.DialTLSContext}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Get(relay.Origin(id.ID()) + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("disabling Web interrupted native relay access")
	}
	settings.RelayWebEnabled = true
	if err = service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err = service.Restart(); err != nil {
		t.Fatal(err)
	}
	waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.WebURL == address })
	check(401) // The same running relay restores the same public path.
}
