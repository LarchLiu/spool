package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

type desktopDevFrontend struct {
	url    string
	server *http.Server
}

// Keep the UI, HMR socket and API on one loopback origin. Resolving the active
// service for each API request also handles port changes without restarting Vite.
func startDesktopDevFrontend(development bool, address string, serviceURL func() (string, error)) (*desktopDevFrontend, error) {
	if !development || address == "" {
		return nil, nil
	}
	target, err := url.Parse(address)
	if err != nil || target.Opaque != "" || target.User != nil ||
		(target.Scheme != "http" && target.Scheme != "https") ||
		(target.Path != "" && target.Path != "/") || target.RawPath != "" ||
		target.RawQuery != "" || target.ForceQuery || target.Fragment != "" || strings.Contains(address, "#") {
		return nil, fmt.Errorf("MYGO_DEV_URL must be an HTTP(S) loopback origin")
	}
	switch target.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return nil, fmt.Errorf("MYGO_DEV_URL must use 127.0.0.1, localhost or [::1]")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	frontend := &desktopDevFrontend{url: "http://" + listener.Addr().String()}
	assets := httputil.NewSingleHostReverseProxy(target)
	frontend.server = &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != listener.Addr().String() {
				http.Error(w, "invalid desktop development host", http.StatusForbidden)
				return
			}
			if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
				address, err := serviceURL()
				if err != nil {
					http.Error(w, "chat service is not running", http.StatusServiceUnavailable)
					return
				}
				target, err := url.Parse(address)
				if err != nil {
					http.Error(w, "invalid chat service address", http.StatusBadGateway)
					return
				}
				// The service allows plain HTTP only for a direct loopback request and
				// reads any forwarding header as a remote proxy, so add none here.
				// Restore the ones Rewrite strips: a request another proxy relayed
				// to this listener must stay recognizable to the service.
				api := &httputil.ReverseProxy{
					Rewrite: func(r *httputil.ProxyRequest) {
						r.SetURL(target)
						// Preserve Host/Origin for the existing CSRF checks.
						r.Out.Host = r.In.Host
						for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
							if values := r.In.Header.Values(name); len(values) != 0 {
								r.Out.Header[name] = append([]string(nil), values...)
							}
						}
					},
					// Flush SSE immediately rather than buffering streamed chat events.
					FlushInterval: -1,
				}
				api.ServeHTTP(w, r)
				return
			}
			// ReverseProxy also forwards Vite's WebSocket upgrade for HMR.
			assets.ServeHTTP(w, r)
		}),
	}
	go func() {
		if err := frontend.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("event=desktop_dev_frontend_failed error=%q", err)
		}
	}()
	return frontend, nil
}

func (f *desktopDevFrontend) close() {
	if f != nil {
		_ = f.server.Close()
	}
}

func (p *panel) settingsURL() string {
	if p.devFrontend != nil {
		return p.devFrontend.url + "/desktop.html"
	}
	return settingsPageURL
}

func (p *panel) settingsDocumentAllowed(address string) bool {
	if p.devFrontend != nil {
		return address == p.settingsURL()
	}
	return settingsDocumentAllowed(address)
}
