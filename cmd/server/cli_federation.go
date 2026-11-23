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
	addFederationTrust(c, out)
	set := c.add("set", "Configure an approved peer from JSON", "Use a peer from federation list. Specify routes, accepted login direction and default project membership. Omitted fields reset to their defaults.")
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
		if err := s.Store.UpdateFederationPeer(p); err != nil {
			return err
		}
		return writeJSON(out, map[string]string{"saved": p.ID})
	}
}

func addOnlineFederationTrust(c *command, out io.Writer) {
	inbox := c.add("inbox", "Enable or disable online trust applications", "New requests require explicit administrator review. Restart the server after using this local command.")
	enable := inbox.flags.Bool("enable", false, "Enable the trust inbox (use --enable=false to close it)")
	inbox.validate = func() error {
		if !changed(inbox, "enable") {
			return usage("specify --enable=true or --enable=false")
		}
		return nil
	}
	inbox.run = func(_ context.Context, s *workspace.Service) error {
		if err := s.Store.SetFederationTrustInbox(*enable); err != nil {
			return err
		}
		return writeJSON(out, map[string]bool{"inbox_open": *enable})
	}
	for _, name := range []string{"probe", "send"} {
		cmd := c.add(name, name+" using the destination’s server trust endpoint", "Use probe to verify the destination's identity, then pass that ID to send. Provide Relay routes via --route-file, not arguments.")
		transport := cmd.flags.String("transport", "https", "Destination transport: https, tailscale, or relay")
		address := cmd.flags.String("address", "", "HTTPS origin for Public HTTPS or Tailscale")
		file := cmd.flags.String("route-file", "", "Private file containing the Relay trust route")
		var expected string
		if name == "send" {
			cmd.flags.StringVar(&expected, "expect", "", "Verified destination server ID (required)")
			cmd.validate = func() error { return required("expect", expected) }
		}
		cmd.run = func(ctx context.Context, s *workspace.Service) error {
			value := *address
			if *transport == "relay" {
				if value != "" || *file == "" {
					return usage("relay requires --route-file and no --address")
				}
				f, err := os.Open(*file)
				if err != nil {
					return err
				}
				defer f.Close()
				data, err := io.ReadAll(io.LimitReader(f, 8193))
				if err != nil {
					return err
				}
				if len(data) > 8192 {
					return usage("Relay trust route is too large")
				}
				value = string(data)
			} else if *file != "" {
				return usage("--route-file applies only to relay")
			}
			if name == "probe" {
				preview, err := s.PreviewFederationTrust(ctx, *transport, value)
				if err != nil {
					return err
				}
				return writeJSON(out, preview)
			}
			return federationTrustStatusAfter(out, s, s.SubmitFederationTrust(ctx, *transport, value, expected))
		}
	}
	check := c.add("check", "Fetch the decision over the saved connection route", "Use 'federation status' to find the outgoing request ID.")
	id := check.flags.String("id", "", "Outgoing request ID (required)")
	check.validate = func() error { return required("id", *id) }
	check.run = func(ctx context.Context, s *workspace.Service) error {
		return federationTrustStatusAfter(out, s, s.CheckFederationTrust(ctx, *id))
	}
}

func federationTrustStatusAfter(out io.Writer, s *workspace.Service, err error) error {
	if err != nil {
		return err
	}
	v, err := s.Store.FederationTrustSnapshot()
	if err != nil {
		return err
	}
	return writeJSON(out, v)
}

func addFederationTrust(c *command, out io.Writer) {
	addOnlineFederationTrust(c, out)
	status := c.add("status", "List server trust requests and approved directions", "Use check to fetch the decision for a submitted request.")
	status.run = func(_ context.Context, s *workspace.Service) error {
		v, err := s.Store.FederationTrustSnapshot()
		if err != nil {
			return err
		}
		return writeJSON(out, v)
	}
	for _, name := range []string{"approve", "reject"} {
		cmd := c.add(name, name+" an incoming trust request", "The requester retrieves the decision with check. Approval still requires the requester to allow specific users or all local users.")
		id := cmd.flags.String("id", "", "Incoming request ID (required)")
		var policyFile string
		if name == "approve" {
			cmd.flags.StringVar(&policyFile, "policy", "", "Optional JSON with default projects and a return route to the requester")
		}
		cmd.validate = func() error { return required("id", *id) }
		cmd.run = func(_ context.Context, s *workspace.Service) error {
			var policy workspace.FederationTrustApproval
			if policyFile != "" {
				f, err := os.Open(policyFile)
				if err != nil {
					return err
				}
				defer f.Close()
				const maxPolicy = 256 << 10
				data, err := io.ReadAll(io.LimitReader(f, maxPolicy+1))
				if err != nil {
					return err
				}
				if len(data) > maxPolicy || json.Unmarshal(data, &policy) != nil {
					return usage("invalid federation approval policy")
				}
			}
			return federationTrustStatusAfter(out, s, s.Store.DecideFederationTrust(*id, name == "approve", policy))
		}
	}
}
