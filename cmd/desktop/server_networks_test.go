package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spool/internal/workspace"
)

func TestDesktopServersUseCanonicalWorkspaceStorage(t *testing.T) {
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(working, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := workspace.OpenService(relative)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	profiles, err := defaultDesktopServers().save(desktopServer{Direct: "https://home.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	profiles.Selected = profiles.Entries[1].ID
	path := filepath.Join(service.Store.Root(), "desktop-servers.json")
	if err := writeDesktopServers(path, profiles); err != nil {
		t.Fatal(err)
	}
	p := &panel{service: service}
	if err := p.prepareDesktopServers(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.serverNetworks.close)
	t.Cleanup(p.serverGateway.close)
	if p.servers.Selected != profiles.Selected || p.serversPath != path {
		t.Fatal("relative data directory did not restore its server profiles", p.servers, p.serversPath)
	}
	for _, mode := range []string{"tailscale", "relay"} {
		directory := p.serverNetworks.directory(profiles.Selected, mode)
		want := filepath.Join(service.Store.Root(), "desktop-connections", profiles.Selected, mode)
		if !filepath.IsAbs(directory) || directory != want {
			t.Fatalf("%s native storage = %q, want canonical absolute directory %q", mode, directory, want)
		}
	}
}

func TestDesktopServerNetworksCloseCancelsAndWaitsForOwnedWork(t *testing.T) {
	networks := newDesktopServerNetworks(t.TempDir())
	t.Cleanup(networks.close)
	started, stopped := make(chan struct{}), make(chan struct{})
	networks.run(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(stopped)
	})
	<-started
	closed := make(chan struct{})
	go func() { networks.close(); close(closed) }()
	select {
	case <-closed:
		select {
		case <-stopped:
		default:
			t.Fatal("close returned before owned work stopped")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close did not cancel owned work")
	}
	if _, err := networks.client(desktopServer{ID: "server-1234", Mode: "tailscale", Address: "https://home.example.ts.net/"}); err == nil {
		t.Fatal("closed networks started a new node")
	}
	networks.run(func(context.Context) { t.Error("closed networks accepted new work") })
}
