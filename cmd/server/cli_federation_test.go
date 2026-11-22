package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"spool/internal/workspace"
)

func TestFederationCLITrustConfiguration(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	local := decode[workspace.FederationPeer](t, cliOK(t, a, "federation", "identity"))
	peer := decode[workspace.FederationPeer](t, cliOK(t, b, "federation", "identity"))
	if local.ID == "" || peer.ID == "" || local.ID == peer.ID || peer.PublicKey == "" {
		t.Fatal("expected distinct public server identities")
	}
	peer.Accept, peer.Transport, peer.Address = true, "https", "https://peer.example.test"
	config := filepath.Join(t.TempDir(), "peer.json")
	data, err := json.Marshal(peer)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	cliOK(t, a, "federation", "set", "--file", config)
	peers := decode[[]workspace.FederationPeer](t, cliOK(t, a, "federation", "list"))
	if len(peers) != 1 || peers[0].ID != peer.ID || !peers[0].Accept || peers[0].Address != peer.Address {
		t.Fatalf("unexpected saved peers: %+v", peers)
	}
}
