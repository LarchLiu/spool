package adbbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// patience bounds every wait for something that should happen promptly. Real
// adb processes take longer than the fakes, and a loaded machine longer still.
const patience = 20 * time.Second

type fakeADB struct {
	connect func(ctx context.Context, serial string) (string, error)
	wait    func(ctx context.Context, serial string) error
	pair    func(ctx context.Context, address, code string) (string, error)

	mu           sync.Mutex
	disconnected []string
	transports   []net.Conn
}

func (f *fakeADB) Connect(ctx context.Context, serial string) (string, error) {
	if f.connect != nil {
		return f.connect(ctx, serial)
	}
	// What adb's server does: open the endpoint and announce itself.
	transport, err := f.open(serial, "CNXN")
	if err != nil {
		return "failed to connect to " + serial, nil
	}
	f.keep(transport)
	return "connected to " + serial, nil
}

func (f *fakeADB) open(address, greeting string) (net.Conn, error) {
	transport, err := net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	if _, err = transport.Write([]byte(greeting)); err != nil {
		transport.Close()
		return nil, err
	}
	return transport, nil
}

func (f *fakeADB) keep(transport net.Conn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transports = append(f.transports, transport)
}

func (f *fakeADB) Disconnect(_ context.Context, serial string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnected = append(f.disconnected, serial)
}

func (f *fakeADB) disconnects(serial string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, value := range f.disconnected {
		if value == serial {
			count++
		}
	}
	return count
}

func (f *fakeADB) WaitForDevice(ctx context.Context, serial string) error {
	if f.wait != nil {
		return f.wait(ctx, serial)
	}
	return nil
}

func (f *fakeADB) Pair(ctx context.Context, address, code string) (string, error) {
	return f.pair(ctx, address, code)
}

type outcome struct {
	serial string
	err    error
}

type harness struct {
	t       *testing.T
	adb     *fakeADB
	manager *Manager
	url     string

	mu      sync.Mutex
	next    int
	streams map[string]*client
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, adb: &fakeADB{}, streams: map[string]*client{}}
	h.manager = NewWith(h.adb)
	ctx, stop := context.WithCancel(context.Background())
	var handlers sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A hijacked connection is no longer tracked by the test server.
		handlers.Add(1)
		defer handlers.Done()
		query := r.URL.Query()
		h.mu.Lock()
		stream := h.streams[query.Get("id")]
		h.mu.Unlock()
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		streamCtx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		stream.revoke = cancel
		if query.Has("revoked") {
			cancel(&Error{"revoked", "ADB bridge access ended."})
		}
		close(stream.accepted)
		var result outcome
		if code := query.Get("code"); query.Has("code") {
			result.err = h.manager.Pair(streamCtx, conn, code)
		} else {
			result.serial, result.err = h.manager.Attach(streamCtx, conn, Request{Owner: query.Get("owner"), Transport: Transport(query.Get("transport")), Device: query.Get("device")})
		}
		stream.outcome <- result
	}))
	t.Cleanup(func() {
		stop()
		h.mu.Lock()
		streams := h.streams
		h.mu.Unlock()
		for _, stream := range streams {
			stream.conn.CloseNow()
			<-stream.done
		}
		handlers.Wait()
		server.Close()
		h.adb.mu.Lock()
		for _, transport := range h.adb.transports {
			transport.Close()
		}
		h.adb.mu.Unlock()
	})
	h.url = "ws" + strings.TrimPrefix(server.URL, "http")
	return h
}

func (h *harness) attach(owner string, transport Transport, device string) *client {
	h.t.Helper()
	return h.dial(url.Values{"owner": {owner}, "transport": {string(transport)}, "device": {device}})
}

func (h *harness) pair(code string) *client {
	h.t.Helper()
	return h.dial(url.Values{"code": {code}})
}

func (h *harness) dial(query url.Values) *client {
	h.t.Helper()
	c := &client{t: h.t, texts: make(chan message, 64), grew: make(chan struct{}, 1), done: make(chan struct{}), accepted: make(chan struct{}), outcome: make(chan outcome, 1)}
	h.mu.Lock()
	h.next++
	id := strconv.Itoa(h.next)
	h.streams[id] = c
	h.mu.Unlock()
	query.Set("id", id)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, h.url+"/?"+query.Encode(), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	conn.SetReadLimit(readLimit)
	c.conn = conn
	go c.read()
	return c
}

// client is the device side of one stream: a browser or the Android app.
type client struct {
	t        *testing.T
	conn     *websocket.Conn
	texts    chan message
	grew     chan struct{}
	done     chan struct{}
	accepted chan struct{}
	outcome  chan outcome
	revoke   context.CancelCauseFunc

	mu     sync.Mutex
	binary []byte
	alive  int
}

func (c *client) read() {
	defer close(c.done)
	for {
		kind, data, err := c.conn.Read(context.Background())
		if err != nil {
			return
		}
		if kind == websocket.MessageBinary {
			c.mu.Lock()
			c.binary = append(c.binary, data...)
			c.mu.Unlock()
			select {
			case c.grew <- struct{}{}:
			default:
			}
			continue
		}
		var m message
		if err := json.Unmarshal(data, &m); err != nil {
			c.t.Errorf("invalid control message %q: %v", data, err)
			return
		}
		if m.Type == "alive" {
			c.mu.Lock()
			c.alive++
			c.mu.Unlock()
			continue
		}
		c.texts <- m
	}
}

func (c *client) expect(kind string) message {
	c.t.Helper()
	select {
	case m := <-c.texts:
		if m.Type != kind {
			c.t.Fatalf("got %+v, want a %q message", m, kind)
		}
		return m
	case <-c.done:
		select {
		case m := <-c.texts:
			if m.Type != kind {
				c.t.Fatalf("got %+v, want a %q message", m, kind)
			}
			return m
		default:
		}
		c.t.Fatalf("stream closed while waiting for %q", kind)
	case <-time.After(patience):
		c.t.Fatalf("timed out waiting for %q", kind)
	}
	return message{}
}

func (c *client) expectState(state string) {
	c.t.Helper()
	if m := c.expect("state"); m.State != state {
		c.t.Fatalf("got state %q, want %q", m.State, state)
	}
}

func (c *client) expectError(code string) message {
	c.t.Helper()
	m := c.expect("error")
	if m.Code != code {
		c.t.Fatalf("got error %+v, want code %q", m, code)
	}
	c.expectClosed()
	return m
}

// ready consumes the messages every attachment begins with.
func (c *client) ready() string {
	c.t.Helper()
	serial := c.expect("ready").Serial
	host, _, err := net.SplitHostPort(serial)
	if err != nil || host != "127.0.0.1" {
		c.t.Fatalf("serial %q is not a loopback endpoint", serial)
	}
	c.expectState("connecting")
	return serial
}

func (c *client) receive(want string) {
	c.t.Helper()
	got, ok := c.next(len(want), len(want), time.After(patience))
	if !ok {
		c.t.Fatalf("stream ended before the device received %q", want)
	}
	if string(got) != want {
		c.t.Fatalf("device received %q, want %q", got, want)
	}
}

// next returns between least and most of the bytes adb sent, in order, or
// false if the stream closed or expired first.
func (c *client) next(least, most int, expired <-chan time.Time) ([]byte, bool) {
	for {
		c.mu.Lock()
		if available := len(c.binary); available >= least {
			n := min(available, most)
			taken := append([]byte(nil), c.binary[:n]...)
			c.binary = c.binary[n:]
			c.mu.Unlock()
			return taken, true
		}
		c.mu.Unlock()
		select {
		case <-c.grew:
		case <-c.done:
			// The reader appends everything it received before it stops.
			c.mu.Lock()
			complete := len(c.binary) >= least
			c.mu.Unlock()
			if !complete {
				return nil, false
			}
		case <-expired:
			return nil, false
		}
	}
}

func (c *client) send(data string) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageBinary, []byte(data)); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) expectClosed() {
	c.t.Helper()
	select {
	case <-c.done:
	case <-time.After(patience):
		c.t.Fatal("stream stayed open")
	}
}

func (c *client) result() outcome {
	c.t.Helper()
	select {
	case result := <-c.outcome:
		return result
	case <-time.After(patience):
		c.t.Fatal("the bridge did not finish")
	}
	return outcome{}
}

func readExactly(t *testing.T, source net.Conn, want string) {
	t.Helper()
	_ = source.SetReadDeadline(time.Now().Add(patience))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(source, got); err != nil || string(got) != want {
		t.Fatalf("adb received %q (%v), want %q", got, err, want)
	}
	_ = source.SetReadDeadline(time.Time{})
}

func expectEOF(t *testing.T, source net.Conn) {
	t.Helper()
	_ = source.SetReadDeadline(time.Now().Add(patience))
	if n, err := source.Read(make([]byte, 1)); n != 0 || err == nil || errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		t.Fatalf("adb transport stayed open: %d %v", n, err)
	}
}

func isTimeout(err error) bool {
	var timeout net.Error
	return errors.As(err, &timeout) && timeout.Timeout()
}

func TestAttachRelaysTheTransportAndRemovesTheDeviceWhenTheClientLeaves(t *testing.T) {
	h := newHarness(t)
	transports := make(chan net.Conn, 1)
	h.adb.connect = func(_ context.Context, serial string) (string, error) {
		transport, err := h.adb.open(serial, "CNXN host banner")
		if err != nil {
			return "failed to connect to " + serial, nil
		}
		h.adb.keep(transport)
		// adb only reports success once the device answered.
		reply := make([]byte, len("CNXN device banner"))
		if _, err := io.ReadFull(transport, reply); err != nil {
			return "failed to connect to " + serial, nil
		}
		transports <- transport
		return "connected to " + serial, nil
	}
	device := h.attach("alice", TransportUSB, "R58M000001")
	serial := device.ready()
	device.receive("CNXN host banner")
	device.send("CNXN device banner")
	device.expectState("online")
	if attached := h.manager.Devices(); len(attached) != 1 || attached[0] != (Device{Owner: "alice", Transport: TransportUSB, Serial: serial}) {
		t.Fatalf("attached devices are %+v, want alice's %s", attached, serial)
	}

	transport := <-transports
	if _, err := transport.Write([]byte("shell:id")); err != nil {
		t.Fatal(err)
	}
	device.receive("shell:id")
	device.send("uid=2000(shell)")
	readExactly(t, transport, "uid=2000(shell)")

	// A large payload crosses in order whatever the frame boundaries are.
	payload := strings.Repeat("0123456789abcdef", 40_000)
	go transport.Write([]byte(payload))
	device.receive(payload)
	device.send(payload)
	readExactly(t, transport, payload)

	if err := device.conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatal(err)
	}
	result := device.result()
	if result.serial != serial || !errors.Is(result.err, errClientGone) {
		t.Fatalf("attachment ended as %+v, want client departure for %s", result, serial)
	}
	expectEOF(t, transport)
	// Once to clear a stale transport before connecting, once to detach.
	if got := h.adb.disconnects(serial); got != 2 {
		t.Fatalf("adb disconnect ran %d times for %s, want 2", got, serial)
	}
	if attached := h.manager.Devices(); len(attached) != 0 {
		t.Fatalf("a detached device is still listed: %+v", attached)
	}
}

func TestAttachReportsUnauthorizedUntilThePhoneAllowsTheServerKey(t *testing.T) {
	h := newHarness(t)
	allowed := make(chan struct{})
	h.adb.connect = func(_ context.Context, serial string) (string, error) {
		transport, err := h.adb.open(serial, "CNXN")
		if err != nil {
			return "failed to connect to " + serial, nil
		}
		h.adb.keep(transport)
		return "failed to authenticate to " + serial, nil
	}
	h.adb.wait = func(ctx context.Context, _ string) error {
		select {
		case <-allowed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	device := h.attach("alice", TransportUSB, "R58M000001")
	device.ready()
	device.expectState("unauthorized")
	select {
	case m := <-device.texts:
		t.Fatalf("device went %+v before the phone allowed the key", m)
	case <-time.After(50 * time.Millisecond):
	}
	close(allowed)
	device.expectState("online")
}

func TestAttachEndsWhenADBClosesAnEstablishedTransport(t *testing.T) {
	h := newHarness(t)
	device := h.attach("alice", TransportUSB, "R58M000001")
	serial := device.ready()
	device.expectState("online")
	h.adb.mu.Lock()
	h.adb.transports[0].Close()
	h.adb.mu.Unlock()
	device.expectError("adb_closed")
	var reported *Error
	if result := device.result(); !errors.As(result.err, &reported) || reported.Code != "adb_closed" {
		t.Fatalf("attachment ended as %+v", result)
	}
	if got := h.adb.disconnects(serial); got != 2 {
		t.Fatalf("adb disconnect ran %d times, want 2", got)
	}
}

func TestRefusedTransportsDistinguishAnUnpairedPhone(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		transport Transport
		answered  bool
		code      string
	}{
		{"wireless phone that answered and dropped the key", TransportWireless, true, "unpaired"},
		{"wireless phone that never answered", TransportWireless, false, "connect_failed"},
		{"usb phone that answered", TransportUSB, true, "connect_failed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			h := newHarness(t)
			// adb waits for the handshake before giving up, so the device has
			// always seen the host's first packet by then.
			seen := make(chan struct{})
			h.adb.connect = func(ctx context.Context, serial string) (string, error) {
				transport, err := h.adb.open(serial, "CNXN")
				if err != nil {
					return "failed to connect to " + serial, nil
				}
				defer transport.Close()
				if scenario.answered {
					readExactly(t, transport, "STLS")
				} else {
					select {
					case <-seen:
					case <-ctx.Done():
					}
				}
				return "failed to connect to " + serial, nil
			}
			device := h.attach("alice", scenario.transport, "phone")
			device.ready()
			device.receive("CNXN")
			close(seen)
			if scenario.answered {
				device.send("STLS")
			}
			device.expectError(scenario.code)
		})
	}
}

func TestAttachingTheSameDeviceAgainReplacesItsStreamAndKeepsItsSerial(t *testing.T) {
	h := newHarness(t)
	first := h.attach("alice", TransportUSB, "phone")
	serial := first.ready()
	first.expectState("online")

	second := h.attach("alice", TransportUSB, "phone")
	first.expectError("replaced")
	if got := second.ready(); got != serial {
		t.Fatalf("reattached device got serial %s, want %s", got, serial)
	}
	second.expectState("online")
	// The earlier stream removed its transport before the new one connected.
	if got := h.adb.disconnects(serial); got != 3 {
		t.Fatalf("adb disconnect ran %d times, want 3", got)
	}

	// The key belongs to its owner: another user's device is unrelated.
	other := h.attach("bob", TransportUSB, "phone")
	if got := other.ready(); got == serial {
		t.Fatal("another user's device reused the serial")
	}
	other.expectState("online")
	select {
	case m := <-second.texts:
		t.Fatalf("another user's device disturbed the stream: %+v", m)
	case <-second.done:
		t.Fatal("another user's device closed the stream")
	case <-time.After(50 * time.Millisecond):
	}

	// A serial outlives the stream, so scripts on the host keep working.
	second.conn.Close(websocket.StatusNormalClosure, "")
	second.result()
	third := h.attach("alice", TransportUSB, "phone")
	if got := third.ready(); got != serial {
		t.Fatalf("returning device got serial %s, want %s", got, serial)
	}
}

func TestAttachOnlyAcceptsALocalPeerThatOpensLikeADB(t *testing.T) {
	h := newHarness(t)
	h.adb.connect = func(_ context.Context, serial string) (string, error) {
		probe, err := h.adb.open(serial, "GET / HTTP/1.1\r\n\r\n")
		if err != nil {
			t.Error(err)
			return "failed to connect to " + serial, nil
		}
		defer probe.Close()
		expectEOF(t, probe)
		transport, err := h.adb.open(serial, "CNXN")
		if err != nil {
			t.Error(err)
			return "failed to connect to " + serial, nil
		}
		h.adb.keep(transport)
		return "connected to " + serial, nil
	}
	device := h.attach("alice", TransportUSB, "phone")
	serial := device.ready()
	device.receive("CNXN")
	device.expectState("online")
	// The endpoint is gone once adb holds the device.
	if extra, err := net.DialTimeout("tcp", serial, time.Second); err == nil {
		extra.Close()
		t.Fatal("the endpoint accepted a second local connection")
	}
}

func TestAttachLimitsDevicesPerOwnerAndRejectsInvalidRequests(t *testing.T) {
	h := newHarness(t)
	for i := range maxOwnerAttached {
		device := h.attach("alice", TransportUSB, "phone-"+strconv.Itoa(i))
		device.ready()
		device.expectState("online")
	}
	h.attach("alice", TransportUSB, "one-too-many").expectError("busy")
	other := h.attach("bob", TransportUSB, "phone")
	other.ready()

	for _, request := range []Request{
		{Owner: "alice", Transport: "serial", Device: "phone"},
		{Owner: "alice", Transport: TransportUSB, Device: ""},
		{Owner: "alice", Transport: TransportUSB, Device: "has space"},
		{Owner: "alice", Transport: TransportUSB, Device: strings.Repeat("a", 97)},
		{Owner: "", Transport: TransportUSB, Device: "phone"},
	} {
		h.attach(request.Owner, request.Transport, request.Device).expectError("invalid")
	}
}

func TestAttachIsUnavailableWithoutADBAndReportsAParentCause(t *testing.T) {
	h := newHarness(t)
	h.manager = NewWith(nil)
	if h.manager.Available() {
		t.Fatal("a manager without adb reported itself available")
	}
	h.attach("alice", TransportUSB, "phone").expectError("unavailable")

	h.manager = NewWith(h.adb)
	device := h.attach("alice", TransportUSB, "phone")
	serial := device.ready()
	device.expectState("online")
	<-device.accepted
	device.revoke(&Error{"revoked", "ADB bridge access ended."})
	device.expectError("revoked")
	device.result()
	if got := h.adb.disconnects(serial); got != 2 {
		t.Fatalf("a revoked device stayed in adb: %d disconnects", got)
	}

	// Access that ended before the bridge started never reaches adb.
	early := h.dial(url.Values{"owner": {"alice"}, "transport": {"usb"}, "device": {"tablet"}, "revoked": {"1"}})
	early.expectError("revoked")
	if result := early.result(); result.serial != "" {
		t.Fatalf("a revoked request was offered to adb as %s", result.serial)
	}
	pairing := h.dial(url.Values{"code": {"123456"}, "revoked": {"1"}})
	pairing.expectError("revoked")
	pairing.result()
}

func TestAttachSendsRegularSignsOfLife(t *testing.T) {
	previous := aliveInterval
	aliveInterval = 20 * time.Millisecond
	t.Cleanup(func() { aliveInterval = previous })
	h := newHarness(t)
	device := h.attach("alice", TransportUSB, "phone")
	device.ready()
	device.expectState("online")
	deadline := time.Now().Add(patience)
	for {
		device.mu.Lock()
		alive := device.alive
		device.mu.Unlock()
		if alive >= 3 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("received %d signs of life", alive)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPairRelaysTheExchangeAndReportsADBsVerdict(t *testing.T) {
	hello := string([]byte{0x16, 0x03, 0x01, 0x00, 0x05}) + "hello"
	exchange := func(h *harness, verdict string) func(context.Context, string, string) (string, error) {
		return func(_ context.Context, address, code string) (string, error) {
			if code != "123456" {
				t.Errorf("adb received pairing code %q", code)
			}
			pairing, err := h.adb.open(address, hello)
			if err != nil {
				return "", err
			}
			defer pairing.Close()
			readExactly(t, pairing, "server hello")
			return "Enter pairing code: " + verdict, nil
		}
	}
	t.Run("paired", func(t *testing.T) {
		h := newHarness(t)
		h.adb.pair = exchange(h, "Successfully paired to 127.0.0.1:1 [guid=adb-test]")
		phone := h.pair("123456")
		phone.expect("ready")
		phone.receive(hello)
		phone.send("server hello")
		phone.expect("paired")
		phone.expectClosed()
		if result := phone.result(); result.err != nil {
			t.Fatal(result.err)
		}
	})
	t.Run("wrong code", func(t *testing.T) {
		h := newHarness(t)
		h.adb.pair = exchange(h, "Failed: Wrong password or connection was dropped.")
		phone := h.pair("123456")
		phone.expect("ready")
		phone.receive(hello)
		phone.send("server hello")
		if m := phone.expectError("pair_failed"); m.Message != "Failed: Wrong password or connection was dropped." {
			t.Fatalf("pairing failure reported as %q", m.Message)
		}
	})
	t.Run("invalid code never reaches adb", func(t *testing.T) {
		h := newHarness(t)
		h.adb.pair = func(context.Context, string, string) (string, error) {
			t.Error("adb pair ran for an invalid code")
			return "", nil
		}
		for _, code := range []string{"", "12345", "12345a", "123456\nshell"} {
			h.pair(code).expectError("invalid")
		}
	})
	t.Run("a departing phone stops adb", func(t *testing.T) {
		h := newHarness(t)
		stopped := make(chan struct{})
		h.adb.pair = func(ctx context.Context, _, _ string) (string, error) {
			<-ctx.Done()
			close(stopped)
			return "", ctx.Err()
		}
		phone := h.pair("123456")
		phone.expect("ready")
		phone.conn.Close(websocket.StatusNormalClosure, "")
		select {
		case <-stopped:
		case <-time.After(patience):
			t.Fatal("adb pair kept running")
		}
		if result := phone.result(); !errors.Is(result.err, errClientGone) {
			t.Fatalf("pairing ended as %v", result.err)
		}
	})
}
