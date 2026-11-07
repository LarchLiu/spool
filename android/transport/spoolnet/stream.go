package spoolnet

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// EventListener callbacks run off the Android UI thread and must dispatch before drawing.
type EventListener interface{ OnEvent(kind, data string) }

type EventStream struct {
	cancel    context.CancelFunc
	conn      *websocket.Conn
	ctx       context.Context
	once      sync.Once
	sendMu    sync.Mutex
	mu        sync.Mutex
	closed    bool
	hidden    bool
	helloSent bool
}

func (s *EventStream) Close() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.mu.Lock()
		s.closed = true
		conn := s.conn
		s.mu.Unlock()
		if conn != nil {
			conn.CloseNow()
		}
	})
}

func (s *EventStream) Send(message string) error {
	if len(message) > 32<<10 || !json.Valid([]byte(message)) {
		return errors.New("invalid workspace message")
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	var control struct {
		Type    string `json:"type"`
		Visible *bool  `json:"visible"`
	}
	_ = json.Unmarshal([]byte(message), &control)
	s.mu.Lock()
	if control.Visible != nil {
		s.hidden = !*control.Visible
	}
	conn := s.conn
	closed := s.closed
	helloSent := s.helloSent
	s.mu.Unlock()
	// The handle is returned before dialing. A visibility update during the
	// handshake is retained for hello and subsequent heartbeats.
	if !helloSent && !closed && control.Type == "visibility" {
		return nil
	}
	return s.write(conn, closed, message)
}

func (s *EventStream) write(conn *websocket.Conn, closed bool, message string) error {
	if conn == nil || closed {
		return errors.New("stream is not connected")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, []byte(message))
}

// Probe confirms a round trip on the existing WebSocket. A buffered data write
// alone cannot establish that a socket survived Android's background freeze.
func (s *EventStream) Probe() error {
	s.mu.Lock()
	conn, closed, ready := s.conn, s.closed, s.helloSent
	s.mu.Unlock()
	if conn == nil || closed || !ready {
		return errors.New("stream is not connected")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
	defer cancel()
	return conn.Ping(ctx)
}

func (s *EventStream) sendPresence(kind, csrf string) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.mu.Lock()
	conn, closed, visible := s.conn, s.closed, !s.hidden
	s.mu.Unlock()
	message := map[string]any{"type": kind, "visible": visible}
	if csrf != "" {
		message["csrf"] = csrf
	}
	data, _ := json.Marshal(message)
	if err := s.write(conn, closed, string(data)); err != nil {
		return err
	}
	if kind == "hello" {
		s.mu.Lock()
		s.helloSent = true
		s.mu.Unlock()
	}
	return nil
}

func (s *EventStream) attach(conn *websocket.Conn) bool {
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		conn.CloseNow()
		return false
	}
	s.conn = conn
	s.mu.Unlock()
	return true
}

var chatIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func streamError(ctx context.Context, listener EventListener, status int) {
	if ctx.Err() == nil {
		listener.OnEvent("transport_error", fmt.Sprintf(`{"status":%d}`, status))
	}
}

func (c *Client) SubscribeChat(chatID, language string, listener EventListener) (*EventStream, error) {
	if !chatIDPattern.MatchString(chatID) || listener == nil {
		return nil, errors.New("invalid chat subscription")
	}
	ctx, cancel := context.WithCancel(c.ctx)
	stream := &EventStream{ctx: ctx, cancel: cancel}
	go func() {
		defer cancel()
		resp, err := c.request(ctx, http.MethodGet, "/api/chats/"+chatID+"/events?tool_details=lazy", nil, "", "", language)
		if err != nil {
			streamError(ctx, listener, 0)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			streamError(ctx, listener, resp.StatusCode)
			return
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			streamError(ctx, listener, 0)
			return
		}
		err = readSSE(resp.Body, listener)
		if err != nil || ctx.Err() == nil {
			streamError(ctx, listener, 0)
		}
	}()
	return stream, nil
}

func readSSE(reader interface{ Read([]byte) (int, error) }, listener EventListener) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxResponseBytes)
	kind := "message"
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() > 0 {
				listener.OnEvent(kind, strings.TrimSuffix(data.String(), "\n"))
			}
			kind = "message"
			data.Reset()
		} else if strings.HasPrefix(line, "event:") {
			kind = strings.TrimSpace(line[6:])
		} else if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line[5:], " ")
			if data.Len()+len(value) > maxResponseBytes {
				return errors.New("SSE event is too large")
			}
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	return scanner.Err()
}

func (c *Client) SubscribeWorkspace(csrf, language string, listener EventListener) (*EventStream, error) {
	if listener == nil || csrf == "" {
		return nil, errors.New("authenticated workspace required")
	}
	ctx, cancel := context.WithCancel(c.ctx)
	stream := &EventStream{ctx: ctx, cancel: cancel}
	// Return a cancelable handle before dialing, just as WebSocket creation does.
	go func() {
		defer stream.Close()
		headers := http.Header{"Origin": {c.origin.String()}, "X-UI-Language": {language}}
		conn, resp, err := websocket.Dial(ctx, "wss://"+c.origin.Host+"/api/events", &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: headers})
		if err != nil {
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			streamError(ctx, listener, status)
			return
		}
		if !stream.attach(conn) {
			return
		}
		conn.SetReadLimit(maxResponseBytes)
		if err := stream.sendPresence("hello", csrf); err != nil {
			streamError(ctx, listener, 0)
			return
		}
		for {
			readCtx, done := context.WithTimeout(ctx, 45*time.Second)
			_, data, err := conn.Read(readCtx)
			done()
			if err != nil {
				if ctx.Err() == nil && websocket.CloseStatus(err) == websocket.StatusPolicyViolation {
					listener.OnEvent("auth_expired", "{}")
				} else {
					streamError(ctx, listener, 0)
				}
				return
			}
			var event struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal(data, &event) != nil {
				streamError(ctx, listener, 0)
				return
			}
			if event.Type == "ping" {
				if err := stream.sendPresence("pong", ""); err != nil {
					streamError(ctx, listener, 0)
					return
				}
			}
			payload := string(event.Data)
			if payload == "" {
				payload = "null"
			}
			// Include heartbeat frames so native resume/watchdog logic sees every message.
			listener.OnEvent(event.Type, payload)
		}
	}()
	return stream, nil
}
