package main

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDesktopRenderDiagnosticsValidateOriginAndBoundPayload(t *testing.T) {
	for _, origin := range []string{"http://127.0.0.1:8787", "mygo://localhost"} {
		query := url.Values{"name": {"TypeError"}, "components": {"\n    at MarkdownMessage (app.js:1:2)"}, "fatal": {"true"}}
		target := origin + desktopRenderErrorPath + "?" + query.Encode()
		failure, ok := desktopRenderErrorNavigation(origin+"/", target)
		if !ok || failure.name != "TypeError" || !failure.fatal || failure.components != query.Get("components") {
			t.Fatal("valid rendering diagnostic rejected", failure, ok)
		}
		for _, invalid := range []string{
			"https://evil.example" + desktopRenderErrorPath + "?" + query.Encode(),
			target + "&name=Error", target + "&extra=value", target + "#fragment",
			strings.Replace(target, "fatal=true", "fatal=unknown", 1),
			origin + desktopRenderErrorPath + "?name=Error&fatal=true&components=" + strings.Repeat("a", 8193),
		} {
			if _, ok := desktopRenderErrorNavigation(origin+"/", invalid); ok {
				t.Fatal("invalid rendering diagnostic accepted", invalid[:min(len(invalid), 100)])
			}
		}
	}
}

func TestDesktopRendererRecoveryLimitsCrashLoopsPerPage(t *testing.T) {
	now := time.Now()
	var chat, settings desktopRendererRecovery
	for _, at := range []time.Time{now, now.Add(time.Second)} {
		if !chat.allow(at) {
			t.Fatal("transient renderer exit was not recovered")
		}
	}
	if chat.allow(now.Add(2*time.Second)) || !chat.blocked(now.Add(59*time.Second)) {
		t.Fatal("repeated crashes can cause an endless reload loop")
	}
	if !settings.allow(now.Add(2 * time.Second)) {
		t.Fatal("one page exhausted another page's recovery budget")
	}
	if chat.blocked(now.Add(time.Minute)) || !chat.allow(now.Add(time.Minute)) || chat.attempts != 1 {
		t.Fatal("renderer did not regain recovery after the crash window")
	}
}

func TestDesktopRendererCrashBudgetAlsoStopsHealthReloads(t *testing.T) {
	now := time.Now()
	chat := &desktopServerChat{failed: true, status: "error", pageStatus: "error"}
	chat.rendererRecovery.allow(now)
	chat.rendererRecovery.allow(now)
	server := desktopServer{ID: "remote-1234", Enabled: true}
	p := &panel{
		servers:        desktopServers{Entries: []desktopServer{server}},
		serverChats:    map[string]*desktopServerChat{server.ID: chat},
		serverNetworks: newDesktopServerNetworks(t.TempDir()),
	}
	t.Cleanup(p.serverNetworks.close)
	p.checkDesktopServerHealth()
	if chat.healthChecking || !chat.lastHealthCheck.IsZero() {
		t.Fatal("health probe bypassed the renderer crash budget")
	}
	p.applyDesktopServerHealth(server, chat, chat.statusRevision, true)
	if !chat.failed || chat.status != "error" {
		t.Fatal("an in-flight health probe bypassed the renderer crash budget")
	}
}
