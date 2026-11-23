package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spool/internal/workspace"
)

func TestDesktopServerProfilesKeepLocalAndRestoreSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop-servers.json")
	profiles, err := readDesktopServers(path)
	if err != nil || profiles.Selected != localDesktopServer || len(profiles.Entries) != 1 || !profiles.Entries[0].Enabled {
		t.Fatalf("first launch: %+v, %v", profiles, err)
	}
	profiles, err = profiles.save(desktopServer{Name: " Home ", Address: " https://HOME.example ", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	remote := profiles.Entries[1]
	if remote.Name != "Home" || remote.Address != "https://home.example/" || remote.ID == "" {
		t.Fatalf("profile not normalized: %+v", remote)
	}
	profiles.Selected = remote.ID
	profiles.SidebarWidth = 338
	if err = writeDesktopServers(path, profiles); err != nil {
		t.Fatal(err)
	}
	restored, err := readDesktopServers(path)
	if err != nil || restored.Selected != remote.ID || len(restored.Entries) != 2 || restored.SidebarWidth != 338 {
		t.Fatalf("restore: %+v, %v", restored, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatal("profiles are readable by other users")
	}
	remote.Enabled = false
	disabled, err := restored.save(remote)
	if err != nil || disabled.Selected != remote.ID || len(disabled.Entries) != 2 {
		t.Fatal("disabling selected server changed the selected sidebar", disabled, err)
	}
	if err = writeDesktopServers(path, disabled); err != nil {
		t.Fatal(err)
	}
	if restored, err := readDesktopServers(path); err != nil || restored.Selected != remote.ID || restored.Entries[1].Enabled {
		t.Fatal("disabled selection was not restored", restored, err)
	}
	removed, err := restored.remove(remote.ID)
	if err != nil || removed.Selected != localDesktopServer || len(removed.Entries) != 1 || len(restored.Entries) != 2 {
		t.Fatal("removal mutated the previous snapshot or lost local", removed, err)
	}
	if _, err = restored.remove(localDesktopServer); err == nil {
		t.Fatal("removed built-in local service")
	}
	if _, err = restored.save(desktopServer{ID: localDesktopServer}); err == nil {
		t.Fatal("overwrote built-in local service")
	}
	if _, err = restored.save(desktopServer{ID: "unknown-1234", Name: "Missing", Address: "https://example.com"}); err == nil {
		t.Fatal("updated a missing profile")
	}
}

func TestDesktopServerNamesFollowMetadataUnlessCustomized(t *testing.T) {
	p := &panel{servers: defaultDesktopServers(), serversPath: filepath.Join(t.TempDir(), "profiles.json"), settings: workspace.Settings{ServerName: "本机开发"}}
	var err error
	p.servers, err = p.servers.save(desktopServer{Direct: "https://home.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	id := p.servers.Entries[1].ID
	p.recordDesktopServerName(id, "家里的服务器")
	if tabs := p.desktopServerTabs(); tabs.Entries[0].Name != "本机开发" || tabs.Entries[1].Name != "家里的服务器" {
		t.Fatal(tabs)
	}
	server, _ := p.servers.find(id)
	server.Name = "我的自定义名"
	p.servers, err = p.servers.save(server)
	if err != nil {
		t.Fatal(err)
	}
	p.recordDesktopServerName(id, "服务端已改名")
	if got := p.desktopServerTabs().Entries[1].Name; got != "我的自定义名" {
		t.Fatal(got)
	}
	// An editor opened before a live rename must not overwrite fresh metadata.
	server.Name = ""
	p.servers, err = p.servers.save(server)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.desktopServerTabs().Entries[1].Name; got != "服务端已改名" {
		t.Fatal(got)
	}
	server, _ = p.servers.find(id)
	server.Tailscale = "https://home.example.ts.net"
	p.servers, err = p.servers.save(server)
	if err != nil || p.desktopServerTabs().Entries[1].Name != "服务端已改名" {
		t.Fatal("adding an alternate route lost the active server name", err)
	}
	p.recordDesktopServerName(localDesktopServer, "本地新名称")
	if got := p.desktopServerTabs().Entries[0].Name; got != "本地新名称" {
		t.Fatal(got)
	}
	if err = writeDesktopServers(p.serversPath, p.servers); err != nil {
		t.Fatal(err)
	}
	restored, err := readDesktopServers(p.serversPath)
	if err != nil || restored.Entries[1].displayName() != "服务端已改名" {
		t.Fatal(restored, err)
	}
	server, _ = restored.find(id)
	server.Direct = "https://other.example"
	restored, err = restored.save(server)
	if err != nil || restored.Entries[1].ServerName != "" {
		t.Fatal("destination edit retained stale name", restored, err)
	}
}

func TestDesktopServerModesKeepIndependentAddressesAndSessions(t *testing.T) {
	profiles, err := defaultDesktopServers().save(desktopServer{Direct: "https://public.example", Tailscale: "https://HOME.EXAMPLE.ts.net:443/", Mode: "tailscale", Enabled: true, Automatic: true})
	if err != nil {
		t.Fatal(err)
	}
	server := profiles.Entries[1]
	if server.Address != "https://home.example.ts.net/" || server.Direct != "https://public.example/" {
		t.Fatal(server)
	}
	direct := server.connection("direct")
	if direct.Address != server.Direct || direct.Tailscale != server.Tailscale || desktopServerMount(direct) == desktopServerMount(server) {
		t.Fatal("mode settings or sessions were combined", direct)
	}
	for _, invalid := range []desktopServer{
		{Enabled: true, Mode: "tailscale", Tailscale: "http://example.ts.net"},
		{Enabled: true, Mode: "tailscale", Tailscale: "https://127.0.0.1"},
		{Enabled: true, Mode: "relay", Relay: "not-a-code"},
		{Enabled: true, Mode: "other"},
		{Enabled: true, Mode: "relay", Direct: "https://public.example"},
	} {
		if _, err := validateDesktopServer(invalid); err == nil {
			t.Fatal("invalid connection accepted", invalid)
		}
	}
}

func TestServerDialogNewProfileCanAutosaveBeforeConnecting(t *testing.T) {
	snapshot := (&panel{}).serverDialogSnapshot()
	if snapshot.Server.Enabled || snapshot.Active || snapshot.Status != "disconnected" {
		t.Fatalf("new server must wait for an explicit connection: %+v", snapshot)
	}
	snapshot.Server.Name = "New home"
	if _, err := validateDesktopServer(snapshot.Server); err != nil {
		t.Fatalf("name cannot autosave before the address is configured: %v", err)
	}
}

func TestServerDialogOnlyAllowsItsExactPrivateDocument(t *testing.T) {
	p := &panel{}
	for _, address := range []string{serverDialogURL, "http://mygo.localhost/servers.html"} {
		if !p.serverDialogDocumentAllowed(address) {
			t.Fatal(address)
		}
	}
	for _, address := range []string{settingsPageURL, "http://127.0.0.1/servers.html", serverDialogURL + "?", serverDialogURL + "#", "mygo://localhost/%73ervers.html", "mygo://user@localhost/servers.html", "mygo://foreign/servers.html"} {
		if p.serverDialogDocumentAllowed(address) {
			t.Fatal("foreign dialog accepted", address)
		}
	}
	p.devFrontend = &desktopDevFrontend{url: "http://127.0.0.1:54321"}
	if !p.serverDialogDocumentAllowed(p.serverDialogURL()) || p.serverDialogDocumentAllowed(serverDialogURL) || p.serverDialogDocumentAllowed(p.serverDialogURL()+"?x=1") {
		t.Fatal("development dialog guard")
	}
	for _, bridge := range []*desktopServerSettings{{}, {panel: &panel{}}} {
		ctx := context.Background()
		if _, err := bridge.Snapshot(ctx); err == nil {
			t.Fatal("snapshot exposed without dialog")
		}
		if _, err := bridge.Ready(ctx, "", 420); err == nil {
			t.Fatal("dialog shown without its page")
		}
		if _, err := bridge.Save(ctx, desktopServer{}, true); err == nil {
			t.Fatal("save allowed without dialog")
		}
		if _, err := bridge.Disconnect(ctx); err == nil {
			t.Fatal("disconnect allowed without dialog")
		}
		if _, err := bridge.Delete(ctx); err == nil {
			t.Fatal("delete allowed without dialog")
		}
		if _, err := bridge.Close(ctx); err == nil {
			t.Fatal("close allowed without dialog")
		}
		if _, err := bridge.TailscaleLogin(ctx); err == nil {
			t.Fatal("login allowed without dialog")
		}
		if _, err := bridge.PairRelay(ctx); err == nil {
			t.Fatal("pairing allowed without dialog")
		}
	}
}

func TestDesktopServerAddressesAndDestinationIsolation(t *testing.T) {
	for _, address := range []string{"https://host.example", "http://127.0.0.1:59001/", "https://[::1]:8787", "https://relay.example/web/" + strings.Repeat("a", 52) + "/"} {
		if _, err := desktopServerAddress(address); err != nil {
			t.Errorf("rejected %q: %v", address, err)
		}
	}
	for _, address := range []string{"", "%", "file:///etc/passwd", "javascript:alert(1)", "https://user:password@host.example", "https://host.example/?token=secret", "https://host.example/#", "https://host.example?", "https://host.example/other", "https://host.example/%77eb/aaa", "//host.example", "http://host.example:0"} {
		if _, err := desktopServerAddress(address); err == nil {
			t.Errorf("accepted invalid server address %q", address)
		}
	}
	first := desktopServer{ID: "server-1234", Name: "One", Address: "https://one.example/"}
	rename := first
	rename.Name = "Renamed"
	if desktopServerMount(first) != desktopServerMount(rename) {
		t.Fatal("rename discarded session namespace")
	}
	changed := first
	changed.Address = "https://two.example/"
	if desktopServerMount(first) == desktopServerMount(changed) {
		t.Fatal("editing destination retained the old credential namespace")
	}
	changed = first
	changed.ID = "server-5678"
	if desktopServerMount(first) == desktopServerMount(changed) {
		t.Fatal("two profiles share a credential namespace")
	}
}

func TestDesktopServerProfileRestoreNeverDropsLocal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	for _, body := range []string{`{"entries":[],"selected":"missing"}`, `{"entries":[{"id":"local","address":"https://evil.example","enabled":false}],"selected":"local"}`} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		profiles, err := readDesktopServers(path)
		if err != nil || profiles.Selected != localDesktopServer || len(profiles.Entries) != 1 || profiles.Entries[0].Address != "" || !profiles.Entries[0].Enabled {
			t.Fatalf("local default lost: %+v %v", profiles, err)
		}
	}
}

func TestDesktopServerHintsValidateOriginAndQuery(t *testing.T) {
	const base = "http://127.0.0.1:40000"
	for _, path := range []string{desktopServerConnectPath} {
		if !chatReservedNavigation(base, base+path, path) {
			t.Fatal("valid presentation action rejected", path)
		}
		for _, target := range []string{"https://foreign.test" + path, base + path + "?id=local", base + path + "#x"} {
			if chatReservedNavigation(base, target, path) {
				t.Fatal("invalid presentation action accepted", target)
			}
		}
	}
	if width, ok := desktopSidebarWidthNavigation(base, base+desktopSidebarWidthPath+"?width=308"); !ok || width != 308 {
		t.Fatal("valid sidebar width rejected", width)
	}
	for _, target := range []string{"https://foreign.test" + desktopSidebarWidthPath + "?width=308", base + desktopSidebarWidthPath + "?width=219", base + desktopSidebarWidthPath + "?width=481", base + desktopSidebarWidthPath + "?width=NaN", base + desktopSidebarWidthPath + "?width=308&width=320", base + desktopSidebarWidthPath + "?width=308#x"} {
		if _, ok := desktopSidebarWidthNavigation(base, target); ok {
			t.Fatal("invalid sidebar width accepted", target)
		}
	}
	for _, target := range []string{base + desktopServersPath, base + desktopServersPath + "?id=local", base + desktopServersPath + "?id=server-1234"} {
		if _, ok := desktopServerNavigation(base, target); !ok {
			t.Fatal("valid selector rejected", target)
		}
	}
	for _, target := range []string{"http://foreign.test" + desktopServersPath, base + desktopServersPath + "?id=one&id=two", base + desktopServersPath + "?id=local&address=https://evil.example", base + desktopServersPath + "#x", base + "/__desktop/%73ervers", base + desktopServersPath + "?"} {
		if _, ok := desktopServerNavigation(base, target); ok {
			t.Fatal("invalid selector accepted", target)
		}
	}
	if selection, ok := desktopServerNavigation(base, base+desktopServersPath+"?id=server-1234&scroll_left=156.5"); !ok || selection.ID != "server-1234" || selection.ScrollLeft == nil || *selection.ScrollLeft != 156.5 {
		t.Fatal("tab selection lost its scroll origin", selection)
	}
	for _, query := range []string{"scroll_left=100", "id=local&scroll_left=", "id=local&scroll_left=-1", "id=local&scroll_left=NaN", "id=local&scroll_left=Inf", "id=local&scroll_left=1e8", "id=local&scroll_left=100&scroll_left=200", "id=local&scroll_left=100&other=1"} {
		if _, ok := desktopServerNavigation(base, base+desktopServersPath+"?"+query); ok {
			t.Fatal("invalid tab scroll accepted", query)
		}
	}
	if _, ok := desktopServerStateNavigation(base, base+desktopServerStatePath+"?state=connected"); !ok {
		t.Fatal("valid state rejected")
	}
	if _, ok := desktopServerStateNavigation(base, base+desktopServerStatePath+"?state=unknown"); ok {
		t.Fatal("invalid state accepted")
	}
	for _, path := range []string{desktopServerStatePath, desktopServerReadyPath} {
		selection := ""
		if path == desktopServerReadyPath {
			selection = "&selection=local%3A0"
		}
		for _, authenticated := range []string{"true", "false"} {
			target := base + path + "?state=error&authenticated=" + authenticated + selection
			connection, ok := desktopServerStateNavigation(base, target)
			if path == desktopServerReadyPath {
				paint, valid := desktopServerReadyNavigation(base, target)
				connection, ok = paint.desktopServerConnection, valid
			}
			if !ok || connection.State != "error" || connection.Authenticated != (authenticated == "true") {
				t.Fatal("session state was not preserved before socket ready", target, connection)
			}
		}
		for _, query := range []string{"authenticated=unknown", "authenticated=true&authenticated=false", "authenticated=true&other=value"} {
			target := base + path + "?state=error&" + query + selection
			_, ok := desktopServerStateNavigation(base, target)
			if path == desktopServerReadyPath {
				_, ok = desktopServerReadyNavigation(base, target)
			}
			if ok {
				t.Fatal("invalid session hint accepted", target)
			}
		}
	}
	for _, state := range []string{"connecting", "connected", "login", "error", "disconnected"} {
		if got, ok := desktopServerReadyNavigation(base, base+desktopServerReadyPath+"?state="+state+"&selection=local%3A0"); !ok || got.State != state || got.Selection != "local:0" {
			t.Fatal("painted state rejected", state, got)
		}
	}
	for _, target := range []string{
		base + desktopServerReadyPath,
		base + desktopServerReadyPath + "?state=unknown&selection=local%3A0",
		base + desktopServerReadyPath + "?state=login&state=connected&selection=local%3A0",
		base + desktopServerReadyPath + "?state=login&selection=local%3A0&id=local",
		base + desktopServerReadyPath + "?state=login&selection=local%3A0#x",
		"https://foreign.test" + desktopServerReadyPath + "?state=login&selection=local%3A0",
	} {
		if _, ok := desktopServerReadyNavigation(base, target); ok {
			t.Fatal("invalid painted state accepted", target)
		}
	}
	if name, ok := desktopServerNameNavigation(base, base+desktopServerNamePath+"?name=%E5%AE%B6"); !ok || name != "家" {
		t.Fatal("server metadata rejected", name)
	}
	for _, target := range []string{"https://foreign.test" + desktopServerNamePath + "?name=Other", base + desktopServerNamePath + "?name=one&name=two", base + desktopServerNamePath + "?name=one&id=local", base + desktopServerNamePath + "?name=%00"} {
		if _, ok := desktopServerNameNavigation(base, target); ok {
			t.Fatal("invalid metadata hint accepted", target)
		}
	}
}

func TestDesktopSignedOutStatusReachesTabsAndDialog(t *testing.T) {
	profiles, err := defaultDesktopServers().save(desktopServer{Direct: "https://home.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	id := profiles.Entries[1].ID
	p := &panel{
		servers: profiles,
		serverChats: map[string]*desktopServerChat{
			localDesktopServer: {status: "connecting"},
			id:                 {status: "connecting"},
		},
		serverDialogID: id,
	}
	p.recordDesktopServerState(id, "login")
	if got := p.desktopServerTabs().Entries[1].Status; got != "login" {
		t.Fatal("reachable signed-out server still connecting in tabs", got)
	}
	if got := p.serverDialogSnapshot().Status; got != "login" {
		t.Fatal("reachable signed-out server still connecting in settings", got)
	}
	if got := p.desktopServerTabs().Entries[0].Status; got != "connecting" {
		t.Fatal("remote status changed another server", got)
	}
	p.recordDesktopServerState(localDesktopServer, "login")
	if got := p.desktopServerTabs().Entries[0].Status; got != "login" {
		t.Fatal("local signed-out status did not update", got)
	}
}

func TestDesktopServerDialogTracksActiveConnection(t *testing.T) {
	profiles, err := defaultDesktopServers().save(desktopServer{Direct: "https://home.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	id := profiles.Entries[1].ID
	for _, test := range []struct {
		name    string
		status  string
		enabled bool
		failed  bool
		active  bool
	}{
		{"connecting", "connecting", true, false, true},
		{"connected", "connected", true, false, true},
		{"sign-in required", "login", true, false, true},
		{"connection error", "error", true, false, false},
		{"disconnected", "disconnected", true, false, false},
		{"failed view", "connecting", true, true, false},
		{"disabled profile", "connected", false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &panel{
				servers:        profiles,
				serverDialogID: id,
				serverChats:    map[string]*desktopServerChat{id: {status: test.status, failed: test.failed}},
			}
			p.servers.Entries = append([]desktopServer(nil), profiles.Entries...)
			p.servers.Entries[1].Enabled = test.enabled
			snapshot := p.serverDialogSnapshot()
			if snapshot.Active != test.active {
				t.Fatalf("active connection: %+v", snapshot)
			}
			if !test.enabled && snapshot.Status != "disconnected" {
				t.Fatal("disabled connection retained a live status", snapshot)
			}
		})
	}
}

func TestDesktopServerDisconnectPreservesRoutesAndOtherServers(t *testing.T) {
	profiles, err := defaultDesktopServers().save(desktopServer{
		Name: "Home", Direct: "https://home.example", Tailscale: "https://home.example.ts.net", Mode: "tailscale", Enabled: true, Automatic: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err = profiles.save(desktopServer{Direct: "https://other.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	target := profiles.Entries[1]
	profiles.Selected = target.ID
	p := &panel{
		servers: profiles, serversPath: filepath.Join(t.TempDir(), "profiles.json"), serverDialogID: target.ID,
		serverChats: map[string]*desktopServerChat{target.ID: {status: "connecting"}, profiles.Entries[2].ID: {status: "connected"}},
	}
	for _, invalid := range []string{"", "unknown-1234", localDesktopServer} {
		if err := p.disconnectDesktopServer(invalid); err == nil {
			t.Fatal("disconnect accepted invalid target", invalid)
		}
	}
	if err := p.disconnectDesktopServer(target.ID); err != nil {
		t.Fatal(err)
	}
	target.Enabled = false
	stored, err := readDesktopServers(p.serversPath)
	if err != nil || stored.Entries[1] != target || stored.Selected != target.ID {
		t.Fatal("disconnect lost the selected profile or its route settings", stored, err)
	}
	if stored.Entries[0] != profiles.Entries[0] || stored.Entries[2] != profiles.Entries[2] || p.serverChats[profiles.Entries[2].ID] == nil {
		t.Fatal("disconnect changed another server", stored)
	}
	if p.serverChats[target.ID] != nil || p.serverDialogSnapshot().Active {
		t.Fatal("disconnect kept an active server view")
	}
}

func TestDesktopServerReconnectDiscardsDisabledPageState(t *testing.T) {
	profiles, err := defaultDesktopServers().save(desktopServer{Direct: "https://home.example", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	server := profiles.Entries[1]
	profiles.Selected = server.ID
	p := &panel{
		servers: profiles, serversPath: filepath.Join(t.TempDir(), "profiles.json"),
		serverChats: map[string]*desktopServerChat{server.ID: {status: "connecting"}},
	}
	server.Enabled = true
	next, err := profiles.save(server)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.setDesktopServers(next); err != nil {
		t.Fatal(err)
	}
	if p.serverChats[server.ID] != nil {
		t.Fatal("reconnecting kept the disabled page's stale connection notifications")
	}
	if p.selectedDesktopServer() != server.ID || desktopServerMount(server) != desktopServerMount(profiles.Entries[1]) {
		t.Fatal("reconnecting changed the selected server or its credential namespace")
	}
}
