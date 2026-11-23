package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"spool/android/transport/spoolnet"
)

// Reuse Android's pinned tsnet dialer, relay pairing, device trust and admission
// rotation. Each desktop profile owns separate node and device directories.
type desktopServerNetworks struct {
	mu       sync.Mutex
	root     string
	closed   bool
	clients  map[string]*spoolnet.Client
	revision atomic.Uint64
	ctx      context.Context
	cancel   context.CancelFunc
	workers  sync.WaitGroup
}

func newDesktopServerNetworks(root string) *desktopServerNetworks {
	ctx, cancel := context.WithCancel(context.Background())
	return &desktopServerNetworks{root: root, ctx: ctx, cancel: cancel}
}

func (n *desktopServerNetworks) run(fn func(context.Context)) {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	n.workers.Add(1)
	n.mu.Unlock()
	go func() { defer n.workers.Done(); fn(n.ctx) }()
}

func desktopNetworkKey(server desktopServer) string {
	return server.connectionID() + "\x00" + server.Mode + "\x00" + server.Address + "\x00" + server.Relay
}

func (server desktopServer) connectionID() string {
	if server.ConnectionID != "" {
		return server.ConnectionID
	}
	return server.ID
}

func (n *desktopServerNetworks) directory(id, mode string) string {
	return filepath.Join(n.root, id, mode)
}

func (n *desktopServerNetworks) client(server desktopServer) (*spoolnet.Client, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil, errors.New("server connections are closed")
	}
	key := desktopNetworkKey(server)
	if client := n.clients[key]; client != nil {
		return client, nil
	}
	for previous, client := range n.clients {
		if strings.HasPrefix(previous, server.connectionID()+"\x00"+server.Mode+"\x00") {
			_ = client.Close()
			delete(n.clients, previous)
		}
	}
	var client *spoolnet.Client
	var err error
	switch server.Mode {
	case "tailscale":
		client, err = spoolnet.NewClientForDevice(n.directory(server.connectionID(), "tailscale"), server.Address, "Desktop")
	case "relay":
		client, err = spoolnet.NewRelayClient(n.directory(server.connectionID(), "relay"), server.Relay)
	default:
		return nil, errors.New("native transport is only used for Tailscale and Relay")
	}
	if err != nil {
		return nil, err
	}
	if n.clients == nil {
		n.clients = make(map[string]*spoolnet.Client)
	}
	n.clients[key] = client
	return client, nil
}

func (n *desktopServerNetworks) retain(profiles desktopServers, revision uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed || revision != n.revision.Load() {
		return
	}
	keep := map[string]bool{}
	for _, server := range profiles.Entries {
		if server.Enabled {
			for _, mode := range []string{"direct", "tailscale", "relay"} {
				connection := server.connection(mode)
				if connection.Address != "" {
					keep[desktopNetworkKey(connection)] = true
				}
			}
		}
	}
	for key, client := range n.clients {
		if !keep[key] {
			_ = client.Close()
			delete(n.clients, key)
		}
	}
}

func (n *desktopServerNetworks) update(profiles desktopServers) {
	revision := n.revision.Add(1)
	n.run(func(context.Context) { n.retain(profiles, revision) })
}

func (n *desktopServerNetworks) networkChanged(server desktopServer) {
	n.run(func(context.Context) {
		n.mu.Lock()
		client := n.clients[desktopNetworkKey(server)]
		n.mu.Unlock()
		if client != nil {
			client.NetworkChanged()
		}
	})
}

func (n *desktopServerNetworks) close() {
	n.mu.Lock()
	n.closed = true
	if n.cancel != nil {
		n.cancel()
	}
	for key, client := range n.clients {
		_ = client.Close()
		delete(n.clients, key)
	}
	n.mu.Unlock()
	n.workers.Wait()
}

type desktopNetworkTransport struct {
	networks *desktopServerNetworks
	server   desktopServer
}

func (t desktopNetworkTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	client, err := t.networks.client(t.server)
	if err != nil {
		return nil, err
	}
	return client.RoundTrip(r)
}

func (n *desktopServerNetworks) reachable(ctx context.Context, server desktopServer) bool {
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(server.Address, "/")+"/healthz", nil)
	if err != nil {
		return false
	}
	var transport http.RoundTripper = http.DefaultTransport
	if server.Mode == "tailscale" || server.Mode == "relay" {
		transport = desktopNetworkTransport{n, server}
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode == http.StatusOK
}
