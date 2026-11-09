package workspace

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"spool/relay"
)

func testRelayBroker(t *testing.T) (*relay.Server, string) {
	t.Helper()
	broker, err := relay.NewServer(relay.ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { broker.Close() })
	_, token, err := broker.CreateRegistration("workspace integration")
	if err != nil {
		t.Fatal(err)
	}
	return broker, token
}

func TestRelayPreservesSpoolAuthorizationAndDesktopRevocation(t *testing.T) {
	store, app, manager := remoteDesktopFixture(t)
	alice := testUser(t, store, "alice")
	_, err := store.SaveUser(UserInput{ID: alice.ID, Username: alice.Username, Nickname: alice.Nickname, RemoteDesktop: true})
	if err != nil {
		t.Fatal(err)
	}
	testUser(t, store, "bob")
	manager.SetEnabled(t.Context(), true)
	cookie, csrf := loginRequest(t, app, "alice")
	bob, bobCSRF := loginRequest(t, app, "bob")
	broker, token := testRelayBroker(t)
	public := httptest.NewServer(broker)
	id, _ := relay.NewIdentity()
	devices, err := relay.OpenDevices(filepath.Join(store.Root(), "relay"))
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store, relayDevices: devices}
	host, _ := relay.NewHost(t.Context(), public.URL, token, id, nil)
	ready := make(chan struct{}, 1)
	host.OnStatus = func(s string) {
		if s == "connected" {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	}
	cfg, _ := id.TLSConfig()
	server := &http.Server{Handler: devices.Handler(app, id.ID(), func() bool { return false }), ReadHeaderTimeout: time.Second}
	go server.Serve(devices.Listener(host, cfg, id.ID(), func() bool { return false }, true))
	done := make(chan struct{})
	go func() { host.Run(); host.Wait(); close(done) }()
	t.Cleanup(func() {
		host.Close()
		server.Close()
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
		t.Fatal("host registration timed out")
	}
	p := relay.Pairing{Version: 2, RelayURL: public.URL, PublicKey: id.PublicKey(), AccessToken: id.AccessToken}
	p.Invitation, p.ExpiresAt = devices.Invite()
	dialer := pairServiceDevice(t, service, p)
	defer dialer.Close()
	transport := &http.Transport{DialTLSContext: dialer.DialTLSContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	origin := relay.Origin(id.ID())
	request := func(path, body string, cookie *http.Cookie, csrf string) (int, []byte) {
		t.Helper()
		method := "GET"
		if body != "" {
			method = "POST"
		}
		req, _ := http.NewRequest(method, origin+path, strings.NewReader(body))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		return r.StatusCode, data
	}
	if status, _ := request("/api/me", "", nil, ""); status != 401 {
		t.Fatal("relay admission bypassed login", status)
	}
	if status, _ := request("/api/desktop/connect", `{}`, bob, bobCSRF); status != 403 {
		t.Fatal("relay bypassed desktop grant", status)
	}
	if status, _ := request("/api/desktop/connect", `{}`, cookie, "wrong"); status != 403 {
		t.Fatal("relay bypassed CSRF", status)
	}
	status, body := request("/api/desktop/connect", `{}`, cookie, csrf)
	var view struct{ Token string }
	json.Unmarshal(body, &view)
	if status != 200 || view.Token == "" {
		t.Fatal("desktop view creation failed", status, string(body))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	header := http.Header{"Origin": {origin}, "Cookie": {cookie.String()}}
	conn, _, err := websocket.Dial(ctx, "wss://"+id.ID()+".spool.invalid/api/desktop/view/ws?token="+view.Token, &websocket.DialOptions{HTTPClient: client, HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{0, 255, 128, 1}); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != string([]byte{0, 255, 128, 1}) {
		t.Fatal("voxl binary proxy failed", data, err)
	}
	if status, _ := request("/api/logout", `{}`, cookie, csrf); status != 200 {
		t.Fatal("logout failed", status)
	}
	_, _, err = conn.Read(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatal("logout did not promptly revoke desktop connection", err)
	}
	if status, _ := request("/api/me", "", cookie, ""); status != 401 {
		t.Fatal("revoked session survived relay", status)
	}
	snapshot, err := service.RelayDevices()
	if err != nil || len(snapshot.Devices) != 1 {
		t.Fatal(snapshot, err)
	}
	if err := service.RevokeRelayDevice(snapshot.Devices[0].ID); err != nil {
		t.Fatal(err)
	}
	if response, err := client.Get(origin + "/healthz"); err == nil {
		response.Body.Close()
		t.Fatal("revoked device reached Spool")
	}
}

func TestPairingCodeKeepsRegistrationCredentialLocal(t *testing.T) {
	service, settings := settingsService(t)
	broker, secret := testRelayBroker(t)
	public := httptest.NewServer(broker)
	defer public.Close()
	defer service.Stop()
	settings.RelayURL, settings.RelayLANPort = public.URL, availableServicePort(t)
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RelayPairingCode(); err == nil {
		t.Fatal("pairing allowed before relay starts")
	}
	if err := service.RegisterRelay(t.Context(), public.URL, secret); err != nil {
		t.Fatal(err)
	}
	settings.RelayEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	info, err := service.RelayPairingInfo()
	if err != nil {
		t.Fatal(err)
	}
	code, err := relay.ParsePairing(info.Code)
	if err != nil || code.ID() != info.ServerID {
		t.Fatal("invalid pairing", err)
	}
	if info.Fingerprint == "" || !strings.HasPrefix(info.QR, "data:image/png;base64,") {
		t.Fatal("missing scannable QR or identity")
	}
	png, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(info.QR, "data:image/png;base64,"))
	if err != nil || !strings.HasPrefix(string(png), "\x89PNG\r\n\x1a\n") {
		t.Fatal("invalid QR image")
	}
	encoded, _ := json.Marshal(info)
	saved, _ := json.Marshal(settings)
	if strings.Contains(string(encoded), secret) || strings.Contains(string(saved), secret) {
		t.Fatal("registration credential escaped local secret storage")
	}
	if service.relayRegistrationToken(settings.RelayURL) != secret {
		t.Fatal("registration credential did not persist")
	}
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "environment-token-that-must-be-overridden")
	if err := service.SetRelayRegistrationToken(""); err != nil {
		t.Fatal(err)
	}
	if service.relayRegistrationToken(settings.RelayURL) != "" {
		t.Fatal("automatic registration did not clear environment credential")
	}
}

func TestRelayServiceStartsStopsAndRestartsWithSameIdentity(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "required-token"
		if automatic {
			name = "automatic-token"
		}
		t.Run(name, func(t *testing.T) { testRelayServiceLifecycle(t, automatic) })
	}
}

func TestRelayRegistrationGatesEnablementAndPersistsConfirmation(t *testing.T) {
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
	service, settings := settingsService(t)
	broker, token := testRelayBroker(t)
	public := httptest.NewServer(broker)
	t.Cleanup(public.Close)
	settings.RelayURL = public.URL
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	assertLocked := func() {
		t.Helper()
		if service.Status().Relay.Registered {
			t.Fatal("unconfirmed registration unlocked the switch")
		}
		enabled := settings
		enabled.RelayEnabled = true
		if err := service.SaveSettings(enabled); err == nil {
			t.Fatal("enabled relay without registration")
		}
	}
	assertLocked()
	if err := service.SetRelayRegistrationToken(token); err != nil {
		t.Fatal(err)
	}
	assertLocked()
	for _, invalid := range []string{"", strings.Repeat("a", 43)} {
		if err := service.RegisterRelay(t.Context(), public.URL, invalid); err == nil {
			t.Fatal("relay accepted invalid registration")
		}
		assertLocked()
		if service.relayRegistrationToken(settings.RelayURL) != token {
			t.Fatal("failed registration replaced the saved credential")
		}
	}
	if err := service.RegisterRelay(t.Context(), public.URL, token); err != nil {
		t.Fatal(err)
	}
	if !service.Status().Relay.Registered || service.relay != nil {
		t.Fatal("registration must confirm enrollment without starting relay")
	}
	root := service.Store.Root()
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenService(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	service = reopened
	if !service.Status().Relay.Registered {
		t.Fatal("registration did not survive app restart")
	}
	settings.RelayEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal("confirmed registration did not unlock enablement", err)
	}
	settings.RelayEnabled = false
	settings.RelayURL = "https://another-relay.example.com"
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	assertLocked()
	settings.RelayURL = public.URL
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.SetRelayRegistrationToken(strings.Repeat("b", 43)); err != nil {
		t.Fatal(err)
	}
	assertLocked()
}

func TestRelayCanceledRegistrationKeepsSwitchLocked(t *testing.T) {
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
	service, settings := settingsService(t)
	requested := make(chan struct{})
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requested)
		<-r.Context().Done()
	}))
	t.Cleanup(public.Close)
	settings.RelayURL = public.URL
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- service.RegisterRelay(ctx, public.URL, "") }()
	select {
	case <-requested:
	case <-time.After(3 * time.Second):
		t.Fatal("registration did not contact relay")
	}
	if !service.Status().Relay.Registering {
		t.Fatal("in-progress registration was not visible")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled registration succeeded")
	}
	status := service.Status().Relay
	if status.Registered || status.Registering || status.RegistrationTokenSet {
		t.Fatal("canceled registration left confirmed state", status)
	}
}

func testRelayServiceLifecycle(t *testing.T, automatic bool) {
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
	service, settings := settingsService(t)
	broker, err := relay.NewServer(relay.ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir(), AllowUnregisteredHosts: automatic})
	if err != nil {
		t.Fatal(err)
	}
	var token string
	if !automatic {
		_, token, err = broker.CreateRegistration("service lifecycle")
		if err != nil {
			t.Fatal(err)
		}
	}
	public := httptest.NewServer(broker)
	defer public.Close()
	defer broker.Close()
	port, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	settings.RelayLANPort = port.Addr().(*net.TCPAddr).Port
	port.Close()
	settings.RelayURL = public.URL
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterRelay(t.Context(), public.URL, token); err != nil {
		t.Fatal(err)
	}
	if status := service.Status(); !status.Relay.Registered || status.Relay.Registering || service.relay != nil || status.RestartRequired {
		t.Fatal("registration enabled ingress or was not confirmed", status)
	}
	settings.RelayEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if !service.Status().RestartRequired {
		t.Fatal("relay setting did not require restart")
	}
	if err := service.Restart(); err != nil {
		t.Fatal(err)
	}
	status := waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.State == "connected" })
	if status.RestartRequired || status.Error != "" {
		t.Fatal(status)
	}
	if !status.Relay.RegistrationTokenSet || service.relayRegistrationToken(settings.RelayURL) == "" {
		t.Fatal("issued token was not saved locally")
	}
	if info, err := os.Stat(filepath.Join(service.Store.Root(), "relay", "registrations.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("registration token storage is not private", err)
	}
	token = service.relayRegistrationToken(settings.RelayURL)
	code, err := service.RelayPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := relay.ParsePairing(code)
	p.LAN = nil
	dialer := pairServiceDevice(t, service, p)
	defer dialer.Close()
	transport := &http.Transport{DialTLSContext: dialer.DialTLSContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get(relay.Origin(p.ID()) + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	dialer.NetworkChanged()
	if response, err = client.Get(relay.Origin(p.ID()) + "/healthz"); err == nil {
		response.Body.Close()
		t.Fatal("stopped service accepted relay ingress")
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	status = waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.State == "connected" })
	if status.Relay.ServerID != p.ID() {
		t.Fatal("identity changed across restart")
	}
	if service.relayRegistrationToken(settings.RelayURL) != token || status.RestartRequired {
		t.Fatal("restart did not reuse saved registration token")
	}
	response, err = client.Get(relay.Origin(p.ID()) + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
}

func pairServiceDevice(t *testing.T, service *Service, p relay.Pairing) *relay.Dialer {
	t.Helper()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	dialer, err := relay.NewDialer(t.Context(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	dialer.TLSConfig, err = relay.DeviceTLSConfig(p, key.Seed(), true)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialTLSContext: dialer.DialTLSContext}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	if state, err := relay.PairDevice(t.Context(), client, p, "Test phone"); err != nil || state != "pending" {
		t.Fatal(state, err)
	}
	snapshot, err := service.RelayDevices()
	if err != nil || len(snapshot.Pending) != 1 {
		t.Fatal(snapshot, err)
	}
	if err := service.DecideRelayDevice(snapshot.Pending[0].RequestID, true); err != nil {
		t.Fatal(err)
	}
	if state, err := relay.PairDevice(t.Context(), client, p, "Test phone"); err != nil || state != "approved" {
		t.Fatal(state, err)
	}
	dialer.Close()
	transport.CloseIdleConnections()
	dialer, err = relay.NewDialer(t.Context(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	dialer.TLSConfig, err = relay.DeviceTLSConfig(p, key.Seed(), false)
	if err != nil {
		t.Fatal(err)
	}
	return dialer
}
