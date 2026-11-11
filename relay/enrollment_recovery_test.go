package relay

import (
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReplacementEnrollmentRetiresOnlyPreviousCredential(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		name := "token"
		if automatic {
			name = "automatic"
		}
		t.Run(name, func(t *testing.T) {
			s := testBrokerMode(t, automatic)
			public := httptest.NewServer(s)
			t.Cleanup(func() { s.Close(); public.Close() })
			id, _ := NewIdentity()
			var token string
			if !automatic {
				_, token = createViaAPI(t, s, "home")
			}
			closed := make(chan struct{})
			old := startAdminTestHost(t, public, token, id, closed)
			token = old.host.RegistrationToken()
			previous := s.registrationStatuses("")[0]
			_, otherToken := createViaAPI(t, s, "office")
			otherID, _ := NewIdentity()
			other := startAdminTestHost(t, public, otherToken, otherID, make(chan struct{}))
			client, dialer := old.client(t, old.pair)
			stream, err := client.Get(Origin(id.ID()) + "/stream")
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Body.Close()
			if _, err := io.ReadFull(stream.Body, make([]byte, 6)); err != nil {
				t.Fatal(err)
			}
			replacement, newToken := createViaAPI(t, s, "recovered home")
			if credential, err := Register(t.Context(), public.URL, newToken, id, nil); err != nil || credential != newToken {
				t.Fatal("new token could not recover the existing identity", err)
			}
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("replaced credential kept its data connection")
			}
			retired := s.registrationStatuses(previous.ID)[0]
			if retired.UnregisteredAt == nil || retired.RevokedAt != nil || retired.Connected {
				t.Fatal("replacement did not retire the old credential", retired)
			}
			current := s.registrationStatuses(replacement.ID)[0]
			if current.ServerID != id.ID() || current.UnregisteredAt != nil || current.RevokedAt != nil || current.Connected {
				t.Fatal("replacement did not bind the same identity without enabling ingress", current)
			}
			if _, err := Register(t.Context(), public.URL, token, id, nil); !errors.Is(err, ErrRegistrationRejected) {
				t.Fatal("retired token still authorized registration", err)
			}
			otherClient, _ := other.client(t, other.pair)
			if request(t, otherClient, other.pair, "/") != otherID.ID() {
				t.Fatal("replacement disconnected another server")
			}
			startAdminTestHost(t, public, newToken, id, make(chan struct{}))
			dialer.NetworkChanged()
			if request(t, client, old.pair, "/") != id.ID() {
				t.Fatal("recovery changed the paired server identity")
			}
			if _, err := Register(t.Context(), public.URL, newToken, id, nil); err != nil || !s.registrationStatuses(replacement.ID)[0].Connected {
				t.Fatal("retry interrupted the recovered host", err)
			}
			s.Close()
			restored, err := NewServer(s.opts)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			server := httptest.NewServer(restored)
			defer server.Close()
			if _, err := Register(t.Context(), server.URL, token, id, nil); !errors.Is(err, ErrRegistrationRejected) {
				t.Fatal("retired credential returned after restart", err)
			}
			if _, err := Register(t.Context(), server.URL, newToken, id, nil); err != nil {
				t.Fatal("replacement did not persist", err)
			}
			if automatic {
				if err := Unregister(t.Context(), server.URL, newToken, id, nil); err != nil {
					t.Fatal(err)
				}
				if _, err := Register(t.Context(), server.URL, "", id, nil); err != nil {
					t.Fatal("credential replacement became an administrative identity ban", err)
				}
			}
		})
	}
}

func TestReplacementEnrollmentRequiresDurableWrite(t *testing.T) {
	s := testBroker(t)
	public := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); public.Close() })
	previous, token := createViaAPI(t, s, "home")
	id, _ := NewIdentity()
	network := startAdminTestHost(t, public, token, id, make(chan struct{}))
	replacement, newToken := createViaAPI(t, s, "recovered home")
	file := filepath.Join(s.opts.DataDir, "registrations.json")
	if err := os.Rename(file, file+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(t.Context(), public.URL, newToken, id, nil); err == nil {
		t.Fatal("replacement succeeded without persistence")
	}
	if got := s.registrationStatuses(previous.ID)[0]; got.UnregisteredAt != nil || !got.Connected {
		t.Fatal("failed replacement retired the existing host", got)
	}
	if got := s.registrationStatuses(replacement.ID)[0]; got.ServerID != "" {
		t.Fatal("failed replacement claimed the new token", got)
	}
	client, _ := network.client(t, network.pair)
	if request(t, client, network.pair, "/") != id.ID() {
		t.Fatal("failed replacement interrupted service")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file+".saved", file); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(t.Context(), public.URL, newToken, id, nil); err != nil {
		t.Fatal("replacement could not be retried", err)
	}
}

func TestHostConnectionCannotReplaceRegistration(t *testing.T) {
	s := testBroker(t)
	public := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); public.Close() })
	previous, token := createViaAPI(t, s, "home")
	id, _ := NewIdentity()
	if _, err := Register(t.Context(), public.URL, token, id, nil); err != nil {
		t.Fatal(err)
	}
	replacement, newToken := createViaAPI(t, s, "recovered home")
	host, err := NewHost(t.Context(), public.URL, newToken, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if c, _, err := host.handshake("/v1/host"); err == nil {
		c.CloseNow()
		t.Fatal("ordinary connection replaced a registration")
	}
	if got := s.registrationStatuses(previous.ID)[0]; got.Connected || got.UnregisteredAt != nil || got.RevokedAt != nil {
		t.Fatal("ordinary connection changed the existing registration", got)
	}
	if got := s.registrationStatuses(replacement.ID)[0]; got.ServerID != "" {
		t.Fatal("ordinary connection claimed a conflicting token", got)
	}
}

func TestReplacementRejectsOldInFlightRegistration(t *testing.T) {
	s := testBroker(t)
	public := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); public.Close() })
	_, token := createViaAPI(t, s, "home")
	id, _ := NewIdentity()
	if _, err := Register(t.Context(), public.URL, token, id, nil); err != nil {
		t.Fatal(err)
	}
	old, challenge := beginRegistration(t, public.URL, token)
	_, newToken := createViaAPI(t, s, "recovered home")
	if _, err := Register(t.Context(), public.URL, newToken, id, nil); err != nil {
		t.Fatal(err)
	}
	if finishRegistration(t.Context(), old, challenge, id) {
		t.Fatal("in-flight old credential replaced the recovered registration")
	}
}
