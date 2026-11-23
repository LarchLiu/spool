package main

import (
	"context"
	"time"
)

// Signed-in pages have a WebSocket heartbeat. Signed-out pages need a native
// availability check so a cached or suspended login page cannot stay blue after
// its server goes offline. Initial failures are probed too so startup can recover
// without selecting the server. Authenticated sockets own their reconnection.
func (p *panel) checkDesktopServerHealth() {
	if p.quitting || p.serverNetworks == nil {
		return
	}
	p.checkDesktopFederationHealth(time.Now())
	for id, chat := range p.serverChats {
		server, ok := p.servers.find(id)
		if !ok || !server.Enabled || chat.authenticated && !chat.failed || chat.pageStatus != "login" && chat.pageStatus != "error" || chat.healthChecking || time.Since(chat.lastHealthCheck) < 15*time.Second {
			continue
		}
		chat.lastHealthCheck = time.Now()
		if id == localDesktopServer {
			address, err := p.service.LocalWebURL()
			if err != nil {
				p.applyDesktopServerHealth(server, chat, chat.statusRevision, false)
				continue
			}
			server.Address, server.Mode = address, "direct"
		}
		chat.healthChecking = true
		revision := chat.statusRevision
		p.serverNetworks.run(func(lifetime context.Context) {
			ctx, cancel := context.WithTimeout(lifetime, 4*time.Second)
			reachable := p.serverNetworks.reachable(ctx, server)
			cancel()
			p.window.Update(func() { p.applyDesktopServerHealth(server, chat, revision, reachable) })
		})
	}
}

func (p *panel) applyDesktopServerHealth(server desktopServer, chat *desktopServerChat, revision uint64, reachable bool) {
	chat.healthChecking = false
	current, exists := p.servers.find(server.ID)
	if p.quitting || !exists || !current.Enabled || p.serverChats[server.ID] != chat || chat.authenticated && !chat.failed || chat.pageStatus != "login" && chat.pageStatus != "error" || chat.statusRevision != revision {
		return
	}
	if server.ID != localDesktopServer && desktopNetworkKey(current) != desktopNetworkKey(server) {
		return
	}
	if reachable && (chat.pageStatus == "error" || chat.failed) {
		// Reachability cannot establish the account state. Reload the bundled
		// page to validate its saved session and start the authenticated socket.
		chat.failed = true
		if p.window != nil {
			if err := p.loadDesktopServerChat(server.ID, chat.address); err != nil {
				p.recordDesktopServerState(server.ID, "error")
			}
		}
		return
	}
	chat.healthFailed = !reachable
	status := "login"
	if !reachable {
		status = "error"
	}
	p.updateDesktopServerState(server.ID, status)
}
