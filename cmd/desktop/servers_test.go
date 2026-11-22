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
		{name: "another selected server", change: func(p *panel, _ *desktopServerChat, _ string) {
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
