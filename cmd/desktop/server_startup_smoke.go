package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"spool/internal/workspace"
)

// Persist the profiles before the desktop reads them. No remote is selected by
// this probe: startup itself must create their pages and restore a real session.
type desktopServerStartupProbe struct {
	upstreams    []*httptest.Server
	profiles     []desktopServer
	requests     [4]atomic.Int32
	online       atomic.Bool
	socketOnline atomic.Bool
	token        string
	seedError    error
}

func prepareDesktopServerStartupProbe(p *panel) (_ *desktopServerStartupProbe, err error) {
	probe := &desktopServerStartupProbe{}
	defer func() {
		if err != nil {
			probe.close()
		}
	}()
	if _, err = p.service.Store.SaveUser(workspace.UserInput{Username: "startup-probe", Nickname: "Startup account", Password: "startup-probe-password"}); err != nil {
		return nil, err
	}
	if err = p.service.Start(); err != nil {
		return nil, err
	}
	address, err := p.service.LocalWebURL()
	if err != nil {
		return nil, err
	}
	request, _ := http.NewRequest("POST", address+"/api/login", strings.NewReader(`{"username":"startup-probe","password":"startup-probe-password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", address)
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	response.Body.Close()
	for _, cookie := range response.Cookies() {
		if cookie.Name == "spool_session" {
			probe.token = cookie.Value
		}
	}
	if response.StatusCode != http.StatusOK || probe.token == "" {
		return nil, fmt.Errorf("startup fixture login failed: HTTP %d", response.StatusCode)
	}
	target, _ := url.Parse(address)
	profiles := defaultDesktopServers()
	for index, name := range []string{"Signed in at startup", "Signed out at startup", "Offline at startup", "Disabled at startup"} {
		proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = target.Host
			if r.In.Header.Get("Origin") != "" {
				r.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
			}
		}}
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			probe.requests[index].Add(1)
			if index == 0 && r.URL.Path == "/api/events" && !probe.socketOnline.Load() {
				http.Error(w, "startup fixture socket unavailable", http.StatusServiceUnavailable)
				return
			}
			if index == 2 && !probe.online.Load() {
				http.Error(w, "startup fixture offline", http.StatusServiceUnavailable)
				return
			}
			proxy.ServeHTTP(w, r)
		}))
		probe.upstreams = append(probe.upstreams, upstream)
		profiles, err = profiles.save(desktopServer{Name: name, Direct: upstream.URL, Enabled: index != 3})
		if err != nil {
			return nil, err
		}
	}
	probe.profiles = profiles.Entries[1:]
	err = writeDesktopServers(filepath.Join(p.service.Store.Root(), "desktop-servers.json"), profiles)
	return probe, err
}

func (probe *desktopServerStartupProbe) close() {
	for _, upstream := range probe.upstreams {
		upstream.CloseClientConnections()
		upstream.Close()
	}
}

func (probe *desktopServerStartupProbe) restoreSession(p *panel) {
	server := probe.profiles[0]
	mount := desktopServerMount(server)
	target, _ := url.Parse(p.serverGateway.url)
	probe.seedError = mygo.Cookies.Set(mygo.Cookie{
		Name:  "spool_remote_" + strings.TrimSuffix(strings.TrimPrefix(mount, "/web/"), "/") + "_spool_session",
		Value: probe.token, Domain: target.Hostname(), Path: mount,
		HTTPOnly: true, Expires: time.Now().Add(time.Hour),
	})
}

func (probe *desktopServerStartupProbe) run(p *panel, report string, result chan<- error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err := probe.seedError
	if err == nil {
		err = p.waitDesktopServerUI(ctx, func() bool {
			for index, status := range []string{"error", "login", "error"} {
				chat := p.serverChats[probe.profiles[index].ID]
				if chat == nil || chat.status != status || !chat.loaded || index == 0 && !chat.authenticated {
					return false
				}
			}
			return true
		})
	}
	if err == nil {
		err = probe.verifySessionMigration(ctx, p)
	}
	if err == nil {
		verified := make(chan error, 1)
		p.window.Update(func() {
			server := probe.profiles[0]
			chat := p.serverChats[server.ID]
			chat.lastHealthCheck = time.Time{}
			p.checkDesktopServerHealth()
			p.applyDesktopServerHealth(server, chat, chat.statusRevision, true)
			if chat.failed || !chat.loaded || chat.healthChecking || !chat.lastHealthCheck.IsZero() || !chat.authenticated || chat.status != "error" {
				verified <- fmt.Errorf("healthy HTTP reloaded the first failed authenticated socket")
				return
			}
			verified <- nil
		})
		select {
		case err = <-verified:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err == nil {
		probe.socketOnline.Store(true)
		err = p.waitDesktopServerUI(ctx, func() bool {
			return p.serverChats[probe.profiles[0].ID].status == "connected"
		})
	}
	if err == nil {
		probe.online.Store(true)
		err = p.waitDesktopServerUI(ctx, func() bool {
			chat := p.serverChats[probe.profiles[2].ID]
			return chat != nil && chat.status == "login"
		})
	}
	if err == nil {
		verified := make(chan error, 1)
		p.window.Update(func() {
			if p.selectedDesktopServer() != localDesktopServer || p.chatView != p.serverChats[localDesktopServer].view || p.serverChats[probe.profiles[3].ID] != nil || probe.requests[3].Load() != 0 {
				verified <- fmt.Errorf("startup changed selection or connected a disabled server")
				return
			}
			for _, server := range probe.profiles[:3] {
				chat := p.serverChats[server.ID]
				if chat.view == p.chatPresentedView || p.desktopServerTabsFor(server.ID).Selected != server.ID {
					verified <- fmt.Errorf("background server lost its identity or replaced the foreground")
					return
				}
			}
			verified <- nil
		})
		select {
		case err = <-verified:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err == nil {
		body, _ := json.Marshal(map[string]any{"scope": "server-startup", "native": true, "dev_frontend": p.devFrontend != nil, "saved_profiles": true, "background_signed_in": true, "background_signed_out": true, "first_socket_retry_preserved": true, "startup_failure_recovery": true, "disabled_requests": probe.requests[3].Load(), "selection_preserved": true, "cookie_migration": true, "session_isolation": persistentDesktopSessions()})
		err = os.WriteFile(report, body, 0600)
	}
	if err != nil {
		p.window.Update(func() {
			for _, server := range p.desktopServerTabs().Entries {
				fmt.Fprintf(os.Stderr, "startup probe server=%s status=%s\n", server.ID, server.Status)
			}
		})
	}
	result <- err
	mygo.App.Quit()
}

func (probe *desktopServerStartupProbe) verifySessionMigration(ctx context.Context, p *panel) error {
	if !persistentDesktopSessions() {
		return nil
	}
	var first, second *desktopServerChat
	if err := p.rendererSmokeUI(ctx, func() error {
		first, second = p.serverChats[probe.profiles[0].ID], p.serverChats[probe.profiles[1].ID]
		if first.session == second.session || first.session == mygo.DefaultSession() || second.session == mygo.DefaultSession() || p.serverChats[localDesktopServer].session != mygo.DefaultSession() {
			return fmt.Errorf("server sessions lost account isolation")
		}
		return nil
	}); err != nil {
		return err
	}
	filter := mygo.CookieFilter{URL: first.address, Path: desktopServerMount(probe.profiles[0])}
	legacy, err := mygo.Cookies.List(filter)
	if err != nil {
		return err
	}
	for _, cookie := range legacy {
		if strings.HasSuffix(cookie.Name, "spool_session") {
			return fmt.Errorf("migrated login remained in the default cookie store")
		}
	}
	current, err := first.session.Cookies.List(filter)
	if err != nil {
		return err
	}
	matched := false
	for _, cookie := range current {
		matched = matched || cookie.Value == probe.token && cookie.HTTPOnly
	}
	if !matched {
		return fmt.Errorf("existing login was not migrated into its native session")
	}
	// Both pages use the same gateway origin. The same unprefixed key must be
	// isolated by the native data store, independently of our path namespacing.
	if _, err := first.view.Page().EvalContext(ctx, `localStorage.setItem('spool-session-smoke', 'first');`); err != nil {
		return err
	}
	defer first.view.Page().EvalContext(ctx, `localStorage.removeItem('spool-session-smoke');`)
	value, err := second.view.Page().EvalContext(ctx, `localStorage.getItem('spool-session-smoke') === null`)
	if err != nil || value != true {
		return fmt.Errorf("native sessions shared localStorage: %v %v", value, err)
	}
	return nil
}
