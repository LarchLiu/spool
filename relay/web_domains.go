package relay

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// WebURL assigns a stable, single-label hostname to an authenticated identity.
// The suffix is always web.<public relay hostname>; the port is preserved.
func WebURL(publicURL, id string) (string, error) {
	origin, err := publicOrigin(publicURL)
	if err != nil || !validServerID(id) {
		return "", errors.New("Web publishing requires an HTTPS relay origin and a DNS domain")
	}
	u, _ := url.Parse(origin)
	domain := webDomain(u.Hostname())
	if domain == "" {
		return "", errors.New("Web publishing requires a DNS domain")
	}
	port := u.Port()
	u.Host, u.Path, u.RawPath = id+"."+domain, "/", ""
	if port != "" {
		u.Host = net.JoinHostPort(u.Host, port)
	}
	return u.String(), nil
}

func webDomain(hostname string) string {
	if !validWebDomain(hostname) {
		return ""
	}
	domain := "web." + hostname
	if !validWebDomain(domain) {
		return ""
	}
	return domain
}

func validWebDomain(domain string) bool {
	// Reserve enough room for the full identity label and dot.
	if len(domain) > 200 || !strings.Contains(domain, ".") || strings.ToLower(domain) != domain || net.ParseIP(domain) != nil {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// ValidateWebURL checks the authenticated relay's assignment before publishing
// it locally. The assignment must use the identity label followed by web.,
// without credentials, extra paths, or a non-HTTPS destination.
func ValidateWebURL(address, id string) bool {
	u, err := url.Parse(address)
	prefix := id + ".web."
	if err != nil || !strings.HasPrefix(u.Hostname(), prefix) {
		return false
	}
	u.Host = strings.TrimPrefix(u.Host, prefix)
	expected, err := WebURL(u.String(), id)
	return err == nil && address == expected
}

// Caller holds Server.mu.
func (s *Server) webURLLocked(id string) string {
	address, _ := WebURL(s.settings.PublicURL, id)
	return address
}
