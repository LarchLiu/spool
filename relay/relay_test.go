package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type testNetwork struct {
	pair     Pairing
	host     *Host
	relay    *Server
	app      *http.Server
	lan      net.Listener
	hostDone chan struct{}
}

func setup(t *testing.T, handler http.Handler) *testNetwork {
	t.Helper()
	broker := testBroker(t)
	_, token, err := broker.CreateRegistration("test server")
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(broker)
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(context.Background(), public.URL, token, id, public.Client())
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 4)
	host.OnStatus = func(state string) {
		if state == "connected" {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	}
	cfg, _ := id.TLSConfig()
	app := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	lan, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	go app.Serve(lan)
	go app.Serve(tls.NewListener(host, cfg))
	done := make(chan struct{})
	go func() { host.Run(); host.Wait(); close(done) }()
	n := &testNetwork{pair: Pairing{Version: 1, RelayURL: public.URL, PublicKey: id.PublicKey(), AccessToken: id.AccessToken}, host: host, relay: broker, app: app, lan: lan, hostDone: done}
	t.Cleanup(func() {
		host.Close()
		app.Close()
		broker.Close()
		public.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("host did not stop")
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("host did not register")
	}
	return n
}
func (n *testNetwork) client(t *testing.T, p Pairing) (*http.Client, *Dialer) {
	t.Helper()
	d, err := NewDialer(context.Background(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialTLSContext: d.DialTLSContext, ResponseHeaderTimeout: 3 * time.Second}
	c := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	t.Cleanup(func() { transport.CloseIdleConnections(); d.Close() })
	return c, d
}
func request(t *testing.T, c *http.Client, p Pairing, path string) string {
	t.Helper()
	r, err := c.Get(Origin(p.ID()) + path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestConcurrentClientsStreamsAndBinary(t *testing.T) {
	var active atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/file", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			t.Error("missing end-to-end TLS")
		}
		io.Copy(w, io.LimitReader(r.Body, 4<<20))
	})
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: snapshot\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/api/desktop/view/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != OriginFromRequest(r) || r.Header.Get("Cookie") != "spool_session=test" {
			t.Error("lost origin/cookie")
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(1 << 20)
		for {
			kind, payload, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if c.Write(r.Context(), kind, payload) != nil {
				return
			}
		}
	})
	n := setup(t, mux)
	var tasks sync.WaitGroup
	for i := 0; i < 3; i++ {
		client, _ := n.client(t, n.pair)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", Origin(n.pair.ID())+"/api/events", nil)
		stream, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Body.Close()
		first := make([]byte, len("data: snapshot\n\n"))
		if _, err := io.ReadFull(stream.Body, first); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 3; j++ {
			tasks.Add(1)
			go func() {
				defer tasks.Done()
				payload := bytes.Repeat([]byte{0, 255, 1, 2, 3}, 100000)
				r, err := client.Post(Origin(n.pair.ID())+"/api/file", "application/octet-stream", bytes.NewReader(payload))
				if err != nil {
					t.Error(err)
					return
				}
				defer r.Body.Close()
				got, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(got, payload) {
					t.Error("concurrent upload corruption", err)
				}
			}()
		}
		tasks.Add(1)
		go func() {
			defer tasks.Done()
			header := http.Header{"Origin": {Origin(n.pair.ID())}, "Cookie": {"spool_session=test"}}
			c, _, err := websocket.Dial(ctx, strings.Replace(Origin(n.pair.ID()), "https:", "wss:", 1)+"/api/desktop/view/ws", &websocket.DialOptions{HTTPClient: client, HTTPHeader: header})
			if err != nil {
				t.Error(err)
				return
			}
			defer c.CloseNow()
			c.SetReadLimit(1 << 20)
			payload := bytes.Repeat([]byte{0, 0, 0, 1, 103, 255}, 100000)
			if err := c.Write(ctx, websocket.MessageBinary, payload); err != nil {
				t.Error(err)
				return
			}
			kind, got, err := c.Read(ctx)
			if err != nil || kind != websocket.MessageBinary || !bytes.Equal(got, payload) {
				t.Error("desktop binary corruption", err)
			}
		}()
	}
	tasks.Wait()
	if active.Load() != 3 {
		t.Fatalf("SSE streams lost during concurrent traffic: %d", active.Load())
	}
}
func OriginFromRequest(r *http.Request) string { return "https://" + r.Host }

func TestLANPreferenceFallbackAndUntrustedDiscovery(t *testing.T) {
	n := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("paired")) }))
	p := n.pair
	p.LAN = []string{n.lan.Addr().String()}
	c, d := n.client(t, p)
	if request(t, c, p, "/") != "paired" || d.Path() != "lan" {
		t.Fatal("did not prefer LAN")
	}
	n.lan.Close()
	d.NetworkChanged()
	if request(t, c, p, "/") != "paired" || d.Path() != "relay" {
		t.Fatal("did not fall back to relay")
	}
	var leaked atomic.Bool
	impostor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer impostor.Close()
	if err := d.SetLANAddresses([]string{impostor.Listener.Addr().String()}); err != nil {
		t.Fatal(err)
	}
	d.NetworkChanged()
	if request(t, c, p, "/") != "paired" || leaked.Load() || d.Path() != "relay" {
		t.Fatal("discovery bypassed identity verification")
	}
	if err := d.SetLANAddresses([]string{"8.8.8.8:443"}); err == nil {
		t.Fatal("accepted internet discovery hint")
	}
}

func TestWrongPairingAndNoMutationReplay(t *testing.T) {
	var calls atomic.Int32
	n := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(io.Discard, r.Body)
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Close()
	}))
	p := n.pair
	p.AccessToken = randomID()
	bad, _ := n.client(t, p)
	if _, err := bad.Get(Origin(p.ID()) + "/"); err == nil {
		t.Fatal("accepted wrong admission token")
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized request reached server")
	}
	c, _ := n.client(t, n.pair)
	if _, err := c.Post(Origin(n.pair.ID())+"/api/chats/chat/messages", "application/json", strings.NewReader(`{"text":"once"}`)); err == nil {
		t.Fatal("expected interrupted response")
	}
	if calls.Load() != 1 {
		t.Fatalf("mutation delivered %d times", calls.Load())
	}
}

func TestIdentityPairingAndRegistrationSecurity(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadIdentity(dir)
	if err != nil || id.ID() != again.ID() {
		t.Fatal("identity did not persist", err)
	}
	p := Pairing{Version: 1, RelayURL: "https://relay.example.com", PublicKey: id.PublicKey(), AccessToken: id.AccessToken, LAN: []string{"192.168.1.20:8789"}}
	code, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParsePairing(code)
	if err != nil || decoded.ID() != p.ID() {
		t.Fatal("pairing did not round trip", err)
	}
	for _, address := range []string{"http://relay.example.com", "https://relay.example.com/path", "https://user:pass@relay.example.com", "https://relay.example.com#"} {
		if _, err := ParseRelayURL(address); err == nil {
			t.Error("accepted unsafe relay", address)
		}
	}
	s := testBroker(t)
	_, token, err := s.CreateRegistration("identity proof")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s)
	defer server.Close()
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c, err := dialWebsocket(ctx, nil, server.URL, "/v1/host", "wrong"); err == nil {
		c.CloseNow()
		t.Fatal("accepted wrong registration token")
	}
	c, err := dialWebsocket(ctx, nil, server.URL, "/v1/host", token)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	challenge, err := readControl(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeControl(ctx, c, controlMessage{Type: "register", PublicKey: id.PublicKey(), AccessToken: id.AccessToken, Signature: make([]byte, 64)}); err != nil {
		t.Fatal(err)
	}
	if challenge.Type != "challenge" {
		t.Fatal("missing challenge")
	}
	if _, err := readControl(ctx, c); err == nil {
		t.Fatal("accepted forged identity")
	}
	if got := s.registrationStatuses(""); len(got) != 1 || got[0].ServerID != "" {
		t.Fatal("forged proof claimed the unused token")
	}
}

func TestSlowConsumerIsolationAndControlReconnect(t *testing.T) {
	blocked := make(chan struct{})
	n := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/slow" {
			w.Write([]byte("ready"))
			return
		}
		defer close(blocked)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		chunk := make([]byte, 64<<10)
		for {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			select {
			case <-r.Context().Done():
				return
			default:
			}
		}
	}))
	slow, slowDialer := n.client(t, n.pair)
	response, err := slow.Get(Origin(n.pair.ID()) + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	fast, d := n.client(t, n.pair)
	if request(t, fast, n.pair, "/fast") != "ready" {
		t.Fatal("slow consumer blocked another client")
	}
	n.relay.mu.Lock()
	registered := n.relay.hosts[n.pair.ID()]
	registered.control.CloseNow()
	n.relay.mu.Unlock()
	slowDialer.NetworkChanged()
	d.NetworkChanged()
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("control disconnect leaked active stream")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		n.relay.mu.Lock()
		current := n.relay.hosts[n.pair.ID()]
		connected := current != nil && current != registered
		n.relay.mu.Unlock()
		if connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("host did not reconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if request(t, fast, n.pair, "/fast") != "ready" {
		t.Fatal("new requests failed after control reconnect")
	}
}

func TestRelayConnectionLimitAndRelease(t *testing.T) {
	entered := make(chan struct{}, 4)
	n := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	n.relay.mu.Lock()
	n.relay.opts.MaxConnectionsPerHost = 1
	n.relay.mu.Unlock()
	c, d := n.client(t, n.pair)
	r, err := c.Get(Origin(n.pair.ID()) + "/held")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	<-entered
	second, _ := n.client(t, n.pair)
	if response, err := second.Get(Origin(n.pair.ID()) + "/second"); err == nil {
		response.Body.Close()
		t.Fatal("quota did not reject another connection")
	}
	r.Body.Close()
	d.NetworkChanged()
	deadline := time.Now().Add(3 * time.Second)
	for {
		n.relay.mu.Lock()
		remaining := n.relay.connections
		n.relay.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("closed connection retained its quota")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, err = second.Get(Origin(n.pair.ID()) + "/replacement")
	if err != nil {
		t.Fatal("released quota not reusable", err)
	}
	r.Body.Close()
}
