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

// Only operational state is persisted. Public addresses always come from the
// deployment; a stale dashboard file must never override a redeployment.
type relayRuntimeSettings struct {
	AdminOrigin    string     `json:"admin_origin"`
	TrustedProxies []string   `json:"trusted_proxies"`
	DetectedProxy  string     `json:"detected_proxy"`
	DetectedOrigin string     `json:"detected_origin"`
	DetectedAt     *time.Time `json:"detected_at,omitempty"`
}

type relaySettings struct {
	PublicURL string `json:"public_url"`
	WebDomain string `json:"web_domain"`
	relayRuntimeSettings
}

type settingsInput struct {
	BrowserOrigin  string    `json:"browser_origin"`
	TrustedProxies *[]string `json:"trusted_proxies,omitempty"`
}

type settingsStatus struct {
	relaySettings
	Detecting            bool   `json:"detecting"`
	WaitingForProxy      bool   `json:"waiting_for_proxy"`
	DetectionError       string `json:"detection_error"`
	RegistrationRequired bool   `json:"registration_required"`
}

var errInvalidSettings = errors.New("invalid relay settings: check the HTTPS origin and proxy CIDRs")

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
	s.settings.PublicURL = strings.TrimSpace(s.opts.PublicURL)
	if s.settings.PublicURL != "" {
		origin, err := publicOrigin(s.settings.PublicURL)
		if err != nil {
			return err
		}
		s.settings.PublicURL = origin
	}
	s.settings.WebDomain = ""
	if s.settings.PublicURL != "" {
		public, _ := url.Parse(s.settings.PublicURL)
		s.settings.WebDomain = webDomain(public.Hostname())
	}
	file, err := os.Open(filepath.Join(s.opts.DataDir, "settings.json"))
	if os.IsNotExist(err) {
		s.applyProxySettingsLocked()
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	var saved struct {
		Version int `json:"version"`
		// Versions 1–3 persisted deployment choices. Accept them for migration,
		// but never use them as a fallback for missing environment variables.
		LegacyPublicURL       string `json:"public_url,omitempty"`
		LegacyOverride        string `json:"public_url_override,omitempty"`
		LegacyWebDomain       string `json:"web_domain,omitempty"`
		LegacyCertificateMode string `json:"certificate_mode,omitempty"`
		relayRuntimeSettings
	}
	decoder := json.NewDecoder(io.LimitReader(file, 32769))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&saved) != nil || decoder.Decode(new(any)) != io.EOF || saved.Version < 1 || saved.Version > 4 {
		return errors.New("invalid relay settings store")
	}
	if saved.AdminOrigin != "" {
		origin, err := publicOrigin(saved.AdminOrigin)
		if err != nil || origin != saved.AdminOrigin || origin == s.settings.PublicURL {
			return errInvalidSettings
		}
	}
	if saved.DetectedOrigin != "" || saved.DetectedProxy != "" || saved.DetectedAt != nil {
		origin, err := publicOrigin(saved.DetectedOrigin)
		if err != nil || origin != saved.DetectedOrigin || saved.DetectedAt == nil {
			return errInvalidSettings
		}
		if saved.DetectedProxy != "" {
			prefix, err := netip.ParsePrefix(saved.DetectedProxy)
			if err != nil || prefix.Addr().Is4In6() || prefix.Bits() != prefix.Addr().BitLen() || prefix.String() != saved.DetectedProxy {
				return errInvalidSettings
			}
		}
	}
	// Trust is valid only for the deployed origin that was actually verified.
	// Version 1 inferred the URL from the management page unless overridden.
	if saved.DetectedOrigin != s.settings.PublicURL || saved.Version == 1 && saved.LegacyOverride == "" {
		saved.DetectedProxy, saved.DetectedOrigin, saved.DetectedAt = "", "", nil
	}
	saved.TrustedProxies, err = normalizedProxies(saved.TrustedProxies)
	if err != nil {
		return err
	}
	s.settings.relayRuntimeSettings = saved.relayRuntimeSettings
	s.applyProxySettingsLocked()
	return nil
}

// Caller holds mu; persist before publishing a change to the running policy.
func (s *Server) saveSettingsLocked(next relaySettings) error {
	data, err := json.MarshalIndent(struct {
		Version int `json:"version"`
		relayRuntimeSettings
	}{4, next.relayRuntimeSettings}, "", "  ")
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
	return settingsStatus{relaySettings: next, Detecting: s.proxyJob != nil, WaitingForProxy: s.proxyJob != nil && s.proxyJob.waiting, DetectionError: s.proxyError, RegistrationRequired: !s.opts.AllowUnregisteredHosts}
}

func (s *Server) updateSettings(input settingsInput) error {
	var proxies []string
	var err error
	if input.TrustedProxies != nil {
		proxies, err = normalizedProxies(*input.TrustedProxies)
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
		// Management origin protects tenant isolation; it never chooses the
		// public URL or the destination of a proxy check.
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
	if input.TrustedProxies != nil {
		next.TrustedProxies = proxies
	}
	if next.AdminOrigin != "" && next.AdminOrigin == next.PublicURL {
		return errInvalidSettings
	}
	if err := s.saveSettingsLocked(next); err != nil {
		return err
	}
	s.settings = next
	s.applyProxySettingsLocked()
	return nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
