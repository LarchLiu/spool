package spoolnet

import (
	"context"
	"fmt"
	"time"

	"tailscale.com/ipn"
)

// NetworkChanged wakes tsnet's network monitor after Android publishes its
// interface snapshot. Updating the snapshot alone does not notify the engine.
func (c *Client) NetworkChanged() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.responseActivity.Store(nil)
	c.transport.CloseIdleConnections()
	if c.relay != nil {
		c.relay.NetworkChanged()
	}
	if c.server != nil {
		c.server.Sys().NetMon.Get().InjectEvent()
	}
}

type reconnectAPI interface {
	healthAPI
	EditPrefs(context.Context, *ipn.MaskedPrefs) (*ipn.Prefs, error)
}

// Reconnect bounces WantRunning on the existing app-private node. Node keys,
// enrollment, HTTPS origin and session cookies survive; API writes are never
// replayed. The foreground supervisor serializes and rate-limits this action.
func (c *Client) Reconnect() error {
	c.NetworkChanged()
	if c.direct {
		return c.ctx.Err()
	}
	return reconnectNode(c.ctx, c.local)
}

func reconnectNode(ctx context.Context, api reconnectAPI) error {
	statusCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	status, err := api.StatusWithoutPeers(statusCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("read embedded node before reconnect: %w", err)
	}
	if status != nil && (status.BackendState == "NeedsLogin" || status.BackendState == "NeedsMachineAuth") {
		return nil
	}
	if status != nil && status.BackendState == "Running" {
		downCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, _ = api.EditPrefs(downCtx, &ipn.MaskedPrefs{WantRunningSet: true, Prefs: ipn.Prefs{WantRunning: false}})
		cancel()
		// Even if the down response was lost, try to restore WantRunning.
		// Closing the client cancels both phases and prevents it coming back up.
	}
	upCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := api.EditPrefs(upCtx, &ipn.MaskedPrefs{WantRunningSet: true, Prefs: ipn.Prefs{WantRunning: true}}); err != nil {
		return fmt.Errorf("reconnect embedded node: %w", err)
	}
	return nil
}
