package spoolnet

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/relay"
)

// A phone must follow the server's admission token when another phone is
// revoked, and keep following it after the app restarts from its saved code.
func TestPairedClientCollectsRotatedAdmissionToken(t *testing.T) {
	home := t.TempDir()
	id, err := relay.LoadIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	devices, err := relay.OpenDevices(home)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := relay.NewServer(relay.ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(broker)
	_, registration, err := broker.CreateRegistration("Android admission")
	if err != nil {
		t.Fatal(err)
	}
	host, err := relay.NewHost(t.Context(), public.URL, registration, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 1)
	host.OnStatus = func(state string) {
		if state == "connected" {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	}
	settled := make(chan struct{}, 4)
	devices.SetAdmission(host.AccessToken, func() { settled <- struct{}{} })
	never := func() bool { return false }
	app := &http.Server{Handler: devices.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}), id.ID(), never)}
	cfg, _ := id.TLSConfig()
	// No LAN listener: the phone can only reach the server through the relay.
	go app.Serve(devices.Listener(host, cfg, id.ID(), never, true))
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

	original := id.AccessToken
	p := relay.Pairing{Version: 2, RelayURL: public.URL, PublicKey: id.PublicKey(), AccessToken: original}
	p.Invitation, p.ExpiresAt = devices.Invite()
	invitation, _ := p.Encode()
	data := filepath.Join(t.TempDir(), "relay")
	pending, err := BeginRelayPairing(data, invitation, "Android test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pending.Close)
	if state, err := pending.Poll(); err != nil || state != "pending" {
		t.Fatal(state, err)
	}
	if err := devices.Decide(devices.Snapshot().Pending[0].RequestID, true); err != nil {
		t.Fatal(err)
	}
	if state, err := pending.Poll(); err != nil || state != "approved" {
		t.Fatal(state, err)
	}
	code, err := pending.Code()
	if err != nil {
		t.Fatal(err)
	}
	pending.Close()

	admitted := func(token string) bool {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, public.URL+"/v1/connect/"+id.ID(), nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode != http.StatusNotFound
	}
	reach := func(c *Client) {
		t.Helper()
		if _, err := c.Request(http.MethodGet, "/api/ping", "", "", ""); err != nil {
			t.Fatal("paired client cannot reach the server:", err)
		}
	}
	c, err := NewRelayClient(data, code)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	reach(c)

	rotated, err := relay.RotateAccessToken(home)
	if err != nil {
		t.Fatal(err)
	}
	host.SetAdmission(rotated)
	c.NetworkChanged()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("phone did not collect and confirm the rotated token")
	}
	if saved, err := loadDevice(data, id.ID()); err != nil || saved.AccessToken != rotated.AccessToken || !saved.Approved {
		t.Fatal("rotated token was not saved with the device identity", err)
	}

	// The server ends the grace period; only the rotated token is admitted.
	current, err := relay.DropRetiredTokens(home)
	if err != nil {
		t.Fatal(err)
	}
	host.SetAdmission(current)
	deadline := time.Now().Add(3 * time.Second)
	for admitted(original) {
		if time.Now().After(deadline) {
			t.Fatal("relay still admits the replaced token")
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.NetworkChanged()
	reach(c)
	// The app's saved code still carries the original token after a restart.
	c.Close()
	restarted, err := NewRelayClient(data, code)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	reach(restarted)
}
