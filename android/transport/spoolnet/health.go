package spoolnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"syscall"
	"time"

	"tailscale.com/ipn/ipnstate"
)

type healthAPI interface {
	StatusWithoutPeers(context.Context) (*ipnstate.Status, error)
}

type healthSnapshot struct {
	State   string `json:"state"`
	AuthURL string `json:"authURL,omitempty"`
	HTTPS   string `json:"https"`
	Problem string `json:"problem,omitempty"`
	Message string `json:"message,omitempty"`
}

// Health checks backend state and the selected HTTPS endpoint independently of
// DISCO/path diagnostics. One foreground sample takes at most 11 seconds; the
// Android supervisor owns backoff and recovery, rather than nesting retry loops.
func (c *Client) Health() (string, error) {
	var api healthAPI
	if !c.direct {
		api = c.node
	}
	info := checkHealth(c.ctx, api, c.probeOnce)
	result, err := json.Marshal(info)
	return string(result), err
}

func checkHealth(ctx context.Context, api healthAPI, probe func(context.Context) error) healthSnapshot {
	info := healthSnapshot{State: "Unknown", HTTPS: "Not checked"}
	if api == nil {
		info.State = "Running"
	} else {
		statusCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		status, err := api.StatusWithoutPeers(statusCtx)
		cancel()
		if err != nil || status == nil {
			info.Problem = "status"
			info.Message = "Embedded node status is unavailable"
			if err != nil {
				info.Message = fmt.Sprintf("%s: %v", info.Message, err)
			}
		} else {
			info.State, info.AuthURL = status.BackendState, status.AuthURL
		}
		if status != nil && err == nil && info.State != "Running" {
			switch info.State {
			case "NeedsLogin":
				info.Problem = "login"
			case "NeedsMachineAuth":
				info.Problem = "approval"
			default:
				info.Problem = "starting"
			}
			return info
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	err := probe(probeCtx)
	cancel()
	if err == nil {
		info.HTTPS = "Verified"
		info.Problem, info.Message = "", ""
		return info
	}
	statusUnavailable := info.Problem == "status"
	info.HTTPS, info.Message = "Failed", err.Error()
	info.Problem = "endpoint"
	if transientProbeError(err) {
		info.Problem = "network"
		if statusUnavailable {
			info.Problem = "status"
		}
	}
	// An HTTP response proves the HTTPS path worked. Server maintenance or
	// overload needs another health GET, not a reset of the embedded node.
	var statusError healthStatusError
	if errors.As(err, &statusError) {
		info.Problem = "endpoint"
		if transientProbeError(err) {
			info.Problem = "server"
		}
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		info.Problem = "server"
	}
	return info
}
