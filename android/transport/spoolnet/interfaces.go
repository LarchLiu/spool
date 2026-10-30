package spoolnet

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"

	"tailscale.com/net/netmon"
)

var interfaceState struct {
	sync.RWMutex
	list []netmon.Interface
}

type deviceInterface struct {
	Name      string   `json:"name"`
	Index     int      `json:"index"`
	MTU       int      `json:"mtu"`
	Addresses []string `json:"addresses"`
}

// UpdateInterfaces supplies Android LinkProperties instead of forbidden netlink queries.
// It must be called before NewClient and whenever ConnectivityManager changes.
func UpdateInterfaces(snapshot string) error {
	if len(snapshot) > 64<<10 {
		return errors.New("interface snapshot too large")
	}
	var devices []deviceInterface
	if err := json.Unmarshal([]byte(snapshot), &devices); err != nil {
		return err
	}
	if len(devices) > 64 {
		return errors.New("too many interfaces")
	}
	list := make([]netmon.Interface, 0, len(devices))
	for _, device := range devices {
		if device.Name == "" || device.Index < 1 || device.MTU < 576 || len(device.Addresses) > 64 {
			return errors.New("invalid interface")
		}
		addresses := make([]net.Addr, 0, len(device.Addresses))
		for _, address := range device.Addresses {
			ip, network, err := net.ParseCIDR(address)
			if err != nil {
				return fmt.Errorf("invalid interface address: %w", err)
			}
			network.IP = ip
			addresses = append(addresses, network)
		}
		list = append(list, netmon.Interface{
			Interface: &net.Interface{Name: device.Name, Index: device.Index, MTU: device.MTU,
				Flags: net.FlagUp | net.FlagRunning | net.FlagBroadcast | net.FlagMulticast},
			AltAddrs: addresses,
		})
	}
	interfaceState.Lock()
	interfaceState.list = list
	interfaceState.Unlock()
	return nil
}

func androidInterfaces() ([]netmon.Interface, error) {
	interfaceState.RLock()
	defer interfaceState.RUnlock()
	return append([]netmon.Interface(nil), interfaceState.list...), nil
}
