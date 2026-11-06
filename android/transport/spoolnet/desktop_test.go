package spoolnet

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type desktopMessage struct {
	data   []byte
	binary bool
}
type desktopProbe struct {
	opened   chan struct{}
	messages chan desktopMessage
	closed   chan int
}

func (p *desktopProbe) OnOpen() { p.opened <- struct{}{} }
func (p *desktopProbe) OnMessage(data []byte, binary bool) {
	p.messages <- desktopMessage{append([]byte(nil), data...), binary}
}
func (p *desktopProbe) OnClosed(status int) { p.closed <- status }
func newDesktopProbe() *desktopProbe {
	return &desktopProbe{make(chan struct{}, 1), make(chan desktopMessage, 4), make(chan int, 2)}
}

func receiveDesktop[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("desktop event timed out")
		var zero T
		return zero
	}
}

func TestDesktopUsesPrivateSessionExactOriginAndBinaryProtocol(t *testing.T) {
	token := strings.Repeat("x", 43)
	commands := make(chan string, 1)
	stopped := make(chan struct{})
	var sockets atomic.Int32
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" {
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "desktop-test", Path: "/", Secure: true, HttpOnly: true})
			io.WriteString(w, `{}`)
			return
		}
		sockets.Add(1)
		if r.TLS == nil || r.Host != "example.com" || r.Header.Get("Origin") != "https://example.com" || r.URL.Path != "/api/desktop/view/ws" || r.URL.Query().Get("token") != token {
			t.Error("desktop escaped the authenticated exact-origin route")
		}
		cookie, err := r.Cookie("spool_session")
		if err != nil || cookie.Value != "desktop-test" {
			t.Error("desktop lost the private session jar")
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"example.com"}})
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		defer close(stopped)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		conn.Write(ctx, websocket.MessageText, []byte(`{"t":"hello","w":1280,"h":720}`))
		conn.Write(ctx, websocket.MessageBinary, []byte{1, 0, 255, 128, 0})
		for _, expected := range []string{"v,0", "au,0", "k,65293,1"} {
			_, command, err := conn.Read(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			if string(command) != expected {
				t.Errorf("expected %q, got %q", expected, command)
			}
			if strings.HasPrefix(expected, "k,") {
				commands <- string(command)
			}
		}
		conn.Close(websocket.StatusPolicyViolation, "revoked")
	})
	if _, err := client.Request("POST", "/api/login", `{}`, "csrf", "en"); err != nil {
		t.Fatal(err)
	}
	probe := newDesktopProbe()
	stream, err := client.OpenDesktop(token, probe)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.Close)
	receiveDesktop(t, probe.opened)
	text := receiveDesktop(t, probe.messages)
	binary := receiveDesktop(t, probe.messages)
	if text.binary || !strings.Contains(string(text.data), `"hello"`) || !binary.binary || !bytes.Equal(binary.data, []byte{1, 0, 255, 128, 0}) {
		t.Fatal("wire data corrupted")
	}
	if err := stream.Send("k,65293,1"); err != nil {
		t.Fatal(err)
	}
	if receiveDesktop(t, commands) != "k,65293,1" {
		t.Fatal("input command changed")
	}
	receiveDesktop(t, probe.closed)
	receiveDesktop(t, stopped)
	if sockets.Load() != 1 {
		t.Fatal("revoked stream reconnected")
	}
}

func TestDesktopRefusesForeignTokensAndReportsForbiddenWithoutRetry(t *testing.T) {
	var requests atomic.Int32
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(http.StatusForbidden) })
	for _, token := range []string{"", "short", strings.Repeat("a", 129), strings.Repeat("x", 32) + "&token=foreign", "https://foreign.example/desktop"} {
		if stream, err := client.OpenDesktop(token, newDesktopProbe()); err == nil {
			stream.Close()
			t.Errorf("accepted unsafe token %q", token)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid token reached network")
	}
	probe := newDesktopProbe()
	stream, err := client.OpenDesktop(strings.Repeat("x", 43), probe)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.Close)
	if status := receiveDesktop(t, probe.closed); status != http.StatusForbidden {
		t.Fatalf("forbidden status lost: %d", status)
	}
	if requests.Load() != 1 {
		t.Fatal("forbidden connection retried")
	}
}

func TestDesktopCommandBounds(t *testing.T) {
	for _, command := range []string{"a,4294967295", "po,12345", "m,1920,1080", "b,3,0", "k,16832768,1", "r", "v,0", "au,0", "kf", "sp,-4.5,60.0"} {
		if !validDesktopCommand(command) {
			t.Errorf("refused valid command %q", command)
		}
	}
	for _, command := range []string{"", "a,-1", "a,4294967296", "m,1,2,3", "m,NaN,0", "b,4,1", "k,10,2", "v,yes", "sp,NaN,0", "sp,Inf,0", "sp,1e12,0", "r\nk,1,1", "sz,1280,720", `{"type":"hello"}`, "c,private"} {
		if validDesktopCommand(command) {
			t.Errorf("accepted invalid command %q", command)
		}
	}
}
