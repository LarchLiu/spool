package spoolnet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
)

type peerStatusFunc func(context.Context) (*ipnstate.Status, error)

func (f peerStatusFunc) Status(ctx context.Context) (*ipnstate.Status, error) { return f(ctx) }

func TestTailnetDialPinsSelectedPeerAndNeverPassesHostnameToDialer(t *testing.T) {
	var calls int
	d := tailnetDialer{
		api:       peerStatusFunc(func(context.Context) (*ipnstate.Status, error) { return serverStatus(), nil }),
		authority: "spool.example.ts.net:8443",
		dialTCP: func(_ context.Context, target netip.AddrPort) (net.Conn, error) {
			calls++
			if target.String() != "100.64.0.3:8443" {
				t.Fatalf("did not pin selected peer and port: %s", target)
			}
			return nil, syscall.ECONNRESET
		},
	}
	if _, err := d.DialContext(context.Background(), "tcp", "SPOOL.example.ts.net:8443"); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("lost pinned dial error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("retried or fell back from netstack: %d dials", calls)
	}
	for _, target := range []string{"spool.example.ts.net:443", "other.example.ts.net:8443", "198.18.1.64:8443", "100.64.0.3:8443"} {
		if _, err := d.DialContext(context.Background(), "tcp", target); err == nil {
			t.Errorf("accepted a different origin: %s", target)
		}
	}
	if _, err := d.DialContext(context.Background(), "udp", d.authority); err == nil || calls != 1 {
		t.Fatal("unsafe requests reached the dialer")
	}
}

func TestTailnetDialWaitsForSelectedPeerInsteadOfUsingSystemDNS(t *testing.T) {
	var statuses, dials int
	d := tailnetDialer{
		api: peerStatusFunc(func(context.Context) (*ipnstate.Status, error) {
			statuses++
			if statuses == 1 {
				return &ipnstate.Status{BackendState: "Running"}, nil
			}
			return serverStatus(), nil
		}),
		authority: "spool.example.ts.net:443",
		dialTCP: func(_ context.Context, target netip.AddrPort) (net.Conn, error) {
			dials++
			if statuses != 2 || target.String() != "100.64.0.3:443" {
				t.Fatalf("dialed before selected peer was ready: %d, %s", statuses, target)
			}
			return nil, syscall.ECONNREFUSED
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := d.DialContext(ctx, "tcp", d.authority); !errors.Is(err, syscall.ECONNREFUSED) || dials != 1 {
		t.Fatalf("unexpected dial result: %d dials, %v", dials, err)
	}
}

func TestTailnetDialFailsClosedOnUnavailableOrUnsafePeers(t *testing.T) {
	for _, name := range []string{"missing status", "starting", "unknown peer", "ambiguous peer", "fake IP", "public IP", "LAN IP", "wrong family"} {
		t.Run(name, func(t *testing.T) {
			status := serverStatus()
			peer := serverPeer(status, "spool.example.ts.net")
			network := "tcp"
			switch name {
			case "missing status":
				status = nil
			case "starting":
				status.BackendState = "Starting"
			case "unknown peer":
				peer.DNSName = "different.example.ts.net."
			case "ambiguous peer":
				status.Peer[key.NewNode().Public()] = &ipnstate.PeerStatus{DNSName: peer.DNSName, TailscaleIPs: peer.TailscaleIPs}
			case "fake IP":
				peer.TailscaleIPs = []netip.Addr{netip.MustParseAddr("198.18.1.64")}
			case "public IP":
				peer.TailscaleIPs = []netip.Addr{netip.MustParseAddr("203.0.113.7")}
			case "LAN IP":
				peer.TailscaleIPs = []netip.Addr{netip.MustParseAddr("192.168.1.7")}
			case "wrong family":
				peer.TailscaleIPs = []netip.Addr{netip.MustParseAddr("fd7a:115c:a1e0::3")}
				network = "tcp4"
			}
			d := tailnetDialer{
				api:       peerStatusFunc(func(context.Context) (*ipnstate.Status, error) { return status, nil }),
				authority: "spool.example.ts.net:443",
				dialTCP: func(context.Context, netip.AddrPort) (net.Conn, error) {
					t.Fatal("missing or unsafe peer reached the network")
					return nil, nil
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if _, err := d.DialContext(ctx, network, d.authority); !errors.Is(err, errServerNotReady) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("missing readiness/deadline error: %v", err)
			}
		})
	}
}

func TestTailnetDialCancellationAndStatusErrorsNeverDial(t *testing.T) {
	want := errors.New("local status unavailable")
	var statuses int
	d := tailnetDialer{
		api:       peerStatusFunc(func(context.Context) (*ipnstate.Status, error) { statuses++; return nil, want }),
		authority: "spool.example.ts.net:443",
		dialTCP: func(context.Context, netip.AddrPort) (net.Conn, error) {
			t.Fatal("dialed without a peer")
			return nil, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.DialContext(ctx, "tcp", d.authority); !errors.Is(err, context.Canceled) || statuses != 0 {
		t.Fatalf("canceled dial did work: %d, %v", statuses, err)
	}
	if _, err := d.DialContext(context.Background(), "tcp", d.authority); !errors.Is(err, want) || statuses != 1 {
		t.Fatalf("lost status error: %d, %v", statuses, err)
	}
}

func TestClientCloseCancelsTransportDetachedReadinessAndTCPDial(t *testing.T) {
	for _, when := range []string{"waiting for peer", "dialing TCP"} {
		t.Run(when, func(t *testing.T) {
			lifetime, closeClient := context.WithCancel(context.Background())
			defer closeClient()
			d := tailnetDialer{
				api: peerStatusFunc(func(context.Context) (*ipnstate.Status, error) {
					if when == "waiting for peer" {
						closeClient()
						return nil, nil
					}
					return serverStatus(), nil
				}),
				authority: "spool.example.ts.net:443",
				lifetime:  lifetime,
				dialTCP: func(ctx context.Context, _ netip.AddrPort) (net.Conn, error) {
					if when != "dialing TCP" {
						t.Fatal("dialed before peer was known")
					}
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 20*time.Second {
						t.Error("detached transport dial has no deadline")
					}
					closeClient()
					<-ctx.Done()
					return nil, ctx.Err()
				},
			}
			if _, err := d.DialContext(context.Background(), "tcp", d.authority); !errors.Is(err, context.Canceled) {
				t.Fatalf("client close did not cancel detached transport dial: %v", err)
			}
		})
	}
}

func TestServerIPFiltersUnsafeAddressesAndPreservesAddressFamily(t *testing.T) {
	peer := &ipnstate.PeerStatus{TailscaleIPs: []netip.Addr{
		{}, netip.MustParseAddr("198.18.1.64"), netip.MustParseAddr("fd7a:115c:a1e0::3"), netip.MustParseAddr("100.64.0.3"),
	}}
	for network, want := range map[string]string{"tcp": "100.64.0.3", "tcp4": "100.64.0.3", "tcp6": "fd7a:115c:a1e0::3"} {
		if got := serverIP(peer, network).String(); got != want {
			t.Errorf("%s: got %s, want %s", network, got, want)
		}
	}
}

func TestPinnedTailnetIPPreservesHTTPSHostnameSNIAndAPIOrigin(t *testing.T) {
	var requests, dials int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Host != "example.com:8443" || r.TLS.ServerName != "example.com" {
			t.Errorf("pinned IP replaced HTTPS identity: host=%q SNI=%q", r.Host, r.TLS.ServerName)
		}
		if r.URL.Path != "/healthz" && r.Header.Get("Origin") != "https://example.com:8443" {
			t.Error("API lost exact origin")
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)
	status := serverStatus()
	serverPeer(status, "spool.example.ts.net").DNSName = "example.com."
	d := tailnetDialer{
		api:       peerStatusFunc(func(context.Context) (*ipnstate.Status, error) { return status, nil }),
		authority: "example.com:8443",
		dialTCP: func(ctx context.Context, target netip.AddrPort) (net.Conn, error) {
			dials++
			if target.String() != "100.64.0.3:8443" {
				t.Errorf("wrong pinned address: %s", target)
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
		},
	}
	httpClient, transport := nativeHTTPClient(d.DialContext)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); transport.CloseIdleConnections() })
	origin, _ := url.Parse("https://example.com:8443")
	client := &Client{http: httpClient, transport: transport, origin: origin, ctx: ctx}
	if err := client.Probe(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Request("GET", "/api/me", "", "", "en"); err != nil || requests != 2 || dials != 1 {
		t.Fatalf("pinned transport/connection reuse failed: requests=%d dials=%d error=%v", requests, dials, err)
	}
	transport.CloseIdleConnections()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: "wrong.invalid.test"}
	if err := client.Probe(); err == nil || !strings.Contains(err.Error(), "TLS handshake") || dials != 2 {
		t.Fatalf("hostname mismatch bypassed validation or was retried: dials=%d, %v", dials, err)
	}
}
