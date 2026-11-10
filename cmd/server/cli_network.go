package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"spool/internal/workspace"
)

func addRelay(root *command, out io.Writer) {
	c := root.add("relay", "Manage relay enrollment and Android pairing locally", "All commands require other instances using this data directory to be stopped.\nConfigure relay-url with 'config set', register, then enable with 'config set --relay=true'.\nTokens are read from SPOOL_RELAY_REGISTRATION_TOKEN and never printed.")
	status := c.add("status", "Inspect saved relay enrollment (no live connection)", "This does not start the service or probe connectivity.")
	status.run = func(_ context.Context, s *workspace.Service) error { return writeJSON(out, s.Status().Relay) }
	register := c.add("register", "Register with the configured relay", "SPOOL_RELAY_REGISTRATION_TOKEN is optional when the relay permits automatic registration.\nSuccessful enrollment saves a private credential. This does not enable the relay listener.")
	name := register.flags.String("name", "", "Optional server display name (at most 128 bytes)")
	register.run = func(ctx context.Context, s *workspace.Service) error {
		v, err := s.Store.Settings()
		if err != nil {
			return err
		}
		if err := s.RegisterRelayNamed(ctx, v.RelayURL, os.Getenv("SPOOL_RELAY_REGISTRATION_TOKEN"), *name); err != nil {
			return err
		}
		return writeJSON(out, s.Status().Relay)
	}
	unregister := c.add("unregister", "Unregister the configured relay and clear its credential", "Requires a remote acknowledgement; failure preserves the credential for retry.\nThe relay is disabled after successful unregistration.")
	unregister.run = func(ctx context.Context, s *workspace.Service) error {
		v, err := s.Store.Settings()
		if err != nil {
			return err
		}
		if err := s.UnregisterRelay(ctx, v.RelayURL); err != nil {
			return err
		}
		return writeJSON(out, s.Status().Relay)
	}
	devices := c.add("devices", "List paired device fingerprints", "Prints paired and pending device metadata; no service listeners are started.")
	devices.run = func(_ context.Context, s *workspace.Service) error {
		v, err := s.RelayDevices()
		if err != nil {
			return err
		}
		return writeJSON(out, v)
	}
	revoke := c.add("revoke", "Revoke a paired Android device", "Use the full fingerprint from 'relay devices'.")
	id := revoke.flags.String("device", "", "Device fingerprint (required)")
	revoke.validate = func() error { return required("device", *id) }
	revoke.run = func(_ context.Context, s *workspace.Service) error {
		if err := s.RevokeRelayDevice(*id); err != nil {
			return err
		}
		return writeJSON(out, map[string]string{"revoked": *id})
	}
	pair := c.add("pair", "Run the service and approve one Android pairing interactively", "Relay must already be registered and enabled. Prints a five-minute invitation.\nCompare the device fingerprint with the phone, then type yes locally.\nThe service stays running after pairing until SIGINT/SIGTERM.")
	pair.run = func(ctx context.Context, s *workspace.Service) error {
		v, err := s.Store.Settings()
		if err != nil {
			return err
		}
		if !v.RelayEnabled {
			return fmt.Errorf("enable relay first with 'config set --relay=true'")
		}
		if err := s.Start(); err != nil {
			return err
		}
		if err := approveRelayPhoneIO(ctx, s, os.Stdin, out); err != nil {
			return err
		}
		fmt.Fprintln(out, "Phone paired. Service remains running until interrupted.")
		<-ctx.Done()
		return nil
	}
}

func addTailscale(root *command, out io.Writer) {
	c := root.add("tailscale", "Enroll the embedded Tailscale node without a GUI", "Configure hostname/HTTPS port using 'config set --tsnet=true ...'.\nNo system Tailscale daemon is required. Run as the service OS user while Spool is stopped.")
	login := c.add("login", "Run the service and print a browser login URL", "Enables embedded Tailscale and starts the service. Open the printed URL on any device.\nKeep this process running during authorization; it continues serving until SIGINT/SIGTERM.\nEnable MagicDNS and HTTPS certificates in the Tailscale admin console.")
	login.run = func(ctx context.Context, s *workspace.Service) error {
		if err := saveConfig(s, func(v *workspace.Settings) { v.TSNetEnabled = true }); err != nil {
			return err
		}
		if err := s.Start(); err != nil {
			return err
		}
		loginCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		address, err := s.TSNetLoginURL(loginCtx)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, address)
		fmt.Fprintln(out, "Complete login in a browser. Service remains running until interrupted.")
		<-ctx.Done()
		return nil
	}
	enroll := c.add("enroll", "Register using SPOOL_TSNET_AUTH_KEY, then exit", "Enables embedded Tailscale and saves the resulting node identity; the Auth Key is\nnot printed or persisted. Start 'serve' afterwards to activate HTTPS.")
	timeout := enroll.flags.Duration("timeout", time.Minute, "Maximum enrollment duration (e.g. 1m)")
	enroll.validate = func() error {
		if *timeout <= 0 {
			return usage("--timeout must be positive")
		}
		_, err := envRequired("SPOOL_TSNET_AUTH_KEY")
		return err
	}
	enroll.run = func(ctx context.Context, s *workspace.Service) error {
		key, _ := envRequired("SPOOL_TSNET_AUTH_KEY")
		if err := s.SetTSNetAuthKey(key); err != nil {
			return err
		}
		if err := saveConfig(s, func(v *workspace.Settings) { v.TSNetEnabled = true }); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		if err := s.RegisterTSNetAuthKey(ctx, key); err != nil {
			return err
		}
		return writeJSON(out, map[string]bool{"registered": true})
	}
}
