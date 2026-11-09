package relay

import (
	"net/http/httptest"
	"testing"
)

func TestRegistrationIPTrustBoundary(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/24, 2001:db8:1::/48")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{opts: ServerOptions{TrustedProxies: trusted}}
	for _, test := range []struct{ name, peer, forwarded, want string }{
		{"direct", "198.51.100.1:1234", "", "198.51.100.1"},
		{"untrusted spoof", "198.51.100.1:1234", "192.0.2.2", "198.51.100.1"},
		{"proxy", "10.0.0.2:1234", "198.51.100.1", "198.51.100.1"},
		{"multiple proxies", "10.0.0.2:1234", "198.51.100.1, 10.0.0.3", "198.51.100.1"},
		{"spoof before boundary", "10.0.0.2:1234", "192.0.2.2, 198.51.100.1", "198.51.100.1"},
		{"malformed trusted chain", "10.0.0.2:1234", "bad-address, 10.0.0.3", "10.0.0.2"},
		{"malformed untrusted prefix ignored", "10.0.0.2:1234", "bad-address, 198.51.100.1", "198.51.100.1"},
		{"IPv6", "[2001:db8:2::1]:1234", "", "2001:db8:2::1"},
		{"IPv6 proxy", "[2001:db8:1::1]:1234", "2001:db8:2::1", "2001:db8:2::1"},
		{"mapped peer", "[::ffff:10.0.0.2]:1234", "198.51.100.1", "198.51.100.1"},
		{"no forwarded", "10.0.0.2:1234", "", "10.0.0.2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/register", nil)
			r.RemoteAddr = test.peer
			r.Header.Set("X-Forwarded-For", test.forwarded)
			r.Header.Set("CF-Connecting-IP", "192.0.2.99")
			if got := s.registrationIP(r); got != test.want {
				t.Fatalf("IP = %s, want %s", got, test.want)
			}
		})
	}
	for _, invalid := range []string{"10.0.0.2", "10.0.0.0/33", "10.0.0.0/24,", "::ffff:10.0.0.0/120"} {
		if _, err := ParseTrustedProxies(invalid); err == nil {
			t.Fatal("invalid CIDR accepted", invalid)
		}
	}
}
