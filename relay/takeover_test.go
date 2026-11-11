package relay

import (
	"context"
	"crypto/ed25519"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// abandonedControl completes a host handshake and then never reads, so relay
// pings stay unanswered like a socket whose peer moved to another network.
func abandonedControl(t *testing.T, public *httptest.Server, token string, id *Identity) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := dialWebsocket(ctx, public.Client(), public.URL, "/v1/host", token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	challenge, err := readControl(ctx, c)
	if err != nil || challenge.Type != "challenge" {
		t.Fatal("missing challenge", err)
	}
	signature := ed25519.Sign(id.PrivateKey(), hostRegistrationProof(challenge.Challenge, id.AccessToken, "", "", 0))
	if err := writeControl(ctx, c, controlMessage{Type: "register", PublicKey: id.PublicKey(), AccessToken: id.AccessToken, Signature: signature}); err != nil {
		t.Fatal(err)
	}
	if ready, err := readControl(ctx, c); err != nil || ready.Type != "ready" {
		t.Fatal("abandoned control did not register", err)
	}
	return c
}

func connectedHost(s *Server, id string) *registeredHost {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hosts[id]
}

func TestUnreachableControlConnectionIsReplaced(t *testing.T) {
	broker := testBroker(t)
	broker.takeoverProbe = 200 * time.Millisecond
	// The replacement must not count as a second host.
	broker.opts.MaxHosts = 1
	public := httptest.NewServer(broker)
	t.Cleanup(func() { broker.Close(); public.Close() })
	record, token := createViaAPI(t, broker, "home")
	id, _ := NewIdentity()
	abandonedControl(t, public, token, id)
	previous := connectedHost(broker, id.ID())
	if previous == nil {
		t.Fatal("abandoned control was not connected")
	}

	started := time.Now()
	network := startAdminTestHost(t, public, token, id, make(chan struct{}))
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatal("takeover waited for the heartbeat", elapsed)
	}
	if current := connectedHost(broker, id.ID()); current == nil || current == previous || previous.ctx.Err() == nil {
		t.Fatal("unreachable control connection was not retired")
	}
	if status := broker.registrationStatuses(record.ID)[0]; !status.Connected {
		t.Fatal("replacement is not connected")
	}
	client, _ := network.client(t, network.pair)
	response, err := client.Get(Origin(id.ID()))
	if err != nil {
		t.Fatal("replacement does not serve traffic", err)
	}
	response.Body.Close()

	// Capacity still applies to a different identity.
	_, otherToken := createViaAPI(t, broker, "other")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if c, err := dialWebsocket(ctx, public.Client(), public.URL, "/v1/host", otherToken); err == nil {
		c.CloseNow()
		t.Fatal("relay exceeded its host limit")
	}
}

func TestLiveControlConnectionIsNotReplaced(t *testing.T) {
	broker := testBroker(t)
	broker.takeoverProbe = 200 * time.Millisecond
	public := httptest.NewServer(broker)
	t.Cleanup(func() { broker.Close(); public.Close() })
	_, token := createViaAPI(t, broker, "home")
	id, _ := NewIdentity()
	first := startAdminTestHost(t, public, token, id, make(chan struct{}))
	incumbent := connectedHost(broker, id.ID())

	duplicate, err := NewHost(context.Background(), public.URL, token, id, public.Client())
	if err != nil {
		t.Fatal(err)
	}
	states := make(chan string, 8)
	duplicate.OnStatus = func(state string) {
		select {
		case states <- state:
		default:
		}
	}
	done := make(chan struct{})
	go func() { duplicate.Run(); duplicate.Wait(); close(done) }()
	t.Cleanup(func() {
		duplicate.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("duplicate host did not stop")
		}
	})
	select {
	case state := <-states:
		if state != "reconnecting" {
			t.Fatal("duplicate instance was admitted:", state)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("duplicate instance was neither admitted nor refused")
	}
	if current := connectedHost(broker, id.ID()); current != incumbent || incumbent.ctx.Err() != nil {
		t.Fatal("a live control connection was replaced")
	}
	client, _ := first.client(t, first.pair)
	response, err := client.Get(Origin(id.ID()))
	if err != nil {
		t.Fatal("incumbent no longer serves traffic", err)
	}
	response.Body.Close()
}
