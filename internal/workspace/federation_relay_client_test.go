package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"spool/android/transport/spoolnet"
	"spool/relay"
)

func federationRelayHostForTest(t *testing.T, store *Store, app *App) (string, *Service) {
	t.Helper()
	id, err := store.federationKey()
	if err != nil {
		t.Fatal(err)
	}
	broker, token := testRelayBroker(t)
	public := httptest.NewServer(broker)
	t.Cleanup(public.Close)
	host, err := relay.NewHost(t.Context(), public.URL, token, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	host.FederationEnabled = true
	devices, err := relay.OpenDevices(filepath.Join(store.Root(), "relay"))
	if err != nil {
		host.Close()
		t.Fatal(err)
	}
	config, err := id.TLSConfig()
	if err != nil {
		host.Close()
		t.Fatal(err)
	}
	admission := store.federationRelayAdmission()
	devices.SetAdmission(host.AccessToken, nil)
	noWeb := func() bool { return false }
	server := &http.Server{Handler: devices.HandlerWithFederation(app, id.ID(), noWeb, admission), ReadHeaderTimeout: time.Second}
	ready, served, stopped := make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
	host.OnStatus = func(state string) {
		if state == "connected" {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	}
	go func() {
		defer close(served)
		_ = server.Serve(devices.ListenerWithFederation(host, config, id.ID(), noWeb, true, admission))
	}()
	go func() { defer close(stopped); host.Run(); host.Wait() }()
	t.Cleanup(func() {
		host.Close()
		_ = server.Close()
		devices.CloseConnections()
		for _, done := range []chan struct{}{served, stopped} {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("Relay fixture did not stop")
			}
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("Relay fixture did not connect")
	}
	code, err := (relay.Pairing{Version: 2, RelayURL: public.URL, PublicKey: id.PublicKey(), AccessToken: id.FederationAccessToken()}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	return code, &Service{Store: store, relayDevices: devices, relay: &relayRuntime{host: host}}
}

func TestFederationRelayAutomaticAdmissionBindsTheClientAndRevokes(t *testing.T) {
	sa, sb := testStore(t), testStore(t)
	a, b := NewApp(sa, nil, "", ""), NewApp(sb, nil, "", "")
	alice := testUser(t, sa, "alice")
	as := httptest.NewTLSServer(a)
	t.Cleanup(as.Close)
	pa := federationPairForTest(t, sb, sa, as.URL, true, nil, nil)
	b.federationDial = func(ctx context.Context, peer FederationPeer, body []byte) ([]byte, error) {
		return federationHTTP(ctx, peer.Address, body, as.Client().Transport.(*http.Transport).Clone())
	}
	code, service := federationRelayHostForTest(t, sb, b)
	pb, err := sb.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	pb.Transport, pb.RelayCode, pb.Users = "relay", code, []string{alice.ID}
	// A previous public client URL must not change the Relay route chosen by
	// the approved outgoing application.
	pb.ClientURL = "https://old-client.example.test/"
	if err := seedApprovedFederationPeer(sa, pb); err != nil {
		t.Fatal(err)
	}
	account, csrf := loginRequest(t, a, "alice")
	id, err := sa.authenticate(account.Value)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := sa.federationTargets(id)
	if err != nil || len(targets) != 1 || targets[0].ClientMode != "relay" || targets[0].ClientURL != relay.Origin(pb.ID)+"/" {
		t.Fatal("Relay target was not advertised", err)
	}
	sourcePost := func(path, csrfToken string) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(map[string]string{"target": pb.ID})
		r := httptest.NewRequest(http.MethodPost, "https://source.example.test"+path, bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrfToken)
		r.AddCookie(account)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if w := sourcePost("/api/federation/relay-route", "wrong"); w.Code != http.StatusForbidden {
		t.Fatal("route disclosure bypassed CSRF", w.Code)
	}
	w := sourcePost("/api/federation/relay-route", csrf)
	var route struct {
		Code string `json:"relay_code"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &route) != nil || route.Code != code {
		t.Fatal("allowed user could not obtain Relay routing", w.Code)
	}
	root := t.TempDir()
	device, err := spoolnet.NewFederationRelayClient(root, route.Code)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = device.Close() })
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: device, Jar: jar, CheckRedirect: federationNoRedirect}
	request := func(client *http.Client, method, path string, body any) *http.Response {
		t.Helper()
		var payload io.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			payload = bytes.NewReader(data)
		}
		r, err := http.NewRequestWithContext(t.Context(), method, relay.Origin(pb.ID)+path, payload)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Origin", relay.Origin(pb.ID))
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	check := func(client *http.Client, path string, want int) {
		t.Helper()
		response := request(client, http.MethodGet, path, nil)
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s: status %d, want %d", path, response.StatusCode, want)
		}
	}
	check(client, "/healthz", http.StatusOK)
	check(client, "/api/me", http.StatusUnauthorized)
	for _, path := range []string{FederationControlPath, FederationTrustPath, "/_spool/pair", "/_spool/admission", "/web/" + pb.ID + "/", "/api/login"} {
		check(client, path, http.StatusNotFound)
	}
	var issued struct {
		Ticket string `json:"ticket"`
	}
	w = sourcePost("/api/federation/issue", csrf)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &issued) != nil {
		t.Fatal("source did not issue a ticket")
	}
	response := request(client, http.MethodPost, "/api/federation/exchange", map[string]string{"ticket": issued.Ticket})
	var signedIn struct {
		User User   `json:"user"`
		CSRF string `json:"csrf"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&signedIn) != nil {
		response.Body.Close()
		t.Fatal("automatic admission failed", response.StatusCode)
	}
	response.Body.Close()
	if signedIn.User.SourceServerID != pa.ID || signedIn.CSRF == "" {
		t.Fatal("login lost the source identity")
	}
	check(client, "/api/me", http.StatusOK)
	for _, path := range []string{FederationControlPath, FederationTrustPath, "/_spool/pair", "/_spool/admission", "/api/login"} {
		check(client, path, http.StatusNotFound)
	}
	other, err := spoolnet.NewFederationRelayClient(t.TempDir(), code)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	check(&http.Client{Transport: other, Jar: jar}, "/api/me", http.StatusUnauthorized)
	response = request(client, http.MethodPost, "/api/federation/exchange", map[string]string{"ticket": issued.Ticket})
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("one-time ticket was reused")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, strings.Replace(relay.Origin(pb.ID), "https:", "wss:", 1)+"/api/events", &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Origin": []string{relay.Origin(pb.ID)}}})
	if err != nil {
		t.Fatal("workspace stream was not admitted", err)
	}
	defer socket.CloseNow()
	hello, _ := json.Marshal(map[string]any{"type": "hello", "csrf": signedIn.CSRF})
	if err := socket.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatal(err)
	}
	if _, _, err := socket.Read(ctx); err != nil {
		t.Fatal("workspace stream failed after admission", err)
	}
	// Revoking the last ordinary device retires its broker credential at once.
	// An independent federated session must still reconnect with its saved route.
	pairing, err := relay.ParsePairing(code)
	if err != nil {
		t.Fatal(err)
	}
	local, err := sb.federationKey()
	if err != nil {
		t.Fatal(err)
	}
	pairing.AccessToken = local.AccessToken
	pairing.Invitation, pairing.ExpiresAt = service.relayDevices.Invite()
	paired := pairServiceDevice(t, service, pairing)
	paired.Close()
	snapshot, err := service.RelayDevices()
	if err != nil || len(snapshot.Devices) != 1 {
		t.Fatal("ordinary device was not paired", err)
	}
	if err := service.RevokeRelayDevice(snapshot.Devices[0].ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, pairing.RelayURL+"/v1/connect/"+pb.ID, nil)
		r.Header.Set("Authorization", "Bearer "+pairing.AccessToken)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revoked device's broker token was not retired")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	device, err = spoolnet.NewFederationRelayClient(root, code)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = device.Close() })
	client.Transport = device
	check(client, "/api/me", http.StatusOK)
	if err := sa.SetFederationTrustUsers(pb.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	if w := sourcePost("/api/federation/relay-route", csrf); w.Code != http.StatusForbidden {
		t.Fatal("revoked user can still fetch a route", w.Code)
	}
	if err := sb.RevokeFederationTrust(pa.ID); err != nil {
		t.Fatal(err)
	}
	check(client, "/api/me", http.StatusUnauthorized)
}
