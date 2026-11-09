package relay

import (
	"fmt"
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
