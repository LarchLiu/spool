package workspace

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"spool/relay"
)

func TestRelayDeviceRevocationRotatesAdmissionToken(t *testing.T) {
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
	service, settings := settingsService(t)
	_, public, registration := relayWebBroker(t)
	defer public.Close()
	defer service.Stop()
	settings.RelayURL, settings.RelayLANPort = public.URL, availableServicePort(t)
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterRelay(t.Context(), public.URL, registration); err != nil {
		t.Fatal(err)
	}
	settings.RelayEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	waitTailnet(t, service, func(status ServiceStatus) bool { return status.Relay.State == "connected" })

	deviceIDs := func() []string {
		t.Helper()
		snapshot, err := service.RelayDevices()
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, device := range snapshot.Devices {
			ids = append(ids, device.ID)
		}
		return ids
	}
	// pair approves one device that can only reach the server through the relay.
	pair := func() (*http.Client, *relay.Dialer, relay.Pairing, string) {
		t.Helper()
		code, err := service.RelayPairingCode()
		if err != nil {
			t.Fatal(err)
		}
		p, err := relay.ParsePairing(code)
		if err != nil {
			t.Fatal(err)
		}
		p.LAN = nil
		known := deviceIDs()
		dialer := pairServiceDevice(t, service, p)
		transport := &http.Transport{DialTLSContext: dialer.DialTLSContext}
		t.Cleanup(func() { transport.CloseIdleConnections(); dialer.Close() })
		for _, id := range deviceIDs() {
			if !slices.Contains(known, id) {
				return &http.Client{Transport: transport, Timeout: 5 * time.Second}, dialer, p, id
			}
		}
		t.Fatal("paired device is not listed")
		return nil, nil, p, ""
	}
	_, _, p, lostID := pair()
	kept, keptDialer, _, keptID := pair()
	original := p.AccessToken
	directory := filepath.Join(service.Store.Root(), "relay")
	admitted := func(token string) bool {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, public.URL+"/v1/connect/"+p.ID(), nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := public.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode != http.StatusNotFound
	}
	eventually := func(what string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !condition() {
			if time.Now().After(deadline) {
				t.Fatal(what)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	if err := service.RevokeRelayDevice(lostID); err != nil {
		t.Fatal(err)
	}
	rotated, err := relay.LoadIdentity(directory)
	if err != nil || rotated.ID() != p.ID() || rotated.AccessToken == original || len(rotated.Retired) != 1 || rotated.Retired[0].Token != original {
		t.Fatal("revocation did not rotate the admission token within the same identity", err)
	}
	code, err := service.RelayPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	if fresh, err := relay.ParsePairing(code); err != nil || fresh.AccessToken != rotated.AccessToken {
		t.Fatal("new pairing codes still carry the replaced token", err)
	}
	if err := service.CancelRelayPairing(code); err != nil {
		t.Fatal(err)
	}
	eventually("relay did not learn the rotated token", func() bool { return admitted(rotated.AccessToken) })
	if !admitted(original) {
		t.Fatal("replaced token refused while a paired device still depends on it")
	}

	// The remaining device collects the token over its own pinned connection.
	token, err := relay.SyncAdmission(t.Context(), kept, p, original)
	if err != nil || token != rotated.AccessToken {
		t.Fatal("paired device did not receive the rotated token", err)
	}
	if err := keptDialer.SetAccessToken(token); err != nil {
		t.Fatal(err)
	}
	if _, err := relay.SyncAdmission(t.Context(), kept, p, token); err != nil {
		t.Fatal(err)
	}
	eventually("grace period did not end after the last device confirmed", func() bool {
		id, err := relay.LoadIdentity(directory)
		return err == nil && len(id.Retired) == 0 && id.AccessToken == token
	})
	eventually("relay still admits the revoked device's token", func() bool { return !admitted(original) })
	response, err := kept.Get(relay.Origin(p.ID()) + "/healthz")
	if err != nil {
		t.Fatal("remaining device lost relay access", err)
	}
	response.Body.Close()

	// With nobody left to collect it, the replaced token is dropped at once.
	if err := service.RevokeRelayDevice(keptID); err != nil {
		t.Fatal(err)
	}
	final, err := relay.LoadIdentity(directory)
	if err != nil || final.AccessToken == token || len(final.Retired) != 0 {
		t.Fatal("last revocation kept a token no device needs", err)
	}
	eventually("relay still admits a token after its last device was revoked", func() bool { return !admitted(token) })
}
