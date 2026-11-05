package workspace

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"spool/internal/voxl"
)

const remoteDesktopPrefix = "/api/desktop/view"

type remoteDesktopView struct {
	userID, session string
	ctx             context.Context
	cancel          context.CancelFunc
	connections     int
	lastSeen        time.Time
}

func (a *App) remoteDesktopAvailable(id authIdentity) bool {
	return id.User.RemoteDesktop && a.desktop != nil && a.desktop.Available()
}

// Only an explicit connect action creates a view. Revoked view IDs are removed,
// so voxl's automatic WebSocket retries cannot recreate a kicked connection.
func (a *App) createRemoteDesktopView(w http.ResponseWriter, r *http.Request, id authIdentity) {
	if !a.remoteDesktopAvailable(id) {
		apiError(w, http.StatusForbidden, "Remote desktop is unavailable or not authorized.")
		return
	}
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.closing {
		apiError(w, http.StatusServiceUnavailable, "Remote desktop is unavailable or not authorized.")
		return
	}
	a.desktopMu.Lock()
	defer a.desktopMu.Unlock()
	if a.desktopViews == nil {
		a.desktopViews = make(map[string]*remoteDesktopView)
	}
	now := time.Now()
	for token, view := range a.desktopViews {
		if view.connections == 0 && now.Sub(view.lastSeen) > 10*time.Minute {
			view.cancel()
			delete(a.desktopViews, token)
		}
	}
	token := randomToken()
	ctx, cancel := context.WithCancel(context.Background())
	a.desktopViews[token] = &remoteDesktopView{userID: id.User.ID, session: id.TokenHash, ctx: ctx, cancel: cancel, lastSeen: now}
	apiJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (a *App) remoteDesktopViewAvailable(id authIdentity, token string) bool {
	if !a.remoteDesktopAvailable(id) {
		return false
	}
	if token == "" {
		return true
	}
	return a.remoteDesktopView(id, token) != nil
}

func (a *App) remoteDesktopView(id authIdentity, token string) *remoteDesktopView {
	a.desktopMu.Lock()
	defer a.desktopMu.Unlock()
	view := a.desktopViews[token]
	if view == nil || view.userID != id.User.ID || view.session != id.TokenHash || view.ctx.Err() != nil {
		return nil
	}
	view.lastSeen = time.Now()
	return view
}

func (a *App) closeRemoteDesktopView(id authIdentity, token string) {
	a.desktopMu.Lock()
	if view := a.desktopViews[token]; view != nil && view.userID == id.User.ID && view.session == id.TokenHash {
		view.cancel()
		delete(a.desktopViews, token)
	}
	a.desktopMu.Unlock()
	a.store.changes.publish(changeRemoteDesktop)
}

func (a *App) disconnectRemoteDesktopUser(userID string) {
	a.desktopMu.Lock()
	for token, view := range a.desktopViews {
		if userID == "" || view.userID == userID {
			view.cancel()
			delete(a.desktopViews, token)
		}
	}
	a.desktopMu.Unlock()
	a.store.changes.publish(changeRemoteDesktop)
}

func (a *App) remoteDesktopConnections() map[string]int {
	a.desktopMu.Lock()
	defer a.desktopMu.Unlock()
	counts := make(map[string]int)
	for _, view := range a.desktopViews {
		if view.ctx.Err() == nil && view.connections > 0 {
			counts[view.userID] += view.connections
		}
	}
	return counts
}

// All proxy resources, including the worker's WebSocket, require a fresh Spool
// session and a separate host-level grant. This is never a management API.
func (a *App) remoteDesktopRoute(w http.ResponseWriter, r *http.Request, id authIdentity) {
	if !a.remoteDesktopAvailable(id) {
		apiError(w, http.StatusForbidden, "Remote desktop is unavailable or not authorized.")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, remoteDesktopPrefix)
	if !strings.HasPrefix(r.URL.Path, remoteDesktopPrefix+"/") {
		apiError(w, http.StatusNotFound, "接口不存在")
		return
	}
	switch path {
	case "/", "/index.html", "/client.js", "/worker.js", "/audio.js", "/mic.js", "/favicon.ico", "/inter.woff2", "/ws", "/stats", "/files/list", "/files/download":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			apiError(w, http.StatusNotFound, "接口不存在")
			return
		}
	case "/files/upload":
		if r.Method != http.MethodPut || r.Header.Get("X-Voxl-Upload") != "1" {
			apiError(w, http.StatusNotFound, "接口不存在")
			return
		}
	default:
		apiError(w, http.StatusNotFound, "接口不存在")
		return
	}
	if !a.sameOrigin(r) || (path == "/ws" && r.Header.Get("Origin") == "") {
		apiError(w, http.StatusForbidden, "请求来源或 CSRF 校验失败")
		return
	}
	view := a.remoteDesktopView(id, r.URL.Query().Get("token"))
	// Static assets resolve relative to the iframe without its query string.
	// Every page, socket, stats and file request must belong to a live view.
	switch path {
	case "/client.js", "/worker.js", "/audio.js", "/mic.js", "/favicon.ico", "/inter.woff2":
	default:
		if view == nil {
			apiError(w, http.StatusForbidden, "Remote desktop is unavailable or not authorized.")
			return
		}
	}
	a.turnMu.Lock()
	if a.closing {
		a.turnMu.Unlock()
		apiError(w, http.StatusServiceUnavailable, "Remote desktop is unavailable or not authorized.")
		return
	}
	a.streams.Add(1)
	a.turnMu.Unlock()
	defer a.streams.Done()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if view != nil {
		stop := context.AfterFunc(view.ctx, cancel)
		defer stop()
	}
	sub := a.store.changes.subscribe()
	defer sub.close()
	// Subscribe before rechecking to close the grant/revocation race. Closing the
	// request context also closes ReverseProxy's upgraded socket in Go.
	if current, err := a.identity(r); err != nil || !a.remoteDesktopAvailable(current) {
		apiError(w, http.StatusForbidden, "Remote desktop is unavailable or not authorized.")
		return
	}
	address, password, stopped, release, err := a.desktop.Acquire(ctx, path)
	if err != nil {
		apiError(w, http.StatusServiceUnavailable, "Remote desktop is unavailable or not authorized.")
		return
	}
	defer release()
	counted := false
	defer func() {
		if counted {
			a.desktopMu.Lock()
			view.connections--
			view.lastSeen = time.Now()
			a.desktopMu.Unlock()
			a.store.changes.publish(changeRemoteDesktop)
		}
	}()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.shutdown:
				cancel()
				return
			case <-stopped:
				cancel()
				return
			case <-sub.wake:
				if sub.take()&(changeAuth|changeRemoteDesktop) == 0 {
					continue
				}
			case <-ticker.C:
			}
			if current, err := a.identity(r); err != nil || !a.remoteDesktopAvailable(current) {
				cancel()
				return
			}
		}
	}()
	defer func() { cancel(); <-watchDone }()
	target, _ := url.Parse(address)
	proxy := &httputil.ReverseProxy{
		Transport: remoteDesktopTransport,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.Host = target.Host
			p.Out.URL.Path, p.Out.URL.RawPath = path, ""
			query := p.Out.URL.Query()
			query.Del("token")
			p.Out.URL.RawQuery = query.Encode()
			p.Out.Header.Del("Cookie")
			p.Out.Header.Del("Referer")
			p.Out.Header.Del("X-CSRF-Token")
			p.Out.Header.Set("Authorization", "Bearer "+password)
			if p.Out.Header.Get("Origin") != "" {
				p.Out.Header.Set("Origin", address)
			}
		},
		ModifyResponse: func(response *http.Response) error {
			if path == "/ws" && response.StatusCode == http.StatusSwitchingProtocols {
				a.desktopMu.Lock()
				if view.ctx.Err() != nil {
					a.desktopMu.Unlock()
					return context.Canceled
				}
				view.connections++
				counted = true
				a.desktopMu.Unlock()
				a.store.changes.publish(changeRemoteDesktop)
			}
			response.Header.Del("Set-Cookie")
			response.Header.Del("WWW-Authenticate")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
			if ctx.Err() == nil {
				apiError(w, http.StatusBadGateway, "Remote desktop is unavailable or not authorized.")
			}
		},
	}
	// Only the viewer page is embeddable. The private native settings bridge is
	// never exported; inline styles belong to voxl's embedded client.
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src "+a.realtimeConnectSources(r)+"; worker-src 'self'; media-src 'self' blob:; frame-ancestors 'self'; base-uri 'none'; form-action 'self'")
	w.Header().Set("Cache-Control", "no-store")
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

var remoteDesktopTransport = &http.Transport{Proxy: nil, ResponseHeaderTimeout: 10 * time.Second}

// These operations are called only by the private desktop bridge or a local CLI.
func (s *Service) InstallVoxl(ctx context.Context, source ...string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("service is closed")
	}
	manager := s.desktop
	s.workers.Add(1)
	s.mu.Unlock()
	defer s.workers.Done()
	installCtx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return manager.Install(installCtx, source...)
}

func (s *Service) SetRemoteDesktopEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("service is closed")
	}
	if enabled && !s.desktop.Status().Installed {
		return fmt.Errorf("Install voxl locally before enabling remote desktop.")
	}
	settings, err := s.Store.Settings()
	if err != nil {
		return err
	}
	settings.RemoteDesktopEnabled = enabled
	if err = s.Store.SaveSettings(settings); err != nil {
		return err
	}
	s.desktop.SetEnabled(s.ctx, enabled && s.server != nil)
	if !enabled && s.app != nil {
		s.app.disconnectRemoteDesktopUser("")
	}
	s.Store.changes.publish(changeRemoteDesktop)
	return err
}

// DisconnectRemoteDesktopUser is a local management operation. It preserves the
// user's grant and revokes all current views, including pending file requests.
func (s *Service) DisconnectRemoteDesktopUser(userID string) error {
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("user ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("service is closed")
	}
	if s.app != nil {
		s.app.disconnectRemoteDesktopUser(userID)
	}
	return nil
}

func (s *Service) VoxlStatus() voxl.Status { return s.desktop.Status() }

func (s *Service) BeginVoxlInstall(source ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("service is closed")
	}
	if s.desktop.Status().Installing {
		return fmt.Errorf("voxl is busy. Try again shortly.")
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		_ = s.desktop.Install(s.ctx, source...)
	}()
	return nil
}
