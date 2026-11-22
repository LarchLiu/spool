package tailnet

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/tsaddr"
)

// DialContext connects to a MagicDNS peer through this endpoint's netstack.
func (e *Endpoint) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	node, ok := e.node.(interface {
		dialPeer(context.Context, string, string) (net.Conn, error)
	})
	if !ok {
		return nil, errors.New("Tailscale peer dialing is unavailable")
	}
	return node.dialPeer(ctx, network, address)
}

func (n embeddedTailnet) dialPeer(ctx context.Context, network, address string) (net.Conn, error) {
	client, err := n.LocalClient()
	if err != nil {
		return nil, err
	}
	status, err := client.Status(ctx)
	if err != nil {
		return nil, err
	}
	target, err := tailnetPeerAddress(status, network, address)
	if err != nil {
		return nil, err
	}
	return n.Sys().Dialer.Get().NetstackDialTCP(ctx, target)
}

func tailnetPeerAddress(status *ipnstate.Status, network, address string) (netip.AddrPort, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return netip.AddrPort{}, errors.New("Tailscale requires a TCP connection")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return netip.AddrPort{}, err
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil || p == 0 || host == "" {
		return netip.AddrPort{}, errors.New("invalid Tailscale address")
	}
	if status == nil || status.BackendState != "Running" {
		return netip.AddrPort{}, errors.New("Tailscale is unavailable")
	}
	var peer *ipnstate.PeerStatus
	for _, candidate := range status.Peer {
		if candidate == nil || !strings.EqualFold(strings.TrimSuffix(candidate.DNSName, "."), host) {
			continue
		}
		if peer != nil {
			return netip.AddrPort{}, errors.New("ambiguous Tailscale peer")
		}
		peer = candidate
	}
	var target netip.Addr
	if peer != nil {
		for _, ip := range peer.TailscaleIPs {
			ip = ip.Unmap()
			if tsaddr.IsTailscaleIP(ip) && ip.Zone() == "" && (network != "tcp4" || ip.Is4()) && (network != "tcp6" || ip.Is6()) && (!target.IsValid() || !target.Is4() && ip.Is4()) {
				target = ip
			}
		}
	}
	if !target.IsValid() {
		return netip.AddrPort{}, errors.New("Tailscale peer has no reachable address")
	}
	return netip.AddrPortFrom(target, uint16(p)), nil
}
