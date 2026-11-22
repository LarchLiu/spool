package main

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"spool/internal/workspace"
)

func addFederation(root *command, out io.Writer) {
	c := root.add("federation", "Manage server trust locally", "Stop Spool before editing this data directory. Verify the peer's ID and public key with its administrator before saving.")
	identity := c.add("identity", "Print this server's public identity", "The private signing key is not printed.")
	identity.run = func(_ context.Context, s *workspace.Service) error {
		p, err := s.Store.FederationIdentity()
		if err != nil {
			return err
		}
		return writeJSON(out, map[string]string{"id": p.ID, "name": p.Name, "public_key": p.PublicKey})
	}
	route := c.add("relay-route", "Export a private Federation Relay route", "Contains a tunnel admission credential; share it only with the paired server administrator. It is not a device invitation.")
	route.run = func(_ context.Context, s *workspace.Service) error {
		code, err := s.Store.FederationRelayRoute()
		if err != nil {
			return err
		}
		return writeJSON(out, map[string]string{"relay_code": code})
	}
	list := c.add("list", "List saved server trust relationships", "Private routing credentials are not printed.")
	list.run = func(_ context.Context, s *workspace.Service) error {
		peers, err := s.Store.FederationPeers()
		if err != nil {
			return err
		}
		for i := range peers {
			peers[i].RelayCode = ""
		}
		return writeJSON(out, peers)
	}
	set := c.add("set", "Save or replace a trust relationship from JSON", "Use the peer's public identity and specify routes, accepted login direction and default project membership. Omitted fields reset to their defaults.")
	file := set.flags.String("file", "", "Peer configuration JSON file (required)")
	set.validate = func() error { return required("file", *file) }
	set.run = func(_ context.Context, s *workspace.Service) error {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		const maxConfig = 256 << 10
		data, err := io.ReadAll(io.LimitReader(f, maxConfig+1))
		if err != nil {
			return err
		}
		if len(data) > maxConfig {
			return usage("federation configuration is too large")
		}
		var p workspace.FederationPeer
		if err := json.Unmarshal(data, &p); err != nil {
			return usage("invalid federation configuration: %v", err)
		}
		if err := s.Store.SaveFederationPeer(p); err != nil {
			return err
		}
		return writeJSON(out, map[string]string{"saved": p.ID})
	}
}
