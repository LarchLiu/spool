package workspace

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
	"spool/internal/tailnet"
	"spool/relay"
)

func TestRelayDeviceAdmissionFollowsAppliedSettings(t *testing.T) {
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
	service, settings := settingsService(t)
	connection := newTestTailnetConnection(t)
	settings = enableTestTailnet(t, service, settings, func(context.Context, string, tailnet.Options, func(tailnet.Status)) (tailnetConnection, error) {
		return connection, nil
	})
	waitTailnet(t, service, func(status ServiceStatus) bool { return status.TSNet.URL != "" })
	_, public, token := relayWebBroker(t)
	defer public.Close()
	defer service.Stop()
	settings.RelayURL, settings.RelayLANPort = public.URL, availableServicePort(t)
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterRelay(t.Context(), public.URL, token); err != nil {
		t.Fatal(err)
	}
	settings.RelayEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	waitReady := func() {
		t.Helper()
		waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.State == "connected" })
	}
	waitReady()
	app, web, node := service.app, service.web, service.tailnet
	app.turnMu.Lock()
	work, control := app.registerTurn(t.Context(), "relay-device-restart", "turn")
	app.turnMu.Unlock()
	defer app.releaseTurn("relay-device-restart", control)
	code, err := service.RelayPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	p, err := relay.ParsePairing(code)
	if err != nil {
		t.Fatal(err)
	}
	p.LAN = nil
	dialer := pairServiceDevice(t, service, p)
	defer dialer.Close()
	transport := &http.Transport{DialTLSContext: dialer.DialTLSContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	checkDevice := func() {
		t.Helper()
		response, err := client.Get(relay.Origin(p.ID()) + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal(response.Status)
		}
	}
	checkDevice()
	user := testUser(t, service.Store, "paired-phone")
	cookie, identity, err := service.Store.login(user.Username, "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	origin := relay.Origin(p.ID())
	socket, _, err := websocket.Dial(t.Context(), origin+"/api/events", &websocket.DialOptions{
		HTTPClient: client, HTTPHeader: http.Header{"Origin": {origin}, "Cookie": {(&http.Cookie{Name: authCookie, Value: cookie}).String()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	realtimeWrite(t, socket, realtimeInput{Type: "hello", CSRF: identity.CSRF, Visible: true})
	realtimeReady(t, socket)
	publicClient := &http.Client{Timeout: 3 * time.Second}
	defer publicClient.CloseIdleConnections()
	checkWeb := func(want int) {
		t.Helper()
		response, err := publicClient.Get(relayTenantURL(t, public.URL, p.ID()) + "api/me")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("Web status %d, want %d", response.StatusCode, want)
		}
	}
	checkWeb(503)
	// A pending switch to another relay must not put that unstarted address or
	// port into a QR, or disable admission on the current relay.
	pending := settings
	pending.RelayEnabled = false
	pending.RelayURL = "https://pending-relay.example.test"
	pending.RelayLANPort = availableServicePort(t)
	if err := service.SaveSettings(pending); err != nil {
		t.Fatal(err)
	}
	activeCode, err := service.RelayPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	active, err := relay.ParsePairing(activeCode)
	if err != nil || active.RelayURL != settings.RelayURL || active.ID() != p.ID() {
		t.Fatal("QR used pending relay", active.RelayURL, err)
	}
	for _, address := range active.LAN {
		_, port, err := net.SplitHostPort(address)
		if err != nil || port != strconv.Itoa(settings.RelayLANPort) {
			t.Fatal("QR used pending LAN port", address, err)
		}
	}
	checkDevice()
	if err := service.CancelServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	settings.RelayWebEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	checkWeb(503) // Saving does not activate the unpaired Web exception.
	if err := service.ApplyServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	waitReady()
	checkWeb(401) // Public Web retains ordinary Spool account authentication.
	if service.app != app || service.web != web || service.tailnet != node || work.Err() != nil {
		t.Fatal("relay restart interrupted the Web entry or ongoing task")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, _, err := socket.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatal("old authenticated event socket survived relay restart", err)
	}
	checkDevice() // The same approved key reconnects without another scan.
	stale, err := relay.NewDialer(t.Context(), active, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	stale.TLSConfig = dialer.TLSConfig.Clone()
	stale.TLSConfig.ServerName = relay.PairingServerName(p.ID())
	staleTransport := &http.Transport{DialTLSContext: stale.DialTLSContext}
	defer staleTransport.CloseIdleConnections()
	if _, err := relay.PairDevice(t.Context(), &http.Client{Transport: staleTransport, Timeout: 3 * time.Second}, active, "Old invitation"); err == nil {
		t.Fatal("invitation survived relay restart")
	}
	if snapshot, err := service.RelayDevices(); err != nil || len(snapshot.Devices) != 1 || len(snapshot.Pending) != 0 {
		t.Fatal("relay restart lost approvals or kept requests", snapshot, err)
	}
	// A failed Apply restores both the old listener and its Web policy.
	occupied, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	pending = settings
	pending.RelayWebEnabled = false
	pending.RelayLANPort = occupied.Addr().(*net.TCPAddr).Port
	if err := service.SaveSettings(pending); err != nil {
		t.Fatal(err)
	}
	checkWeb(401)
	if err := service.ApplyServiceChanges("relay"); err == nil {
		t.Fatal("occupied LAN port accepted")
	}
	waitReady()
	checkWeb(401)
	checkDevice()
	if err := service.CancelServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	settings.RelayWebEnabled = false
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	waitReady()
	checkWeb(503)
	checkDevice()
	if service.app != app || service.web != web || service.tailnet != node || work.Err() != nil {
		t.Fatal("relay apply replaced another entry or stopped active work")
	}
	requireServiceHealth(t, "http://"+service.Status().Address)
	requireServiceHealth(t, "http://"+connection.Listener().Addr().String())
}
