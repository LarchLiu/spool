package spoolnet

import "tailscale.com/net/netmon"

func init() {
	netmon.RegisterInterfaceGetter(androidInterfaces)
}
