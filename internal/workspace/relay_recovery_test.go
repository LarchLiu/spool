package workspace

import (
	"net/http/httptest"
	"testing"
)

func TestRelayRegistrationRecoversForgottenEnrollment(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := "same-token"
		if replacement {
			name = "new-token"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
			service, settings := settingsService(t)
			broker, token := testRelayBroker(t)
			public := httptest.NewServer(broker)
			t.Cleanup(public.Close)
			settings.RelayURL = public.URL
			if err := service.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if err := service.RegisterRelay(t.Context(), public.URL, token); err != nil {
				t.Fatal(err)
			}
			// Lose only the local enrollment record; the server identity survives.
			service.mu.Lock()
			err := service.saveRelayRegistrationsLocked(map[string]relayRegistration{})
			service.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if status := service.Status().Relay; status.Registered || status.RegistrationTokenSet {
				t.Fatal("fixture still remembers enrollment", status)
			}
			if replacement {
				_, token, err = broker.CreateRegistration("recovered server")
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := service.RegisterRelay(t.Context(), public.URL, token); err != nil {
				t.Fatal("could not recover forgotten enrollment", err)
			}
			if status := service.Status().Relay; !status.Registered || !status.RegistrationTokenSet || status.Registering || service.relay != nil {
				t.Fatal("recovery failed to confirm enrollment without enabling relay", status)
			}
			if service.relayRegistrationToken(public.URL) != token {
				t.Fatal("recovery did not save the supplied token")
			}
		})
	}
}
