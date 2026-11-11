package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// relayAdmits reports whether the relay accepts token for a connected server.
// An admitted plain request fails later, at the WebSocket upgrade.
func relayAdmits(t *testing.T, relayURL, serverID, token string) bool {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, relayURL+"/v1/connect/"+serverID, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode != http.StatusNotFound
}

func waitRelayAdmission(t *testing.T, relayURL, serverID, token string, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for relayAdmits(t, relayURL, serverID, token) != want {
		if time.Now().After(deadline) {
			t.Fatalf("relay admission for the token is %t, want %t", !want, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAccessTokenRotationKeepsIdentityAndBoundsRetiredTokens(t *testing.T) {
	directory := t.TempDir()
	id, err := LoadIdentity(directory)
	if err != nil {
		t.Fatal(err)
	}
	tokens := []string{id.AccessToken}
	for range maxRetiredTokens + 2 {
		next, err := RotateAccessToken(directory)
		if err != nil || next.ID() != id.ID() || slices.Contains(tokens, next.AccessToken) {
			t.Fatal("rotation changed the identity or reused a token", err)
		}
		tokens = append(tokens, next.AccessToken)
	}
	loaded, err := LoadIdentity(directory)
	if err != nil || loaded.AccessToken != tokens[len(tokens)-1] {
		t.Fatal("rotation was not saved", err)
	}
	var retired []string
	for _, token := range loaded.RetiredTokens(time.Now()) {
		retired = append(retired, token.Token)
	}
	if want := tokens[len(tokens)-1-maxRetiredTokens : len(tokens)-1]; !slices.Equal(retired, want) {
		t.Fatal("retired tokens are not the most recent ones")
	}
	if len(loaded.RetiredTokens(time.Now().Add(AdmissionGrace+time.Minute))) != 0 {
		t.Fatal("retired tokens outlive the grace period")
	}
	if info, err := os.Stat(filepath.Join(directory, "identity.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("rotated identity is not private", err)
	}
	dropped, err := DropRetiredTokens(directory)
	if err != nil || len(dropped.Retired) != 0 || dropped.AccessToken != loaded.AccessToken || dropped.ID() != id.ID() {
		t.Fatal("dropping retired tokens changed the identity", err)
	}
	if reloaded, err := LoadIdentity(directory); err != nil || len(reloaded.Retired) != 0 {
		t.Fatal("dropped tokens were not saved", err)
	}
	invalid := &Identity{Seed: id.Seed, AccessToken: id.AccessToken, Retired: []RetiredToken{{Token: "not a token", ExpiresAt: time.Now().Add(time.Hour).Unix()}}}
	if err := saveIdentity(directory, invalid); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(directory); err == nil {
		t.Fatal("accepted an identity with an invalid retired token")
	}
}

type admissionNetwork struct {
	broker    *Server
	public    *httptest.Server
	directory string
	id        *Identity
	host      *Host
	devices   *Devices
	settled   chan struct{}
}

// newAdmissionNetwork serves paired devices through a real relay only.
func newAdmissionNetwork(t *testing.T) *admissionNetwork {
	t.Helper()
	directory := t.TempDir()
	id, err := LoadIdentity(directory)
	if err != nil {
		t.Fatal(err)
	}
	devices, err := OpenDevices(directory)
	if err != nil {
		t.Fatal(err)
	}
	broker := testBroker(t)
	public := httptest.NewServer(broker)
	_, token := createViaAPI(t, broker, "home")
	host, err := NewHost(context.Background(), public.URL, token, id, public.Client())
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
	n := &admissionNetwork{broker: broker, public: public, directory: directory, id: id, host: host, devices: devices, settled: make(chan struct{}, 4)}
	devices.SetAdmission(host.AccessToken, func() { n.settled <- struct{}{} })
	never := func() bool { return false }
	app := &http.Server{ReadHeaderTimeout: time.Second, Handler: devices.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			io.WriteString(w, "connected\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		io.WriteString(w, "ok")
	}), id.ID(), never)}
	cfg, _ := id.TLSConfig()
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
			t.Error("host did not stop")
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("host did not register")
	}
	return n
}

func (n *admissionNetwork) pairing(token string) Pairing {
	return Pairing{Version: 2, RelayURL: n.public.URL, PublicKey: n.id.PublicKey(), AccessToken: token}
}

func (n *admissionNetwork) client(t *testing.T, token string, seed []byte, pairing bool) (*http.Client, *Dialer) {
	t.Helper()
	p := n.pairing(token)
	dialer, err := NewDialer(context.Background(), p, n.public.Client())
	if err != nil {
		t.Fatal(err)
	}
	if dialer.TLSConfig, err = DeviceTLSConfig(p, seed, pairing); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialTLSContext: dialer.DialTLSContext}
	t.Cleanup(func() { transport.CloseIdleConnections(); dialer.Close() })
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}, dialer
}

// pair approves a new device through the relay and returns its key and ID.
func (n *admissionNetwork) pair(t *testing.T, name string) ([]byte, string) {
	t.Helper()
	p := n.pairing(n.host.AccessToken())
	p.Invitation, p.ExpiresAt = n.devices.Invite()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	client, _ := n.client(t, p.AccessToken, key.Seed(), true)
	if state, err := PairDevice(t.Context(), client, p, name); err != nil || state != "pending" {
		t.Fatal(state, err)
	}
	request := n.devices.Snapshot().Pending[0]
	if err := n.devices.Decide(request.RequestID, true); err != nil {
		t.Fatal(err)
	}
	return key.Seed(), request.ID
}

func TestRevokedDeviceLosesRelayAdmissionOnceOthersHoldTheNewToken(t *testing.T) {
	n := newAdmissionNetwork(t)
	serverID, original := n.id.ID(), n.id.AccessToken
	lostSeed, lostID := n.pair(t, "lost phone")
	keptSeed, _ := n.pair(t, "kept phone")
	if !n.devices.AdmissionSettled(original) {
		t.Fatal("approved devices are not recorded as holding the paired token")
	}
	kept, keptDialer := n.client(t, original, keptSeed, false)
	stream, err := kept.Get(Origin(serverID) + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if _, err := io.ReadFull(stream.Body, make([]byte, len("connected\n"))); err != nil {
		t.Fatal(err)
	}
	registered := connectedHost(n.broker, serverID)

	// The home server revokes the device, then replaces the token it holds.
	if err := n.devices.Revoke(lostID); err != nil {
		t.Fatal(err)
	}
	rotated, err := RotateAccessToken(n.directory)
	if err != nil {
		t.Fatal(err)
	}
	if n.devices.AdmissionSettled(rotated.AccessToken) {
		t.Fatal("a device was assumed to hold a token it has not collected")
	}
	n.host.SetAdmission(rotated)
	waitRelayAdmission(t, n.public.URL, serverID, rotated.AccessToken, true)
	if !relayAdmits(t, n.public.URL, serverID, original) {
		t.Fatal("replaced token refused before the kept device collected the new one")
	}
	if connectedHost(n.broker, serverID) != registered || registered.ctx.Err() != nil {
		t.Fatal("rotation re-registered the host and dropped its connections")
	}
	// Admission was never authorization: the revoked key fails the inner TLS.
	lost, _ := n.client(t, original, lostSeed, false)
	if response, err := lost.Get(Origin(serverID) + "/"); err == nil {
		response.Body.Close()
		t.Fatal("revoked device reached the server")
	}

	token, err := SyncAdmission(t.Context(), kept, n.pairing(original), original)
	if err != nil || token != rotated.AccessToken {
		t.Fatal("kept device did not receive the current token", err)
	}
	select {
	case <-n.settled:
		t.Fatal("token was recorded as held before the device reported it")
	default:
	}
	if err := keptDialer.SetAccessToken(token); err != nil {
		t.Fatal(err)
	}
	if again, err := SyncAdmission(t.Context(), kept, n.pairing(original), token); err != nil || again != token {
		t.Fatal("kept device could not confirm the current token", err)
	}
	select {
	case <-n.settled:
	case <-time.After(3 * time.Second):
		t.Fatal("last confirmation did not settle admission")
	}

	// The home server ends the grace period early.
	settled, err := DropRetiredTokens(n.directory)
	if err != nil {
		t.Fatal(err)
	}
	n.host.SetAdmission(settled)
	waitRelayAdmission(t, n.public.URL, serverID, original, false)
	if !relayAdmits(t, n.public.URL, serverID, token) {
		t.Fatal("current token refused")
	}
	if response, err := kept.Get(Origin(serverID) + "/"); err != nil {
		t.Fatal("kept device lost access after the rotation", err)
	} else {
		response.Body.Close()
	}
	if connectedHost(n.broker, serverID) != registered {
		t.Fatal("ending the grace period re-registered the host")
	}
	reopened, err := OpenDevices(n.directory)
	if err != nil || !reopened.AdmissionSettled(token) {
		t.Fatal("confirmation was not saved", err)
	}
}

func TestRetiredAdmissionTokenExpires(t *testing.T) {
	n := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	serverID, original := n.pair.ID(), n.pair.AccessToken
	expires := time.Now().Add(time.Hour)
	n.host.SetAdmission(&Identity{Seed: n.host.Identity.Seed, AccessToken: randomID(), Retired: []RetiredToken{{Token: original, ExpiresAt: expires.Unix()}}})
	current := n.host.AccessToken()
	waitRelayAdmission(t, n.pair.RelayURL, serverID, current, true)
	n.host.ExpireAdmission(expires.Add(-time.Minute))
	if !relayAdmits(t, n.pair.RelayURL, serverID, original) {
		t.Fatal("retired token refused within its grace period")
	}
	n.host.ExpireAdmission(expires)
	waitRelayAdmission(t, n.pair.RelayURL, serverID, original, false)
	if !relayAdmits(t, n.pair.RelayURL, serverID, current) {
		t.Fatal("current token refused")
	}
}

// A relay from before live admission updates only learns the token at
// registration, so the host must register again for a rotation to apply.
func TestRotationRegistersAgainWithOlderRelay(t *testing.T) {
	id, _ := NewIdentity()
	registered := make(chan string, 4)
	older := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		if writeControl(r.Context(), c, controlMessage{Type: "challenge", Challenge: randomID()}) != nil {
			return
		}
		m, err := readControl(r.Context(), c)
		if err != nil {
			return
		}
		registered <- m.AccessToken
		if writeControl(r.Context(), c, controlMessage{Type: "ready"}) != nil {
			return
		}
		for {
			m, err := readControl(r.Context(), c)
			if err != nil {
				return
			}
			if m.Type == "admission" {
				t.Error("sent an admission update the relay did not offer")
			}
		}
	}))
	t.Cleanup(older.Close)
	host, err := NewHost(context.Background(), older.URL, randomID(), id, older.Client())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { host.Run(); host.Wait(); close(done) }()
	t.Cleanup(func() {
		host.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("host did not stop")
		}
	})
	next := func() string {
		t.Helper()
		select {
		case token := <-registered:
			return token
		case <-time.After(5 * time.Second):
			t.Fatal("host did not register")
			return ""
		}
	}
	if next() != id.AccessToken {
		t.Fatal("host registered another token")
	}
	rotated := &Identity{Seed: id.Seed, AccessToken: randomID(), Retired: []RetiredToken{{Token: id.AccessToken, ExpiresAt: time.Now().Add(time.Hour).Unix()}}}
	host.SetAdmission(rotated)
	if next() != rotated.AccessToken {
		t.Fatal("host did not register again with the rotated token")
	}
}
