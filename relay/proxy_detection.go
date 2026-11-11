package relay

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const proxyProbePath = "/.well-known/spool-relay/proxy-check/"

const (
	proxyDetectionTimeout = 12 * time.Second
	proxyReadyTimeout     = 5 * time.Minute
	proxyReadyInterval    = time.Second
)

const proxyFailed = "无法验证此地址。请检查 HTTPS 证书，并将检测路径转发到 Relay 服务端口（28080）。"
const proxyHeaders = "地址已到达 Relay，但转发信息不完整。请让代理设置 X-Forwarded-Proto、X-Forwarded-For，并保留公网 Host。"

type proxyDetectionJob struct {
	cancel              context.CancelFunc
	done                chan struct{}
	path, origin, proof string
	expires             time.Time
	completed           bool
	waiting             bool
	peer                netip.Addr
	err                 error
}

// StartProxyDetection immediately rechecks the deployed address once.
func (s *Server) StartProxyDetection() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startProxyDetectionLocked(false)
}

// StartProxyDetectionWhenReady waits for the public HTTPS entry before checking
// proxy headers. Startup must not depend on the front proxy already running.
func (s *Server) StartProxyDetectionWhenReady() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startProxyDetectionLocked(true)
}

func (s *Server) startProxyDetectionLocked(waitForProxy bool) {
	previous := s.proxyJob
	if previous != nil {
		previous.cancel()
	}
	s.proxyJob, s.proxyError = nil, ""
	if s.closed || s.settings.PublicURL == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &proxyDetectionJob{cancel: cancel, done: make(chan struct{}), path: proxyProbePath + randomID(), origin: s.settings.PublicURL, proof: randomID(), waiting: waitForProxy}
	s.proxyJob = job
	s.proxyWorkers.Add(1)
	go func() {
		defer s.proxyWorkers.Done()
		defer close(job.done)
		defer cancel()
		if previous != nil {
			select {
			case <-previous.done:
			case <-ctx.Done():
			}
		}
		err := s.runProxyDetection(ctx, job)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.proxyJob == job {
			s.proxyJob = nil
			if err != nil && !s.closed {
				s.proxyError = err.Error()
			}
		}
	}()
}

func (s *Server) runProxyDetection(ctx context.Context, job *proxyDetectionJob) error {
	if job.waiting {
		readyCtx, cancel := context.WithTimeout(ctx, proxyReadyTimeout)
		err := waitForPublicProxy(readyCtx, job.origin)
		cancel()
		if err != nil {
			return errors.New("公网 HTTPS 代理尚未就绪。请检查代理、DNS 和证书，准备好后点击检测重试。")
		}
	}
	s.mu.Lock()
	if ctx.Err() != nil || s.closed || s.proxyJob != job {
		s.mu.Unlock()
		return errors.New("检测已取消。")
	}
	job.waiting = false
	job.expires = time.Now().Add(proxyDetectionTimeout)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, proxyDetectionTimeout)
	defer cancel()
	return s.detectProxy(ctx, job)
}

// Health is only a readiness signal. TLS, the one-use callback and forwarded
// headers are still verified separately before any proxy becomes trusted.
func waitForPublicProxy(ctx context.Context, origin string) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/healthz", nil)
		if err != nil {
			return err
		}
		r.Header.Set("Cache-Control", "no-store")
		response, err := client.Do(r)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
			response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && string(body) == "ok\n" {
				return nil
			}
		}
		timer := time.NewTimer(proxyReadyInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// A random callback path identifies this attempt; its independent response
// proof is never sent outbound. A reflecting endpoint cannot verify itself.
func (s *Server) serveProxyProbe(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	job := s.proxyJob
	if job == nil || job.completed || !time.Now().Before(job.expires) || job.path != r.URL.Path ||
		r.Method != http.MethodGet || r.URL.RawQuery != "" || r.URL.ForceQuery {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	job.completed = true
	public, _ := url.Parse(job.origin)
	host, hostOK := r.Host, true
	if len(r.Header.Values("X-Forwarded-Host")) != 0 {
		host, hostOK = singleForwardedHeader(r, "X-Forwarded-Host")
	}
	peerHost, _, _ := net.SplitHostPort(r.RemoteAddr)
	peer, peerErr := netip.ParseAddr(peerHost)
	valid := hostOK && strings.EqualFold(host, public.Host) && peerErr == nil
	if r.TLS == nil || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Proto") != "" {
		proto, protoOK := singleForwardedHeader(r, "X-Forwarded-Proto")
		chain := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
		client, err := netip.ParseAddr(strings.TrimSpace(chain[len(chain)-1]))
		valid = valid && protoOK && proto == "https" && err == nil && client.Zone() == "" && !client.IsUnspecified() && !client.IsMulticast()
		if valid {
			job.peer = peer.Unmap().WithZone("")
		}
	} // Direct TLS needs no automatic trusted proxy.
	if !valid {
		job.err = errors.New(proxyHeaders)
		s.mu.Unlock()
		http.Error(w, proxyHeaders, http.StatusBadRequest)
		return
	}
	proof := job.proof
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, proof)
}

func singleForwardedHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", false
	}
	value := strings.TrimSpace(values[0])
	return value, value != ""
}

func (s *Server) detectProxy(ctx context.Context, job *proxyDetectionJob) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, job.origin+job.path, nil)
	if err != nil {
		return errors.New(proxyFailed)
	}
	r.Header.Set("Cache-Control", "no-store")
	response, requestErr := client.Do(r)
	var body []byte
	if requestErr == nil {
		body, requestErr = io.ReadAll(io.LimitReader(response.Body, 1024))
		response.Body.Close()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.err != nil {
		return job.err
	}
	if requestErr != nil || response.StatusCode != http.StatusOK || !job.completed || subtle.ConstantTimeCompare(body, []byte(job.proof)) != 1 {
		return errors.New(proxyFailed)
	}
	if ctx.Err() != nil || s.closed || s.proxyJob != job || s.settings.PublicURL != job.origin {
		return errors.New("检测已取消。")
	}
	next := s.settings
	now := time.Now().UTC()
	next.DetectedOrigin, next.DetectedAt, next.DetectedProxy = job.origin, &now, ""
	if job.peer.IsValid() {
		next.DetectedProxy = netip.PrefixFrom(job.peer, job.peer.BitLen()).String()
	}
	if err := s.saveSettingsLocked(next); err != nil {
		return errors.New("检测成功，但无法保存设置。请检查 Relay 数据目录权限。")
	}
	s.settings = next
	s.applyProxySettingsLocked()
	return nil
}
