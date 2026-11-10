package workspace

import (
	"fmt"
	"log"
	"strings"

	"spool/internal/netproxy"
)

func (s Settings) proxyOptions() netproxy.Options {
	// Inactive custom values stay saved without affecting the live policy or
	// requesting a Tailscale restart until their mode is selected again.
	if !s.ProxyEnabled {
		return netproxy.Options{}
	}
	if s.UseSystemProxy {
		return netproxy.Options{Enabled: true, UseSystemProxy: true}
	}
	return netproxy.Options{Enabled: true, ProxyURL: strings.TrimSpace(s.ProxyURL), NoProxy: strings.TrimSpace(s.NoProxy)}
}

// SaveSettings applies the app-wide network policy after a successful save.
// It never restarts the service or interrupts active tasks. Embedded Tailscale
// connections pick up proxy changes when their own pending changes are applied.
func (s *Service) SaveSettings(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveSettingsLocked(settings)
}

func (s *Service) saveSettingsLocked(settings Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	previous, err := s.Store.Settings()
	if err != nil {
		return err
	}
	// Detection owns its verified peer independently of manual CIDRs. A stale
	// settings form must not restore an old automatically detected address.
	settings.WebDetectedProxy, settings.WebDetectedOrigin = previous.WebDetectedProxy, previous.WebDetectedOrigin
	if s.relayRegistering && (settings.RelayURL != previous.RelayURL || settings.RelayEnabled != previous.RelayEnabled || settings.RelayLANPort != previous.RelayLANPort || settings.RelayWebEnabled != previous.RelayWebEnabled) {
		return fmt.Errorf("Relay registration is unavailable or already in progress.")
	}
	if settings.RelayEnabled && (!previous.RelayEnabled || relayOrigin(previous.RelayURL) != relayOrigin(settings.RelayURL)) && !s.relayRegisteredLocked(settings.RelayURL) {
		return fmt.Errorf("Register this server with the relay before enabling it.")
	}
	var policy *netproxy.Policy
	var proxyErr error
	options := settings.proxyOptions()
	apply := !s.networkProxyReady || options != s.networkProxy
	if apply {
		policy, proxyErr = netproxy.Prepare(s.ctx, options)
	}
	if err := s.Store.SaveSettings(settings); err != nil {
		return err
	}
	if s.tailnet != nil && s.tailnet.enrolling && !sameTailnetSettings(s.tailnet.settings, settings) {
		s.tailnet.cancel()
		s.tailnet = nil
	}
	if apply {
		s.activateNetworkPolicyLocked(options, policy, proxyErr)
	}
	if settings.PublicURL != previous.PublicURL {
		s.startWebProxyDetectionLocked(settings)
	}
	return nil
}

func (s *Service) applyNetworkSettingsLocked(settings Settings) {
	options := settings.proxyOptions()
	policy, err := netproxy.Prepare(s.ctx, options)
	s.activateNetworkPolicyLocked(options, policy, err)
}

func (s *Service) activateNetworkPolicyLocked(options netproxy.Options, policy *netproxy.Policy, err error) {
	s.networkProxy = options
	s.networkProxyReady = err == nil
	if err != nil {
		netproxy.Reject(err)
		log.Printf("event=proxy_settings_unavailable error=%q", err)
		return
	}
	netproxy.Activate(policy)
}
