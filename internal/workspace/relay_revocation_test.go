package workspace

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"spool/relay"
)

func TestRelayRejectedAuthorizationDisablesOnlyRelay(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		connected, invalidated bool
	}{
		{name: "before-connect"},
		{name: "while-connected", connected: true},
		{name: "already-invalidated", connected: true, invalidated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
			service, settings := settingsService(t)
			broker, public := relayWebPublicServer(t, relay.ServerOptions{})
			t.Cleanup(func() { broker.Close() })
			registration, token, err := broker.CreateRegistration("revoked server")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(public.Close)
			settings.RelayURL, settings.RelayLANPort = public.URL, availableServicePort(t)
			settings.RelayWebEnabled = true
			if err := service.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if err := service.RegisterRelay(t.Context(), public.URL, token); err != nil {
				t.Fatal(err)
			}
			app, runner, web := service.app, service.runner, service.web
			localURL, _ := service.LocalWebURL()
			app.turnMu.Lock()
			work, control := app.registerTurn(t.Context(), "relay-revocation", "turn")
			app.turnMu.Unlock()
			defer app.releaseTurn("relay-revocation", control)
			revoke := func() {
				t.Helper()
				if err := broker.RevokeRegistration(registration.ID); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.connected {
				revoke()
			}
			settings.RelayEnabled = true
			if err := service.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if err := service.ApplyServiceChanges("relay"); err != nil {
				t.Fatal(err)
			}
			if tc.connected {
				waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.State == "connected" && s.Relay.WebURL != "" })
				if tc.invalidated {
					service.mu.Lock()
					err := service.invalidateRelayRegistrationLocked(public.URL)
					service.mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
				}
				revoke()
			}
			status := waitTailnet(t, service, func(s ServiceStatus) bool { return !s.Relay.Registered && s.Relay.State == "disabled" })
			saved, err := service.Store.Settings()
			if err != nil {
				t.Fatal(err)
			}
			if saved.RelayEnabled || service.relay != nil || service.activeSettings.RelayEnabled || service.app.activeSettings().RelayEnabled {
				t.Fatal("rejected authorization did not disable the saved and active relay")
			}
			if !status.Running || status.RestartRequired || status.Relay.WebURL != "" || !status.Relay.RegistrationTokenSet {
				t.Fatal("rejected authorization left an inconsistent service state", status)
			}
			if service.app != app || service.runner != runner || service.web != web || work.Err() != nil {
				t.Fatal("rejected relay authorization interrupted local work")
			}
			requireServiceHealth(t, localURL)
			if err := service.Restart(); err != nil {
				t.Fatal(err)
			}
			if status := service.Status(); status.Relay.State != "disabled" || status.Relay.Registered || service.relay != nil {
				t.Fatal("restart restored the revoked relay", status)
			}
		})
	}
}

func TestRelayTemporaryFailurePreservesEnabledRegistration(t *testing.T) {
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
	service, settings := settingsService(t)
	broker, token := testRelayBroker(t)
	var unavailable atomic.Bool
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() && r.URL.Path == "/v1/host" {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		broker.ServeHTTP(w, r)
	}))
	t.Cleanup(public.Close)
	settings.RelayURL, settings.RelayLANPort = public.URL, availableServicePort(t)
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterRelay(t.Context(), public.URL, token); err != nil {
		t.Fatal(err)
	}
	unavailable.Store(true)
	settings.RelayEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	status := waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.State == "reconnecting" })
	saved, err := service.Store.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.RelayEnabled || !status.Relay.Registered || status.RestartRequired || service.relay == nil {
		t.Fatal("temporary relay failure disabled a valid registration", status)
	}
	unavailable.Store(false)
	waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.State == "connected" })
}
