package spoolnet

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"strings"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

type connectionAPI interface {
	Status(context.Context) (*ipnstate.Status, error)
	Ping(context.Context, netip.Addr, tailcfg.PingType) (*ipnstate.PingResult, error)
}

type connectionSnapshot struct {
	State       string   `json:"state"`
	Path        string   `json:"path"`
	Reason      string   `json:"reason,omitempty"`
	TargetIP    string   `json:"targetIP,omitempty"`
	Relay       string   `json:"relay,omitempty"`
	LatencyMS   *float64 `json:"latencyMs,omitempty"`
	SampledAtMS int64    `json:"sampledAtMs"`
}

// ConnectionInfo probes only the selected server's node, not an arbitrary peer.
// It is independent of the HTTPS check and safe to call from a diagnostic worker.
func (c *Client) ConnectionInfo() (string, error) {
	ctx, cancel := context.WithTimeout(c.ctx, 4*time.Second)
	defer cancel()
	snapshot, err := inspectConnection(ctx, c.local, c.origin.Hostname())
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(snapshot)
	return string(result), err
}

func inspectConnection(ctx context.Context, api connectionAPI, hostname string) (connectionSnapshot, error) {
	info := connectionSnapshot{Path: "unknown"}
	status, err := api.Status(ctx)
	if err != nil {
		return info, err
	}
	info.SampledAtMS = time.Now().UnixMilli()
	if status == nil {
		info.Reason = "status_unavailable"
		return info, nil
	}
	info.State = status.BackendState
	if info.State != "Running" {
		info.Reason = "not_running"
		return info, nil
	}
	peer := serverPeer(status, hostname)
	if peer == nil {
		info.Reason = "target_unknown"
		return info, nil
	}
	var target netip.Addr
	for _, ip := range peer.TailscaleIPs {
		if ip.IsValid() && (!target.IsValid() || ip.Is4()) {
			target = ip
		}
	}
	if !target.IsValid() {
		info.Reason = "target_unknown"
		return info, nil
	}
	info.TargetIP = target.String()
	result, err := api.Ping(ctx, target, tailcfg.PingDisco)
	info.SampledAtMS = time.Now().UnixMilli()
	if err != nil {
		info.Reason = "probe_failed"
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			info.Reason = "probe_timeout"
		}
		return info, nil
	}
	applyPingResult(&info, result)
	return info, nil
}

func serverPeer(status *ipnstate.Status, hostname string) *ipnstate.PeerStatus {
	var found *ipnstate.PeerStatus
	for _, peer := range status.Peer {
		if peer == nil || !strings.EqualFold(strings.TrimSuffix(peer.DNSName, "."), strings.TrimSuffix(hostname, ".")) {
			continue
		}
		if found != nil {
			return nil
		}
		found = peer
	}
	return found
}

func applyPingResult(info *connectionSnapshot, result *ipnstate.PingResult) {
	info.Path, info.Reason, info.Relay, info.LatencyMS = "unknown", "", "", nil
	if result == nil || result.Err != "" {
		info.Reason = "probe_failed"
		return
	}
	// A peer's Relay field is its home DERP, not proof that traffic uses DERP.
	// Only a successful SDK ping response establishes a confirmed path/latency.
	switch {
	case result.PeerRelay != "":
		info.Path = "peer-relay"
	case result.Endpoint != "":
		info.Path = "direct"
	case result.DERPRegionID != 0:
		info.Path = "derp"
		info.Relay = result.DERPRegionCode
	default:
		info.Reason = "path_unknown"
		return
	}
	if seconds := result.LatencySeconds; seconds >= 0 && !math.IsNaN(seconds) && !math.IsInf(seconds, 0) {
		ms := math.Round(seconds*10000) / 10
		if !math.IsInf(ms, 0) {
			info.LatencyMS = &ms
		}
	}
}
