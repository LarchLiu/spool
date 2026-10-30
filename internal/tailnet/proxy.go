package tailnet

import (
	"spool/internal/netproxy"

	"tailscale.com/net/tshttpproxy"
)

// The SDK owns its transports, so connect its supported hook to the same
// application policy used by update checks and other built-in HTTP clients.
var proxyInitError = tshttpproxy.SetProxyFunc(netproxy.Resolve)
