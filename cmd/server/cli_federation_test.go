package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/internal/workspace"
	"spool/relay"
)

func TestFederationCLIOnlineTrust(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	local := decode[workspace.FederationPeer](t, cliOK(t, a, "federation", "identity"))
	peer := decode[workspace.FederationPeer](t, cliOK(t, b, "federation", "identity"))
	if local.ID == "" || peer.ID == "" || local.ID == peer.ID || peer.PublicKey == "" {
		t.Fatal("expected distinct public server identities")
	}
	local.Accept, local.Transport, local.Address = true, "https", "https://source.example.test"
	config := filepath.Join(t.TempDir(), "peer.json")
	data, err := json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, b, "federation", "set", "--file", config); err == nil {
		t.Fatal("an unapproved peer could be configured")
	}

	cliOK(t, b, "federation", "inbox", "--enable=true")
	routeFile := federationCLIRelayTarget(t, b)
	preview := decode[workspace.FederationTrustPreview](t, cliOK(t, a, "federation", "probe", "--transport", "relay", "--route-file", routeFile))
	if preview.ID != peer.ID || !preview.Accepting {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	pending := decode[workspace.FederationTrustSnapshot](t, cliOK(t, a, "federation", "send", "--transport", "relay", "--route-file", routeFile, "--expect", preview.ID))
	if len(pending.Peers) != 0 || len(pending.Requests) != 1 || pending.Requests[0].Status != "pending" {
		t.Fatal("sending a request did not leave A waiting for approval")
	}
	requestID := pending.Requests[0].ID
	decision := cliOK(t, b, "federation", "approve", "--id", requestID)
	if strings.Contains(decision, "spool-trust1.") {
		t.Fatal("approval printed an internal signed message")
	}
	in := decode[workspace.FederationTrustSnapshot](t, decision)
	if len(in.Requests) != 1 || in.Requests[0].Status != "approved" || len(in.Peers) != 1 || !in.Peers[0].Incoming {
		t.Fatalf("unexpected decision: %+v", in)
	}
	confirmed := decode[workspace.FederationTrustSnapshot](t, cliOK(t, a, "federation", "check", "--id", requestID))
	if len(confirmed.Requests) != 1 || confirmed.Requests[0].Status != "approved" || len(confirmed.Peers) != 1 || !confirmed.Peers[0].Outgoing || confirmed.Peers[0].ID != peer.ID {
		t.Fatalf("unexpected confirmation: %+v", confirmed)
	}
	cliOK(t, b, "federation", "set", "--file", config)
	peers := decode[[]workspace.FederationPeer](t, cliOK(t, b, "federation", "list"))
	if len(peers) != 1 || peers[0].ID != local.ID || !peers[0].Accept || peers[0].Address != local.Address {
		t.Fatalf("unexpected saved peers: %+v", peers)
	}
	revoked := decode[workspace.FederationTrustSnapshot](t, cliOK(t, b, "federation", "revoke", "--peer", local.ID))
	if len(revoked.Peers) != 1 || revoked.Peers[0].Incoming || revoked.Peers[0].Accept || !revoked.Peers[0].Revoked {
		t.Fatalf("CLI did not revoke incoming authorization: %+v", revoked.Peers)
	}
	if _, err := invoke(t, b, "federation", "set", "--file", config); err == nil {
		t.Fatal("CLI policy configuration restored revoked approval")
	}
	revoked = decode[workspace.FederationTrustSnapshot](t, cliOK(t, a, "federation", "revoke", "--peer", peer.ID))
	if len(revoked.Peers) != 1 || revoked.Peers[0].Outgoing || !revoked.Peers[0].Revoked {
		t.Fatalf("CLI source could not revoke its trust: %+v", revoked.Peers)
	}
}

func federationCLIRelayTarget(t *testing.T, root string) string {
	t.Helper()
	store, err := workspace.OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := workspace.NewApp(store, nil, "", "")
	broker, err := relay.NewServer(relay.ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	public := httptest.NewServer(broker)
	t.Cleanup(public.Close)
	_, registration, err := broker.CreateRegistration("federation CLI test")
	if err != nil {
		t.Fatal(err)
	}
	id, err := relay.LoadIdentity(filepath.Join(root, "relay"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := id.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	devices, err := relay.OpenDevices(filepath.Join(root, "relay"))
	if err != nil {
		t.Fatal(err)
	}
	host, err := relay.NewHost(t.Context(), public.URL, registration, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	connected := make(chan struct{}, 1)
	host.OnStatus = func(state string) {
		if state == "connected" {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	}
	noWeb := func() bool { return false }
	admission := relay.FederationAdmission{TrustPath: workspace.FederationTrustPath}
	server := &http.Server{Handler: devices.HandlerWithFederation(app, id.ID(), noWeb, admission), ReadHeaderTimeout: time.Second}
	served, hosted := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(devices.ListenerWithFederation(host, cfg, id.ID(), noWeb, true, admission))
	}()
	go func() { defer close(hosted); host.Run(); host.Wait() }()
	t.Cleanup(func() {
		host.Close()
		_ = server.Close()
		<-hosted
		<-served
	})
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("Relay did not connect")
	}
	code, err := (relay.Pairing{Version: 2, RelayURL: public.URL, PublicKey: id.PublicKey(), AccessToken: id.AccessToken}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "relay-route")
	if err := os.WriteFile(path, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFederationCLIDoesNotOfferManualHandoff(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"request", "receive", "confirm", "export"} {
		_, err := invoke(t, root, "federation", command, "--help")
		var invalid *usageError
		if !errors.As(err, &invalid) {
			t.Fatalf("manual handoff command %q was not rejected: %v", command, err)
		}
	}
}
