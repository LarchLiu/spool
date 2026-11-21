package spoolnet

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// The phone shares its own wireless debugging: adbd's TLS port and, for the
// one-time pairing, the port its pairing dialog opens. Both are reached on
// loopback. The caller names the port from what the phone itself advertises or
// the user typed; the server never chooses where its bytes go.

var (
	adbDeviceKey   = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,96}$`)
	adbPairingCode = regexp.MustCompile(`^[0-9]{6,12}$`)
)

const (
	adbRelayChunk = 64 << 10
	// The server sends at most 64 KiB of ADB traffic in one message.
	adbFrameLimit = 1 << 20
)

// Timings are variables so tests can shorten them.
var (
	adbDeviceDial = 3 * time.Second
	adbServerDial = 30 * time.Second
	// The server reports that it is alive every 15 seconds.
	adbSilenceLimit = 45 * time.Second
	adbDeviceWrite  = 30 * time.Second
	// How long the server's own explanation is awaited once the phone has
	// hung up: its adb knows why a pairing ended, and usually why a
	// connection was refused.
	adbVerdictWait     = 3 * time.Second
	adbPairVerdictWait = 10 * time.Second
	// The server may have said why it hung up before a write to it failed.
	adbDrainWait = 2 * time.Second
)

// ADBStream is one attempt to share the phone or to pair it. It never
// reconnects: the caller decides whether, when and through which route to try
// again.
type ADBStream struct {
	stream   *EventStream
	listener EventListener
	// Closed once nothing of the attempt is running any more.
	done chan struct{}

	mu       sync.Mutex
	closed   bool
	finished bool
}

// Close ends the attempt. No event follows it.
func (s *ADBStream) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stream.Close()
}

// emit delivers events one at a time; nothing follows a final one.
func (s *ADBStream) emit(kind string, value any, final bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.finished {
		return
	}
	s.finished = final
	data, _ := json.Marshal(value)
	s.listener.OnEvent(kind, string(data))
}

type adbHello struct {
	Type      string `json:"type"`
	CSRF      string `json:"csrf"`
	Transport string `json:"transport,omitempty"`
	Device    string `json:"device,omitempty"`
	Code      string `json:"code,omitempty"`
}

type adbNotice struct {
	Type    string `json:"type"`
	Serial  string `json:"serial"`
	State   string `json:"state"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type adbEnding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ShareADB offers this phone's wireless debugging, listening on the given
// loopback port, to the adb server of the selected Spool server. device is a
// stable key for this phone, so that sharing it again keeps its serial there.
//
// Events, in order:
//
//	ready   {"serial":"127.0.0.1:41000"}  how programs on the server address the phone
//	state   {"state":"connecting"|"unauthorized"|"online"}
//	closed  {"code":…,"message":…}        last, unless the caller closed the stream
//
// closed carries the server's code and diagnostic, or one of these codes:
// device_unreachable (nothing accepts connections on the port), device_closed
// (the phone ended the debugging connection), unpaired (the phone refused the
// server's adb key), network, forbidden and auth_expired.
func (c *Client) ShareADB(csrf, device string, port int, listener EventListener) (*ADBStream, error) {
	if !adbDeviceKey.MatchString(device) {
		return nil, errors.New("invalid ADB bridge request")
	}
	return c.relayADB(adbHello{Type: "attach", CSRF: csrf, Transport: "wireless", Device: device}, port, listener)
}

// PairADB relays one pairing exchange between the server's adb and the pairing
// port the phone shows beside its pairing code. Events: ready {}, then either
// paired {} or closed as for ShareADB.
func (c *Client) PairADB(csrf, code string, port int, listener EventListener) (*ADBStream, error) {
	if !adbPairingCode.MatchString(code) {
		return nil, errors.New("invalid ADB bridge request")
	}
	return c.relayADB(adbHello{Type: "pair", CSRF: csrf, Code: code}, port, listener)
}

func (c *Client) relayADB(hello adbHello, port int, listener EventListener) (*ADBStream, error) {
	if listener == nil || hello.CSRF == "" || port < 1 || port > 65535 {
		return nil, errors.New("invalid ADB bridge request")
	}
	ctx, cancel := context.WithCancel(c.ctx)
	s := &ADBStream{stream: &EventStream{ctx: ctx, cancel: cancel}, listener: listener, done: make(chan struct{})}
	// Return a cancelable handle before dialing, as the other streams do.
	go s.run(c, hello, port)
	return s, nil
}

func (s *ADBStream) run(c *Client, hello adbHello, port int) {
	defer close(s.done)
	ctx := s.stream.ctx
	pairing := hello.Type == "pair"
	end := func(code, message string) { s.emit("closed", adbEnding{code, message}, true) }
	// Also how a stream ends when its route is closed under it.
	defer func() { end("network", ""); s.stream.Close() }()

	// The phone first: without wireless debugging there is nothing to offer,
	// and the server is not asked to wait for it.
	dialer := net.Dialer{Timeout: adbDeviceDial}
	device, err := dialer.DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		if ctx.Err() == nil {
			end("device_unreachable", "")
		}
		return
	}
	defer device.Close()
	defer context.AfterFunc(ctx, func() { device.Close() })()

	dialCtx, dialed := context.WithTimeout(ctx, adbServerDial)
	conn, resp, err := websocket.Dial(dialCtx, "wss://"+c.origin.Host+"/api/adb/bridge", &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: http.Header{"Origin": {c.origin.String()}}})
	dialed()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			end("auth_expired", "")
		} else if resp != nil && resp.StatusCode == http.StatusForbidden {
			end("forbidden", "")
		}
		return
	}
	if !s.stream.attach(conn) {
		return
	}
	conn.SetReadLimit(adbFrameLimit)
	payload, _ := json.Marshal(hello)
	writeCtx, written := context.WithTimeout(ctx, 5*time.Second)
	err = conn.Write(writeCtx, websocket.MessageText, payload)
	written()
	if err != nil {
		return
	}

	var established, deviceGone atomic.Bool
	var fromDevice atomic.Int64
	// Ends the stream with what is known here unless the server explains first.
	settle := func(wait time.Duration, verdict func() string) {
		timer := time.AfterFunc(wait, func() {
			end(verdict(), "")
			s.stream.Close()
		})
		context.AfterFunc(ctx, func() { timer.Stop() })
	}
	var deviceEnded sync.Once
	deviceClosed := func() {
		// Ending the stream closes the phone's connection as well.
		if ctx.Err() != nil {
			return
		}
		deviceEnded.Do(func() {
			deviceGone.Store(true)
			if pairing {
				// The phone hangs up when the exchange is over, whichever way
				// it went. Only the server's adb knows which.
				settle(adbPairVerdictWait, func() string { return "device_closed" })
				return
			}
			if established.Load() {
				end("device_closed", "")
				s.stream.Close()
				return
			}
			settle(adbVerdictWait, func() string {
				// A phone that answers and then hangs up before adb has it has
				// refused the server's key.
				if !established.Load() && fromDevice.Load() > 0 {
					return "unpaired"
				}
				return "device_closed"
			})
		})
	}

	uplink := make(chan struct{})
	go func() {
		defer close(uplink)
		chunk := make([]byte, adbRelayChunk)
		for {
			n, err := device.Read(chunk)
			if n > 0 {
				fromDevice.Add(int64(n))
				if conn.Write(ctx, websocket.MessageBinary, chunk[:n]) != nil {
					if ctx.Err() == nil {
						settle(adbDrainWait, func() string { return "network" })
					}
					return
				}
			}
			if err != nil {
				deviceClosed()
				return
			}
		}
	}()
	defer func() { s.stream.Close(); device.Close(); <-uplink }()

	for {
		readCtx, read := context.WithTimeout(ctx, adbSilenceLimit)
		kind, data, err := conn.Read(readCtx)
		read()
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusPolicyViolation {
				end("auth_expired", "")
			}
			return
		}
		c.receivedResponse()
		if kind == websocket.MessageBinary {
			if deviceGone.Load() {
				continue
			}
			_ = device.SetWriteDeadline(time.Now().Add(adbDeviceWrite))
			if _, err := device.Write(data); err != nil {
				deviceClosed()
			}
			continue
		}
		var notice adbNotice
		if json.Unmarshal(data, &notice) != nil {
			return
		}
		switch notice.Type {
		case "ready":
			if pairing {
				s.emit("ready", struct{}{}, false)
			} else {
				s.emit("ready", struct {
					Serial string `json:"serial"`
				}{notice.Serial}, false)
			}
		case "state":
			if notice.State == "online" || notice.State == "unauthorized" {
				established.Store(true)
			}
			s.emit("state", struct {
				State string `json:"state"`
			}{notice.State}, false)
			if established.Load() && deviceGone.Load() {
				end("device_closed", "")
				return
			}
		case "paired":
			s.emit("paired", struct{}{}, true)
			return
		case "error":
			end(notice.Code, notice.Message)
			return
		}
	}
}
