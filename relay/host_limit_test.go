package relay

import (
	"net/http"
	"testing"
	"time"
)

func TestHostConnectionLimitFollowsRelayWithinLocalCap(t *testing.T) {
	for announced, want := range map[int]int{0: defaultHostConnections, -1: defaultHostConnections, 1: 1, 300: 300, maxHostConnections + 1: maxHostConnections} {
		if got := hostConnectionLimit(announced); got != want {
			t.Errorf("announced %d: limit %d, want %d", announced, got, want)
		}
	}
	broker := testBroker(t)
	broker.opts.MaxConnectionsPerHost = 300
	n := setupWith(t, broker, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	n.host.mu.Lock()
	limit := n.host.limit
	n.host.mu.Unlock()
	if limit != 300 {
		t.Fatal("host ignored the relay's announced limit:", limit)
	}
}

// A home server at its own limit must refuse immediately. Without a refusal
// the relay holds the client until its join timeout.
func TestHostAtItsLimitRejectsWithoutWaitingForTimeout(t *testing.T) {
	entered := make(chan struct{}, 1)
	n := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	// An older or differently configured relay may allow more than the host.
	n.host.mu.Lock()
	n.host.limit = 1
	n.host.mu.Unlock()
	held, _ := n.client(t, n.pair)
	response, err := held.Get(Origin(n.pair.ID()) + "/held")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-entered

	refused, _ := n.client(t, n.pair)
	started := time.Now()
	if response, err := refused.Get(Origin(n.pair.ID()) + "/refused"); err == nil {
		response.Body.Close()
		t.Fatal("host exceeded its connection limit")
	}
	if elapsed := time.Since(started); elapsed > handshakeTimeout/2 {
		t.Fatal("refusal waited for the join timeout:", elapsed)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		n.relay.mu.Lock()
		remaining := n.relay.connections
		n.relay.mu.Unlock()
		n.host.mu.Lock()
		reserved := n.host.reserved
		n.host.mu.Unlock()
		if remaining == 1 && reserved == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("refused connection kept a reservation: relay %d, host %d", remaining, reserved)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// More simultaneous clients than the control queue holds must all connect.
func TestConnectionBurstIsQueuedNotDropped(t *testing.T) {
	n := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ready")) }))
	const clients = 48
	results := make(chan error, clients)
	for range clients {
		client, _ := n.client(t, n.pair)
		go func() {
			response, err := client.Get(Origin(n.pair.ID()) + "/burst")
			if err == nil {
				response.Body.Close()
			}
			results <- err
		}()
	}
	for range clients {
		if err := <-results; err != nil {
			t.Fatal("a connection in the burst was dropped:", err)
		}
	}
}
