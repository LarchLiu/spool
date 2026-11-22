package main

import (
	"context"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"spool"
)

// Remote workspaces use the bundled client and a separate API/cookie mount per
// destination. No remote document receives a trusted settings-page identity.
type desktopServerGateway struct {
	url         string
	server      *http.Server
	mu          sync.RWMutex
	routes      map[string]http.Handler
	assets      http.Handler
	cancel      context.CancelFunc
	done        chan struct{}
	networks    *desktopServerNetworks
	development bool
}

func startDesktopServerGateway(dev *desktopDevFrontend, profiles desktopServers) (*desktopServerGateway, error) {
	assets, err := fs.Sub(spool.Frontend(), "frontend")
	if err != nil {
		return nil, err
	}
	g := &desktopServerGateway{assets: http.FileServer(http.FS(assets))}
	if dev != nil {
		target, _ := url.Parse(dev.url)
		g.development = true
		g.assets = &httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			// The development frontend validates its own loopback Host. This
			// gateway is a separate origin, so address that frontend explicitly.
			p.Out.Host = target.Host
		}, FlushInterval: -1, ErrorHandler: desktopDevProxyError}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	g.url = "http://" + listener.Addr().String()
	g.update(profiles)
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel, g.done = cancel, make(chan struct{})
	g.server = &http.Server{Handler: g, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		defer close(g.done)
		_ = g.server.Serve(listener)
	}()
	return g, nil
}

func (g *desktopServerGateway) close() {
	if g != nil {
		g.cancel()
		_ = g.server.Close()
		<-g.done
	}
}

func (g *desktopServerGateway) update(profiles desktopServers) {
	routes := make(map[string]http.Handler)
	for _, server := range profiles.Entries {
		if server.ID != localDesktopServer {
			mount := desktopServerMount(server)
			if !server.Enabled || server.Address == "" {
				routes[mount] = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":"Connect to view this server’s workspace."}`))
				})
				continue
			}
			target, _ := url.Parse(server.Address)
			routes[mount] = remoteDesktopAPI(target, mount)
			if g.networks != nil && (server.Mode == "tailscale" || server.Mode == "relay") {
				routes[mount].(*httputil.ReverseProxy).Transport = desktopNetworkTransport{g.networks, server}
			}
		}
	}
	g.mu.Lock()
	g.routes = routes
	g.mu.Unlock()
}

func (g *desktopServerGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if "http://"+r.Host != g.url || r.Header.Get("Sec-Fetch-Site") == "cross-site" ||
		r.Header.Get("Origin") != "" && r.Header.Get("Origin") != g.url {
		http.Error(w, "invalid desktop request origin", http.StatusForbidden)
		return
	}
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Via"} {
		if r.Header.Get(header) != "" {
			http.Error(w, "forwarded desktop requests are not allowed", http.StatusForbidden)
			return
		}
	}
	// Vite imports and its HMR connection use absolute asset paths.
	if strings.HasPrefix(r.URL.Path, "/src/") || strings.HasPrefix(r.URL.Path, "/@") || strings.HasPrefix(r.URL.Path, "/node_modules/") || g.development && r.URL.Path == "/" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		g.assets.ServeHTTP(w, r)
		return
	}
	parts := strings.SplitN(r.URL.Path, "/", 4)
	if len(parts) != 4 || parts[1] != "web" {
		http.NotFound(w, r)
		return
	}
	mount := "/web/" + parts[2] + "/"
	g.mu.RLock()
	api := g.routes[mount]
	g.mu.RUnlock()
	if api == nil || r.URL.RawPath != "" && !strings.HasPrefix(r.URL.EscapedPath(), mount) {
		http.NotFound(w, r)
		return
	}
	path := "/" + parts[3]
	if path == "/api" || strings.HasPrefix(path, "/api/") || path == "/healthz" {
		api.ServeHTTP(w, r)
		return
	}
	// Only local assets are served here, even when an upstream is offline.
	http.StripPrefix(strings.TrimSuffix(mount, "/"), g.assets).ServeHTTP(w, r)
}

func remoteDesktopAPI(target *url.URL, mount string) http.Handler {
	prefix := "spool_remote_" + strings.Split(mount, "/")[2] + "_"
	return &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL.Path = strings.TrimPrefix(p.In.URL.Path, strings.TrimSuffix(mount, "/"))
			if p.In.URL.RawPath != "" {
				p.Out.URL.RawPath = strings.TrimPrefix(p.In.URL.RawPath, strings.TrimSuffix(mount, "/"))
			}
			p.SetURL(target)
			p.Out.Host = target.Host
			if p.In.Header.Get("Origin") != "" {
				p.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
			}
			p.Out.Header.Del("Referer")
			p.Out.Header.Del("Cookie")
			p.Out.Header.Del("Authorization")
			for _, cookie := range p.In.Cookies() {
				if strings.HasPrefix(cookie.Name, prefix) {
					cookie.Name = strings.TrimPrefix(cookie.Name, prefix)
					p.Out.AddCookie(cookie)
				}
			}
		},
		ModifyResponse: func(r *http.Response) error {
			cookies := r.Cookies()
			r.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				cookie.Name = prefix + cookie.Name
				cookie.Path, cookie.Domain, cookie.Secure = mount, "", false
				cookie.HttpOnly, cookie.SameSite = true, http.SameSiteStrictMode
				r.Header.Add("Set-Cookie", cookie.String())
			}
			if location := r.Header.Get("Location"); location != "" {
				u, err := target.Parse(location)
				if err == nil && u.Scheme == target.Scheme && u.Host == target.Host && strings.HasPrefix(u.Path, target.Path) {
					u.Scheme, u.Host = "", ""
					u.Path = mount + strings.TrimPrefix(u.Path, target.Path)
					u.RawPath = ""
					r.Header.Set("Location", u.String())
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Could not connect to this server.", http.StatusBadGateway)
		},
		FlushInterval: -1,
	}
}
