package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"spool/internal/workspace"
)

// Explicit bindings keep desktop-only settings out of headless configuration.
// Applying only visited flags preserves unrelated saved settings.
func configFlags(c *command) func(*workspace.Settings) {
	v := workspace.DefaultSettings()
	f := c.flags
	f.BoolVar(&v.WebEnabled, "web", v.WebEnabled, "Enable the local HTTP listener")
	f.StringVar(&v.Host, "host", v.Host, "HTTP listen IP (use a reverse proxy or SSH for remote access)")
	f.IntVar(&v.Port, "port", v.Port, "HTTP listen port (1-65535)")
	f.StringVar(&v.PublicURL, "public-url", "", "Reverse proxy public origin, e.g. https://chat.example.com; empty clears it")
	f.IntVar(&v.IdleMinutes, "idle-minutes", v.IdleMinutes, "Release idle agent processes after this many minutes (1-10080)")
	f.IntVar(&v.HistoryCacheMiB, "history-cache-mib", v.HistoryCacheMiB, "Shared transcript cache target in MiB")
	f.BoolVar(&v.ProxyEnabled, "proxy-enabled", v.ProxyEnabled, "Enable outbound proxies; false forces direct connections")
	f.BoolVar(&v.UseSystemProxy, "system-proxy", false, "Use OS HTTP/HTTPS proxy settings (macOS only)")
	f.StringVar(&v.ProxyURL, "proxy-url", "", "Custom outbound proxy URL; empty inherits proxy environment variables")
	f.StringVar(&v.NoProxy, "no-proxy", "", "Comma-separated proxy bypass hosts; empty inherits NO_PROXY")
	f.BoolVar(&v.TSNetEnabled, "tsnet", false, "Enable embedded Tailscale HTTPS")
	f.StringVar(&v.TSNetHostname, "tsnet-hostname", v.TSNetHostname, "Embedded Tailscale node hostname")
	f.IntVar(&v.TSNetPort, "tsnet-port", v.TSNetPort, "HTTPS port on the Tailscale node (not a host port)")
	f.BoolVar(&v.RelayEnabled, "relay", false, "Enable outbound relay and secure LAN connections (register first)")
	f.BoolVar(&v.RelayWebEnabled, "relay-web", false, "Enable public Web/PWA access through the trusted relay")
	f.StringVar(&v.RelayURL, "relay-url", "", "Relay HTTPS origin, e.g. https://relay.example.com")
	f.IntVar(&v.RelayLANPort, "relay-lan-port", v.RelayLANPort, "Secure LAN listener/discovery port (1-65535)")
	return func(s *workspace.Settings) {
		if changed(c, "web") {
			s.WebEnabled = v.WebEnabled
		}
		if changed(c, "host") {
			s.Host = v.Host
		}
		if changed(c, "port") {
			s.Port = v.Port
		}
		if changed(c, "public-url") {
			s.PublicURL = v.PublicURL
		}
		if changed(c, "idle-minutes") {
			s.IdleMinutes = v.IdleMinutes
		}
		if changed(c, "history-cache-mib") {
			s.HistoryCacheMiB = v.HistoryCacheMiB
		}
		if changed(c, "proxy-enabled") {
			s.ProxyEnabled = v.ProxyEnabled
		}
		if changed(c, "system-proxy") {
			s.UseSystemProxy = v.UseSystemProxy
		}
		if changed(c, "proxy-url") {
			s.ProxyURL = v.ProxyURL
		}
		if changed(c, "no-proxy") {
			s.NoProxy = v.NoProxy
		}
		if changed(c, "tsnet") {
			s.TSNetEnabled = v.TSNetEnabled
		}
		if changed(c, "tsnet-hostname") {
			s.TSNetHostname = v.TSNetHostname
		}
		if changed(c, "tsnet-port") {
			s.TSNetPort = v.TSNetPort
		}
		if changed(c, "relay") {
			s.RelayEnabled = v.RelayEnabled
		}
		if changed(c, "relay-web") {
			s.RelayWebEnabled = v.RelayWebEnabled
		}
		if changed(c, "relay-url") {
			s.RelayURL = v.RelayURL
		}
		if changed(c, "relay-lan-port") {
			s.RelayLANPort = v.RelayLANPort
		}
	}
}

func saveConfig(s *workspace.Service, apply func(*workspace.Settings)) error {
	v, err := s.Store.Settings()
	if err != nil {
		return err
	}
	apply(&v)
	return s.SaveSettings(v)
}

func addConfig(root *command, out io.Writer) {
	c := root.add("config", "Inspect or change persisted service/network settings", "Changes apply on the next serve. Omitted options keep their saved values.\nProxy URLs may contain credentials: keep config output private.")
	show := c.add("show", "Print saved settings as JSON", "Requires the service to be stopped; this is saved configuration, not live status.")
	show.run = func(_ context.Context, s *workspace.Service) error {
		v, err := s.Store.Settings()
		if err != nil {
			return err
		}
		return writeJSON(out, v)
	}
	set := c.add("set", "Update selected service/network settings", "Only explicitly supplied options change. Use --option=false to disable a boolean.\nExample: spool-server config set --tsnet=true --tsnet-hostname build-host")
	apply := configFlags(set)
	set.validate = func() error {
		if set.flags.NFlag() == 0 {
			return usage("supply at least one setting; use '%s --help'", set.path)
		}
		return nil
	}
	set.run = func(_ context.Context, s *workspace.Service) error {
		if err := saveConfig(s, apply); err != nil {
			return err
		}
		v, err := s.Store.Settings()
		if err != nil {
			return err
		}
		return writeJSON(out, v)
	}
}

func addServe(root *command, out io.Writer) {
	c := root.add("serve", "Run the HTTP/Web/PWA and agent service in the foreground", "Options persist to the selected data directory; omitted options keep saved values.\nInstall/login Codex or Claude as the service OS user. SPOOL_CODEX_BIN and\nSPOOL_CLAUDE_BIN override discovery unless an agent path is saved.\nFor remote browsers configure an HTTPS reverse proxy and matching --public-url,\nor enable Tailscale/Relay. SIGINT/SIGTERM gracefully save and stop active turns.")
	apply := configFlags(c)
	c.run = func(ctx context.Context, s *workspace.Service) error {
		if err := saveConfig(s, apply); err != nil {
			return err
		}
		if err := s.Start(); err != nil {
			return err
		}
		fmt.Fprintf(out, "Spool Web: %s\nSQLite: %s\n", s.WebURL(), s.Store.DatabasePath())
		<-ctx.Done()
		log.Print("Stopping service and saving active turns")
		return nil
	}
}
