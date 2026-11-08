package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterConfirmsIdentityWithoutOpeningIngress(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "token"
		if automatic {
			name = "automatic"
		}
		t.Run(name, func(t *testing.T) {
			broker := testBrokerMode(t, automatic)
			public := httptest.NewServer(broker)
			t.Cleanup(public.Close)
			id, _ := NewIdentity()
			var token string
			if !automatic {
				_, token = createViaAPI(t, broker, "home")
			}
			credential, err := Register(t.Context(), public.URL, token, id, nil)
			if err != nil || !validToken(credential) || (!automatic && credential != token) {
				t.Fatal("registration was not confirmed", err)
			}
			list := broker.registrationStatuses("")
			if len(list) != 1 || list[0].ServerID != id.ID() || list[0].Connected || list[0].Connections != 0 {
				t.Fatal("enrollment did not bind identity or opened ingress")
			}
			req, _ := http.NewRequest("GET", public.URL+"/v1/connect/"+id.ID(), nil)
			req.Header.Set("Authorization", "Bearer "+id.AccessToken)
			response, err := public.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 404 {
				t.Fatal("registration made server reachable")
			}
			other, _ := NewIdentity()
			if _, err := Register(t.Context(), public.URL, credential, other, nil); !errors.Is(err, ErrRegistrationRejected) {
				t.Fatal("another identity used a claimed token", err)
			}
			if _, err := Register(t.Context(), public.URL, credential, id, nil); err != nil {
				t.Fatal("same identity could not confirm registration again", err)
			}
			if err := broker.RevokeRegistration(list[0].ID); err != nil {
				t.Fatal(err)
			}
			if _, err := Register(t.Context(), public.URL, credential, id, nil); !errors.Is(err, ErrRegistrationRejected) {
				t.Fatal("revoked token accepted", err)
			}
		})
	}
}

func TestRegisterDoesNotReplaceExistingHost(t *testing.T) {
	broker := testBroker(t)
	public := httptest.NewServer(broker)
	t.Cleanup(func() { broker.Close(); public.Close() })
	record, token := createViaAPI(t, broker, "home")
	id, _ := NewIdentity()
	network := startAdminTestHost(t, public, token, id, make(chan struct{}))
	if _, err := Register(t.Context(), public.URL, token, id, nil); err != nil {
		t.Fatal(err)
	}
	if !broker.registrationStatuses(record.ID)[0].Connected {
		t.Fatal("registration disconnected the host")
	}
	client, _ := network.client(t, network.pair)
	response, err := client.Get(Origin(id.ID()))
	if err != nil {
		t.Fatal("host connection no longer serves traffic", err)
	}
	response.Body.Close()
}
