package workspace

import (
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
// connections pick up proxy changes on an explicit service restart.
func (s *Service) SaveSettings(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := settings.Validate(); err != nil {
		return err
	}
	var policy *netproxy.Policy
	options := settings.proxyOptions()
	if options != s.networkProxy {
		var err error
		policy, err = netproxy.Prepare(s.ctx, options)
		if err != nil {
			return err
		}
	}
	if err := s.Store.SaveSettings(settings); err != nil {
		return err
	}
	if s.tailnet != nil && s.tailnet.enrolling && !sameTailnetSettings(s.tailnet.settings, settings) {
		s.tailnet.cancel()
		s.tailnet = nil
	}
	if policy != nil {
		netproxy.Activate(policy)
		s.networkProxy = options
	}
	return nil
}

func (s *Service) applyNetworkSettingsLocked(settings Settings) error {
	options := settings.proxyOptions()
	policy, err := netproxy.Prepare(s.ctx, options)
	if err != nil {
		return err
	}
	netproxy.Activate(policy)
	s.networkProxy = options
	return nil
}
