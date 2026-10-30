package spoolnet

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"strings"
	"testing"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

type fakeConnectionAPI struct {
	status    *ipnstate.Status
	statusErr error
	ping      *ipnstate.PingResult
	pingErr   error
	target    netip.Addr
	pingType  tailcfg.PingType
}

func (f *fakeConnectionAPI) Status(context.Context) (*ipnstate.Status, error) {
	return f.status, f.statusErr
}

func (f *fakeConnectionAPI) Ping(_ context.Context, ip netip.Addr, kind tailcfg.PingType) (*ipnstate.PingResult, error) {
	f.target, f.pingType = ip, kind
	return f.ping, f.pingErr
}

func serverStatus() *ipnstate.Status {
	return &ipnstate.Status{
		BackendState: "Running", AuthURL: "https://login.tailscale.com/a/secret",
		Peer: map[key.NodePublic]*ipnstate.PeerStatus{
			key.NewNode().Public(): {DNSName: "other.example.ts.net.", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.2")}, CurAddr: "192.0.2.2:1234"},
			key.NewNode().Public(): {DNSName: "spool.example.ts.net.", TailscaleIPs: []netip.Addr{netip.MustParseAddr("fd7a:115c:a1e0::3"), netip.MustParseAddr("100.64.0.3")}, Relay: "hkg"},
		},
	}
}

func TestConnectionPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		ping *ipnstate.PingResult
		path string
	}{
		{"direct", &ipnstate.PingResult{Endpoint: "192.0.2.3:1234", LatencySeconds: 0.01234}, "direct"},
		{"derp", &ipnstate.PingResult{DERPRegionID: 20, DERPRegionCode: "hkg", LatencySeconds: 0.0481}, "derp"},
		{"peer relay", &ipnstate.PingResult{PeerRelay: "192.0.2.4:1234:vni:1", LatencySeconds: 0.02}, "peer-relay"},
		{"zero latency", &ipnstate.PingResult{Endpoint: "192.0.2.3:1234"}, "direct"},
		{"unknown", &ipnstate.PingResult{}, "unknown"},
		{"failure", &ipnstate.PingResult{Err: "unreachable", Endpoint: "192.0.2.3:1234"}, "unknown"},
		{"missing", nil, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeConnectionAPI{status: serverStatus(), ping: tc.ping}
			info, err := inspectConnection(context.Background(), api, "SPOOL.example.ts.net")
			if err != nil || info.Path != tc.path || info.State != "Running" || info.SampledAtMS == 0 {
				t.Fatalf("unexpected connection: %+v, %v", info, err)
			}
			if api.target.String() != "100.64.0.3" || api.pingType != tailcfg.PingDisco {
				t.Fatalf("probed the wrong node or ping type: %v, %s", api.target, api.pingType)
			}
			if tc.path == "unknown" && info.LatencyMS != nil {
				t.Fatal("failed or incomplete probe retained a latency")
			}
			if tc.path != "unknown" && info.LatencyMS == nil {
				t.Fatal("successful probe lost its latency")
			}
			if tc.path == "direct" && tc.name == "direct" && *info.LatencyMS != 12.3 {
				t.Fatalf("incorrect latency conversion: %v", *info.LatencyMS)
			}
			if (tc.path == "derp") != (info.Relay == "hkg") {
				t.Fatal("home DERP was confused with an actual relayed path")
			}
			encoded, err := json.Marshal(info)
			if err != nil || strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "192.0.2") {
				t.Fatalf("diagnostics leaked auth URLs or public endpoints: %s, %v", encoded, err)
			}
		})
	}
}

func TestConnectionDoesNotGuessFromOtherPeers(t *testing.T) {
	for _, hostname := range []string{"alias.example.com", "spool", "spool.example.ts.net.attacker.test"} {
		api := &fakeConnectionAPI{status: serverStatus()}
		info, err := inspectConnection(context.Background(), api, hostname)
		if err != nil || info.Reason != "target_unknown" || api.target.IsValid() {
			t.Fatalf("guessed a route for %q: %+v, %v", hostname, info, err)
		}
	}
}

func TestConnectionWaitingAndFailure(t *testing.T) {
	api := &fakeConnectionAPI{status: serverStatus()}
	api.status.BackendState = "NeedsLogin"
	info, err := inspectConnection(context.Background(), api, "spool.example.ts.net")
	if err != nil || info.Reason != "not_running" || api.target.IsValid() {
		t.Fatalf("probed before enrollment: %+v, %v", info, err)
	}
	api.status.BackendState = "Running"
	api.pingErr = context.DeadlineExceeded
	info, err = inspectConnection(context.Background(), api, "spool.example.ts.net")
	if err != nil || info.Path != "unknown" || info.Reason != "probe_timeout" || info.LatencyMS != nil {
		t.Fatalf("timeout reported a confirmed path: %+v, %v", info, err)
	}
	api.pingErr = errors.New("offline")
	info, err = inspectConnection(context.Background(), api, "spool.example.ts.net")
	if err != nil || info.Reason != "probe_failed" {
		t.Fatalf("unexpected probe error: %+v, %v", info, err)
	}
	api.status = nil
	info, err = inspectConnection(context.Background(), api, "spool.example.ts.net")
	if err != nil || info.Reason != "status_unavailable" {
		t.Fatalf("unexpected missing status: %+v, %v", info, err)
	}
	api.statusErr = errors.New("status failed")
	if _, err := inspectConnection(context.Background(), api, "spool.example.ts.net"); !errors.Is(err, api.statusErr) {
		t.Fatal("lost local API error")
	}
}

func TestConnectionRejectsInvalidLatency(t *testing.T) {
	for _, latency := range []float64{-1, math.NaN(), math.Inf(1), math.MaxFloat64} {
		info := connectionSnapshot{Path: "unknown"}
		applyPingResult(&info, &ipnstate.PingResult{Endpoint: "192.0.2.3:1234", LatencySeconds: latency})
		if info.LatencyMS != nil {
			t.Fatalf("accepted invalid latency %v", latency)
		}
		if _, err := json.Marshal(info); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConnectionDropsStalePathAndLatency(t *testing.T) {
	info := connectionSnapshot{Path: "unknown"}
	applyPingResult(&info, &ipnstate.PingResult{DERPRegionID: 20, DERPRegionCode: "hkg", LatencySeconds: 0.04})
	applyPingResult(&info, &ipnstate.PingResult{Err: "timeout"})
	if info.Path != "unknown" || info.Relay != "" || info.LatencyMS != nil {
		t.Fatalf("failed probe retained an old confirmed path: %+v", info)
	}
}
