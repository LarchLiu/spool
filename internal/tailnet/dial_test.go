package tailnet

import (
	"net/netip"
	"testing"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
)

func TestTailnetDialResolvesOnlyPeers(t *testing.T) {
	name := "peer.example.ts.net"
	peer := &ipnstate.PeerStatus{DNSName: name + ".", TailscaleIPs: []netip.Addr{
		netip.MustParseAddr("fd7a:115c:a1e0::123"), netip.MustParseAddr("100.64.0.23"), netip.MustParseAddr("192.0.2.1"),
	}}
	status := &ipnstate.Status{BackendState: "Running", Peer: map[key.NodePublic]*ipnstate.PeerStatus{key.NewNode().Public(): peer}}
	for _, tc := range []struct{ network, authority, want string }{
		{"tcp", "PEER.example.ts.net:443", "100.64.0.23:443"},
		{"tcp6", name + ":8443", "[fd7a:115c:a1e0::123]:8443"},
		{"tcp", "unrelated.example.ts.net:443", ""},
		{"tcp", "100.64.0.23:443", ""},
		{"udp", name + ":443", ""},
	} {
		address, err := tailnetPeerAddress(status, tc.network, tc.authority)
		if tc.want == "" {
			if err == nil {
				t.Errorf("unexpected non-peer route %v", address)
			}
		} else if err != nil || address.String() != tc.want {
			t.Errorf("route = %v, %v; want %s", address, err, tc.want)
		}
	}
	status.Peer[key.NewNode().Public()] = peer
	if _, err := tailnetPeerAddress(status, "tcp", name+":443"); err == nil {
		t.Fatal("duplicate DNS name selected an arbitrary peer")
	}
}
