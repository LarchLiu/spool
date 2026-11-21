// Package adbbridge attaches an Android device that is physically with a Spool
// client to the adb server on this host.
//
// The client owns the device side: a browser relays the phone's USB ADB
// interface, the Android app relays its own wireless-debugging port. Either way
// one WebSocket carries the bytes of exactly one ADB transport connection. The
// bridge offers that stream to the local adb server as a loopback TCP device,
// so every adb client on this host addresses it as 127.0.0.1:<port>.
//
// The bridge never interprets ADB traffic and holds no key. Authorization stays
// between the phone and the adb server's own key, exactly as with a cable.
package adbbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

type Transport string

const (
	// TransportUSB carries the plain ADB protocol of a USB interface.
	TransportUSB Transport = "usb"
	// TransportWireless carries a wireless-debugging connection, which upgrades
	// to TLS and needs the phone to be paired with the server's adb key.
	TransportWireless Transport = "wireless"
)

const (
	// The totals only bound loopback listeners and adb transports on a shared host.
	maxAttached      = 16
	maxOwnerAttached = 4
	maxPairing       = 4
	maxSlots         = 256
	// One client message holds at most a complete ADB packet: a 24-byte header
	// and a payload of up to 1 MiB.
	readLimit  = 1<<20 + 4096
	relayChunk = 64 << 10
)

// Timings are variables so tests can shorten them.
var (
	aliveInterval = 15 * time.Second
	aliveTimeout  = 30 * time.Second
	firstBytes    = 5 * time.Second
	takeoverWait  = 15 * time.Second
)

var (
	deviceKey   = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,96}$`)
	pairingCode = regexp.MustCompile(`^[0-9]{6,12}$`)

	errClientGone = errors.New("adb bridge client disconnected")
	errPeerClosed = errors.New("adb closed the transport")
)

// Error is a terminal condition the client is told about before its stream
// closes. Codes are part of the client protocol; messages are diagnostics.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Request identifies one shared device. Device is a client-chosen stable key;
// the same owner attaching the same device again replaces its earlier stream
// and gets the same serial back.
type Request struct {
	Owner     string
	Transport Transport
	Device    string
}

func (r Request) valid() bool {
	return r.Owner != "" && (r.Transport == TransportUSB || r.Transport == TransportWireless) && deviceKey.MatchString(r.Device)
}

type message struct {
	Type    string `json:"type"`
	Serial  string `json:"serial,omitempty"`
	State   string `json:"state,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// Manager owns the loopback endpoints offered to the host's adb server.
type Manager struct {
	locate func() (ADB, bool)

	mu      sync.Mutex
	slots   map[string]*slot
	pairing int
}

// A slot remembers the port of one owner's device so that reattaching keeps the
// serial that agents and scripts on this host already use.
type slot struct {
	port   int
	active *lease
	used   time.Time
}

type lease struct {
	manager   *Manager
	slot      *slot
	owner     string
	transport Transport
	serial    string
	cancel    context.CancelCauseFunc
	done      chan struct{}
}

// Device is one device currently offered to the host's adb server.
type Device struct {
	Owner     string
	Transport Transport
	Serial    string
}

// New looks adb up on every use, so installing the SDK needs no restart.
func New() *Manager {
	return &Manager{locate: func() (ADB, bool) {
		path, ok := Locate()
		if !ok {
			return nil, false
		}
		return CLI{Path: path}, true
	}}
}

// NewWith uses a fixed adb, such as one bound to a private adb server.
func NewWith(adb ADB) *Manager {
	return &Manager{locate: func() (ADB, bool) { return adb, adb != nil }}
}

func (m *Manager) Available() bool {
	_, ok := m.locate()
	return ok
}

// Devices lists what is attached, ordered by serial.
func (m *Manager) Devices() []Device {
	m.mu.Lock()
	defer m.mu.Unlock()
	devices := []Device{}
	for _, slot := range m.slots {
		if held := slot.active; held != nil && held.serial != "" {
			devices = append(devices, Device{Owner: held.owner, Transport: held.transport, Serial: held.serial})
		}
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Serial < devices[j].Serial })
	return devices
}

// Attach serves one shared device until its stream, the adb transport or ctx
// ends, then removes the device from the adb server. It returns the serial the
// device had and why the attachment ended.
func (m *Manager) Attach(parent context.Context, conn *websocket.Conn, request Request) (string, error) {
	conn.SetReadLimit(readLimit)
	// The stream outlives a cancelled parent just long enough to say why.
	ioCtx, ioCancel := context.WithCancel(context.WithoutCancel(parent))
	defer ioCancel()
	r := &relay{conn: conn}
	adb, ok := m.locate()
	if !ok {
		return "", r.reject(ioCtx, &Error{"unavailable", "adb is not installed on the server."})
	}
	if !request.valid() {
		return "", r.reject(ioCtx, &Error{"invalid", "Invalid ADB bridge request."})
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	if ctx.Err() != nil {
		return "", r.reject(ioCtx, context.Cause(ctx))
	}
	held, err := m.acquire(ctx, request, cancel)
	if err != nil {
		return "", r.reject(ioCtx, err)
	}
	// Released last: the next stream for this device may only bind the port and
	// run adb connect after this one has finished adb disconnect.
	defer held.release()
	listener, err := held.listen()
	if err != nil {
		return "", r.reject(ioCtx, &Error{"internal", "The server could not open a local ADB endpoint."})
	}
	r.listener = listener
	serial := listener.Addr().String()
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer done()
		adb.Disconnect(cleanup, serial)
	}()
	if r.send(ioCtx, message{Type: "ready", Serial: serial}) != nil {
		r.end(ioCtx, nil)
		return serial, errClientGone
	}

	// Until adb accepts the device, a closed transport is a refused connection
	// rather than a lost one.
	var established atomic.Bool
	var work sync.WaitGroup
	work.Go(func() { cancel(r.readClient(ioCtx)) })
	work.Go(func() {
		err := r.serveADB(ioCtx, isConnectPacket)
		if errors.Is(err, errPeerClosed) {
			if established.Load() {
				err = &Error{"adb_closed", "The server's adb closed the device connection."}
			} else {
				err = r.refused(request.Transport, "")
			}
		}
		if err != nil {
			cancel(err)
		}
	})
	work.Go(func() {
		if err := r.keepAlive(ctx, ioCtx); err != nil {
			cancel(err)
		}
	})
	work.Go(func() {
		// A transport left behind by a crashed server would answer "already
		// connected" without ever opening this endpoint.
		adb.Disconnect(ctx, serial)
		if r.send(ioCtx, message{Type: "state", State: "connecting"}) != nil {
			return
		}
		output, err := adb.Connect(ctx, serial)
		if ctx.Err() != nil {
			return
		}
		switch {
		case strings.Contains(output, "connected to "+serial):
		case strings.Contains(output, "failed to authenticate"):
			// The phone is showing its authorization dialog; adb keeps the
			// transport and goes online once the user allows the key.
			established.Store(true)
			if r.send(ioCtx, message{Type: "state", State: "unauthorized"}) != nil {
				return
			}
		default:
			cancel(r.refused(request.Transport, diagnostic(output, err)))
			return
		}
		established.Store(true)
		if err := adb.WaitForDevice(ctx, serial); err != nil {
			if ctx.Err() == nil {
				cancel(&Error{"connect_failed", diagnostic("", err)})
			}
			return
		}
		_ = r.send(ioCtx, message{Type: "state", State: "online"})
	})

	<-ctx.Done()
	cause := context.Cause(ctx)
	r.end(ioCtx, terminal(cause))
	ioCancel()
	work.Wait()
	return serial, cause
}

// Pair runs one wireless-debugging pairing between the server's adb key and
// the phone whose pairing port the client relays. A nil result means paired.
func (m *Manager) Pair(parent context.Context, conn *websocket.Conn, code string) error {
	conn.SetReadLimit(readLimit)
	ioCtx, ioCancel := context.WithCancel(context.WithoutCancel(parent))
	defer ioCancel()
	r := &relay{conn: conn}
	adb, ok := m.locate()
	if !ok {
		return r.reject(ioCtx, &Error{"unavailable", "adb is not installed on the server."})
	}
	if !pairingCode.MatchString(code) {
		return r.reject(ioCtx, &Error{"invalid", "Invalid pairing code."})
	}
	if parent.Err() != nil {
		return r.reject(ioCtx, context.Cause(parent))
	}
	m.mu.Lock()
	busy := m.pairing >= maxPairing
	if !busy {
		m.pairing++
	}
	m.mu.Unlock()
	if busy {
		return r.reject(ioCtx, &Error{"busy", "Too many devices are pairing. Try again shortly."})
	}
	defer func() {
		m.mu.Lock()
		m.pairing--
		m.mu.Unlock()
	}()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return r.reject(ioCtx, &Error{"internal", "The server could not open a local ADB endpoint."})
	}
	r.listener = listener
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	if r.send(ioCtx, message{Type: "ready"}) != nil {
		r.end(ioCtx, nil)
		return errClientGone
	}

	type outcome struct {
		output string
		err    error
	}
	finished := make(chan outcome, 1)
	var work sync.WaitGroup
	work.Go(func() { cancel(r.readClient(ioCtx)) })
	work.Go(func() {
		// adb closes the pairing connection as soon as the exchange ends; the
		// verdict comes from the command below.
		if err := r.serveADB(ioCtx, isTLSClientHello); errors.Is(err, errClientGone) {
			cancel(err)
		}
	})
	work.Go(func() {
		output, err := adb.Pair(ctx, listener.Addr().String(), code)
		finished <- outcome{output, err}
	})

	var cause error
	final := &message{Type: "paired"}
	select {
	case result := <-finished:
		if !strings.Contains(result.output, "Successfully paired") {
			failure := &Error{"pair_failed", diagnostic(strings.TrimPrefix(result.output, "Enter pairing code: "), result.err)}
			cause, final = failure, terminal(failure)
		}
	case <-ctx.Done():
		cause = context.Cause(ctx)
		final = terminal(cause)
	}
	cancel(cause)
	r.end(ioCtx, final)
	ioCancel()
	work.Wait()
	return cause
}

func (m *Manager) acquire(ctx context.Context, request Request, cancel context.CancelCauseFunc) (*lease, error) {
	key := request.Owner + "\x00" + string(request.Transport) + "\x00" + request.Device
	deadline := time.NewTimer(takeoverWait)
	defer deadline.Stop()
	for {
		m.mu.Lock()
		if m.slots == nil {
			m.slots = make(map[string]*slot)
		}
		current := m.slots[key]
		if current == nil || current.active == nil {
			owned, total := 0, 0
			for _, other := range m.slots {
				if other.active == nil {
					continue
				}
				total++
				if other.active.owner == request.Owner {
					owned++
				}
			}
			if owned >= maxOwnerAttached || total >= maxAttached {
				m.mu.Unlock()
				return nil, &Error{"busy", "Too many devices are attached. Detach one and try again."}
			}
			if current == nil {
				m.forgetOldestLocked()
				current = &slot{}
				m.slots[key] = current
			}
			held := &lease{manager: m, slot: current, owner: request.Owner, transport: request.Transport, cancel: cancel, done: make(chan struct{})}
			current.active, current.used = held, time.Now()
			m.mu.Unlock()
			return held, nil
		}
		previous := current.active
		m.mu.Unlock()
		// The same device attached again, usually because its earlier stream died
		// without the server noticing. The newest stream wins.
		previous.cancel(&Error{"replaced", "This device was attached from another connection."})
		select {
		case <-previous.done:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-deadline.C:
			return nil, &Error{"busy", "The device is still detaching. Try again shortly."}
		}
	}
}

func (m *Manager) forgetOldestLocked() {
	if len(m.slots) < maxSlots {
		return
	}
	oldest := ""
	for key, candidate := range m.slots {
		if candidate.active == nil && (oldest == "" || candidate.used.Before(m.slots[oldest].used)) {
			oldest = key
		}
	}
	delete(m.slots, oldest)
}

func (l *lease) listen() (net.Listener, error) {
	l.manager.mu.Lock()
	port := l.slot.port
	l.manager.mu.Unlock()
	var listener net.Listener
	var err error
	if port != 0 {
		listener, err = net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	}
	if listener == nil {
		// A first attachment, or another program took the remembered port.
		if listener, err = net.Listen("tcp4", "127.0.0.1:0"); err != nil {
			return nil, err
		}
	}
	l.manager.mu.Lock()
	l.slot.port, l.serial = listener.Addr().(*net.TCPAddr).Port, listener.Addr().String()
	l.manager.mu.Unlock()
	return listener, nil
}

func (l *lease) release() {
	l.manager.mu.Lock()
	if l.slot.active == l {
		l.slot.active, l.slot.used = nil, time.Now()
	}
	l.manager.mu.Unlock()
	close(l.done)
}

// relay joins one client stream to the single connection adb opens.
type relay struct {
	conn     *websocket.Conn
	listener net.Listener

	mu    sync.Mutex
	adb   net.Conn
	ended bool

	fromDevice atomic.Int64
	lastRead   atomic.Int64
}

func (r *relay) send(ctx context.Context, m message) error {
	return r.sendWithin(ctx, aliveTimeout, m)
}

func (r *relay) sendWithin(ctx context.Context, limit time.Duration, m message) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	return r.conn.Write(ctx, websocket.MessageText, payload)
}

// reject ends a stream that never started relaying.
func (r *relay) reject(ctx context.Context, cause error) error {
	r.end(ctx, terminal(cause))
	return cause
}

func terminal(cause error) *message {
	var reported *Error
	if !errors.As(cause, &reported) {
		return nil
	}
	return &message{Type: "error", Code: reported.Code, Message: reported.Message}
}

// end stops relaying and closes the client stream. The final message is the
// client's only notice: a close handshake would let an unresponsive client hold
// up the device's removal from adb for several seconds.
func (r *relay) end(ctx context.Context, final *message) {
	if r.listener != nil {
		_ = r.listener.Close()
	}
	r.mu.Lock()
	r.ended = true
	if r.adb != nil {
		_ = r.adb.Close()
	}
	r.mu.Unlock()
	if final != nil {
		_ = r.sendWithin(ctx, 2*time.Second, *final)
	}
	_ = r.conn.CloseNow()
}

// readClient forwards device bytes to adb and must run for the stream's whole
// life: it is also what answers pings and notices the client closing.
func (r *relay) readClient(ctx context.Context) error {
	for {
		kind, data, err := r.conn.Read(ctx)
		if err != nil {
			return errClientGone
		}
		r.lastRead.Store(time.Now().UnixNano())
		if kind != websocket.MessageBinary || len(data) == 0 {
			continue
		}
		r.mu.Lock()
		adb := r.adb
		r.mu.Unlock()
		if adb == nil {
			continue // the host always speaks first
		}
		r.fromDevice.Add(int64(len(data)))
		// A failed adb side is reported by the goroutine reading from it.
		_, _ = adb.Write(data)
	}
}

// serveADB forwards the one connection adb opens. It returns nil when the
// endpoint closed before adb arrived.
func (r *relay) serveADB(ctx context.Context, expected func([]byte) bool) error {
	adb, pending, err := r.acceptADB(expected)
	if err != nil {
		return nil
	}
	defer adb.Close()
	chunk := make([]byte, relayChunk)
	for {
		if len(pending) > 0 {
			if err := r.conn.Write(ctx, websocket.MessageBinary, pending); err != nil {
				return errClientGone
			}
		}
		n, err := adb.Read(chunk)
		pending = chunk[:n]
		if err != nil {
			if n > 0 && r.conn.Write(ctx, websocket.MessageBinary, pending) != nil {
				return errClientGone
			}
			return errPeerClosed
		}
	}
}

// acceptADB takes the first local connection that opens the way adb does, then
// closes the endpoint. Another local program probing the port can neither
// occupy the device nor speak an arbitrary protocol to it.
func (r *relay) acceptADB(expected func([]byte) bool) (net.Conn, []byte, error) {
	for {
		adb, err := r.listener.Accept()
		if err != nil {
			return nil, nil, err
		}
		head := make([]byte, 4)
		_ = adb.SetReadDeadline(time.Now().Add(firstBytes))
		if _, err := io.ReadFull(adb, head); err != nil || !expected(head) {
			_ = adb.Close()
			continue
		}
		_ = adb.SetReadDeadline(time.Time{})
		r.mu.Lock()
		if r.ended {
			r.mu.Unlock()
			_ = adb.Close()
			return nil, nil, net.ErrClosed
		}
		r.adb = adb
		r.mu.Unlock()
		_ = r.listener.Close()
		return adb, head, nil
	}
}

// keepAlive detects a client that vanished without closing, and gives clients
// that cannot observe WebSocket pings a regular sign of life.
func (r *relay) keepAlive(ctx, ioCtx context.Context) error {
	ticker := time.NewTicker(aliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		// Device traffic already proves the client is there, and a pong queued
		// behind a large upload on a slow uplink would arrive late.
		if time.Since(time.Unix(0, r.lastRead.Load())) >= aliveInterval {
			pingCtx, done := context.WithTimeout(ioCtx, aliveTimeout)
			err := r.conn.Ping(pingCtx)
			done()
			if err != nil {
				return errClientGone
			}
		}
		if r.send(ioCtx, message{Type: "alive"}) != nil {
			return errClientGone
		}
	}
}

// refused explains a transport adb gave up on before the device was accepted.
// A wireless device that answered and was then dropped has rejected the
// server's key in the TLS handshake, which is what an unpaired phone does.
func (r *relay) refused(transport Transport, detail string) *Error {
	if transport == TransportWireless && r.fromDevice.Load() > 0 {
		return &Error{"unpaired", "The phone did not accept the server's adb key. Pair it with the server, then try again."}
	}
	if detail == "" {
		detail = "adb could not connect to the device."
	}
	return &Error{"connect_failed", detail}
}

func isConnectPacket(head []byte) bool { return string(head) == "CNXN" }

// A TLS record carrying a handshake message, as the pairing client opens with.
func isTLSClientHello(head []byte) bool { return head[0] == 0x16 && head[1] == 0x03 }

// diagnostic reduces adb's output to one bounded line for the client.
func diagnostic(output string, err error) string {
	for _, line := range strings.Split(output, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > 240 {
				line = line[:240]
			}
			return line
		}
	}
	if err != nil {
		return "adb failed: " + err.Error()
	}
	return "adb could not connect to the device."
}
