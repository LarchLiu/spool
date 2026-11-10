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
	s.mu.Lock()
	trusted := append([]netip.Prefix(nil), s.trustedProxies...)
	s.mu.Unlock()
	client := peer
	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if forwarded != "" {
		chain := strings.Split(forwarded, ",")
		for i := len(chain) - 1; i >= 0 && trustsProxy(trusted, client); i-- {
			// Bound trusted hops, not an untrusted prefix. A client must not
			// choose the shared proxy bucket by prepending a long forged list.
			if len(chain)-1-i >= 64 {
				return peer.String()
			}
			next, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
			if err != nil || next.Zone() != "" {
				return peer.String()
			}
			client = next.Unmap()
		}
	}
	return client.String()
}

func trustsProxy(trusted []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
