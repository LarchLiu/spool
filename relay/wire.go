package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const frameLimit = 64 << 10
const controlLimit = 4096
const handshakeTimeout = 10 * time.Second

type controlMessage struct {
	Type              string `json:"type"`
	Challenge         string `json:"challenge,omitempty"`
	PublicKey         []byte `json:"key,omitempty"`
	Signature         []byte `json:"signature,omitempty"`
	AccessToken       string `json:"token,omitempty"`
	ID                string `json:"id,omitempty"`
	Secret            string `json:"secret,omitempty"`
	RegistrationToken string `json:"registration_token,omitempty"`
	Name              string `json:"name,omitempty"`
	WebOrigin         string `json:"web_origin,omitempty"`
	WebVersion        int    `json:"web_version,omitempty"`
	WebURL            string `json:"web_url,omitempty"`
	ClientIP          string `json:"client_ip,omitempty"`
}

func randomID() string {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(value[:])
}
func validToken(value string) bool {
	b, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(b) == 32
}
func proof(challenge, token string) []byte {
	return []byte("spool-relay-v1\x00" + challenge + "\x00" + token)
}
func namedRegistrationProof(challenge, token, webOrigin, name string) []byte {
	if name == "" {
		return registrationProof(challenge, token, webOrigin)
	}
	return []byte("spool-relay-register-v1\x00" + challenge + "\x00" + token + "\x00" + webOrigin + "\x00" + name)
}

func hostRegistrationProof(challenge, token, webOrigin, name string, version int) []byte {
	if version == 0 {
		return namedRegistrationProof(challenge, token, webOrigin, name)
	}
	return []byte("spool-relay-subdomain-v2\x00" + challenge + "\x00" + token + "\x00" + webOrigin + "\x00" + name)
}
func unregisterProof(challenge, token string) []byte {
	return []byte("spool-relay-unregister-v1\x00" + challenge + "\x00" + token)
}

func registrationProof(challenge, token, webOrigin string) []byte {
	if webOrigin == "" {
		return proof(challenge, token)
	}
	return []byte("spool-relay-web-v1\x00" + challenge + "\x00" + token + "\x00" + webOrigin)
}
func writeControl(ctx context.Context, c *websocket.Conn, m controlMessage) error {
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	return wsjson.Write(ctx, c, m)
}
func readControl(ctx context.Context, c *websocket.Conn) (controlMessage, error) {
	var m controlMessage
	err := wsjson.Read(ctx, c, &m)
	return m, err
}
func websocketURL(origin, path string) string {
	u, _ := ParseRelayURL(origin)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = path
	return u.String()
}
func dialWebsocket(ctx context.Context, client *http.Client, origin, path, token string) (*websocket.Conn, error) {
	headers := make(http.Header)
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	c, resp, err := websocket.Dial(ctx, websocketURL(origin, path), &websocket.DialOptions{HTTPClient: client, HTTPHeader: headers, CompressionMode: websocket.CompressionDisabled})
	if err != nil && resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil && resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		err = errors.Join(ErrRegistrationRejected, err)
	}
	if err == nil {
		c.SetReadLimit(controlLimit)
	}
	return c, err
}

// TLS writes are small, but arbitrary callers may use a large Write. Bound each
// WS message and its reader without buffering complete application messages.
type streamConn struct {
	net.Conn
	ws         *websocket.Conn
	cancel     context.CancelFunc
	done       <-chan struct{}
	reads      chan readResult
	readMu     sync.Mutex
	current    []byte
	readErr    error
	deadlineMu sync.Mutex
	deadline   time.Time
	changed    chan struct{}
}
type readResult struct {
	data []byte
	err  error
}

func (c *streamConn) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > frameLimit {
			n = frameLimit
		}
		written, err := c.Conn.Write(p[:n])
		total += written
		p = p[written:]
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
func byteStream(ctx context.Context, c *websocket.Conn) net.Conn {
	ctx, cancel := context.WithCancel(ctx)
	n := websocket.NetConn(ctx, c, websocket.MessageBinary)
	c.SetReadLimit(frameLimit)
	s := &streamConn{Conn: n, ws: c, cancel: cancel, done: ctx.Done(), reads: make(chan readResult, 1), changed: make(chan struct{})}
	go func() {
		for {
			data := make([]byte, 32<<10)
			n, err := n.Read(data)
			select {
			case s.reads <- readResult{data[:n], err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return s
}

// net/http aborts a pending Read with a past deadline during WebSocket Hijack,
// then resets that deadline. websocket.NetConn cancels its reader permanently
// in that case. A bounded read pump lets deadlines interrupt the caller without
// destroying the encrypted stream or buffering an unbounded file/video frame.
func (c *streamConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	for {
		c.deadlineMu.Lock()
		deadline, changed := c.deadline, c.changed
		c.deadlineMu.Unlock()
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return 0, os.ErrDeadlineExceeded
		}
		if len(c.current) > 0 {
			n := copy(p, c.current)
			c.current = c.current[n:]
			return n, nil
		}
		if c.readErr != nil {
			return 0, c.readErr
		}
		var timer *time.Timer
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			timer = time.NewTimer(time.Until(deadline))
			timeout = timer.C
		}
		select {
		case r := <-c.reads:
			c.current, c.readErr = r.data, r.err
		case <-changed:
		case <-timeout:
			if timer != nil {
				timer.Stop()
			}
			return 0, os.ErrDeadlineExceeded
		case <-c.done:
			if timer != nil {
				timer.Stop()
			}
			return 0, net.ErrClosed
		}
		if timer != nil {
			timer.Stop()
		}
	}
}
func (c *streamConn) SetReadDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	c.deadline = t
	close(c.changed)
	c.changed = make(chan struct{})
	return nil
}
func (c *streamConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	return c.Conn.SetWriteDeadline(t)
}
func (c *streamConn) Close() error { c.cancel(); err := c.ws.CloseNow(); c.Conn.Close(); return err }

func keepAlive(ctx context.Context, c *websocket.Conn) {
	timer := time.NewTicker(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			ping, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(ping)
			cancel()
			if err != nil {
				c.CloseNow()
				return
			}
		}
	}
}
