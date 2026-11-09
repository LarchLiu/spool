package workspace

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
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
func relayWebFixture(t *testing.T, count int) (*relay.Server, *httptest.Server, []*relayWebNode) {
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
	broker, err := relay.NewServer(relay.ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir()})
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
	dialer, err := relay.NewDialer(t.Context(), pairing, nil)
	if err != nil {
		t.Fatal(err)
	}
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
