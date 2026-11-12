package spoolnet

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

// DesktopListener receives voxl's wire messages without JSON/base64 video copies.
// Callbacks run off the UI thread. Closing a stream never reconnects or creates a view.
type DesktopListener interface {
	OnOpen()
	OnMessage(data []byte, binary bool)
	OnClosed(status int)
}

type DesktopStream struct {
	stream      *EventStream
	commands    chan desktopOutgoing
	queueMu     sync.Mutex
	queuedBytes int
}

type desktopOutgoing struct {
	kind websocket.MessageType
	data []byte
}

const maxDesktopClipboardText = 1 << 20
const maxDesktopClipboardImage = 15 << 20 // voxl's native clipboard limit
const maxDesktopQueueBytes = 16 << 20

var desktopTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)

// OpenDesktop uses the same private session jar, TLS policy and selected
// dialer as API requests. The caller explicitly obtains a view with POST /connect.
func (c *Client) OpenDesktop(token string, listener DesktopListener) (*DesktopStream, error) {
	if !desktopTokenPattern.MatchString(token) || listener == nil {
		return nil, errors.New("invalid desktop subscription")
	}
	ctx, cancel := context.WithCancel(c.ctx)
	s := &DesktopStream{stream: &EventStream{ctx: ctx, cancel: cancel}, commands: make(chan desktopOutgoing, 256)}
	go func() {
		defer s.Close()
		headers := http.Header{"Origin": {c.origin.String()}}
		conn, resp, err := websocket.Dial(ctx, "wss://"+c.origin.Host+"/api/desktop/view/ws?token="+url.QueryEscape(token), &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: headers})
		if err != nil {
			if ctx.Err() == nil {
				status := 0
				if resp != nil {
					status = resp.StatusCode
				}
				listener.OnClosed(status)
			}
			return
		}
		if !s.stream.attach(conn) {
			return
		}
		conn.SetReadLimit(maxResponseBytes)
		// Keep capture disabled until Android has created every output surface.
		for _, command := range []string{"v,0", "au,0"} {
			writeCtx, done := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Write(writeCtx, websocket.MessageText, []byte(command))
			done()
			if err != nil {
				if ctx.Err() == nil {
					listener.OnClosed(0)
				}
				return
			}
		}
		var notify sync.Once
		failed := func() {
			notify.Do(func() {
				if ctx.Err() == nil {
					listener.OnClosed(0)
				}
			})
		}
		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			for {
				select {
				case <-ctx.Done():
					return
				case message := <-s.commands:
					s.queueMu.Lock()
					s.queuedBytes -= len(message.data)
					s.queueMu.Unlock()
					writeCtx, done := context.WithTimeout(ctx, 5*time.Second)
					err := conn.Write(writeCtx, message.kind, message.data)
					done()
					if err != nil {
						failed()
						s.Close()
						return
					}
				}
			}
		}()
		defer func() { s.Close(); <-writerDone }()
		listener.OnOpen()
		for ctx.Err() == nil {
			readCtx, done := context.WithTimeout(ctx, 45*time.Second)
			kind, data, err := conn.Read(readCtx)
			done()
			if err != nil {
				failed()
				return
			}
			if ctx.Err() == nil {
				listener.OnMessage(data, kind == websocket.MessageBinary)
			}
		}
	}()
	return s, nil
}

// Send is bounded and never blocks Android's UI thread on network I/O.
func (s *DesktopStream) Send(command string) error {
	if !validDesktopCommand(command) {
		return errors.New("invalid desktop command")
	}
	return s.enqueue(desktopOutgoing{websocket.MessageText, []byte(command)})
}

// SendClipboardImage queues PNG data before any following paste keys, on the
// same writer as text input. The caller retains ownership of its byte slice.
func (s *DesktopStream) SendClipboardImage(png []byte) error {
	if len(png) > maxDesktopClipboardImage || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		return errors.New("invalid desktop clipboard image")
	}
	data := make([]byte, len(png)+1)
	data[0] = 0x11
	copy(data[1:], png)
	return s.enqueue(desktopOutgoing{websocket.MessageBinary, data})
}

func (s *DesktopStream) enqueue(message desktopOutgoing) error {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if s.stream.ctx.Err() != nil {
		return errors.New("desktop is closed")
	}
	if len(message.data) > maxDesktopQueueBytes-s.queuedBytes {
		return errors.New("desktop input queue is full")
	}
	select {
	case <-s.stream.ctx.Done():
		return errors.New("desktop is closed")
	case s.commands <- message:
		s.queuedBytes += len(message.data)
		return nil
	default:
		return errors.New("desktop input queue is full")
	}
}

func (s *DesktopStream) Close() {
	s.stream.Close()
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	for {
		select {
		case message := <-s.commands:
			s.queuedBytes -= len(message.data)
		default:
			return
		}
	}
}

func validDesktopCommand(command string) bool {
	if strings.HasPrefix(command, "c,") {
		return len(command)-2 <= maxDesktopClipboardText && utf8.ValidString(command) && !strings.ContainsRune(command, '\x00')
	}
	if len(command) > 128 || strings.ContainsAny(command, "\r\n\x00") {
		return false
	}
	p := strings.Split(command, ",")
	uintAt := func(i int, max uint64) bool {
		if i >= len(p) {
			return false
		}
		n, err := strconv.ParseUint(p[i], 10, 64)
		return err == nil && n <= max
	}
	flag := func(i int) bool { return i < len(p) && (p[i] == "0" || p[i] == "1") }
	switch p[0] {
	case "r", "kf":
		return len(p) == 1
	case "a":
		return len(p) == 2 && uintAt(1, math.MaxUint32)
	case "po":
		return len(p) == 2 && uintAt(1, math.MaxUint64)
	case "v", "au":
		return len(p) == 2 && flag(1)
	case "m":
		return len(p) == 3 && uintAt(1, 32768) && uintAt(2, 32768)
	case "b":
		return len(p) == 3 && uintAt(1, 3) && p[1] != "0" && flag(2)
	case "k":
		return len(p) == 3 && uintAt(1, 0x0110ffff) && flag(2)
	case "sp":
		if len(p) != 3 {
			return false
		}
		for _, part := range p[1:] {
			n, err := strconv.ParseFloat(part, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 65536 {
				return false
			}
		}
		return true
	}
	return false
}
