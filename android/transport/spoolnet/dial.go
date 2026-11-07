package spoolnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/tsaddr"
)

var errServerNotReady = errors.New("selected server is not ready in the tailnet")

type directDialer struct {
	authority string
	lifetime  context.Context
	dialTCP   func(context.Context, string, string) (net.Conn, error)
}

func (d directDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errors.New("only HTTPS TCP connections are allowed")
	}
	if !strings.EqualFold(address, d.authority) {
		return nil, errors.New("connection must use the selected server's exact HTTPS origin")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stop := context.AfterFunc(d.lifetime, cancel)
	defer stop()
	if err := d.lifetime.Err(); err != nil {
		return nil, err
	}
	return d.dialTCP(ctx, network, d.authority)
}

type peerStatusAPI interface {
	Status(context.Context) (*ipnstate.Status, error)
}

type tailnetDialer struct {
	api       peerStatusAPI
	authority string
	lifetime  context.Context
	dialTCP   func(context.Context, netip.AddrPort) (net.Conn, error)
}

func (d tailnetDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errors.New("only tailnet TCP connections are allowed")
	}
	if !strings.EqualFold(address, d.authority) {
		return nil, errors.New("connection must use the selected server's exact HTTPS origin")
	}
	host, port, err := net.SplitHostPort(d.authority)
	if err != nil {
		return nil, err
	}
	// http.Transport detaches dial contexts from request cancellation/deadlines.
	// Bound even an orphaned dial, and always stop it when the client closes.
	ctx, cancelDial := context.WithTimeout(ctx, 20*time.Second)
	defer cancelDial()
	if d.lifetime != nil {
		stop := context.AfterFunc(d.lifetime, cancelDial)
		defer stop()
		if err := d.lifetime.Err(); err != nil {
			return nil, err
		}
	}
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		if err := ready.Err(); err != nil {
			return nil, fmt.Errorf("%w: %w", errServerNotReady, err)
		}
		status, err := d.api.Status(ready)
		if err != nil {
			return nil, fmt.Errorf("read tailnet server status: %w", err)
		}
		if status != nil && status.BackendState == "Running" {
			if ip := serverIP(serverPeer(status, host), network); ip.IsValid() {
				target, err := netip.ParseAddrPort(net.JoinHostPort(ip.String(), port))
				if err != nil {
					return nil, err
				}
				conn, err := d.dialTCP(ctx, target)
				if err != nil {
					return nil, fmt.Errorf("dial tailnet server %s: %w", target, err)
				}
				return conn, nil
			}
		}
		if err := waitContext(ready, 250*time.Millisecond); err != nil {
			return nil, fmt.Errorf("%w: %w", errServerNotReady, err)
		}
	}
}

func serverIP(peer *ipnstate.PeerStatus, network string) netip.Addr {
	var target netip.Addr
	if peer == nil {
		return target
	}
	for _, ip := range peer.TailscaleIPs {
		ip = ip.Unmap()
		if !tsaddr.IsTailscaleIP(ip) || ip.Zone() != "" || network == "tcp4" && !ip.Is4() || network == "tcp6" && !ip.Is6() {
			continue
		}
		if !target.IsValid() || ip.Is4() {
			target = ip
		}
	}
	return target
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
