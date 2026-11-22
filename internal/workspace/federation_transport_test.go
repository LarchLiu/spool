package workspace

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/relay"
)

func TestFederationControlBehindTrustedHTTPSProxy(t *testing.T) {
	store, remote := testStore(t), testStore(t)
	app := NewApp(store, nil, "", "")
	app.settings.WebTrustedProxies = "127.0.0.1/32,::1/128"
	backend := httptest.NewServer(ordinaryWebHandler(app))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewUnstartedServer(&httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(target)
		p.SetXForwarded()
	}})
	app.settings.PublicURL = "https://" + front.Listener.Addr().String()
	front.StartTLS()
	defer front.Close()
	peer := federationPairForTest(t, store, remote, "https://partner.example.test", false, nil, nil)
	self, err := store.federationKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := remote.federationKey()
	if err != nil {
		t.Fatal(err)
	}
	m := federationMessage{Version: federationVersion, From: peer.ID, To: self.ID(), Action: federationCheck, Nonce: randomToken(), SentAt: time.Now().UnixMilli(), Value: randomToken()}
	body, err := m.encode(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = federationHTTP(t.Context(), front.URL, body, front.Client().Transport.(*http.Transport).Clone()); err != nil {
		t.Fatalf("trusted TLS proxy rejected the paired request: %v", err)
	}
	if _, err = federationHTTP(t.Context(), backend.URL, body, federationHTTPTransport()); err == nil {
		t.Fatal("the same request passed the plaintext backend without a proxy")
	}
}

func TestFederationRelayUsesControlOnlyAdmission(t *testing.T) {
	aStore, bStore, otherStore := testStore(t), testStore(t), testStore(t)
	a, b := NewApp(aStore, nil, "", ""), NewApp(bStore, nil, "", "")
	local, err := aStore.federationKey()
	if err != nil {
		t.Fatal(err)
	}
	alice := testUser(t, aStore, "alice")
	bPeer, err := bStore.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	bPeer.Users = []string{alice.ID}
	if err := aStore.SaveFederationPeer(bPeer); err != nil {
		t.Fatal(err)
	}
	broker, token := testRelayBroker(t)
	public := httptest.NewServer(broker)
	defer public.Close()
	host, err := relay.NewHost(t.Context(), public.URL, token, local, nil)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 1)
	host.OnStatus = func(state string) {
		if state == "connected" {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	}
	devices, err := relay.OpenDevices(filepath.Join(aStore.Root(), "relay"))
	if err != nil {
		host.Close()
		t.Fatal(err)
	}
	cfg, err := local.TLSConfig()
	if err != nil {
		host.Close()
		t.Fatal(err)
	}
	noWeb := func() bool { return false }
	admission := aStore.federationRelayAdmission()
	server := &http.Server{Handler: devices.HandlerWithFederation(a, local.ID(), noWeb, admission), ReadHeaderTimeout: time.Second}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(devices.ListenerWithFederation(host, cfg, local.ID(), noWeb, true, admission))
	}()
	hostDone := make(chan struct{})
	go func() { defer close(hostDone); host.Run(); host.Wait() }()
	defer func() {
		host.Close()
		server.Close()
		select {
		case <-served:
		case <-time.After(5 * time.Second):
			t.Error("relay listener did not stop")
		}
		select {
		case <-hostDone:
		case <-time.After(5 * time.Second):
			t.Error("relay host did not stop")
		}
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not start")
	}
	p := relay.Pairing{Version: 2, RelayURL: public.URL, PublicKey: local.PublicKey(), AccessToken: local.AccessToken}
	aPeer, err := aStore.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	aPeer.Transport, aPeer.Accept = "relay", true
	aPeer.RelayCode, err = p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := bStore.SaveFederationPeer(aPeer); err != nil {
		t.Fatal(err)
	}
	cookie, _ := loginRequest(t, a, alice.Username)
	identity, err := aStore.authenticate(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := aStore.federationIssue(identity, bPeer.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.federationCall(t.Context(), aPeer, federationRedeem, secret)
	if err != nil || result.Subject == nil || result.Subject.ID != alice.ID {
		t.Fatalf("paired servers cannot redeem through relay with Web and device admission off: %v", err)
	}
	for _, tc := range []struct {
		name  string
		store *Store
		path  string
		want  int
	}{
		{"paired server on business path", bStore, "/api/me", http.StatusNotFound},
		{"unpaired server on control path", otherStore, FederationControlPath, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, err := tc.store.federationKey()
			if err != nil {
				t.Fatal(err)
			}
			dial, err := relay.NewDialer(t.Context(), p, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dial.Close()
			dial.TLSConfig, err = relay.FederationTLSConfig(p, key)
			if err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{DialTLSContext: dial.DialTLSContext}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, relay.Origin(local.ID())+tc.path, strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			r.AddCookie(cookie)
			res, err := client.Do(r)
			if err != nil && tc.want == 0 {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("status=%d, want=%d", res.StatusCode, tc.want)
			}
		})
	}
}

func TestFederationDialTailnetUsesConfiguredPeerAndCancels(t *testing.T) {
	a := NewApp(testStore(t), nil, "", "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	called := make(chan string, 1)
	a.federationTailnet.Store(&tailnetRuntime{ctx: ctx, dial: func(call context.Context, network, address string) (net.Conn, error) {
		called <- network + " " + address
		<-call.Done()
		return nil, call.Err()
	}})
	done := make(chan error, 1)
	go func() {
		_, err := a.dialFederation(t.Context(), FederationPeer{Transport: "tailscale", Address: "https://node.example.ts.net:8443"}, nil)
		done <- err
	}()
	select {
	case address := <-called:
		if address != "tcp node.example.ts.net:8443" {
			t.Fatalf("unexpected destination %q", address)
		}
	case <-time.After(time.Second):
		t.Fatal("Tailscale dialer not called")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("shutdown did not cancel the control request")
		}
	case <-time.After(time.Second):
		t.Fatal("control request continued after closing Tailscale")
	}
}

func TestFederationDialTailnetNeverFallsBack(t *testing.T) {
	a := NewApp(testStore(t), nil, "", "")
	local := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("Tailscale request went to an ordinary network listener")
		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()
	peer := FederationPeer{Transport: "tailscale", Address: local.URL}
	if _, err := a.dialFederation(t.Context(), peer, nil); err == nil {
		t.Fatal("no running tailnet, but request succeeded")
	}
}
