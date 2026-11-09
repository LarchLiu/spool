package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDevicePairingAdmissionRevocationAndWebIsolation(t *testing.T) {
	directory := t.TempDir()
	d, err := OpenDevices(directory)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := NewIdentity()
	p := Pairing{Version: 2, PublicKey: id.PublicKey(), AccessToken: id.AccessToken, RelayURL: "http://127.0.0.1:28080"}
	p.Invitation, p.ExpiresAt = d.Invite()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	cfg, _ := id.TLSConfig()
	var web atomic.Bool
	web.Store(true)
	webAllowed := web.Load
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lan, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/socket" {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			<-c.CloseRead(r.Context()).Done()
			return
		}
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: connected\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		io.WriteString(w, "ok")
	})
	server := &http.Server{Handler: d.Handler(app, id.ID(), webAllowed), ReadHeaderTimeout: time.Second}
	done := make(chan error, 2)
	go func() { done <- server.Serve(d.Listener(base, cfg, id.ID(), webAllowed, true)) }()
	go func() { done <- server.Serve(d.Listener(lan, cfg, id.ID(), webAllowed, false)) }()
	t.Cleanup(func() { server.Close(); d.CloseConnections(); <-done; <-done })
	client := func(seed []byte, pair bool, address string) *http.Client {
		t.Helper()
		config := p.TLSConfig()
		if seed != nil {
			config, err = DeviceTLSConfig(p, seed, pair)
			if err != nil {
				t.Fatal(err)
			}
		}
		transport := &http.Transport{DialTLSContext: func(ctx context.Context, network, unused string) (net.Conn, error) {
			return (&tls.Dialer{Config: config}).DialContext(ctx, "tcp", address)
		}}
		t.Cleanup(transport.CloseIdleConnections)
		return &http.Client{Transport: transport, Timeout: 2 * time.Second}
	}
	request := func(c *http.Client, path string, want int) {
		t.Helper()
		r, err := c.Get(Origin(p.ID()) + path)
		if want == 0 {
			if err == nil {
				r.Body.Close()
				t.Fatal("unexpected accepted connection", path)
			}
			return
		}
		if err != nil {
			t.Fatal(path, err)
		}
		defer r.Body.Close()
		io.Copy(io.Discard, r.Body)
		if r.StatusCode != want {
			t.Fatalf("%s: got %d want %d", path, r.StatusCode, want)
		}
	}
	unapproved := client(key.Seed(), false, base.Addr().String())
	request(unapproved, "/healthz", 0)
	bootstrap := client(key.Seed(), true, base.Addr().String())
	request(bootstrap, "/api/login", 404)
	request(bootstrap, "/healthz", 404)
	if state, err := PairDevice(t.Context(), bootstrap, p, "My phone"); err != nil || state != "pending" {
		t.Fatal(state, err)
	}
	snap := d.Snapshot()
	if len(snap.Pending) != 1 || len(snap.Devices) != 0 || snap.Pending[0].ID != DeviceFingerprint(key.Public().(ed25519.PublicKey)) {
		t.Fatal(snap)
	}
	second := client(other.Seed(), true, base.Addr().String())
	if _, err := PairDevice(t.Context(), second, p, "Stolen QR"); err == nil {
		t.Fatal("invitation reused by another key")
	}
	if err := d.Decide(snap.Pending[0].RequestID, true); err != nil {
		t.Fatal(err)
	}
	// The administrator can leave the pairing UI before the phone's next poll.
	d.CancelInvitation(p.Invitation)
	if state, err := PairDevice(t.Context(), bootstrap, p, "My phone"); err != nil || state != "approved" {
		t.Fatal(state, err)
	}
	// Approval responses may be retried by this key, but never by another phone.
	if _, err := PairDevice(t.Context(), second, p, "Stolen QR"); err == nil {
		t.Fatal("approved invitation reused")
	}
	request(unapproved, "/healthz", 200)
	request(client(key.Seed(), false, lan.Addr().String()), "/healthz", 200)
	request(client(other.Seed(), false, base.Addr().String()), "/healthz", 0)
	// A second phone gets independent authorization, not a shared secret.
	otherPairing := p
	otherPairing.Invitation, otherPairing.ExpiresAt = d.Invite()
	if state, err := PairDevice(t.Context(), bootstrap, p, "My phone"); err != nil || state != "approved" {
		t.Fatal("new invitation discarded a previous approval", state, err)
	}
	if state, err := PairDevice(t.Context(), second, otherPairing, "Second phone"); err != nil || state != "pending" {
		t.Fatal(state, err)
	}
	if err := d.Decide(d.Snapshot().Pending[0].RequestID, true); err != nil {
		t.Fatal(err)
	}
	if state, err := PairDevice(t.Context(), bootstrap, p, "My phone"); err != nil || state != "approved" {
		t.Fatal("another device's approval discarded the first result", state, err)
	}
	otherClient := client(other.Seed(), false, base.Addr().String())
	request(otherClient, "/healthz", 200)
	request(unapproved, WebPath(id.ID())+"api/login", 404)
	// Old clients cannot silently fall back to the gateway or a raw HTTP path.
	gateway := client(nil, false, base.Addr().String())
	request(gateway, "/healthz", 404)
	request(gateway, "/api/login", 404)
	request(gateway, WebPath(id.ID()), 200)
	request(client(nil, false, lan.Addr().String()), WebPath(id.ID()), 0)
	web.Store(false)
	request(gateway, WebPath(id.ID()), 404) // already established Web connections too
	request(client(nil, false, base.Addr().String()), WebPath(id.ID()), 0)
	// Persisted public keys work after restart; invitations never persist.
	reopened, err := OpenDevices(directory)
	if err != nil || len(reopened.Snapshot().Devices) != 2 || len(reopened.Snapshot().Pending) != 0 {
		t.Fatal(err)
	}
	if filepath.Base(reopened.path) != "devices.json" {
		t.Fatal(reopened.path)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, "wss://"+p.ID()+".spool.invalid/socket", &websocket.DialOptions{HTTPClient: unapproved})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	events, err := unapproved.Get(Origin(p.ID()) + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	buf := make([]byte, len("data: connected\n\n"))
	if _, err := io.ReadFull(events.Body, buf); err != nil {
		t.Fatal(err)
	}
	if err := d.Revoke(snap.Pending[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := socket.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatal("revoke did not close WebSocket", err)
	}
	if _, err := events.Body.Read(buf); err == nil {
		t.Fatal("revoke did not close SSE")
	}
	request(unapproved, "/healthz", 0)
	if _, err := PairDevice(t.Context(), bootstrap, p, "My phone"); err == nil {
		t.Fatal("revoked device recovered an old approval")
	}
	request(otherClient, "/healthz", 200)
	reopened, err = OpenDevices(directory)
	if err != nil || len(reopened.Snapshot().Devices) != 1 || reopened.Snapshot().Devices[0].ID != DeviceFingerprint(other.Public().(ed25519.PublicKey)) {
		t.Fatal("revocation did not persist", err)
	}
}

func TestDeviceApprovalReceiptsExpireAndStopWithTheService(t *testing.T) {
	for _, action := range []string{"expire", "restart"} {
		t.Run(action, func(t *testing.T) {
			d, err := OpenDevices(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			_, key, _ := ed25519.GenerateKey(rand.Reader)
			id := DeviceFingerprint(key.Public().(ed25519.PublicKey))
			host, _ := NewIdentity()
			p := Pairing{Version: 2, PublicKey: host.PublicKey(), AccessToken: host.AccessToken, RelayURL: "https://relay.example.com"}
			p.Invitation, p.ExpiresAt = d.Invite()
			cfg, err := DeviceTLSConfig(p, key.Seed(), true)
			if err != nil {
				t.Fatal(err)
			}
			cert, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
			if err != nil {
				t.Fatal(err)
			}
			handler := d.Handler(http.NotFoundHandler(), p.ID(), func() bool { return false })
			poll := func(wantStatus int, wantState string) {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"invitation": p.Invitation, "name": "Phone"})
				r := httptest.NewRequest(http.MethodPost, Origin(p.ID())+devicePairPath, strings.NewReader(string(body)))
				r.TLS = &tls.ConnectionState{ServerName: cfg.ServerName, PeerCertificates: []*x509.Certificate{cert}}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				var result map[string]string
				_ = json.Unmarshal(w.Body.Bytes(), &result)
				if w.Code != wantStatus || wantState != "" && result["state"] != wantState {
					t.Fatalf("pairing response %d %s, want %d %s", w.Code, w.Body.String(), wantStatus, wantState)
				}
			}
			poll(http.StatusOK, "pending")
			if err := d.Decide(d.Snapshot().Pending[0].RequestID, true); err != nil {
				t.Fatal(err)
			}
			d.CancelInvitation(p.Invitation)
			poll(http.StatusOK, "approved")
			switch action {
			case "expire":
				approval := d.approvals[id]
				approval.expires = time.Now().Add(-time.Second)
				d.approvals[id] = approval
			case "restart":
				d.CloseConnections()
			}
			poll(http.StatusForbidden, "")
			if len(d.Snapshot().Devices) != 1 || len(d.Snapshot().Pending) != 0 {
				t.Fatal("expired/stopped results must not revoke or re-enroll devices")
			}
		})
	}
}

func TestDeviceInvitationExpirationRefreshAndRejection(t *testing.T) {
	d, err := OpenDevices(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := d.Invite()
	second, _ := d.Invite()
	d.CancelInvitation(first)
	if d.invitation == nil || d.invitation.token != second {
		t.Fatal("stale UI canceled new invitation")
	}
	d.mu.Lock()
	d.invitation.request = &DeviceRequest{RequestID: "pending", Device: Device{ID: "unused"}}
	d.invitation.expires = time.Now().Add(-time.Second)
	d.mu.Unlock()
	if err := d.Decide("pending", true); err == nil {
		t.Fatal("expired request approved")
	}
	if len(d.Snapshot().Pending) != 0 {
		t.Fatal("expired request visible")
	}
	d.Invite()
	d.invitation.request = &DeviceRequest{RequestID: "pending"}
	if err := d.Decide("pending", false); err != nil || d.invitation != nil {
		t.Fatal("rejection did not consume invitation", err)
	}
	if err := d.Decide("pending", true); err == nil {
		t.Fatal("rejected request approved")
	}
	identity, _ := NewIdentity()
	p := Pairing{Version: 1, RelayURL: "https://example.com", PublicKey: identity.PublicKey(), AccessToken: identity.AccessToken}
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), `"v":1`) || p.Validate() == nil {
		t.Fatal("legacy code accepted")
	}
}
