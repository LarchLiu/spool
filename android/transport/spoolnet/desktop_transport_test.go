//go:build !android && !ios

package spoolnet

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDesktopTransportPinsOriginAndBypassesNativeCookieJar(t *testing.T) {
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "browser=own-session" {
			t.Errorf("cookie leaked from native jar: %s", got)
		}
		http.SetCookie(w, &http.Cookie{Name: "upstream", Value: "browser-only", Secure: true, Path: "/"})
		io.WriteString(w, "ok")
	})
	client.http.Jar.SetCookies(client.origin, []*http.Cookie{{Name: "native", Value: "secret", Path: "/", Secure: true}})
	for _, address := range []string{"http://example.com/api/me", "https://other.example/api/me", "https://example.com:444/api/me", "https://user@example.com/api/me"} {
		request, _ := http.NewRequest("GET", address, nil)
		if _, err := client.RoundTrip(request); err == nil {
			t.Fatal("unpinned origin accepted", address)
		}
	}
	request, _ := http.NewRequest("GET", "https://example.com/api/me", nil)
	request.Header.Set("Cookie", "browser=own-session")
	response, err := client.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(response.Cookies()) != 1 || len(client.http.Jar.Cookies(client.origin)) != 1 {
		t.Fatal("gateway response cookies entered native jar")
	}
}

func TestDesktopTransportStreamsAndCancelsOnClose(t *testing.T) {
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: live\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	request, _ := http.NewRequest("GET", "https://example.com/api/events", nil)
	response, err := client.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	buffer := make([]byte, len("data: live\n\n"))
	if _, err := io.ReadFull(response.Body, buffer); err != nil || string(buffer) != "data: live\n\n" {
		t.Fatal(string(buffer), err)
	}
	if err = client.Close(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, response.Body); ended <- err }()
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("stream cancellation lost")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("closing connection left stream alive")
	}
	if _, err := client.RoundTrip(request); err == nil {
		t.Fatal("closed connection accepted request")
	}
}

func TestDesktopTransportPreservesWebSocketUpgrade(t *testing.T) {
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		kind, message, err := conn.Read(r.Context())
		if err == nil {
			_ = conn.Write(r.Context(), kind, message)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "wss://example.com/api/events", &websocket.DialOptions{HTTPClient: &http.Client{Transport: client}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err = conn.Write(ctx, websocket.MessageText, []byte("live")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || strings.TrimSpace(string(data)) != "live" {
		t.Fatal(string(data), err)
	}
}
