package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesktopServerSignedOutHealthTracksFailureAndRecovery(t *testing.T) {
	server := desktopServer{ID: "remote-1234", Enabled: true, Mode: "direct", Address: "https://home.example/"}
	chat := &desktopServerChat{}
	p := &panel{
		servers:     desktopServers{Entries: []desktopServer{server}, Selected: localDesktopServer},
		serverChats: map[string]*desktopServerChat{server.ID: chat},
	}
	p.recordDesktopServerState(server.ID, "login")
	for _, reachable := range []bool{false, true} {
		p.applyDesktopServerHealth(server, chat, chat.statusRevision, reachable)
		want := "error"
		if reachable {
			want = "login"
		}
		// Cached readiness hints carry page state, not a newer network probe.
		p.recordDesktopServerState(server.ID, "login")
		if got := p.desktopServerTabs().Entries[0].Status; got != want {
			t.Fatalf("reachable %v: tab status = %q, want %q", reachable, got, want)
		}
		p.serverDialogID = server.ID
		if got := p.serverDialogSnapshot().Status; got != want {
			t.Fatalf("reachable %v: dialog status = %q, want %q", reachable, got, want)
		}
		if p.selectedDesktopServer() != localDesktopServer {
			t.Fatal("background health check selected its server")
		}
	}
	p.recordDesktopServerState(server.ID, "connected")
	if chat.healthFailed || p.desktopServerTabs().Entries[0].Status != "connected" {
		t.Fatal("authenticated connection did not replace signed-out health")
	}
}

func TestDesktopServerHealthIgnoresRetiredProbes(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*panel, *desktopServerChat, string)
	}{
		{"signed in", func(p *panel, _ *desktopServerChat, id string) { p.recordDesktopServerState(id, "connected") }},
		{"new login state", func(p *panel, _ *desktopServerChat, id string) {
			p.recordDesktopServerState(id, "connecting")
			p.recordDesktopServerState(id, "login")
		}},
		{"disabled", func(p *panel, _ *desktopServerChat, _ string) { p.servers.Entries[0].Enabled = false }},
		{"new destination", func(p *panel, _ *desktopServerChat, _ string) {
			p.servers.Entries[0].Address = "https://other.example/"
		}},
		{"replaced page", func(p *panel, _ *desktopServerChat, id string) {
			p.serverChats[id] = &desktopServerChat{status: "login", pageStatus: "login"}
		}},
		{"failed renderer", func(p *panel, chat *desktopServerChat, id string) {
			chat.failed = true
			p.recordDesktopServerState(id, "error")
		}},
		{"deleted", func(p *panel, _ *desktopServerChat, _ string) { p.servers.Entries = nil }},
		{"quitting", func(p *panel, _ *desktopServerChat, _ string) { p.quitting = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := desktopServer{ID: "remote-1234", Enabled: true, Mode: "direct", Address: "https://home.example/"}
			chat := &desktopServerChat{status: "login", pageStatus: "login", healthChecking: true}
			p := &panel{servers: desktopServers{Entries: []desktopServer{server}}, serverChats: map[string]*desktopServerChat{server.ID: chat}}
			revision := chat.statusRevision
			test.change(p, chat, server.ID)
			status := chat.status
			p.applyDesktopServerHealth(server, chat, revision, false)
			if chat.status != status || chat.healthFailed || chat.healthChecking {
				t.Fatal("retired probe changed its page or kept the check in flight", chat)
			}
		})
	}
}

func TestDesktopServerHealthLeavesDisabledAndSocketManagedPagesAlone(t *testing.T) {
	networks := newDesktopServerNetworks(t.TempDir())
	t.Cleanup(networks.close)
	server := desktopServer{ID: "remote-1234", Enabled: true, Mode: "direct", Address: "https://home.example/"}
	p := &panel{servers: desktopServers{Entries: []desktopServer{server}}, serverNetworks: networks}
	p.checkDesktopServerHealth()
	if len(p.serverChats) != 0 {
		t.Fatal("health check opened a previously unopened server")
	}
	for _, status := range []string{"disconnected", "connecting", "connected", "error", "login"} {
		chat := &desktopServerChat{status: status, pageStatus: status, authenticated: status == "error"}
		p.serverChats = map[string]*desktopServerChat{server.ID: chat}
		p.servers.Entries[0].Enabled = status != "login"
		p.checkDesktopServerHealth()
		if chat.healthChecking || !chat.lastHealthCheck.IsZero() {
			t.Fatalf("health check started an unnecessary connection for %q", status)
		}
	}
}

func TestDesktopServerSocketRetainsReconnectOwnershipDuringInterruption(t *testing.T) {
	chat := &desktopServerChat{}
	p := &panel{serverChats: map[string]*desktopServerChat{"remote-1234": chat}}
	for _, status := range []string{"connected", "error", "connecting", "connected"} {
		p.recordDesktopServerState("remote-1234", status)
		if !chat.authenticated {
			t.Fatalf("socket lost reconnection ownership after %q", status)
		}
	}
	p.recordDesktopServerState("remote-1234", "login")
	if chat.authenticated {
		t.Fatal("signed-out page retained socket ownership")
	}
}

func TestDesktopServerFirstSocketFailureKeepsItsAuthenticatedSession(t *testing.T) {
	networks := newDesktopServerNetworks(t.TempDir())
	t.Cleanup(networks.close)
	server := desktopServer{ID: "remote-1234", Enabled: true, Mode: "direct", Address: "https://home.example/"}
	chat := &desktopServerChat{}
	p := &panel{
		servers: desktopServers{Entries: []desktopServer{server}}, serverNetworks: networks,
		serverChats: map[string]*desktopServerChat{server.ID: chat},
	}
	// A probe may already be in flight when /me restores the session, before
	// the first WebSocket has ever delivered ready.
	p.recordDesktopServerState(server.ID, "error")
	oldRevision := chat.statusRevision
	for _, status := range []string{"error", "connecting", "error"} {
		p.recordDesktopServerConnection(server.ID, desktopServerConnection{State: status, Authenticated: true})
		p.checkDesktopServerHealth()
		p.applyDesktopServerHealth(server, chat, oldRevision, true)
		p.applyDesktopServerHealth(server, chat, chat.statusRevision, true)
		if !chat.authenticated || chat.failed || chat.healthChecking || !chat.lastHealthCheck.IsZero() || chat.status != status {
			t.Fatalf("health probe replaced the first socket's %q state: %+v", status, chat)
		}
	}
	if chat.statusRevision == oldRevision {
		t.Fatal("session restoration did not retire a signed-out probe")
	}
	p.recordDesktopServerConnection(server.ID, desktopServerConnection{State: "login"})
	p.applyDesktopServerHealth(server, chat, chat.statusRevision, false)
	if chat.authenticated || chat.status != "error" || !chat.healthFailed {
		t.Fatal("signed-out page did not return to native availability checks", chat)
	}
}

func TestDesktopServerFailedPageCannotReportAHealthyConnection(t *testing.T) {
	chat := &desktopServerChat{failed: true, status: "error"}
	p := &panel{serverChats: map[string]*desktopServerChat{"remote-1234": chat}}
	for _, status := range []string{"login", "connected", "connecting"} {
		p.recordDesktopServerState("remote-1234", status)
		if chat.status != "error" {
			t.Fatalf("failed page reported %q", status)
		}
	}
}

func TestDesktopServerAvailabilityUsesHealthEndpointWithoutCredentials(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusServiceUnavailable, http.StatusUnauthorized} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" || r.Method != "GET" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Errorf("unexpected availability request: %s %s, headers %v", r.Method, r.URL.Path, r.Header)
				}
				w.WriteHeader(code)
			}))
			t.Cleanup(upstream.Close)
			networks := newDesktopServerNetworks(t.TempDir())
			t.Cleanup(networks.close)
			if got := networks.reachable(context.Background(), desktopServer{Address: upstream.URL, Mode: "direct"}); got != (code == http.StatusOK) {
				t.Fatalf("HTTP %d: reachable = %v", code, got)
			}
		})
	}
}
