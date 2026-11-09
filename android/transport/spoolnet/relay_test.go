package spoolnet

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"spool/relay"
)

func TestPairedAndroidSessionsStreamsDesktopAndLANFallback(t *testing.T) {
	id, err := relay.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	devices, err := relay.OpenDevices(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	broker, err := relay.NewServer(relay.ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(broker)
	_, token, err := broker.CreateRegistration("Android integration")
	if err != nil {
		t.Fatal(err)
	}
	host, err := relay.NewHost(t.Context(), public.URL, token, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 1)
	host.OnStatus = func(s string) {
		if s == "connected" {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	}
	app := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			t.Error("paired request lost TLS")
		}
		if r.URL.Path == "/healthz" {
			io.WriteString(w, `{"ok":true}`)
			return
		}
		if r.Header.Get("Origin") != relay.Origin(id.ID()) {
			t.Error("paired request lost exact origin")
		}
		if r.URL.Path == "/api/login" {
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "paired-session", Path: "/", Secure: true, HttpOnly: true, MaxAge: 3600})
			io.WriteString(w, `{}`)
			return
		}
		cookie, err := r.Cookie("spool_session")
		if err != nil || cookie.Value != "paired-session" {
			t.Error("lost paired session")
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/api/chats/chat/events":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: snapshot\ndata: {}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/api/events":
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			_, hello, err := c.Read(r.Context())
			if err != nil || !strings.Contains(string(hello), `"csrf":"paired-csrf"`) {
				t.Error("lost workspace CSRF hello", err)
				return
			}
			c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"ready"}`))
			<-c.CloseRead(r.Context()).Done()
		case "/api/desktop/view/ws":
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			c.Write(r.Context(), websocket.MessageBinary, []byte{0, 0, 0, 1, 255, 128})
			for {
				if _, _, err := c.Read(r.Context()); err != nil {
					return
				}
			}
		case "/api/logout":
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Path: "/", Secure: true, HttpOnly: true, MaxAge: -1})
			io.WriteString(w, `{}`)
		default:
			io.WriteString(w, `{"ok":true}`)
		}
	})}
	cfg, _ := id.TLSConfig()
	app.Handler = devices.Handler(app.Handler, id.ID(), func() bool { return false })
	lan, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go app.Serve(devices.Listener(lan, cfg, id.ID(), func() bool { return false }, false))
	go app.Serve(devices.Listener(host, cfg, id.ID(), func() bool { return false }, true))
	done := make(chan struct{})
	go func() { host.Run(); host.Wait(); close(done) }()
	t.Cleanup(func() {
		host.Close()
		app.Close()
		devices.CloseConnections()
		broker.Close()
		public.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("host stuck")
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("relay host did not register")
	}
	p := relay.Pairing{Version: 2, RelayURL: public.URL, PublicKey: id.PublicKey(), AccessToken: id.AccessToken, LAN: []string{lan.Addr().String()}}
	p.Invitation, p.ExpiresAt = devices.Invite()
	code, _ := p.Encode()
	root := t.TempDir()
	if c, err := NewRelayClient(filepath.Join(root, "relay"), code); err == nil {
		c.Close()
		t.Fatal("unpaired client accepted")
	}
	pending, err := BeginRelayPairing(filepath.Join(root, "relay"), code, "Android test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pending.Close)
	if state, err := pending.Poll(); err != nil || state != "pending" {
		t.Fatal(state, err)
	}
	if c, err := NewRelayClient(filepath.Join(root, "relay"), code); err == nil {
		c.Close()
		t.Fatal("unapproved client accepted")
	}
	// Process death before approval reuses the private pending key.
	fingerprint := pending.Fingerprint()
	pending.Close()
	pending, err = BeginRelayPairing(filepath.Join(root, "relay"), code, "Android test")
	if err != nil || pending.Fingerprint() != fingerprint {
		t.Fatal("lost pending identity", err)
	}
	t.Cleanup(pending.Close)
	request := devices.Snapshot().Pending[0]
	if err := devices.Decide(request.RequestID, true); err != nil {
		t.Fatal(err)
	}
	// Closing Desktop's QR or starting another invitation before the next poll
	// must still allow this phone to persist its approved private key.
	devices.CancelInvitation(p.Invitation)
	devices.Invite()
	if state, err := pending.Poll(); err != nil || state != "approved" {
		t.Fatal(state, err)
	}
	code, err = pending.Code()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := relay.ParsePairing(code)
	if err != nil || parsed.Invitation != "" || parsed.ExpiresAt != 0 {
		t.Fatal("invitation retained in saved code", err)
	}
	pending.Close()
	if c, err := NewRelayClient(filepath.Join(root, "reinstall"), code); err == nil {
		c.Close()
		t.Fatal("code paired a fresh installation without a private key")
	}
	open := func() *Client {
		c, e := NewRelayClient(filepath.Join(root, "relay"), code)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { c.Close() })
		if e = c.UsePairedServerSession(filepath.Join(root, "session"), "", "", code); e != nil {
			t.Fatal(e)
		}
		return c
	}
	c := open()
	if _, err = c.Request("POST", "/api/login", `{}`, "", "en"); err != nil {
		t.Fatal(err)
	}
	if c.relay.Path() != "lan" {
		t.Fatal("Android did not prefer LAN")
	}
	lan.Close()
	c.NetworkChanged()
	if _, err = c.Request("GET", "/api/me", "", "", "en"); err != nil {
		t.Fatal(err)
	}
	if c.relay.Path() != "relay" {
		t.Fatal("Android did not fall back")
	}
	events := make(eventRecorder, 8)
	chat, err := c.SubscribeChat("chat", "en", events)
	if err != nil {
		t.Fatal(err)
	}
	defer chat.Close()
	select {
	case event := <-events:
		if event[0] != "snapshot" {
			t.Fatal(event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no SSE snapshot")
	}
	workspace, err := c.SubscribeWorkspace("paired-csrf", "en", events)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	select {
	case event := <-events:
		if event[0] != "ready" {
			t.Fatal(event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no workspace ready")
	}
	probe := newDesktopProbe()
	desktop, err := c.OpenDesktop(strings.Repeat("v", 43), probe)
	if err != nil {
		t.Fatal(err)
	}
	defer desktop.Close()
	receiveDesktop(t, probe.opened)
	frame := receiveDesktop(t, probe.messages)
	if !frame.binary || len(frame.data) != 6 || frame.data[4] != 255 {
		t.Fatal("desktop frame corrupted")
	}
	c.Close()
	restored := open()
	if _, err := restored.Request("GET", "/api/me", "", "", "en"); err != nil {
		t.Fatal("session lost after reopen", err)
	}
	health, err := restored.Health()
	var info healthSnapshot
	if err != nil || json.Unmarshal([]byte(health), &info) != nil || info.HTTPS != "Verified" {
		t.Fatal(health, err)
	}
	if _, err = restored.Request("POST", "/api/logout", `{}`, "paired-csrf", "en"); err != nil {
		t.Fatal(err)
	}
	restored.Close()
	loggedOut := open()
	if len(loggedOut.http.Jar.Cookies(loggedOut.origin)) != 0 {
		t.Fatal("logout revived old paired cookie")
	}
	// A canceled replacement invitation must leave the approved key usable.
	replacement := p
	replacement.Invitation, replacement.ExpiresAt = devices.Invite()
	replacementCode, _ := replacement.Encode()
	replacementSession, err := BeginRelayPairing(filepath.Join(root, "relay"), replacementCode, "Replacement phone")
	if err != nil {
		t.Fatal(err)
	}
	replacementSession.Close()
	devices.CancelInvitation(replacement.Invitation)
	retained := open()
	if health, err := retained.Health(); err != nil || !strings.Contains(health, `"https":"Verified"`) {
		t.Fatal("failed replacement discarded the approved key", health, err)
	}
	if err := devices.Revoke(fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := retained.Request("GET", "/api/me", "", "", "en"); err == nil {
		t.Fatal("revoked Android identity still connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if conn, err := loggedOut.relay.DialTLSContext(ctx, "tcp", net.JoinHostPort("other.invalid", "443")); err == nil {
		conn.Close()
		t.Fatal("paired transport escaped its server")
	}
}
