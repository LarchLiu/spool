package relay

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP resolves only the trusted suffix of an X-Forwarded-For chain. An
// untrusted peer cannot choose its limiter key by prepending an address or by
// sending another forwarding header. No headers are trusted by default.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	peer = peer.Unmap().WithZone("")
	client := peer
	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if forwarded != "" {
		chain := strings.Split(forwarded, ",")
		for i := len(chain) - 1; i >= 0 && s.trustsProxy(client); i-- {
			next, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
			if err != nil || next.Zone() != "" {
				return peer.String()
			}
			client = next.Unmap()
		}
	}
	return client.String()
}

func (s *Server) trustsProxy(address netip.Addr) bool {
	for _, prefix := range s.trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
