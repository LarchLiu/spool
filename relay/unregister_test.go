package relay

import (
	"crypto/ed25519"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnregisterDisconnectsOnlyOwnedServerAndPersists(t *testing.T) {
	s := testBrokerMode(t, true)
	public := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); public.Close() })
	first, token := createViaAPI(t, s, "home")
	_, otherToken := createViaAPI(t, s, "office")
	id, _ := NewIdentity()
	otherID, _ := NewIdentity()
	closed := make(chan struct{})
	a := startAdminTestHost(t, public, token, id, closed)
	b := startAdminTestHost(t, public, otherToken, otherID, make(chan struct{}))
	client, _ := a.client(t, a.pair)
	response, err := client.Get(Origin(id.ID()) + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.ReadFull(response.Body, make([]byte, 6)); err != nil {
		t.Fatal(err)
	}
	if err := Unregister(t.Context(), public.URL, token, otherID, nil); !errors.Is(err, ErrRegistrationRejected) {
		t.Fatal("stolen token unregistered a server", err)
	}
	if err := Unregister(t.Context(), public.URL, otherToken, id, nil); !errors.Is(err, ErrRegistrationRejected) {
		t.Fatal("wrong token unregistered a server", err)
	}
	if err := Unregister(t.Context(), public.URL, token, id, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("unregistered stream remained connected")
	}
	if got := s.registrationStatuses(first.ID)[0]; got.Connected || got.UnregisteredAt == nil || got.RevokedAt != nil {
		t.Fatal(got)
	}
	otherClient, _ := b.client(t, b.pair)
	if request(t, otherClient, b.pair, "/") != otherID.ID() {
		t.Fatal("other server interrupted")
	}
	if _, err := Register(t.Context(), public.URL, token, id, nil); !errors.Is(err, ErrRegistrationRejected) {
		t.Fatal("old token still active", err)
	}
	if err := Unregister(t.Context(), public.URL, token, id, nil); err != nil {
		t.Fatal("retry failed", err)
	}
	s.Close()
	restored, err := NewServer(s.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	second := httptest.NewServer(restored)
	defer second.Close()
	if err := Unregister(t.Context(), second.URL, token, id, nil); err != nil {
		t.Fatal("retry after restart failed", err)
	}
	credential, err := Register(t.Context(), second.URL, "", id, nil)
	if err != nil || credential == token {
		t.Fatal("self-unregistration prevented new enrollment", err)
	}
	if err := Unregister(t.Context(), second.URL, token, id, nil); err != nil {
		t.Fatal("old retry failed", err)
	}
	if _, err := Register(t.Context(), second.URL, credential, id, nil); err != nil {
		t.Fatal("old retry unregistered replacement", err)
	}
}

func TestUnregisterPreservesAdministrativeRevocation(t *testing.T) {
	s := testBrokerMode(t, true)
	public := httptest.NewServer(s)
	defer public.Close()
	id, _ := NewIdentity()
	token, err := Register(t.Context(), public.URL, "", id, nil)
	if err != nil {
		t.Fatal(err)
	}
	record := s.registrationStatuses("")[0]
	if err := s.RevokeRegistration(record.ID); err != nil {
		t.Fatal(err)
	}
	if err := Unregister(t.Context(), public.URL, token, id, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(t.Context(), public.URL, "", id, nil); !errors.Is(err, ErrRegistrationRejected) {
		t.Fatal("unregister bypassed admin revocation", err)
	}
}

func TestUnregisterRequiresActionProofAndDurableWrite(t *testing.T) {
	s := testBroker(t)
	public := httptest.NewServer(s)
	defer public.Close()
	record, token := createViaAPI(t, s, "home")
	id, _ := NewIdentity()
	if _, err := Register(t.Context(), public.URL, token, id, nil); err != nil {
		t.Fatal(err)
	}
	c, err := dialWebsocket(t.Context(), nil, public.URL, "/v1/unregister", token)
	if err != nil {
		t.Fatal(err)
	}
	challenge, _ := readControl(t.Context(), c)
	writeControl(t.Context(), c, controlMessage{Type: "unregister", PublicKey: id.PublicKey(), Signature: ed25519.Sign(id.PrivateKey(), proof(challenge.Challenge, token))})
	if reply, err := readControl(t.Context(), c); err == nil && reply.Type == "unregistered" {
		t.Fatal("registration proof authorized unregister")
	}
	c.CloseNow()
	file := filepath.Join(s.opts.DataDir, "registrations.json")
	backup := file + ".saved"
	if err := os.Rename(file, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Unregister(t.Context(), public.URL, token, id, nil); err == nil {
		t.Fatal("unregister reported success after persistence failure")
	}
	if got := s.registrationStatuses(record.ID)[0]; got.UnregisteredAt != nil {
		t.Fatal("in-memory state changed before durable write")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, file); err != nil {
		t.Fatal(err)
	}
	if err := Unregister(t.Context(), public.URL, token, id, nil); err != nil {
		t.Fatal(err)
	}
}
