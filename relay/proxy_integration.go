package relay

import (
	"net/http"
	"net/url"
	"strings"
)

// ProxyHandler belongs on a private listener. It cannot serve administrative
// operations or tenant content. Caddy's ask protocol has no custom auth header;
// its endpoint discloses only a yes/no public-domain policy and must stay on the
// proxy network. Traefik's full config requires its own read-only bearer.
func (s *Server) ProxyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet || len(r.Header.Values("Origin")) != 0 || r.Header.Get("Sec-Fetch-Site") != "" {
			http.Error(w, "invalid proxy request", 400)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			http.Error(w, "unavailable", 503)
			return
		}
		switch r.URL.Path {
		case "/v1/tls/allow":
			query, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil || len(query) != 1 || len(query["domain"]) != 1 {
				http.Error(w, "forbidden", 403)
				return
			}
			domain := query.Get("domain")
			for _, record := range s.registrations {
				if !record.active() || !record.WebPublished || record.WebBlocked {
					continue
				}
				address, _ := url.Parse(s.webURLLocked(record.ServerID))
				if address != nil && address.Hostname() == domain {
					w.WriteHeader(http.StatusOK)
					return
				}
			}
			http.Error(w, "forbidden", 403)
		case "/v1/traefik":
			if s.opts.TraefikToken == "" || !authorized(r, s.opts.TraefikToken) {
				http.Error(w, "unauthorized", 401)
				return
			}
			if r.URL.RawQuery != "" {
				http.Error(w, "unavailable", 404)
				return
			}
			routers := make(map[string]any)
			for _, record := range s.registrations {
				if !record.active() || !record.WebPublished || record.WebBlocked {
					continue
				}
				u, err := url.Parse(s.webURLLocked(record.ServerID))
				if err != nil || u.Host == "" {
					continue
				}
				name := "spool-web-" + record.ServerID
				rule := "Host(`" + u.Hostname() + "`)"
				routers[name+"-http"] = map[string]any{"rule": rule, "entryPoints": []string{"http"}, "middlewares": []string{"spool-web-https"}, "service": "spool-relay"}
				router := map[string]any{"rule": rule, "entryPoints": []string{"https"}, "service": "spool-relay"}
				// Scope certificate policy to this router, without requiring shared
				// entrypoint defaults. Empty TLS also suppresses such defaults when
				// the proxy uses an already loaded certificate.
				if s.opts.TraefikResolver == "none" {
					router["tls"] = map[string]any{}
				} else {
					router["tls"] = map[string]any{"certResolver": s.opts.TraefikResolver}
				}
				routers[name] = router
			}
			adminJSON(w, 200, map[string]any{"http": map[string]any{
				"routers":     routers,
				"middlewares": map[string]any{"spool-web-https": map[string]any{"redirectScheme": map[string]any{"scheme": "https", "permanent": true}}},
				"services":    map[string]any{"spool-relay": map[string]any{"loadBalancer": map[string]any{"passHostHeader": true, "servers": []any{map[string]string{"url": s.opts.ProxyUpstream}}}}},
			}})
		default:
			http.NotFound(w, r)
		}
	})
}

func validTraefikResolver(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func validProxyUpstream(address string) bool {
	u, err := url.Parse(address)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/") && !strings.ContainsAny(address, "\r\n\t ")
}
