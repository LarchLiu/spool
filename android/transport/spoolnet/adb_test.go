package spoolnet

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"spool/internal/adbbridge"
)

const adbPatience = 5 * time.Second

var (
	// The first packet adb sends on a new transport, and what opens a pairing.
	adbConnectPacket = append([]byte("CNXN"), bytes.Repeat([]byte{0x01}, 20)...)
	adbPairingHello  = []byte{0x16, 0x03, 0x01, 0x00, 0x05, 'h', 'e', 'l', 'l', 'o'}
)

// What the Spool server reads as the first message of a bridge stream.
type bridgeHello struct {
	Type      string `json:"type"`
	CSRF      string `json:"csrf"`
	Transport string `json:"transport"`
	Device    string `json:"device"`
	Code      string `json:"code"`
}

// hostADB stands in for the adb executable on the Spool server. Like adb, it
// reaches a shared phone only through the loopback endpoint the bridge opens.
type hostADB struct {
	// connect and pair give adb's verdict once it holds the connection.
	connect func(transport net.Conn, serial string) string
	pair    func(exchange net.Conn, code string) string

	transports chan net.Conn
	mu         sync.Mutex
	opened     []net.Conn
	detached   []string
}

func newHostADB(t *testing.T) *hostADB {
	h := &hostADB{transports: make(chan net.Conn, 4)}
	t.Cleanup(func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, conn := range h.opened {
			conn.Close()
		}
	})
	return h
}

func (h *hostADB) open(ctx context.Context, address string, greeting []byte) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.opened = append(h.opened, conn)
	h.mu.Unlock()
	// adb drops the connection when the device is disconnected from it.
	context.AfterFunc(ctx, func() { conn.Close() })
	if _, err = conn.Write(greeting); err != nil {
		return nil, err
	}
	return conn, nil
}

func (h *hostADB) Connect(ctx context.Context, serial string) (string, error) {
	transport, err := h.open(ctx, serial, adbConnectPacket)
	if err != nil {
		return "failed to connect to " + serial, nil
	}
	if h.connect != nil {
		return h.connect(transport, serial), nil
	}
	h.transports <- transport
	return "connected to " + serial, nil
}

func (h *hostADB) Disconnect(_ context.Context, serial string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.detached = append(h.detached, serial)
}

func (h *hostADB) WaitForDevice(context.Context, string) error { return nil }

func (h *hostADB) Pair(ctx context.Context, address, code string) (string, error) {
	exchange, err := h.open(ctx, address, adbPairingHello)
	if err != nil {
		return "Failed: Unable to start pairing client.", nil
	}
	return h.pair(exchange, code), nil
}

// detaches waits until adb was told to disconnect the device that many times.
func (h *hostADB) detaches(t *testing.T, serial string, want int) {
	t.Helper()
	deadline := time.Now().Add(adbPatience)
	for {
		h.mu.Lock()
		count := 0
		for _, value := range h.detached {
			if value == serial {
				count++
			}
		}
		h.mu.Unlock()
		if count >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("adb disconnected %s %d times, want %d", serial, count, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// adbServerFixture accepts bridge streams where the Spool server does and
// hands each one, with its first message, to serve.
func adbServerFixture(t *testing.T, serve func(conn *websocket.Conn, hello bridgeHello)) *Client {
	t.Helper()
	var handlers sync.WaitGroup
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		// A hijacked connection is no longer tracked by the test server.
		handlers.Add(1)
		defer handlers.Done()
		if r.TLS == nil || r.Host != "example.com" || r.Header.Get("Origin") != "https://example.com" || r.Method != http.MethodGet || r.URL.Path != "/api/adb/bridge" {
			t.Error("the ADB bridge left the selected server's exact origin")
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"example.com"}})
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), adbPatience)
		kind, data, err := conn.Read(ctx)
		cancel()
		var hello bridgeHello
		if err != nil || kind != websocket.MessageText || json.Unmarshal(data, &hello) != nil {
			t.Errorf("invalid ADB bridge hello %q: %v", data, err)
			return
		}
		serve(conn, hello)
	})
	t.Cleanup(handlers.Wait)
	return client
}

// adbBridgeFixture serves the stream with the server's own bridge.
func adbBridgeFixture(t *testing.T, adb adbbridge.ADB) (*Client, chan bridgeHello) {
	t.Helper()
	manager := adbbridge.NewWith(adb)
	ctx, stop := context.WithCancel(context.Background())
	hellos := make(chan bridgeHello, 4)
	client := adbServerFixture(t, func(conn *websocket.Conn, hello bridgeHello) {
		hellos <- hello
		if hello.Type == "pair" {
			_ = manager.Pair(ctx, conn, hello.Code)
			return
		}
		_, _ = manager.Attach(ctx, conn, adbbridge.Request{Owner: "owner", Transport: adbbridge.Transport(hello.Transport), Device: hello.Device})
	})
	t.Cleanup(stop)
	return client, hellos
}

// wirelessDebugging is the phone's side: a loopback port such as adbd and the
// pairing dialog open.
type wirelessDebugging struct {
	listener net.Listener
	accepted chan net.Conn
}

func newWirelessDebugging(t *testing.T) *wirelessDebugging {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &wirelessDebugging{listener: listener, accepted: make(chan net.Conn, 4)}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			p.accepted <- conn
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-stopped
		for {
			select {
			case conn := <-p.accepted:
				conn.Close()
			default:
				return
			}
		}
	})
	return p
}

func (p *wirelessDebugging) port() int { return p.listener.Addr().(*net.TCPAddr).Port }

func (p *wirelessDebugging) accept(t *testing.T) net.Conn {
	t.Helper()
	select {
	case conn := <-p.accepted:
		t.Cleanup(func() { conn.Close() })
		_ = conn.SetDeadline(time.Now().Add(adbPatience))
		return conn
	case <-time.After(adbPatience):
		t.Fatal("nothing connected to wireless debugging")
		return nil
	}
}

func adbReceive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(adbPatience):
		t.Fatal("the ADB bridge fixture timed out")
		var zero T
		return zero
	}
}

func adbRead(t *testing.T, conn net.Conn, length int) []byte {
	t.Helper()
	data := make([]byte, length)
	_ = conn.SetReadDeadline(time.Now().Add(adbPatience))
	if _, err := io.ReadFull(conn, data); err != nil {
		t.Fatal(err)
	}
	return data
}

func nextADBEvent(t *testing.T, events eventRecorder) (string, map[string]string) {
	t.Helper()
	event := adbReceive(t, events)
	fields := map[string]string{}
	if err := json.Unmarshal([]byte(event[1]), &fields); err != nil {
		t.Fatalf("invalid %s event %q", event[0], event[1])
	}
	return event[0], fields
}

func expectADBEvent(t *testing.T, events eventRecorder, kind string) map[string]string {
	t.Helper()
	got, fields := nextADBEvent(t, events)
	if got != kind {
		t.Fatalf("got %s %v, want %s", got, fields, kind)
	}
	return fields
}

// adbClosed skips progress and returns how the stream ended.
func adbClosed(t *testing.T, events eventRecorder) map[string]string {
	t.Helper()
	for {
		if kind, fields := nextADBEvent(t, events); kind == "closed" {
			return fields
		}
	}
}

func adbQuiet(t *testing.T, events eventRecorder) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("an event followed the end of the stream: %v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func shareADB(t *testing.T, client *Client, port int) (*ADBStream, eventRecorder) {
	t.Helper()
	events := make(eventRecorder, 32)
	stream, err := client.ShareADB("csrf-token", "android-0123456789abcdef", port, events)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stream.Close(); <-stream.done })
	return stream, events
}

// shortenADB applies until every stream the test opens afterwards has stopped.
func shortenADB(t *testing.T, timing *time.Duration, value time.Duration) {
	t.Helper()
	original := *timing
	*timing = value
	t.Cleanup(func() { *timing = original })
}

func adbPattern(length int, seed byte) []byte {
	data := make([]byte, length)
	for i := range data {
		data[i] = byte(i*31>>3) ^ seed
	}
	return data
}

func TestADBShareOffersWirelessDebuggingToTheServersADB(t *testing.T) {
	phone := newWirelessDebugging(t)
	host := newHostADB(t)
	client, hellos := adbBridgeFixture(t, host)
	_, events := shareADB(t, client, phone.port())

	if hello := adbReceive(t, hellos); hello != (bridgeHello{Type: "attach", CSRF: "csrf-token", Transport: "wireless", Device: "android-0123456789abcdef"}) {
		t.Fatalf("unexpected hello: %+v", hello)
	}
	// Programs on the server address the phone by this serial.
	serial := expectADBEvent(t, events, "ready")["serial"]
	if !regexp.MustCompile(`^127\.0\.0\.1:[0-9]+$`).MatchString(serial) {
		t.Fatalf("unexpected serial %q", serial)
	}
	if state := expectADBEvent(t, events, "state")["state"]; state != "connecting" {
		t.Fatalf("got state %q, want connecting", state)
	}
	transport := adbReceive(t, host.transports)
	debugging := phone.accept(t)
	// adb speaks first, and the phone hears exactly what it said.
	if greeting := adbRead(t, debugging, len(adbConnectPacket)); !bytes.Equal(greeting, adbConnectPacket) {
		t.Fatalf("the phone heard %q", greeting)
	}
	if state := expectADBEvent(t, events, "state")["state"]; state != "online" {
		t.Fatalf("got state %q, want online", state)
	}

	// Both directions carry more than several relay chunks, in order.
	install, logs := adbPattern(300<<10, 0x5a), adbPattern(300<<10, 0xa5)
	written := make(chan error, 2)
	go func() { _, err := transport.Write(install); written <- err }()
	go func() { _, err := debugging.Write(logs); written <- err }()
	if got := adbRead(t, debugging, len(install)); !bytes.Equal(got, install) {
		t.Fatal("adb's bytes reached the phone changed")
	}
	if got := adbRead(t, transport, len(logs)); !bytes.Equal(got, logs) {
		t.Fatal("the phone's bytes reached adb changed")
	}
	for range 2 {
		if err := adbReceive(t, written); err != nil {
			t.Fatal(err)
		}
	}

	// Turning wireless debugging off ends the share and takes the phone out of
	// the server's adb.
	debugging.Close()
	if ending := adbClosed(t, events); ending["code"] != "device_closed" {
		t.Fatalf("unexpected ending: %v", ending)
	}
	adbQuiet(t, events)
	// Once to clear a stale transport before connecting, once to detach.
	host.detaches(t, serial, 2)
	_ = transport.SetReadDeadline(time.Now().Add(adbPatience))
	if _, err := transport.Read(make([]byte, 1)); err == nil || strings.Contains(err.Error(), "timeout") {
		t.Fatalf("adb kept a transport to a phone that left: %v", err)
	}
}

func TestADBShareReportsAPhoneThatRefusesTheServersKey(t *testing.T) {
	// A phone that does not know the server's adb key answers the greeting
	// and then ends the TLS handshake.
	refuse := func(t *testing.T, phone *wirelessDebugging, answer string) {
		t.Helper()
		debugging := phone.accept(t)
		adbRead(t, debugging, len(adbConnectPacket))
		if _, err := debugging.Write([]byte(answer)); err != nil {
			t.Fatal(err)
		}
		debugging.Close()
	}

	t.Run("as adb reports it", func(t *testing.T) {
		phone := newWirelessDebugging(t)
		host := newHostADB(t)
		host.connect = func(transport net.Conn, serial string) string {
			_, _ = io.ReadFull(transport, make([]byte, 4))
			return "failed to connect to " + serial
		}
		client, _ := adbBridgeFixture(t, host)
		_, events := shareADB(t, client, phone.port())
		refuse(t, phone, "STLS")
		ending := adbClosed(t, events)
		if ending["code"] != "unpaired" || !strings.Contains(ending["message"], "Pair it with the server") {
			t.Fatalf("unexpected ending: %v", ending)
		}
		adbQuiet(t, events)
	})

	// adb can take its time to give up on a handshake that will never finish.
	waiting := func(transport net.Conn, _ string) string {
		_, _ = io.Copy(io.Discard, transport)
		return ""
	}
	t.Run("while adb is still waiting", func(t *testing.T) {
		shortenADB(t, &adbVerdictWait, 50*time.Millisecond)
		phone := newWirelessDebugging(t)
		host := newHostADB(t)
		host.connect = waiting
		client, _ := adbBridgeFixture(t, host)
		_, events := shareADB(t, client, phone.port())
		refuse(t, phone, "STLS")
		if ending := adbClosed(t, events); ending["code"] != "unpaired" {
			t.Fatalf("unexpected ending: %v", ending)
		}
		adbQuiet(t, events)
	})

	t.Run("a port that hangs up without a word is not a pairing problem", func(t *testing.T) {
		shortenADB(t, &adbVerdictWait, 50*time.Millisecond)
		phone := newWirelessDebugging(t)
		host := newHostADB(t)
		host.connect = waiting
		client, _ := adbBridgeFixture(t, host)
		_, events := shareADB(t, client, phone.port())
		refuse(t, phone, "")
		if ending := adbClosed(t, events); ending["code"] != "device_closed" {
			t.Fatalf("unexpected ending: %v", ending)
		}
		adbQuiet(t, events)
	})
}

func TestADBPairingRelaysTheExchangeAndReportsADBsVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, output, kind, code, message string
	}{
		{"accepted", "Successfully paired to 127.0.0.1:1 [guid=adb-fixture]", "paired", "", ""},
		{"wrong code", "Failed: Wrong password or connection was dropped.", "closed", "pair_failed", "Failed: Wrong password or connection was dropped."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			phone := newWirelessDebugging(t)
			host := newHostADB(t)
			codes := make(chan string, 1)
			host.pair = func(exchange net.Conn, code string) string {
				codes <- code
				_, _ = io.ReadFull(exchange, make([]byte, len("server hello")))
				return tc.output
			}
			client, hellos := adbBridgeFixture(t, host)
			events := make(eventRecorder, 8)
			stream, err := client.PairADB("csrf-token", "123456", phone.port(), events)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { stream.Close(); <-stream.done })

			if hello := adbReceive(t, hellos); hello != (bridgeHello{Type: "pair", CSRF: "csrf-token", Code: "123456"}) {
				t.Fatalf("unexpected hello: %+v", hello)
			}
			if fields := expectADBEvent(t, events, "ready"); len(fields) != 0 {
				t.Fatalf("a pairing has no serial: %v", fields)
			}
			if code := adbReceive(t, codes); code != "123456" {
				t.Fatalf("adb paired with %q", code)
			}
			dialog := phone.accept(t)
			if opening := adbRead(t, dialog, len(adbPairingHello)); !bytes.Equal(opening, adbPairingHello) {
				t.Fatalf("the phone heard %q", opening)
			}
			if _, err := dialog.Write([]byte("server hello")); err != nil {
				t.Fatal(err)
			}
			// The phone hangs up as soon as the exchange is over, either way.
			dialog.Close()
			kind, fields := nextADBEvent(t, events)
			if kind != tc.kind || fields["code"] != tc.code || fields["message"] != tc.message {
				t.Fatalf("got %s %v", kind, fields)
			}
			adbQuiet(t, events)
		})
	}
}

func TestADBShareNeedsWirelessDebuggingBeforeItAsksTheServer(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	client := nativeFixture(t, func(http.ResponseWriter, *http.Request) {
		t.Error("the server was asked to share a phone that is not debuggable")
	})
	_, events := shareADB(t, client, port)
	if kind, fields := nextADBEvent(t, events); kind != "closed" || fields["code"] != "device_unreachable" {
		t.Fatalf("got %s %v", kind, fields)
	}
	adbQuiet(t, events)
}

func TestADBShareReportsHowTheServerEndedIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		serve   func(conn *websocket.Conn)
		code    string
		message string
	}{
		{name: "signed out", status: http.StatusUnauthorized, code: "auth_expired"},
		{name: "not allowed", status: http.StatusForbidden, code: "forbidden"},
		{name: "stopping", status: http.StatusServiceUnavailable, code: "network"},
		{name: "stale session token", serve: func(conn *websocket.Conn) {
			_ = conn.Close(websocket.StatusPolicyViolation, "invalid credentials")
		}, code: "auth_expired"},
		{name: "access withdrawn", serve: func(conn *websocket.Conn) {
			ctx, cancel := context.WithTimeout(context.Background(), adbPatience)
			defer cancel()
			_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"error","code":"revoked","message":"ADB bridge access ended."}`))
		}, code: "revoked", message: "ADB bridge access ended."},
		{name: "connection lost", serve: func(*websocket.Conn) {}, code: "network"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			phone := newWirelessDebugging(t)
			var client *Client
			if tc.serve == nil {
				client = nativeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "refused", tc.status)
				})
			} else {
				client = adbServerFixture(t, func(conn *websocket.Conn, _ bridgeHello) { tc.serve(conn) })
			}
			_, events := shareADB(t, client, phone.port())
			if ending := adbClosed(t, events); ending["code"] != tc.code || ending["message"] != tc.message {
				t.Fatalf("unexpected ending: %v", ending)
			}
			adbQuiet(t, events)
			// The phone's debugging connection does not outlive the share.
			debugging := phone.accept(t)
			if _, err := debugging.Read(make([]byte, 1)); err != io.EOF {
				t.Fatalf("the phone's connection stayed open: %v", err)
			}
		})
	}
}

func TestADBShareStopsQuietlyAndEndsWithItsRoute(t *testing.T) {
	phone := newWirelessDebugging(t)
	host := newHostADB(t)
	client, _ := adbBridgeFixture(t, host)
	online := func(events eventRecorder) string {
		t.Helper()
		serial := expectADBEvent(t, events, "ready")["serial"]
		for _, want := range []string{"connecting", "online"} {
			if state := expectADBEvent(t, events, "state")["state"]; state != want {
				t.Fatalf("got state %q, want %s", state, want)
			}
		}
		return serial
	}

	stream, events := shareADB(t, client, phone.port())
	serial := online(events)
	debugging := phone.accept(t)
	adbRead(t, debugging, len(adbConnectPacket))
	stream.Close()
	// Stopping is the caller's own doing: nothing is reported back, and the
	// phone leaves the server's adb.
	adbQuiet(t, events)
	if _, err := debugging.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("the phone's connection stayed open: %v", err)
	}
	host.detaches(t, serial, 2)

	// Sharing the same phone again keeps the serial that scripts on the
	// server already use.
	_, events = shareADB(t, client, phone.port())
	if again := online(events); again != serial {
		t.Fatalf("the phone came back as %s, was %s", again, serial)
	}
	// A route that is closed under the share ends it like a lost network, so
	// the caller can share again over the next one.
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if ending := adbClosed(t, events); ending["code"] != "network" {
		t.Fatalf("unexpected ending: %v", ending)
	}
	adbQuiet(t, events)
}

func TestADBShareGivesUpOnASilentServer(t *testing.T) {
	shortenADB(t, &adbSilenceLimit, 100*time.Millisecond)
	phone := newWirelessDebugging(t)
	client := adbServerFixture(t, func(conn *websocket.Conn, _ bridgeHello) {
		// Reads until the client hangs up, and never says anything.
		_, _, _ = conn.Read(context.Background())
	})
	_, events := shareADB(t, client, phone.port())
	if kind, fields := nextADBEvent(t, events); kind != "closed" || fields["code"] != "network" {
		t.Fatalf("got %s %v", kind, fields)
	}
	adbQuiet(t, events)
}

func TestADBRequestsAreCheckedBeforeAnythingIsDialed(t *testing.T) {
	client := nativeFixture(t, func(http.ResponseWriter, *http.Request) {
		t.Error("an invalid ADB bridge request reached the server")
	})
	events := make(eventRecorder, 1)
	const device, code, port = "android-0123456789abcdef", "123456", 37000
	for name, open := range map[string]func() (*ADBStream, error){
		"no session token":   func() (*ADBStream, error) { return client.ShareADB("", device, port, events) },
		"no listener":        func() (*ADBStream, error) { return client.ShareADB("csrf-token", device, port, nil) },
		"device key":         func() (*ADBStream, error) { return client.ShareADB("csrf-token", "my phone", port, events) },
		"long device key":    func() (*ADBStream, error) { return client.ShareADB("csrf-token", strings.Repeat("a", 97), port, events) },
		"port zero":          func() (*ADBStream, error) { return client.ShareADB("csrf-token", device, 0, events) },
		"port out of range":  func() (*ADBStream, error) { return client.ShareADB("csrf-token", device, 65536, events) },
		"short pairing code": func() (*ADBStream, error) { return client.PairADB("csrf-token", "12345", port, events) },
		"pairing code":       func() (*ADBStream, error) { return client.PairADB("csrf-token", "12345a", port, events) },
		"pairing token":      func() (*ADBStream, error) { return client.PairADB("", code, port, events) },
		"pairing port":       func() (*ADBStream, error) { return client.PairADB("csrf-token", code, -1, events) },
	} {
		if stream, err := open(); err == nil || stream != nil {
			t.Errorf("%s: an invalid request was accepted", name)
		}
	}
	adbQuiet(t, events)
}
