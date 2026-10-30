package workspace

import "spool/internal/netproxy"

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
	if settings.UseSystemProxy != s.useSystemProxy {
		var err error
		policy, err = netproxy.Prepare(s.ctx, settings.UseSystemProxy)
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
		s.useSystemProxy = settings.UseSystemProxy
	}
	return nil
}

func (s *Service) applyNetworkSettingsLocked(settings Settings) error {
	policy, err := netproxy.Prepare(s.ctx, settings.UseSystemProxy)
	if err != nil {
		return err
	}
	netproxy.Activate(policy)
	s.useSystemProxy = settings.UseSystemProxy
	return nil
}
