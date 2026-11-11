package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// WebPath is the internal gateway admission prefix, not a public URL or credential.
func WebPath(id string) string { return "/web/" + id + "/" }

// ParseWebPath validates the routing prefix before any tunnel is opened.
// Escaped IDs, dot segments and backslashes must not change which server owns
// a URL when a browser, reverse proxy or file server normalizes it.
func ParseWebPath(u *url.URL) (id, path string, ok bool) {
	if !strings.HasPrefix(u.Path, "/web/") {
		return "", "", false
	}
	id, rest, slash := strings.Cut(strings.TrimPrefix(u.Path, "/web/"), "/")
	if !validServerID(id) || strings.Contains(u.Path, "\\") {
		return "", "", false
	}
	for _, segment := range strings.Split(rest, "/") {
		if segment == "." || segment == ".." {
			return "", "", false
		}
	}
	if !slash {
		return id, "", u.EscapedPath() == "/web/"+id
	}
	return id, "/" + rest, strings.HasPrefix(u.EscapedPath(), WebPath(id)) && !strings.HasPrefix(rest, "/")
}

func (s *Server) serveTenant(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	domain := s.settings.WebDomain
	s.mu.Unlock()
	parsed, err := url.Parse("https://" + r.Host)
	if err != nil || domain == "" || !strings.HasSuffix(strings.ToLower(parsed.Hostname()), "."+domain) {
		return false
	}
	id := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), "."+domain)
	if !validServerID(id) {
		http.NotFound(w, r)
		return true
	}
	s.serveWeb(w, r, id)
	return true
}

func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	w.Header().Set("Permissions-Policy", "document-domain=()")
	if strings.HasPrefix(r.URL.Path, "/web/") || strings.Contains(r.URL.Path, "\\") || strings.HasPrefix(r.URL.Path, "//") {
		http.NotFound(w, r)
		return
	}
	for _, segment := range strings.Split(r.URL.Path, "/") {
		if segment == "." || segment == ".." {
			http.NotFound(w, r)
			return
		}
	}
	s.mu.Lock()
	adminOrigin, _ := url.Parse(s.settings.AdminOrigin)
	if adminOrigin != nil && adminOrigin.Host != "" && strings.EqualFold(r.Host, adminOrigin.Host) {
		s.mu.Unlock()
		http.Error(w, "Web applications require a separate origin from Relay administration", http.StatusForbidden)
		return
	}
	h := s.hosts[id]
	// The live host registry owns Web routes. Only authenticated hosts whose
	// signed connection handshake opts into Web access can publish a route.
	allowed := h != nil && !s.closed && h.ctx.Err() == nil && h.webOrigin != "" && h.webOrigin+"/" == s.webURLLocked(id)
	s.mu.Unlock()
	if !allowed {
		http.Error(w, "Spool Web is unavailable or not enabled", http.StatusServiceUnavailable)
		return
	}
	origin, _ := url.Parse(h.webOrigin)
	// The configured, signed origin is authoritative even behind a TLS reverse
	// proxy. Client-supplied Forwarded headers never choose an upstream or scheme.
	if !strings.EqualFold(r.Host, origin.Host) || r.Method == http.MethodConnect || len(r.Header.Values("Origin")) > 1 ||
		(r.Method != http.MethodGet && r.Method != http.MethodHead && strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site")) ||
		(r.Header.Get("Origin") != "" && r.Header.Get("Origin") != h.webOrigin) {
		http.Error(w, "invalid Web origin", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(h.ctx, cancel)
	defer func() { stop(); cancel() }()
	// Tunnels are reused per client address. net/http replays a request on a
	// reused connection only when it is idempotent or nothing was written, so
	// an uncertain mutation still fails instead of running twice.
	transport, finished := h.web.acquire(s.clientIP(r))
	defer finished()
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL.Scheme = "https"
			p.Out.URL.Host = id + ".spool.invalid"
			p.Out.Host = origin.Host
			p.Out.URL.Path = strings.TrimSuffix(WebPath(id), "/") + r.URL.Path
			p.Out.URL.RawPath = strings.TrimSuffix(WebPath(id), "/") + r.URL.EscapedPath()
			// Preserve Origin, CSRF, cookies, paths and streaming bodies for the
			// original Spool handlers. No external network destination is dialed.
			p.Out.Header.Del("X-Real-IP")
			p.Out.Header.Del("X-Forwarded-Prefix")
			p.Out.Header.Del("X-Spool-Web-Prefix")
		},
		Transport:     transport,
		FlushInterval: -1,
		ModifyResponse: func(response *http.Response) error {
			// Clear-Site-Data "cookies" also clears sibling domains' host-only
			// cookies. An untrusted tenant must not log out other servers.
			response.Header.Del("Clear-Site-Data")
			cookies := response.Cookies()
			response.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				cookie.Domain, cookie.Path = "", "/"
				cookie.Secure = origin.Scheme == "https"
				response.Header.Add("Set-Cookie", cookie.String())
			}
			if response.Header.Get("Service-Worker-Allowed") != "" {
				response.Header.Set("Service-Worker-Allowed", "/")
			}
			response.Header.Set("Origin-Agent-Cluster", "?1")
			response.Header.Set("Permissions-Policy", "document-domain=()")
			return nil
		},
		// Do not log request URLs, cookies, tokens or tunnel errors.
		ErrorLog: log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "Spool server is unavailable", http.StatusBadGateway)
		},
	}
	// The native broker's no-store default must not override the application's
	// immutable asset headers. The app still marks authenticated APIs no-store.
	w.Header().Del("Cache-Control")
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

// dialWeb consumes the same bounded one-time data channels as native clients.
// TLS pins the key authenticated during registration before any HTTP is sent.
// The tunnel keeps its reservation until the transport closes it, including
// while it is idle for reuse.
func (s *Server) dialWeb(ctx context.Context, h *registeredHost, clientIP string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	p := s.reservePair(h, true)
	if p == nil {
		return nil, errors.New("Web tunnel unavailable")
	}
	success := false
	defer func() {
		if !success {
			s.releasePair(p)
		}
	}()
	select {
	case h.open <- controlMessage{Type: "open", ID: p.id, Secret: p.secret, ClientIP: clientIP}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.ctx.Done():
		return nil, p.ctx.Err()
	}
	select {
	case socket := <-p.data:
		stream := byteStream(p.ctx, socket)
		go keepAlive(p.ctx, socket)
		conn := tls.Client(stream, (Pairing{PublicKey: h.publicKey}).TLSConfig())
		if err := conn.HandshakeContext(ctx); err != nil {
			stream.Close()
			return nil, err
		}
		success = true
		return &webConn{Conn: conn, release: func() { s.releasePair(p) }}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.ctx.Done():
		return nil, p.ctx.Err()
	}
}

type webConn struct {
	net.Conn
	release func()
}

func (c *webConn) Close() error {
	c.release()
	return c.Conn.Close()
}
