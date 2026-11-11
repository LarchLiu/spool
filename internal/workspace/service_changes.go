package workspace

import "fmt"

func (s *Service) pendingChangesLocked(next Settings) []string {
	pending := []string{}
	if s.server == nil {
		return pending
	}
	active := s.activeSettings
	if active.WebEnabled != next.WebEnabled || next.WebEnabled && (active.Host != next.Host || active.Port != next.Port || active.PublicURL != next.PublicURL || active.WebTrustedProxies != next.WebTrustedProxies || s.web == nil) {
		pending = append(pending, "web")
	}
	if !sameRelaySettings(active, next) || next.RelayEnabled && s.relay == nil || s.relay != nil && s.relay.host.RegistrationToken() != s.relayRegistrationToken(s.relay.host.URL) {
		pending = append(pending, "relay")
	}
	tsnetChanged := !sameTailnetSettings(active, next) || active.TSNetEnabled && active.proxyOptions() != next.proxyOptions()
	if s.tailnet != nil {
		tsnetChanged = tsnetChanged || s.tailnet.authRevision != s.tailnetAuthRevision || s.tailnet.snapshot().State == "Registered" && (next.TSNetEnabled || !s.tailnet.loginOnly)
	}
	if tsnetChanged {
		pending = append(pending, "tsnet")
	}
	return pending
}

func copyServiceSettings(target string, destination *Settings, source Settings) error {
	switch target {
	case "web":
		destination.WebEnabled = source.WebEnabled
		destination.Host, destination.Port, destination.PublicURL = source.Host, source.Port, source.PublicURL
		destination.WebTrustedProxies = source.WebTrustedProxies
		destination.WebDetectedProxy, destination.WebDetectedOrigin = source.WebDetectedProxy, source.WebDetectedOrigin
	case "relay":
		destination.RelayEnabled, destination.RelayURL, destination.RelayLANPort, destination.RelayWebEnabled = source.RelayEnabled, source.RelayURL, source.RelayLANPort, source.RelayWebEnabled
	case "tsnet":
		destination.TSNetEnabled, destination.TSNetHostname, destination.TSNetPort = source.TSNetEnabled, source.TSNetHostname, source.TSNetPort
	default:
		return fmt.Errorf("unknown service entry")
	}
	return nil
}

// ApplyServiceChanges never closes the shared App for an access-entry change.
func (s *Service) ApplyServiceChanges(target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("service is closed")
	}
	if s.updating {
		return ErrUpdateInProgress
	}
	if s.server == nil {
		return fmt.Errorf("chat service is not running")
	}
	next, err := s.Store.Settings()
	if err != nil {
		return err
	}
	if err := next.validateLocal(); err != nil {
		return err
	}
	switch target {
	case "web":
		s.stopWebProxyDetectionLocked()
		s.webProxyCheck = nil
		err = s.restartWebLocked(next)
	case "relay":
		if s.relayRegistering {
			return fmt.Errorf("Relay registration is unavailable or already in progress.")
		}
		if next.RelayEnabled && !s.relayRegisteredLocked(next.RelayURL) {
			return fmt.Errorf("Register this server with the relay before enabling it.")
		}
		previous := s.activeSettings
		if s.relay != nil {
			s.relay.close()
			s.relay = nil
		}
		s.relayError = ""
		if next.RelayEnabled {
			err = s.startRelayLocked(next)
		}
		if err != nil {
			s.relayError = err.Error()
			if previous.RelayEnabled {
				if rollback := s.startRelayLocked(previous); rollback != nil {
					s.relayError = fmt.Sprintf("%v; restore previous relay: %v", err, rollback)
				}
			}
			return err
		}
		s.app.configureRelayWeb(next)
	case "tsnet":
		if s.tailnet != nil {
			s.tailnet.cancel()
			s.tailnet.ingress.close()
			s.tailnet = nil
		}
		if next.TSNetEnabled {
			s.startTailnetLocked(next)
		}
		s.activeSettings.ProxyEnabled, s.activeSettings.UseSystemProxy, s.activeSettings.ProxyURL, s.activeSettings.NoProxy = next.ProxyEnabled, next.UseSystemProxy, next.ProxyURL, next.NoProxy
	default:
		return fmt.Errorf("unknown service entry")
	}
	if err != nil {
		return err
	}
	if err := copyServiceSettings(target, &s.activeSettings, next); err != nil {
		return err
	}
	if target == "web" {
		s.startWebProxyDetectionLocked(next)
	}
	return nil
}

// Cancel restores only the selected entry's saved fields to the active values.
// Other pending entries and unrelated preferences are preserved.
func (s *Service) CancelServiceChanges(target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("service is closed")
	}
	if s.updating {
		return ErrUpdateInProgress
	}
	if s.server == nil {
		return fmt.Errorf("chat service is not running")
	}
	if target == "relay" && s.relayRegistering {
		return fmt.Errorf("Relay registration is unavailable or already in progress.")
	}
	next, err := s.Store.Settings()
	if err != nil {
		return err
	}
	if err := copyServiceSettings(target, &next, s.activeSettings); err != nil {
		return err
	}
	if target == "tsnet" && s.activeSettings.TSNetEnabled && next.proxyOptions() != s.activeSettings.proxyOptions() {
		next.ProxyEnabled, next.UseSystemProxy, next.ProxyURL, next.NoProxy = s.activeSettings.ProxyEnabled, s.activeSettings.UseSystemProxy, s.activeSettings.ProxyURL, s.activeSettings.NoProxy
	}
	if err := s.saveSettingsLocked(next); err != nil {
		return err
	}
	if target == "web" {
		s.webError = ""
	}
	if target == "relay" {
		s.relayError = ""
	}
	if target == "tsnet" {
		s.tailnetAuthKey = ""
		runtime := s.tailnet
		if runtime != nil {
			s.tailnetAuthRevision = runtime.authRevision
			if runtime.snapshot().State == "Registered" {
				// Enrollment stays saved on disk. Cancelling activation disables this entry.
				next.TSNetEnabled = false
				if err := s.saveSettingsLocked(next); err != nil {
					return err
				}
				runtime.cancel()
				runtime.ingress.close()
				s.tailnet = nil
				s.activeSettings.TSNetEnabled = false
			}
		} else if next.TSNetEnabled {
			// Saving a disable can cancel an enrollment that was already in progress.
			// Cancelling that disable restores only the original Tailscale entry.
			s.startTailnetLocked(next)
		}
	}
	return nil
}
