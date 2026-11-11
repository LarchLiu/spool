package relay

import (
	"net/netip"
	"time"
)

const (
	enrollmentWindow = time.Hour
	// Networks tracked at once. Beyond it new networks are refused, which also
	// bounds automatic enrollment across many addresses.
	maxEnrollmentNetworks = 1024
	// Records that automatic enrollment may never use, so an administrator can
	// still issue tokens after open registration has been abused.
	automaticRegistrationReserve = 1000
)

// enrollmentLimiter bounds how many new identities one network may enroll
// without a token. Each one adds a permanent record, and anyone can generate
// identities, so open registration must not be able to fill the store from
// one place. Rotating an existing record's credential is not counted.
//
// Guarded by Server.mu.
type enrollmentLimiter struct {
	limit  int
	recent map[string][]time.Time
}

// enrollmentNetwork is the address budgets are kept for. One subscriber
// controls a whole IPv6 /64, so its addresses share a budget. Addresses the
// relay could not resolve share one as well.
func enrollmentNetwork(ip string) string {
	address, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	address = address.Unmap()
	if address.Is6() {
		prefix, _ := address.Prefix(64)
		return prefix.String()
	}
	return address.String()
}

func currentEnrollments(times []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-enrollmentWindow)
	for len(times) > 0 && !times[0].After(cutoff) {
		times = times[1:]
	}
	return times
}

// allowed reports whether ip may enroll another new identity now.
func (l *enrollmentLimiter) allowed(ip string, now time.Time) bool {
	network := enrollmentNetwork(ip)
	times := currentEnrollments(l.recent[network], now)
	if len(times) > 0 {
		l.recent[network] = times
		return len(times) < l.limit
	}
	delete(l.recent, network)
	if len(l.recent) >= maxEnrollmentNetworks {
		for other, times := range l.recent {
			if len(currentEnrollments(times, now)) == 0 {
				delete(l.recent, other)
			}
		}
	}
	return len(l.recent) < maxEnrollmentNetworks
}

// add records an enrollment that was saved.
func (l *enrollmentLimiter) add(ip string, now time.Time) {
	network := enrollmentNetwork(ip)
	l.recent[network] = append(l.recent[network], now)
}
