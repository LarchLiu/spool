package relay

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientAddressTrustBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, remote, want string
		forwarded          []string
		trusted            []string
	}{
		{name: "direct", remote: "198.51.100.1:1234", want: "198.51.100.1"},
		{name: "untrusted forwarding", remote: "198.51.100.1:1234", forwarded: []string{"203.0.113.2"}, want: "198.51.100.1"},
		{name: "loopback is not implicitly trusted", remote: "127.0.0.1:1234", forwarded: []string{"203.0.113.2"}, want: "127.0.0.1"},
		{name: "trusted proxy", remote: "127.0.0.1:1234", trusted: []string{"127.0.0.1/32"}, forwarded: []string{"198.51.100.1"}, want: "198.51.100.1"},
		{name: "ignore forged prefix", remote: "127.0.0.1:1234", trusted: []string{"127.0.0.1/32"}, forwarded: []string{"203.0.113.2, 198.51.100.1"}, want: "198.51.100.1"},
		{name: "multiple proxy hops and header lines", remote: "127.0.0.1:1234", trusted: []string{"127.0.0.1/32", "10.20.0.0/24"}, forwarded: []string{"203.0.113.2, 198.51.100.1", "10.20.0.5"}, want: "198.51.100.1"},
		{name: "IPv6 client", remote: "127.0.0.1:1234", trusted: []string{"127.0.0.1/32"}, forwarded: []string{"2001:0db8::1"}, want: "2001:db8::1"},
		{name: "mapped IPv4", remote: "[::ffff:198.51.100.1]:1234", want: "198.51.100.1"},
		{name: "malformed trusted chain", remote: "127.0.0.1:1234", trusted: []string{"127.0.0.1/32", "10.20.0.0/24"}, forwarded: []string{"garbage, 10.20.0.5"}, want: "127.0.0.1"},
		{name: "missing forwarding", remote: "127.0.0.1:1234", trusted: []string{"127.0.0.1/32"}, want: "127.0.0.1"},
		{name: "reject forwarded zone", remote: "127.0.0.1:1234", trusted: []string{"127.0.0.1/32"}, forwarded: []string{"fe80::1%fake"}, want: "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broker, err := NewServer(ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir(), TrustedProxies: tc.trusted})
			if err != nil {
				t.Fatal(err)
			}
			defer broker.Close()
			r := httptest.NewRequest("POST", "http://relay.test/", nil)
			r.RemoteAddr = tc.remote
			for _, value := range tc.forwarded {
				r.Header.Add("X-Forwarded-For", value)
			}
			r.Header.Set("X-Real-IP", "203.0.113.99")
			r.Header.Set("Forwarded", "for=203.0.113.99")
			r.Header.Set("X-Spool-Relay-Client", "forged")
			r.Header.Set("Cookie", "spool_relay_client=forged")
			if got := broker.clientIP(r); got != tc.want {
				t.Fatalf("client address = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInvalidTrustedProxyPreventsStartup(t *testing.T) {
	for _, value := range []string{"localhost", "127.0.0.1", "127.0.0.1/33", ""} {
		broker, err := NewServer(ServerOptions{AdminToken: strings.Repeat("a", 32), DataDir: t.TempDir(), TrustedProxies: []string{value}})
		if err == nil {
			broker.Close()
			t.Errorf("accepted invalid trusted proxy %q", value)
		}
	}
}
