package relay

import (
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Automatic trust is kept separate from operator-supplied CIDRs. Only the
// callback verifier can write it; changing the public origin clears it.
type relaySettings struct {
	PublicURL      string     `json:"public_url"`
	AdminOrigin    string     `json:"admin_origin"`
	TrustedProxies []string   `json:"trusted_proxies"`
	DetectedProxy  string     `json:"detected_proxy"`
	DetectedOrigin string     `json:"detected_origin"`
	DetectedAt     *time.Time `json:"detected_at,omitempty"`
}

type settingsInput struct {
	PublicURL      string   `json:"public_url"`
	BrowserOrigin  string   `json:"browser_origin"`
	TrustedProxies []string `json:"trusted_proxies"`
}

type settingsStatus struct {
	relaySettings
	Detecting            bool   `json:"detecting"`
	DetectionError       string `json:"detection_error"`
	RegistrationRequired bool   `json:"registration_required"`
}

var errInvalidSettings = errors.New("invalid relay settings: use an HTTPS origin and valid proxy CIDRs")

func publicOrigin(address string) (string, error) {
	address = strings.TrimSpace(address)
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(address, "#") ||
		(u.Path != "" && u.Path != "/") || strings.ContainsAny(address, "\r\n\t ") {
		return "", errInvalidSettings
	}
	u.Path, u.RawPath, u.Host = "", "", strings.ToLower(u.Host)
	if _, err := ParseRelayURL(u.String()); err != nil {
		return "", errInvalidSettings
	}
	return u.String(), nil
}

func normalizedProxies(values []string) ([]string, error) {
	if len(values) > 128 {
		return nil, errInvalidSettings
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil || prefix.Addr().Is4In6() || prefix.Addr().Zone() != "" {
			return nil, errInvalidSettings
		}
		result = append(result, prefix.Masked().String())
	}
	return result, nil
}

func (s *Server) loadSettings() error {
	var err error
	s.settings.TrustedProxies, err = normalizedProxies(s.opts.TrustedProxies)
	if err != nil {
		return err
	}
	file, err := os.Open(filepath.Join(s.opts.DataDir, "settings.json"))
	if os.IsNotExist(err) {
		return nil // CLI/environment CIDRs bootstrap a store without saved settings.
	}
	if err != nil {
		return err
	}
	defer file.Close()
	var saved struct {
		Version int `json:"version"`
		// Version 1 inferred the service URL from the management browser origin
		// unless an override was provided. Preserve only explicitly chosen URLs.
		LegacyOverride string `json:"public_url_override,omitempty"`
		relaySettings
	}
	decoder := json.NewDecoder(io.LimitReader(file, 32769))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&saved) != nil || decoder.Decode(new(any)) != io.EOF || (saved.Version != 1 && saved.Version != 2) {
		return errors.New("invalid relay settings store")
	}
	if saved.PublicURL != "" {
		origin, err := publicOrigin(saved.PublicURL)
		if err != nil || origin != saved.PublicURL {
			return errInvalidSettings
		}
	}
	if saved.LegacyOverride != "" && (saved.Version != 1 || saved.LegacyOverride != saved.PublicURL) {
		return errInvalidSettings
	}
	if saved.AdminOrigin != "" {
		origin, err := publicOrigin(saved.AdminOrigin)
		if err != nil || origin != saved.AdminOrigin {
			return errInvalidSettings
		}
	}
	if saved.DetectedOrigin != "" || saved.DetectedProxy != "" || saved.DetectedAt != nil {
		if saved.PublicURL == "" || saved.DetectedOrigin != saved.PublicURL || saved.DetectedAt == nil {
			return errInvalidSettings
		}
		if saved.DetectedProxy != "" {
			prefix, err := netip.ParsePrefix(saved.DetectedProxy)
			if err != nil || prefix.Addr().Is4In6() || prefix.Bits() != prefix.Addr().BitLen() || prefix.String() != saved.DetectedProxy {
				return errInvalidSettings
			}
		}
	}
	if saved.Version == 1 && saved.LegacyOverride == "" {
		saved.PublicURL, saved.DetectedProxy, saved.DetectedOrigin, saved.DetectedAt = "", "", "", nil
	}
	proxies, err := normalizedProxies(saved.TrustedProxies)
	if err != nil {
		return err
	}
	saved.TrustedProxies = proxies
	s.settings = saved.relaySettings
	s.applyProxySettingsLocked()
	return nil
}

// Caller holds mu; persist before publishing a change to the running policy.
func (s *Server) saveSettingsLocked(next relaySettings) error {
	data, err := json.MarshalIndent(struct {
		Version int `json:"version"`
		relaySettings
	}{2, next}, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.opts.DataDir, ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), filepath.Join(s.opts.DataDir, "settings.json"))
}

func (s *Server) applyProxySettingsLocked() {
	s.trustedProxies, _ = ParseTrustedProxies(strings.Join(s.settings.TrustedProxies, ","))
	if s.settings.PublicURL != "" && s.settings.DetectedOrigin == s.settings.PublicURL && s.settings.DetectedProxy != "" {
		prefix, _ := netip.ParsePrefix(s.settings.DetectedProxy)
		s.trustedProxies = append(s.trustedProxies, prefix)
	}
}

func (s *Server) settingsStatus() settingsStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.settings
	next.TrustedProxies = append([]string{}, next.TrustedProxies...)
	return settingsStatus{next, s.proxyJob != nil, s.proxyError, !s.opts.AllowUnregisteredHosts}
}

func (s *Server) updateSettings(input settingsInput) error {
	proxies, err := normalizedProxies(input.TrustedProxies)
	if err != nil {
		return err
	}
	var origin string
	if strings.TrimSpace(input.PublicURL) != "" {
		origin, err = publicOrigin(input.PublicURL)
		if err != nil {
			return err
		}
	}
	var adminOrigin string
	if strings.HasPrefix(input.BrowserOrigin, "https://") {
		adminOrigin, err = publicOrigin(input.BrowserOrigin)
		if err != nil {
			return err
		}
	} else if input.BrowserOrigin != "" {
		// The browser origin protects the administrative host from tenant Web
		// content. It never chooses the public service URL or proxy-check target.
		u, parseErr := url.Parse(input.BrowserOrigin)
		if parseErr != nil || u.Scheme != "http" || !loopbackHost(u.Hostname()) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return errInvalidSettings
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("relay is closed")
	}
	next := s.settings
	if adminOrigin != "" {
		next.AdminOrigin = adminOrigin
	}
	next.PublicURL, next.TrustedProxies = origin, proxies
	changed := next.PublicURL != s.settings.PublicURL
	if changed {
		next.DetectedProxy, next.DetectedOrigin, next.DetectedAt = "", "", nil
	}
	if err := s.saveSettingsLocked(next); err != nil {
		return err
	}
	s.settings = next
	s.applyProxySettingsLocked()
	if changed {
		s.startProxyDetectionLocked()
	}
	return nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
