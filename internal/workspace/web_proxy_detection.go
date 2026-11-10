package workspace

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
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
	cancel context.CancelFunc
	done   chan struct{}
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

// DetectWebProxy is a local management operation. A user-specified HTTPS origin
// must reach this listener with a one-time proof before any trust is saved.
// The detected peer replaces the previous automatic IP, never an inferred subnet.
func (s *Service) DetectWebProxy(ctx context.Context, address string) error {
	s.mu.Lock()
	custom := s.webProxyTransport
	s.mu.Unlock()
	if custom != nil {
		return s.detectWebProxy(ctx, address, custom)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	return s.detectWebProxy(ctx, address, transport)
}

// Revalidate after saving a public address or starting an ordinary Web entry.
// Jobs are ordered and superseded jobs are cancelled; no periodic probe runs.
// The entry owns cancellation, and Service.Close waits for these workers.
func (s *Service) startWebProxyDetectionLocked(settings Settings) {
	previous := s.webProxyJob
	if previous != nil {
		previous.cancel()
	}
	origin, err := publicWebProbeOrigin(settings.PublicURL)
	if err != nil || s.web == nil || !settings.WebEnabled {
		return
	}
	ingress := s.web
	ctx, cancel := context.WithCancel(ingress.ctx)
	job := &webProxyDetectionJob{cancel: cancel, done: make(chan struct{})}
	s.webProxyJob = job
	s.webError = ""
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer close(job.done)
		defer cancel()
		if previous != nil {
			select {
			case <-previous.done:
			case <-ctx.Done():
			}
		}
		err := s.DetectWebProxy(ctx, origin)
		s.mu.Lock()
		if s.webProxyJob == job {
			s.webProxyJob = nil
			if err != nil && ctx.Err() == nil && s.web == ingress && err.Error() != webProxyBusy && err.Error() != webProxyChanged {
				s.webError = err.Error()
			}
		}
		s.mu.Unlock()
		if err == nil {
			log.Print("event=web_proxy_detection_succeeded")
		} else if ctx.Err() == nil && err.Error() != webProxyBusy && err.Error() != webProxyChanged {
			log.Printf("event=web_proxy_detection_failed error=%q", err)
		}
	}()
}

func (s *Service) detectWebProxy(ctx context.Context, address string, transport http.RoundTripper) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s", webProxyChanged)
	}
	origin, err := publicWebProbeOrigin(address)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	s.mu.Lock()
	if s.closed || s.updating || s.web == nil || s.app == nil || !s.activeSettings.WebEnabled {
		s.mu.Unlock()
		return fmt.Errorf("%s", webProxyNeedsWeb)
	}
	app, ingress := s.app, s.web
	before, err := s.Store.Settings()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	probe := &webProxyProbe{path: webProxyProbePath + randomToken(), origin: origin, proof: randomToken(), expires: time.Now().Add(12 * time.Second)}
	app.webProxyMu.Lock()
	if app.webProxyProbe != nil {
		app.webProxyMu.Unlock()
		s.mu.Unlock()
		return fmt.Errorf("%s", webProxyBusy)
	}
	app.webProxyProbe = probe
	app.webProxyMu.Unlock()
	s.mu.Unlock()
	defer func() {
		app.webProxyMu.Lock()
		app.webProxyProbe = nil
		app.webProxyMu.Unlock()
	}()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+probe.path, nil)
	if err != nil {
		return fmt.Errorf("%s", webProxyNeedsHTTPS)
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
		return probeErr
	}
	if requestErr != nil || response.StatusCode != http.StatusOK || !peer.IsValid() || subtle.ConstantTimeCompare(body, []byte(probe.proof)) != 1 {
		return fmt.Errorf("%s", webProxyFailed)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil || s.closed || s.updating || s.app != app || s.web != ingress {
		return fmt.Errorf("%s", webProxyChanged)
	}
	next, err := s.Store.Settings()
	if err != nil {
		return err
	}
	if next.PublicURL != before.PublicURL || next.WebTrustedProxies != before.WebTrustedProxies ||
		next.WebEnabled != before.WebEnabled || next.Host != before.Host || next.Port != before.Port {
		return fmt.Errorf("%s", webProxyChanged)
	}
	next.WebDetectedProxy = netip.PrefixFrom(peer, peer.BitLen()).String()
	next.WebDetectedOrigin = origin
	next.PublicURL = origin
	// Only these three fields change, so the outbound network policy is intact.
	// Regular settings saves cannot write detection-owned metadata.
	if err := s.Store.SaveSettings(next); err != nil {
		return err
	}
	// Origin and proxy policy are safe to apply in place. Keep other pending
	// listener edits and running chats intact; no entry restart is needed.
	app.configMu.Lock()
	app.settings.PublicURL = next.PublicURL
	app.settings.WebDetectedProxy, app.settings.WebDetectedOrigin = next.WebDetectedProxy, next.WebDetectedOrigin
	app.configMu.Unlock()
	s.activeSettings.PublicURL = next.PublicURL
	s.activeSettings.WebDetectedProxy, s.activeSettings.WebDetectedOrigin = next.WebDetectedProxy, next.WebDetectedOrigin
	s.webError = ""
	return nil
}
