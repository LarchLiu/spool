package relay

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

func waitForConnections(t *testing.T, broker *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		broker.mu.Lock()
		got := broker.connections
		broker.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay holds %d data connections, want %d", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// webRequest sends one gateway request as a client behind the trusted proxy.
func webRequest(t *testing.T, client *http.Client, address, clientIP string) string {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, address+"address", nil)
	if clientIP != "" {
		request.Header.Set("X-Forwarded-For", clientIP)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal("gateway request failed", response.StatusCode, err)
	}
	return string(data)
}

func trustLoopbackProxy(broker *Server) {
	broker.mu.Lock()
	broker.trustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	broker.mu.Unlock()
}

func echoAddress(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.RemoteAddr) }

func TestWebRequestsReuseTunnelPerClientAddress(t *testing.T) {
	broker, _, public, address := webFixture(t, true, http.HandlerFunc(echoAddress))
	trustLoopbackProxy(broker)
	for range 5 {
		if got := webRequest(t, public.Client(), address, "203.0.113.1"); got != "relay-client/203.0.113.1" {
			t.Fatal("home saw another client address:", got)
		}
	}
	// Without reuse no tunnel remains; a tunnel per request would leave five.
	waitForConnections(t, broker, 1)

	// The login limiter keys on the tunnel's address, so another client must
	// not borrow the first client's idle tunnel.
	if got := webRequest(t, public.Client(), address, "203.0.113.2"); got != "relay-client/203.0.113.2" {
		t.Fatal("request reused another client's tunnel:", got)
	}
	waitForConnections(t, broker, 2)
	if got := webRequest(t, public.Client(), address, "203.0.113.1"); got != "relay-client/203.0.113.1" {
		t.Fatal("home saw another client address:", got)
	}
	waitForConnections(t, broker, 2)
}

func TestIdleWebTunnelsYieldToNewConnections(t *testing.T) {
	broker, host, public, address := webFixture(t, true, http.HandlerFunc(echoAddress))
	trustLoopbackProxy(broker)
	broker.mu.Lock()
	broker.opts.MaxConnectionsPerHost = 1
	broker.mu.Unlock()
	webRequest(t, public.Client(), address, "203.0.113.1")
	waitForConnections(t, broker, 1)

	// The only slot is held by an idle tunnel, which must not refuse real work.
	if got := webRequest(t, public.Client(), address, "203.0.113.2"); got != "relay-client/203.0.113.2" {
		t.Fatal("idle tunnel blocked another Web client:", got)
	}
	pairing := Pairing{Version: 2, RelayURL: public.URL, PublicKey: host.Identity.PublicKey(), AccessToken: host.Identity.AccessToken}
	dialer, err := NewDialer(context.Background(), pairing, public.Client())
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialTLSContext: dialer.DialTLSContext}
	t.Cleanup(func() { transport.CloseIdleConnections(); dialer.Close() })
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Get(Origin(pairing.ID()) + "/native")
	if err != nil {
		t.Fatal("idle Web tunnel blocked a native client", err)
	}
	response.Body.Close()
}

func TestWebTunnelsEndWithHostConnection(t *testing.T) {
	broker, _, public, address := webFixture(t, true, http.HandlerFunc(echoAddress))
	webRequest(t, public.Client(), address, "")
	waitForConnections(t, broker, 1)
	if err := broker.RevokeRegistration(broker.registrationStatuses("")[0].ID); err != nil {
		t.Fatal(err)
	}
	waitForConnections(t, broker, 0)
	response, err := public.Client().Get(address + "address")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("revoked host still served through a reused tunnel:", response.StatusCode)
	}
}
