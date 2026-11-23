package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDesktopFederationHealthChecksPublicTargetsWithoutConnectingThem(t *testing.T) {
	const source = "local"
	now := time.Now()
	networks := newDesktopServerNetworks(t.TempDir())
	t.Cleanup(networks.close)
	p := &panel{serverNetworks: networks, servers: desktopServers{Selected: source, Entries: []desktopServer{
		{ID: source, Enabled: true},
		{ID: "other", Enabled: true},
		{ID: "saved", Mode: "direct", Address: "https://saved.example/", Enabled: false},
	}}}
	first, second := &desktopServerChat{authenticated: true}, &desktopServerChat{authenticated: true}
	p.serverChats = map[string]*desktopServerChat{source: first, "other": second}
	var jobs []func(context.Context) func()
	p.federation.run = func(job func(context.Context) func()) { jobs = append(jobs, job) }
	status := "login"
	p.federation.probeHealth = func(_ context.Context, key desktopFederationHealthKey) string {
		if key.id != testFederationTarget || key.address != "https://team.example/" {
			t.Fatalf("unexpected probe for %+v", key)
		}
		return status
	}
	target := desktopFederationTarget{ID: testFederationTarget, Name: "Team", ClientURL: "https://team.example:443"}
	p.recordDesktopFederationTargets(first, []desktopFederationTarget{
		target,
		{ID: strings.Repeat("c", 52), Name: "Tailnet", ClientURL: "https://mesh.example/", ClientMode: "tailscale"},
		{ID: strings.Repeat("d", 52), Name: "Saved", ClientURL: "https://saved.example/"},
		{ID: strings.Repeat("e", 52), Name: "No route"},
		{ID: strings.Repeat("f", 52), Name: "Wrong Relay mount", ClientURL: "https://relay.example/web/" + testFederationTarget + "/"},
	})
	p.recordDesktopFederationTargets(second, []desktopFederationTarget{target})
	before := append([]desktopServer(nil), p.servers.Entries...)
	checkStatus := func(want string) {
		t.Helper()
		for i, entry := range p.desktopServerTabs().Entries[:2] {
			if got := entry.FederationTargets[0]; got.Status != want || got.ClientURL != "https://team.example/" {
				t.Fatalf("source %d target = %+v, want status %q", i, got, want)
			}
		}
	}
	p.checkDesktopFederationHealth(now)
	checkStatus("connecting")
	p.checkDesktopFederationHealth(now.Add(time.Second))
	if len(jobs) != 1 {
		t.Fatalf("expected one probe shared by both sources, got %d", len(jobs))
	}
	jobs[0](t.Context())()
	checkStatus("login")
	for i, next := range []string{"error", "login"} {
		status = next
		p.checkDesktopFederationHealth(now.Add(time.Duration(i+1) * desktopFederationHealthInterval))
		if len(jobs) != i+2 {
			t.Fatalf("probe after interval did not run: %d", len(jobs))
		}
		jobs[i+1](t.Context())()
		checkStatus(next)
	}
	if p.selectedDesktopServer() != source || !reflect.DeepEqual(p.servers.Entries, before) || p.federation.attempt != nil || len(networks.clients) != 0 {
		t.Fatal("background probe changed a selected server, profile, or session")
	}
	for _, entry := range p.desktopServerTabs().Entries[0].FederationTargets[1:] {
		if entry.Status != "" || (entry.Name == "Tailnet" && entry.ClientMode != "tailscale") {
			t.Fatalf("unprobed target lost its route or received a status: %+v", entry)
		}
	}
}

func TestDesktopFederationHealthDropsRevokedAndEditedRoutes(t *testing.T) {
	for _, change := range []string{"logout", "revoked", "address changed", "mode changed", "profile added"} {
		t.Run(change, func(t *testing.T) {
			const source = "local"
			networks := newDesktopServerNetworks(t.TempDir())
			t.Cleanup(networks.close)
			chat := &desktopServerChat{authenticated: true}
			p := &panel{serverNetworks: networks, servers: desktopServers{Entries: []desktopServer{{ID: source, Enabled: true}}}, serverChats: map[string]*desktopServerChat{source: chat}}
			var jobs []func(context.Context) func()
			p.federation.run = func(job func(context.Context) func()) { jobs = append(jobs, job) }
			var requestContext context.Context
			p.federation.probeHealth = func(ctx context.Context, _ desktopFederationHealthKey) string {
				requestContext = ctx
				return "login"
			}
			target := desktopFederationTarget{ID: testFederationTarget, ClientURL: "https://team.example/"}
			p.recordDesktopFederationTargets(chat, []desktopFederationTarget{target})
			now := time.Now()
			p.checkDesktopFederationHealth(now)
			key, _ := desktopFederationHealthRoute(target)
			switch change {
			case "logout":
				chat.authenticated = false
			case "revoked":
				p.recordDesktopFederationTargets(chat, nil)
			case "address changed":
				target.ClientURL = "https://new.example/"
				p.recordDesktopFederationTargets(chat, []desktopFederationTarget{target})
			case "mode changed":
				target.ClientMode = "tailscale"
				p.recordDesktopFederationTargets(chat, []desktopFederationTarget{target})
			case "profile added":
				p.servers.Entries = append(p.servers.Entries, desktopServer{ID: "saved", Mode: "direct", Address: target.ClientURL})
			}
			p.checkDesktopFederationHealth(now.Add(time.Second))
			apply := jobs[0](t.Context())
			if !errors.Is(requestContext.Err(), context.Canceled) {
				t.Fatalf("obsolete probe was not canceled: %v", requestContext.Err())
			}
			apply()
			if p.federation.health[key] != nil {
				t.Fatal("late probe restored an obsolete route")
			}
		})
	}
}

func TestDesktopFederationHealthRequiresTheExpectedServerWithoutCredentials(t *testing.T) {
	for _, test := range []struct {
		name, id, state string
		code            int
	}{
		{"reachable", testFederationTarget, "login", http.StatusOK},
		{"wrong identity", strings.Repeat("c", 52), "error", http.StatusOK},
		{"unavailable", testFederationTarget, "error", http.StatusServiceUnavailable},
		{"redirect", testFederationTarget, "error", http.StatusTemporaryRedirect},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			mount := "/web/" + testFederationTarget + "/"
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				if r.Method != http.MethodGet || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Origin") != "" {
					t.Error("background probe used account credentials or an unexpected method")
				}
				w.Header().Set("Location", "/not-the-server")
				w.WriteHeader(test.code)
				_ = json.NewEncoder(w).Encode(map[string]string{"server_id": test.id})
			}))
			defer upstream.Close()
			key := desktopFederationHealthKey{testFederationTarget, upstream.URL + mount}
			if got := desktopFederationHealthStatus(t.Context(), key, upstream.Client().Transport); got != test.state {
				t.Fatalf("probe returned %q, want %q", got, test.state)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(paths, []string{mount + "api/version"}) {
				t.Fatalf("unexpected requests: %v", paths)
			}
		})
	}
}

func TestDesktopFederationHealthNeverDialsPrivateAddresses(t *testing.T) {
	for _, address := range []string{"8.8.8.8", "2001:4860:4860::8888"} {
		if !desktopFederationPublicAddress(netip.MustParseAddr(address)) {
			t.Errorf("public IP %s was rejected", address)
		}
	}
	for _, address := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.1.2.3", "192.168.1.1", "169.254.169.254", "100.64.1.2", "198.18.1.1", "192.0.2.1", "fc00::1", "2001:db8::1", "2002:7f00:1::1", "64:ff9b::7f00:1"} {
		if desktopFederationPublicAddress(netip.MustParseAddr(address)) {
			t.Errorf("non-public IP %s was allowed", address)
		}
		key := desktopFederationHealthKey{testFederationTarget, fmt.Sprintf("https://%s/", netip.AddrPortFrom(netip.MustParseAddr(address), 443))}
		if got := probeDesktopFederationHealth(t.Context(), key); got != "" {
			t.Errorf("non-public URL %s returned %q, want unknown", key.address, got)
		}
	}
}
