package main

import (
	"net/url"
	"strings"
	"testing"

	"spool/internal/workspace"
)

func TestRemoteDesktopPageKeepsServerOriginAndMount(t *testing.T) {
	mount := "/web/" + strings.Repeat("a", 52) + "/"
	for _, test := range []struct{ address, want string }{
		{"http://127.0.0.1:8787", "http://127.0.0.1:8787/remote-desktop.html"},
		{"http://127.0.0.1:8787/", "http://127.0.0.1:8787/remote-desktop.html"},
		{"https://relay.example" + mount, "https://relay.example" + mount + "remote-desktop.html"},
		{"http://[::1]:8787/", "http://[::1]:8787/remote-desktop.html"},
		{"", ""},
		{"https://user:password@example.com/", ""},
		{"javascript:alert(1)", ""},
	} {
		if got := remoteDesktopPageURL(test.address); got != test.want {
			t.Errorf("remote desktop for %q = %q, want %q", test.address, got, test.want)
		}
	}
}

func TestRemoteDesktopTitlesDistinguishServersAndPreferSavedNames(t *testing.T) {
	p := &panel{
		preferences: workspace.UIPreferences{Language: "en"},
		settings:    workspace.Settings{ServerName: "Local workstation"},
		servers: desktopServers{Entries: []desktopServer{
			{ID: "first", Name: "Build server", ServerName: "Host one"},
			{ID: "second", ServerName: "Test server"},
		}},
	}
	for id, want := range map[string]string{
		localDesktopServer: "Spool · Local workstation · Remote desktop",
		"first":            "Spool · Build server · Remote desktop",
		"second":           "Spool · Test server · Remote desktop",
	} {
		if got := p.remoteDesktopTitle(id); got != want {
			t.Errorf("title for %q = %q, want %q", id, got, want)
		}
	}
	p.servers.Entries[0].Name = "Renamed build server"
	if got := p.remoteDesktopTitle("first"); got != "Spool · Renamed build server · Remote desktop" {
		t.Fatal("title did not follow the saved server name", got)
	}
	p.servers.Entries = append(p.servers.Entries, desktopServer{ID: localDesktopServer, Name: "My local server"})
	if got := p.remoteDesktopTitle(localDesktopServer); got != "Spool · My local server · Remote desktop" {
		t.Fatal("local title did not prefer the saved server name", got)
	}
}

func TestRemoteDesktopConnectionHintCarriesOnlyOneRevocableToken(t *testing.T) {
	const origin = "http://127.0.0.1:8787"
	const token = "fixture-token"
	target := origin + desktopRemoteDesktopConnectedPath + "?" + url.Values{"token": {token}}.Encode()
	if got, ok := remoteDesktopConnectedNavigation(origin, target); !ok || got != token {
		t.Fatal("valid connection hint rejected", got, ok)
	}
	for _, target := range []string{
		"https://evil.example" + desktopRemoteDesktopConnectedPath + "?token=fixture",
		origin + desktopRemoteDesktopConnectedPath,
		origin + desktopRemoteDesktopConnectedPath + "?token=",
		origin + desktopRemoteDesktopConnectedPath + "?token=one&token=two",
		origin + desktopRemoteDesktopConnectedPath + "?token=one&server=other",
		origin + desktopRemoteDesktopConnectedPath + "?token=one#fragment",
	} {
		if _, ok := remoteDesktopConnectedNavigation(origin, target); ok {
			t.Error("unexpected connection hint", target)
		}
	}
}

func TestRemoteDesktopActionsRequireExactSameOriginHints(t *testing.T) {
	const origin = "http://127.0.0.1:8787"
	for _, path := range []string{desktopRemoteDesktopPath, desktopRemoteDesktopClosePath, desktopRemoteDesktopConnectedPath} {
		if !chatReservedNavigation(origin, origin+path, path) {
			t.Fatal("remote desktop action rejected", path)
		}
		for _, target := range []string{
			"https://evil.example" + path,
			"http://127.0.0.1:8788" + path,
			origin + path + "?token=anything",
			origin + path + "#fragment",
			origin + path + "/",
			origin + "/__desktop/%72emote-desktop",
		} {
			if chatReservedNavigation(origin, target, path) {
				t.Error("unexpected remote desktop action", target)
			}
		}
	}
}
