package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"spool/internal/adbbridge"
)

// hostADB stands in for the host's adb client. Like adb connect, it opens the
// endpoint it is given and keeps that transport until the device is removed.
type hostADB struct {
	mu         sync.Mutex
	transports map[string]net.Conn
	paired     []string
}

func (h *hostADB) Connect(_ context.Context, serial string) (string, error) {
	transport, err := net.Dial("tcp", serial)
	if err != nil {
		return "failed to connect to " + serial, nil
	}
	if _, err := transport.Write([]byte("CNXN")); err != nil {
		transport.Close()
		return "failed to connect to " + serial, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.transports == nil {
		h.transports = map[string]net.Conn{}
	}
	h.transports[serial] = transport
	return "connected to " + serial, nil
}

func (h *hostADB) Disconnect(_ context.Context, serial string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if transport := h.transports[serial]; transport != nil {
		transport.Close()
		delete(h.transports, serial)
	}
}

func (h *hostADB) WaitForDevice(context.Context, string) error { return nil }

func (h *hostADB) Pair(_ context.Context, address, code string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paired = append(h.paired, code)
	return "Successfully paired to " + address, nil
}

func (h *hostADB) devices() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	serials := []string{}
	for serial := range h.transports {
		serials = append(serials, serial)
	}
	return serials
}

func (h *hostADB) pairings() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.paired...)
}

type adbBridgeFixture struct {
	t      *testing.T
	store  *Store
	app    *App
	adb    *hostADB
	server *httptest.Server
}

func newADBBridgeFixture(t *testing.T) *adbBridgeFixture {
	t.Helper()
	adb := &hostADB{}
	f := serveADBBridge(t, adbbridge.NewWith(adb))
	f.adb = adb
	return f
}

func serveADBBridge(t *testing.T, manager *adbbridge.Manager) *adbBridgeFixture {
	t.Helper()
	f := &adbBridgeFixture{t: t, store: testStore(t)}
	f.app = NewApp(f.store, &fakeRunner{}, "", "")
	f.app.adb = manager
	f.server = httptest.NewServer(f.app)
	t.Cleanup(func() { _ = f.app.Close(); f.server.Close() })
	return f
}

// enable is what the Service does when the host's owner flips the switch.
func (f *adbBridgeFixture) enable(enabled bool) {
	f.app.setADBBridgeEnabled(enabled)
}

// user creates an account, optionally with the grant to share devices.
func (f *adbBridgeFixture) user(name string, granted bool) User {
	f.t.Helper()
	user := testUser(f.t, f.store, name)
	if granted {
		f.grant(user, true)
	}
	return user
}

// grant changes a user's grant the way local management does, which also ends
// that user's sessions.
func (f *adbBridgeFixture) grant(user User, granted bool) {
	f.t.Helper()
	if _, err := f.store.SaveUser(UserInput{ID: user.ID, Username: user.Username, Nickname: user.Nickname, ADBBridge: granted}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *adbBridgeFixture) status(cookie *http.Cookie) (int, string) {
	f.t.Helper()
	w := apiRequest(f.app, "GET", "/api/adb", "", cookie, "")
	return w.Code, strings.TrimSpace(w.Body.String())
}

func (f *adbBridgeFixture) dial(cookie *http.Cookie, origin string) (*adbBridgeClient, int) {
	f.t.Helper()
	header := http.Header{}
	if cookie != nil {
		header.Set("Cookie", cookie.String())
	}
	if origin != "" {
		header.Set("Origin", origin)
	}
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/adb/bridge", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		if response == nil {
			f.t.Fatal(err)
		}
		return nil, response.StatusCode
	}
	f.t.Cleanup(func() { _ = conn.CloseNow() })
	conn.SetReadLimit(2 << 20)
	return &adbBridgeClient{t: f.t, conn: conn}, http.StatusSwitchingProtocols
}

func (f *adbBridgeFixture) open(cookie *http.Cookie) *adbBridgeClient {
	f.t.Helper()
	client, status := f.dial(cookie, f.server.URL)
	if client == nil {
		f.t.Fatalf("the bridge stream was refused with status %d", status)
	}
	return client
}

// attach opens a stream the way a signed-in client shares a device.
func (f *adbBridgeFixture) attach(cookie *http.Cookie, csrf, device string) *adbBridgeClient {
	f.t.Helper()
	client := f.open(cookie)
	client.say(adbBridgeHello{Type: "attach", CSRF: csrf, Transport: "usb", Device: device})
	return client
}

func (f *adbBridgeFixture) refuses(cookie *http.Cookie, origin string, want int, why string) {
	f.t.Helper()
	if client, status := f.dial(cookie, origin); client != nil || status != want {
		f.t.Fatalf("%s: the stream was answered with %d, want %d", why, status, want)
	}
}

type adbBridgeMessage struct {
	Type, Serial, State, Code string
}

type adbBridgeClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func (c *adbBridgeClient) say(hello adbBridgeHello) {
	c.t.Helper()
	data, _ := json.Marshal(hello)
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatal(err)
	}
}

// next returns the next control message, skipping relayed bytes and signs of life.
func (c *adbBridgeClient) next() (adbBridgeMessage, error) {
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	for {
		kind, data, err := c.conn.Read(ctx)
		if err != nil {
			return adbBridgeMessage{}, err
		}
		if kind != websocket.MessageText {
			continue
		}
		var message adbBridgeMessage
		if err := json.Unmarshal(data, &message); err != nil {
			c.t.Fatalf("invalid control message %q: %v", data, err)
		}
		if message.Type != "alive" {
			return message, nil
		}
	}
}

func (c *adbBridgeClient) expect(kind string) adbBridgeMessage {
	c.t.Helper()
	message, err := c.next()
	if err != nil || message.Type != kind {
		c.t.Fatalf("got %+v (%v), want a %q message", message, err, kind)
	}
	return message
}

// online consumes an attachment up to the point adb can use the device.
func (c *adbBridgeClient) online() string {
	c.t.Helper()
	serial := c.expect("ready").Serial
	for _, state := range []string{"connecting", "online"} {
		if message := c.expect("state"); message.State != state {
			c.t.Fatalf("got state %q, want %q", message.State, state)
		}
	}
	return serial
}

func (c *adbBridgeClient) expectError(code string) {
	c.t.Helper()
	if message := c.expect("error"); message.Code != code {
		c.t.Fatalf("got error %q, want %q", message.Code, code)
	}
	c.expectClosed()
}

func (c *adbBridgeClient) expectClosed() {
	c.t.Helper()
	if message, err := c.next(); err == nil || errors.Is(err, context.DeadlineExceeded) {
		c.t.Fatalf("the stream stayed open: %+v %v", message, err)
	}
}

func TestADBBridgeNeedsTheHostSwitchAndAUserGrant(t *testing.T) {
	f := newADBBridgeFixture(t)
	alice := f.user("alice", true)
	bob := f.user("bob", false)
	testProject(t, f.store, alice, bob) // Project access must not imply sharing devices.
	if code, _ := f.status(nil); code != http.StatusUnauthorized {
		t.Fatalf("an anonymous status request returned %d", code)
	}
	f.refuses(nil, f.server.URL, http.StatusUnauthorized, "anonymous client")
	aliceCookie, aliceCSRF := loginRequest(t, f.app, "alice")
	bobCookie, _ := loginRequest(t, f.app, "bob")
	status := func(cookie *http.Cookie, want string) {
		t.Helper()
		if code, body := f.status(cookie); code != 200 || body != want {
			t.Fatalf("status is %d %s, want %s", code, body, want)
		}
	}

	status(aliceCookie, `{"available":false,"reason":"disabled"}`)
	status(bobCookie, `{"available":false,"reason":"not_authorized"}`)
	f.refuses(aliceCookie, f.server.URL, http.StatusForbidden, "granted user while the host switch is off")

	f.enable(true)
	status(aliceCookie, `{"available":true}`)
	// Someone without the grant learns nothing about the host's configuration.
	status(bobCookie, `{"available":false,"reason":"not_authorized"}`)
	f.refuses(bobCookie, f.server.URL, http.StatusForbidden, "project member without the grant")
	serial := f.attach(aliceCookie, aliceCSRF, "R58M000001").online()
	if attached := f.adb.devices(); len(attached) != 1 || attached[0] != serial {
		t.Fatalf("adb has %v, want %s", attached, serial)
	}

	// The grant is independent of remote desktop access and survives a restart
	// of the store's readers.
	users, err := f.store.Users()
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.ADBBridge != (user.ID == alice.ID) || user.RemoteDesktop {
			t.Fatalf("saved grants are wrong for %s: %+v", user.Username, user)
		}
	}

	bare := serveADBBridge(t, adbbridge.NewWith(nil))
	bare.user("alice", true)
	cookie, csrf := loginRequest(t, bare.app, "alice")
	bare.enable(true)
	if code, body := bare.status(cookie); code != 200 || body != `{"available":false,"reason":"adb_missing"}` {
		t.Fatalf("status without adb is %d %s", code, body)
	}
	bare.attach(cookie, csrf, "R58M000001").expectError("unavailable")
}

func TestADBBridgeStreamRequiresTheAppOriginAndTheSessionToken(t *testing.T) {
	previous := adbBridgeHelloTimeout
	adbBridgeHelloTimeout = 200 * time.Millisecond
	t.Cleanup(func() { adbBridgeHelloTimeout = previous })
	f := newADBBridgeFixture(t)
	f.user("alice", true)
	f.user("bob", true)
	cookie, csrf := loginRequest(t, f.app, "alice")
	_, bobCSRF := loginRequest(t, f.app, "bob")
	f.enable(true)

	f.refuses(cookie, "", http.StatusForbidden, "request without an origin")
	f.refuses(cookie, "https://evil.example", http.StatusForbidden, "request from another site")
	refused := map[string]adbBridgeHello{
		"another session's token": {Type: "attach", CSRF: bobCSRF, Transport: "usb", Device: "phone"},
		"no token":                {Type: "attach", Transport: "usb", Device: "phone"},
		"an unknown request":      {Type: "shell", CSRF: csrf, Transport: "usb", Device: "phone"},
	}
	for name, hello := range refused {
		client := f.open(cookie)
		client.say(hello)
		if message, err := client.next(); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Fatalf("%s was answered with %+v %v", name, message, err)
		}
	}
	// A client that never introduces itself does not hold a stream open.
	f.open(cookie).expectClosed()
	// The bridge itself rejects what it cannot serve.
	f.attach(cookie, csrf, "not a device key").expectError("invalid")
	unknown := f.open(cookie)
	unknown.say(adbBridgeHello{Type: "attach", CSRF: csrf, Transport: "serial", Device: "phone"})
	unknown.expectError("invalid")
	if attached := f.adb.devices(); len(attached) != 0 {
		t.Fatalf("a refused stream reached adb: %v", attached)
	}
}

func TestADBBridgeDetachesADeviceAsSoonAsItsAccessEnds(t *testing.T) {
	// Every case below has to be noticed through a change notification.
	previous := adbBridgeRecheck
	adbBridgeRecheck = time.Hour
	t.Cleanup(func() { adbBridgeRecheck = previous })
	f := newADBBridgeFixture(t)
	alice := f.user("alice", true)
	f.enable(true)
	service := &Service{Store: f.store, app: f.app, adb: f.app.adb}
	// The client is told first; the device leaves adb right after.
	gone := func(serial string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			attached, listed := f.adb.devices(), service.Status().ADB.Devices
			if len(attached) == 0 && len(listed) == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s stayed attached: adb has %v, the bridge lists %+v", serial, attached, listed)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	cookie, csrf := loginRequest(t, f.app, "alice")
	device := f.attach(cookie, csrf, "R58M000001")
	serial := device.online()
	if listed := service.Status().ADB.Devices; len(listed) != 1 || listed[0] != (adbbridge.Device{Owner: alice.ID, Transport: adbbridge.TransportUSB, Serial: serial}) {
		t.Fatalf("attached devices are %+v, want alice's %s", listed, serial)
	}
	if w := apiRequest(f.app, "POST", "/api/logout", `{}`, cookie, csrf); w.Code != 200 {
		t.Fatal("sign out failed", w.Code, w.Body)
	}
	device.expectError("revoked")
	gone(serial)

	cookie, csrf = loginRequest(t, f.app, "alice")
	device = f.attach(cookie, csrf, "R58M000001")
	if again := device.online(); again != serial {
		t.Fatalf("the returning device got serial %s, want %s", again, serial)
	}
	f.grant(alice, false)
	device.expectError("revoked")
	gone(serial)
	cookie, _ = loginRequest(t, f.app, "alice")
	f.refuses(cookie, f.server.URL, http.StatusForbidden, "user whose grant was revoked")

	f.grant(alice, true)
	cookie, csrf = loginRequest(t, f.app, "alice")
	device = f.attach(cookie, csrf, "R58M000001")
	device.online()
	f.enable(false)
	device.expectError("revoked")
	gone(serial)
	f.refuses(cookie, f.server.URL, http.StatusForbidden, "stream after the host switch was turned off")

	// Stopping the service removes its devices from adb before it returns.
	f.enable(true)
	device = f.attach(cookie, csrf, "R58M000001")
	device.online()
	waiting := f.open(cookie)
	if err := f.app.Close(); err != nil {
		t.Fatal(err)
	}
	if attached := f.adb.devices(); len(attached) != 0 {
		t.Fatalf("the service stopped with %v still in adb", attached)
	}
	device.expectError("stopping")
	waiting.expectClosed()
}

func TestADBBridgeNoticesAccessThatEndsWithoutANotification(t *testing.T) {
	previous := adbBridgeRecheck
	adbBridgeRecheck = 20 * time.Millisecond
	t.Cleanup(func() { adbBridgeRecheck = previous })
	f := newADBBridgeFixture(t)
	alice := f.user("alice", true)
	f.enable(true)

	// A grant can change without replacing the session, and a session can expire.
	for name, end := range map[string]string{
		"revoked grant":   `UPDATE user_adb_bridge SET enabled=0 WHERE user_id=?`,
		"expired session": `UPDATE auth_sessions SET expires_at=0 WHERE user_id=?`,
	} {
		f.grant(alice, true)
		cookie, csrf := loginRequest(t, f.app, "alice")
		device := f.attach(cookie, csrf, "R58M000001")
		device.online()
		if _, err := f.store.db.Exec(end, alice.ID); err != nil {
			t.Fatal(name, err)
		}
		device.expectError("revoked")
	}
}

func TestADBBridgeOldStreamsDoNotSurviveReauthorization(t *testing.T) {
	for _, kind := range []string{"host switch", "user grant"} {
		t.Run(kind, func(t *testing.T) {
			f := newADBBridgeFixture(t)
			alice := f.user("alice", true)
			f.enable(true)
			cookie, csrf := loginRequest(t, f.app, "alice")
			active := f.attach(cookie, csrf, "active")
			active.online()
			pending := f.open(cookie)

			if kind == "host switch" {
				f.enable(false)
				f.enable(true)
			} else {
				// Leave the session intact and make the intermediate state invisible
				// to every reader. Only the revocation epoch changes at commit.
				tx, err := f.store.db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, enabled := range []bool{false, true} {
					if _, err := tx.Exec(`UPDATE user_adb_bridge SET enabled=? WHERE user_id=?`, enabled, alice.ID); err != nil {
						t.Fatal(err)
					}
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				f.store.changes.publish(changeAuth)
			}

			pending.say(adbBridgeHello{Type: "attach", CSRF: csrf, Transport: "usb", Device: "pending"})
			pending.expectError("revoked")
			active.expectError("revoked")
			f.attach(cookie, csrf, "new").online()
		})
	}
}

func TestADBBridgePairsAPhoneWithTheServersADB(t *testing.T) {
	f := newADBBridgeFixture(t)
	f.user("alice", true)
	cookie, csrf := loginRequest(t, f.app, "alice")
	f.enable(true)
	client := f.open(cookie)
	client.say(adbBridgeHello{Type: "pair", CSRF: csrf, Code: "482915"})
	client.expect("ready")
	client.expect("paired")
	client.expectClosed()
	if paired := f.adb.pairings(); len(paired) != 1 || paired[0] != "482915" {
		t.Fatalf("adb paired with %v", paired)
	}
	rejected := f.open(cookie)
	rejected.say(adbBridgeHello{Type: "pair", CSRF: csrf, Code: "12"})
	rejected.expectError("invalid")
}

func TestADBBridgeSwitchAppliesWithoutARestartAndPersists(t *testing.T) {
	service, _ := settingsService(t)
	enabled := func() bool {
		t.Helper()
		saved, err := service.Store.Settings()
		if err != nil {
			t.Fatal(err)
		}
		if live := service.app.adbBridgeEnabled(); live != saved.ADBBridgeEnabled {
			t.Fatalf("the running service has the bridge %v but %v is saved", live, saved.ADBBridgeEnabled)
		}
		return saved.ADBBridgeEnabled
	}
	if enabled() {
		t.Fatal("the ADB bridge is on by default")
	}
	if err := service.SetADBBridgeEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !enabled() || service.Status().RestartRequired {
		t.Fatal("enabling the bridge did not apply at once", service.Status().PendingChanges)
	}
	// Saving unrelated settings keeps it, and a restart starts with it.
	next, err := service.Store.Settings()
	if err != nil {
		t.Fatal(err)
	}
	next.IdleMinutes++
	if err := service.SaveSettings(next); err != nil {
		t.Fatal(err)
	}
	if err := service.Restart(); err != nil {
		t.Fatal(err)
	}
	if !enabled() {
		t.Fatal("the bridge switch did not survive a save and a restart")
	}
	// A settings save that changes it applies as immediately as the switch.
	next.ADBBridgeEnabled = false
	if err := service.SaveSettings(next); err != nil {
		t.Fatal(err)
	}
	if enabled() {
		t.Fatal("saving the setting did not turn the bridge off")
	}
}
