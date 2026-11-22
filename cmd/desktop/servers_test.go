package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopServerRetryOnlySwitchesTheOriginalFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		change     func(*panel, *desktopServerChat, string)
		switchMode bool
	}{
		{name: "still failing", switchMode: true},
		{name: "recovered", change: func(p *panel, _ *desktopServerChat, id string) {
			p.recordDesktopServerState(id, "connected")
		}},
		{name: "recovered then failed again", change: func(p *panel, _ *desktopServerChat, id string) {
			p.recordDesktopServerState(id, "connected")
			p.recordDesktopServerState(id, "error")
		}},
		{name: "manual reconnect", change: func(_ *panel, chat *desktopServerChat, _ string) {
			chat.setStatus("connecting")
		}},
		{name: "reconnect failed again", change: func(_ *panel, chat *desktopServerChat, _ string) {
			chat.setStatus("connecting")
			chat.setStatus("error")
		}},
		{name: "replacement page", change: func(p *panel, _ *desktopServerChat, id string) {
			p.serverChats[id] = &desktopServerChat{status: "error"}
		}},
		{name: "changed settings", change: func(p *panel, _ *desktopServerChat, _ string) {
			p.servers.Entries[1].Automatic = false
		}},
		{name: "another selected server", switchMode: true, change: func(p *panel, _ *desktopServerChat, _ string) {
			p.servers.Selected = localDesktopServer
		}},
		{name: "deleted server", change: func(p *panel, _ *desktopServerChat, id string) {
			p.servers, _ = p.servers.remove(id)
			delete(p.serverChats, id)
		}},
		{name: "quitting", change: func(p *panel, _ *desktopServerChat, _ string) {
			p.quitting = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			profiles, err := defaultDesktopServers().save(desktopServer{
				Direct: "https://public.example", Tailscale: "https://home.example.ts.net", Enabled: true, Automatic: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			server := profiles.Entries[1]
			profiles.Selected = server.ID
			chat := &desktopServerChat{status: "connected", retrying: true}
			p := &panel{
				servers: profiles, serversPath: filepath.Join(t.TempDir(), "profiles.json"),
				serverChats: map[string]*desktopServerChat{server.ID: chat},
			}
			p.recordDesktopServerState(server.ID, "error")
			revision := chat.statusRevision
			if test.change != nil {
				test.change(p, chat, server.ID)
			}
			if err := writeDesktopServers(p.serversPath, p.servers); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(p.serversPath)
			if err != nil {
				t.Fatal(err)
			}
			currentChat := p.serverChats[server.ID]
			if switched := p.applyDesktopServerRetry(server, chat, revision, "tailscale"); switched != test.switchMode {
				t.Fatalf("mode switched = %v, want %v", switched, test.switchMode)
			}
			if chat.retrying {
				t.Fatal("completed probe kept its retry in flight")
			}
			if test.switchMode {
				restored, err := readDesktopServers(p.serversPath)
				if err != nil || restored.Entries[1].Mode != "tailscale" || p.servers.Entries[1].Mode != "tailscale" || p.serverChats[server.ID] != nil {
					t.Fatal("successful fallback did not persist the new route and retire its failed page", restored, err)
				}
			} else {
				after, err := os.ReadFile(p.serversPath)
				if err != nil || string(after) != string(before) || p.serverChats[server.ID] != currentChat {
					t.Fatal("stale probe changed the profile or discarded the current workspace", err)
				}
			}
		})
	}
}

func TestDesktopServerBackgroundSnapshotsKeepTheirOwnIdentity(t *testing.T) {
	p := &panel{
		servers: desktopServers{Selected: localDesktopServer, Entries: []desktopServer{
			{ID: localDesktopServer, Enabled: true},
			{ID: "remote-1234", Enabled: true},
			{ID: "disabled-1234"},
		}},
		serverTabTransition: &desktopTabTransition{From: "remote-1234", StartedAt: 42},
		serverChats: map[string]*desktopServerChat{
			localDesktopServer: {status: "connected"},
			"remote-1234":      {status: "login"},
			"disabled-1234":    {status: "connected"},
		},
	}
	for _, id := range []string{localDesktopServer, "remote-1234"} {
		tabs := p.desktopServerTabsFor(id)
		if tabs.Selected != id || tabs.Entries[0].Status != "connected" || tabs.Entries[1].Status != "login" || tabs.Entries[2].Status != "disconnected" {
			t.Fatal("page received another server's identity or status", tabs)
		}
		if id == localDesktopServer {
			if tabs.Selection != "local:42" || tabs.Transition != p.serverTabTransition {
				t.Fatal("foreground lost its presentation transition", tabs)
			}
		} else if tabs.Selection != id+":0" || tabs.Transition != nil {
			t.Fatal("background page received a foreground presentation token", tabs)
		}
	}
	if p.selectedDesktopServer() != localDesktopServer || p.desktopSelectionToken() != "local:42" {
		t.Fatal("background snapshot changed the foreground selection")
	}
}

func TestDesktopServerBackgroundRetryPreservesDisabledSelectedPage(t *testing.T) {
	profiles, err := defaultDesktopServers().save(desktopServer{
		Direct: "https://public.example/", Tailscale: "https://home.example.ts.net/", Enabled: true, Automatic: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := profiles.Entries[1]
	profiles, err = profiles.save(desktopServer{Direct: "https://disabled.example/", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	disabled := profiles.Entries[2]
	profiles.Selected = disabled.ID
	foreground := &desktopServerChat{status: "disconnected", pageStatus: "disconnected", loaded: true}
	failing := &desktopServerChat{status: "error", pageStatus: "error"}
	p := &panel{
		servers: profiles, serversPath: filepath.Join(t.TempDir(), "profiles.json"),
		serverChats:    map[string]*desktopServerChat{remote.ID: failing, disabled.ID: foreground},
		serverDialogID: disabled.ID,
	}
	if !p.applyDesktopServerRetry(remote, failing, failing.statusRevision, "tailscale") {
		t.Fatal("background fallback did not apply")
	}
	if p.serverChats[disabled.ID] != foreground || !foreground.loaded || p.selectedDesktopServer() != disabled.ID {
		t.Fatal("background fallback discarded the selected disabled page")
	}
	if tabs := p.desktopServerTabs(); tabs.Selected != disabled.ID || tabs.Entries[2].Status != "disconnected" || p.serverDialogSnapshot().Status != "disconnected" {
		t.Fatal("disabled foreground lost its connection controls", tabs)
	}
	// Enabling that same server must still replace its disconnected document.
	disabled.Enabled = true
	next, err := p.servers.save(disabled)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.setDesktopServers(next); err != nil {
		t.Fatal(err)
	}
	if p.serverChats[disabled.ID] != nil {
		t.Fatal("enabling a disabled server retained its disconnected page")
	}
}
