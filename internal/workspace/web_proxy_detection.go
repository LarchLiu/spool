package workspace

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const webProxyProbePath = "/.well-known/spool/proxy-check/"

const (
	webProxyNeedsHTTPS = "Enter a public HTTPS address before detecting."
	webProxyNeedsWeb   = "Start Web access before detecting."
	webProxyBusy       = "Detection is already in progress."
	webProxyChanged    = "Web settings changed during detection. Try again."
	webProxyFailed     = "Could not verify this address. Check that your tunnel or reverse proxy points to this Spool server."
	webProxyHeaders    = "The address reaches Spool, but its forwarding information is incomplete. Check your tunnel or reverse proxy settings."
)

type ordinaryWebKey struct{}

// The callback is accepted only on the ordinary Web listener, never on the
// desktop's private entry, Relay, or Tailscale.
func ordinaryWebHandler(app *App) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ordinaryWebKey{}, true)))
	})
}

type webProxyProbe struct {
	path, origin, proof string
	expires             time.Time
	peer                netip.Addr
	err                 error
	completed           bool
}

type webProxyDetectionJob struct {
	cancel   context.CancelFunc
	done     chan struct{} // Probe and listener cleanup; never needs Service.mu.
	finished chan struct{} // Includes publishing the result under Service.mu.
	err      error
}

// WebProxyCheck describes one check of a specific saved configuration. Preflight
// results are informational and never become the running entry's proxy trust.
type WebProxyCheck struct {
	ID        string
	PublicURL string
	Host      string
	Port      int
	Preflight bool
	Reused    bool
	Succeeded bool
	Proxy     string
	Error     string
}

// The random path only identifies the attempt. The independent response proof
// is never sent outbound, so reflecting a request cannot pass verification.
func (a *App) serveWebProxyProbe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a.webProxyMu.Lock()
	probe := a.webProxyProbe
	ordinary, _ := r.Context().Value(ordinaryWebKey{}).(bool)
	if probe == nil || probe.completed || !time.Now().Before(probe.expires) || r.URL.Path != probe.path ||
		r.Method != http.MethodGet || r.URL.RawQuery != "" || r.URL.ForceQuery || r.TLS != nil || !ordinary {
		a.webProxyMu.Unlock()
		http.NotFound(w, r)
		return
	}
	public, _ := url.Parse(probe.origin)
	proto, validProto := singleProxyHeader(r, "X-Forwarded-Proto")
	host, validHost := r.Host, true
	if len(r.Header.Values("X-Forwarded-Host")) != 0 {
		host, validHost = singleProxyHeader(r, "X-Forwarded-Host")
	}
	forwarded := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	client, err := netip.ParseAddr(strings.TrimSpace(forwarded[len(forwarded)-1]))
	peer := requestPeerIP(r)
	probe.completed = true
	if !validProto || proto != "https" || !validHost || !strings.EqualFold(host, public.Host) ||
		!peer.IsValid() || err != nil || client.Zone() != "" || client.IsUnspecified() || client.IsMulticast() {
		probe.err = fmt.Errorf("%s", webProxyHeaders)
		a.webProxyMu.Unlock()
		http.Error(w, webProxyHeaders, http.StatusBadRequest)
		return
	}
	probe.peer = peer
	proof := probe.proof
	a.webProxyMu.Unlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, proof)
}

func publicWebProbeOrigin(address string) (string, error) {
	address = strings.TrimSpace(address)
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(address, "#") ||
		(u.Path != "" && u.Path != "/") || strings.ContainsAny(address, "\r\n\t ") {
		return "", fmt.Errorf("%s", webProxyNeedsHTTPS)
	}
	u.Path, u.RawPath = "", ""
	return u.String(), nil
}

// DetectWebProxy waits for a check of the saved address. Pending settings use a
// preflight; only a check of the applied entry may update automatic proxy trust.
func (s *Service) DetectWebProxy(ctx context.Context, address string) error {
	return s.detectWebProxy(ctx, address, nil)
}

// BeginWebProxyDetection starts a retry without blocking desktop mutations.
func (s *Service) BeginWebProxyDetection(address string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.beginSavedWebProxyCheckLocked(s.ctx, address, nil)
	return err
}

func (s *Service) detectWebProxy(ctx context.Context, address string, transport http.RoundTripper) error {
	s.mu.Lock()
	job, err := s.beginSavedWebProxyCheckLocked(ctx, address, transport)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	<-job.finished
	return job.err
}

func sameWebProxySettings(a, b Settings) bool {
	return a.WebEnabled == b.WebEnabled && a.Host == b.Host && a.Port == b.Port &&
		a.PublicURL == b.PublicURL && a.WebTrustedProxies == b.WebTrustedProxies
}

func (s *Service) beginSavedWebProxyCheckLocked(ctx context.Context, address string, transport http.RoundTripper) (*webProxyDetectionJob, error) {
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s", webProxyChanged)
	}
	origin, err := publicWebProbeOrigin(address)
	if err != nil {
		return nil, err
	}
	if s.closed || s.updating || s.server == nil {
		return nil, fmt.Errorf("%s", webProxyNeedsWeb)
	}
	settings, err := s.Store.Settings()
	if err != nil {
		return nil, err
	}
	if !settings.WebEnabled {
		return nil, fmt.Errorf("%s", webProxyNeedsWeb)
	}
	if savedOrigin, _ := publicWebProbeOrigin(settings.PublicURL); savedOrigin != origin {
		return nil, fmt.Errorf("%s", webProxyChanged)
	}
	if s.webProxyJob != nil {
		return nil, fmt.Errorf("%s", webProxyBusy)
	}
	return s.beginWebProxyCheckLocked(ctx, settings, !sameWebProxySettings(settings, s.activeSettings) || s.web == nil, transport), nil
}

// Cancellation waits only for network/listener cleanup, not result publication.
// It is safe to hold Service.mu here: a cancelled worker cannot publish later.
func (s *Service) stopWebProxyDetectionLocked() {
	if job := s.webProxyJob; job != nil {
		job.cancel()
		<-job.done
		s.webProxyJob = nil
	}
}

// CancelWebProxyDetection ignores delayed edit notifications for older jobs.
func (s *Service) CancelWebProxyDetection(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.webProxyCheck == nil || s.webProxyCheck.ID != id {
		return
	}
	s.stopWebProxyDetectionLocked()
	s.webProxyCheck, s.webError = nil, ""
}

func (s *Service) startWebProxyPreflightLocked(settings Settings) {
	s.startWebProxyCheckLocked(settings, true)
}

// Revalidate the real entry on startup/apply; settings saves only preflight.
func (s *Service) startWebProxyDetectionLocked(settings Settings) {
	s.startWebProxyCheckLocked(settings, false)
}

func (s *Service) startWebProxyCheckLocked(settings Settings, preflight bool) {
	s.stopWebProxyDetectionLocked()
	s.webProxyCheck, s.webError = nil, ""
	if _, err := publicWebProbeOrigin(settings.PublicURL); err != nil || s.server == nil || !settings.WebEnabled {
		return
	}
	s.beginWebProxyCheckLocked(s.ctx, settings, preflight, nil)
}

// Reuse only a bind conflict attributable to this Service's own listener. Host
// scope changes can overlap it (for example 127.0.0.1 -> 0.0.0.0), too.
func webListenerOverlaps(a, b Settings) bool {
	if a.Port != b.Port {
		return false
	}
	if a.Host == b.Host {
		return true
	}
	ip := func(host string) netip.Addr {
		if host == "localhost" {
			return netip.MustParseAddr("127.0.0.1")
		}
		address, _ := netip.ParseAddr(host)
		return address.Unmap()
	}
	x, y := ip(a.Host), ip(b.Host)
	return x.IsValid() && y.IsValid() && (x == y || x.IsUnspecified() && (x.Is6() || x.Is4() == y.Is4()) || y.IsUnspecified() && (y.Is6() || x.Is4() == y.Is4()))
}

func (s *Service) beginWebProxyCheckLocked(parent context.Context, settings Settings, preflight bool, transport http.RoundTripper) *webProxyDetectionJob {
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	job := &webProxyDetectionJob{cancel: cancel, done: make(chan struct{}), finished: make(chan struct{})}
	check := WebProxyCheck{ID: randomToken(), PublicURL: settings.PublicURL, Host: settings.Host, Port: settings.Port, Preflight: preflight}
	app, ingress := s.app, s.web
	probeApp := app
	var listener net.Listener
	var setupErr error
	if preflight {
		listener, setupErr = net.Listen("tcp", settings.Address())
		if ingress != nil && s.activeSettings.WebEnabled && webListenerOverlaps(settings, s.activeSettings) && (setupErr == nil || errors.Is(setupErr, syscall.EADDRINUSE)) {
			// Some systems permit wildcard and specific-IP listeners to bind
			// the same port. Callbacks may still reach the existing listener,
			// so overlapping scopes must use its callback explicitly.
			if listener != nil {
				listener.Close()
				listener = nil
			}
			check.Reused, setupErr = true, nil
		} else if setupErr == nil {
			// This object owns only a probe; it has no Store, agents or sessions.
			probeApp = &App{}
		}
	} else if ingress == nil || !sameWebProxySettings(settings, s.activeSettings) {
		setupErr = fmt.Errorf("%s", webProxyChanged)
	}
	if transport == nil {
		transport = s.webProxyTransport
	}
	s.webProxyJob, s.webProxyCheck, s.webError = job, &check, ""
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer close(job.finished)
		defer cancel()
		peer, err := runWebProxyCheck(ctx, settings.PublicURL, probeApp, listener, transport, setupErr)
		// Cleanup must finish before Apply can bind this same port. Everything
		// above this point is independent of Service.mu.
		close(job.done)
		s.mu.Lock()
		defer s.mu.Unlock()
		job.err = fmt.Errorf("%s", webProxyChanged)
		if s.webProxyJob != job || ctx.Err() == context.Canceled || s.closed || s.updating || s.app != app || s.web != ingress {
			if s.webProxyJob == job {
				s.webProxyJob, s.webProxyCheck = nil, nil
			}
			return
		}
		s.webProxyJob = nil
		next, readErr := s.Store.Settings()
		if readErr != nil {
			err = readErr
		} else if !sameWebProxySettings(next, settings) {
			s.webProxyCheck = nil
			return
		}
		if ctx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("%s", webProxyFailed)
		}
		if err == nil && !preflight {
			// Detection never writes user-owned URL/host/port fields. A stale
			// startup check cannot overwrite a newer pending configuration.
			next.WebDetectedProxy = netip.PrefixFrom(peer, peer.BitLen()).String()
			next.WebDetectedOrigin = settings.PublicURL
			err = s.Store.SaveSettings(next)
			if err == nil {
				app.configMu.Lock()
				app.settings.WebDetectedProxy, app.settings.WebDetectedOrigin = next.WebDetectedProxy, next.WebDetectedOrigin
				app.configMu.Unlock()
				s.activeSettings.WebDetectedProxy, s.activeSettings.WebDetectedOrigin = next.WebDetectedProxy, next.WebDetectedOrigin
			}
		}
		result := check
		result.Succeeded = err == nil
		if err == nil {
			result.Proxy = netip.PrefixFrom(peer, peer.BitLen()).String()
			log.Printf("event=web_proxy_detection_succeeded preflight=%t", preflight)
		} else {
			result.Error = err.Error()
			log.Printf("event=web_proxy_detection_failed preflight=%t error=%q", preflight, err)
		}
		s.webProxyCheck, s.webError, job.err = &result, result.Error, err
	}()
	return job
}

// runWebProxyCheck owns all probe resources and never locks the Service. A
// temporary listener exposes only the one-time callback, never application APIs.
func runWebProxyCheck(ctx context.Context, address string, app *App, listener net.Listener, transport http.RoundTripper, setupErr error) (netip.Addr, error) {
	if listener != nil {
		defer listener.Close()
	}
	if setupErr != nil {
		return netip.Addr{}, setupErr
	}
	origin, err := publicWebProbeOrigin(address)
	if err != nil {
		return netip.Addr{}, err
	}
	probe := &webProxyProbe{path: webProxyProbePath + randomToken(), origin: origin, proof: randomToken(), expires: time.Now().Add(12 * time.Second)}
	app.webProxyMu.Lock()
	if app.webProxyProbe != nil {
		app.webProxyMu.Unlock()
		if listener != nil {
			listener.Close()
		}
		return netip.Addr{}, fmt.Errorf("%s", webProxyBusy)
	}
	app.webProxyProbe = probe
	app.webProxyMu.Unlock()
	defer func() {
		app.webProxyMu.Lock()
		app.webProxyProbe = nil
		app.webProxyMu.Unlock()
	}()
	if listener != nil {
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			app.serveWebProxyProbe(w, r.WithContext(context.WithValue(r.Context(), ordinaryWebKey{}, true)))
		}), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Second}
		done := make(chan struct{})
		go func() { defer close(done); _ = server.Serve(listener) }()
		defer func() { _ = server.Close(); <-done }()
	}
	if transport == nil {
		owned := http.DefaultTransport.(*http.Transport).Clone()
		defer owned.CloseIdleConnections()
		transport = owned
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+probe.path, nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%s", webProxyNeedsHTTPS)
	}
	request.Header.Set("Cache-Control", "no-store")
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, requestErr := client.Do(request)
	var body []byte
	if requestErr == nil {
		body, requestErr = io.ReadAll(io.LimitReader(response.Body, 1024))
		response.Body.Close()
	}
	app.webProxyMu.Lock()
	peer, probeErr := probe.peer, probe.err
	app.webProxyMu.Unlock()
	if probeErr != nil {
		return netip.Addr{}, probeErr
	}
	if requestErr != nil || response.StatusCode != http.StatusOK || !peer.IsValid() || subtle.ConstantTimeCompare(body, []byte(probe.proof)) != 1 {
		return netip.Addr{}, fmt.Errorf("%s", webProxyFailed)
	}
	return peer, nil
}
