package relay

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseTrustedProxies accepts an explicit comma-separated list of proxy CIDRs.
// An empty list means forwarded headers are never used.
func ParseTrustedProxies(value string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	if strings.TrimSpace(value) == "" {
		return prefixes, nil
	}
	for _, item := range strings.Split(value, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("invalid trusted proxy CIDR: %q", item)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func (s *Server) trustedProxy(address netip.Addr) bool {
	for _, prefix := range s.opts.TrustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// Walk the chain from the socket peer toward the client, stopping at the first
// untrusted address. Headers before that boundary cannot identify the caller.
// Malformed chains fall back to the socket peer; CF-Connecting-IP is not trusted.
func (s *Server) registrationIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	peer = peer.Unmap().WithZone("")
	address := peer
	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if forwarded == "" {
		return peer.String()
	}
	chain := strings.Split(forwarded, ",")
	if len(chain) > 64 {
		return peer.String()
	}
	for i := len(chain) - 1; i >= 0 && s.trustedProxy(address); i-- {
		next, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil || next.Zone() != "" {
			return peer.String()
		}
		address = next.Unmap()
	}
	return address.String()
}
