package tailnet

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

type Options struct {
	Hostname string
	Port     int
	AuthKey  string
}

func (o Options) Validate() error {
	if !validDNSLabel(o.Hostname) {
		return fmt.Errorf("tsnet hostname must be a DNS label (1-63 letters, digits or hyphens, without leading/trailing hyphens)")
	}
	if o.Port < 1 || o.Port > 65535 {
		return fmt.Errorf("tsnet port must be between 1 and 65535")
	}
	return nil
}

type tailnetNode interface {
	Start() error
	Up(context.Context) (*ipnstate.Status, error)
	Listen(string, string) (net.Listener, error)
	GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
	Status(context.Context) (*ipnstate.Status, error)
	StartLoginInteractive(context.Context) error
	Close() error
}

type embeddedTailnet struct{ *tsnet.Server }

func (n embeddedTailnet) Status(ctx context.Context) (*ipnstate.Status, error) {
	client, err := n.LocalClient()
	if err != nil {
		return nil, err
	}
	return client.StatusWithoutPeers(ctx)
}

func (n embeddedTailnet) StartLoginInteractive(ctx context.Context) error {
	client, err := n.LocalClient()
	if err != nil {
		return err
	}
	return client.StartLoginInteractive(ctx)
}

func (n embeddedTailnet) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	client, err := n.LocalClient()
	if err != nil {
		return nil, err
	}
	return client.GetCertificate(hello)
}

type Status struct {
	State   string
	AuthURL string
	URL     string
	Error   string
}

type Endpoint struct {
	node        tailnetNode
	listener    net.Listener
	origin      string
	port        int
	cancelWatch context.CancelFunc
	watchDone   chan struct{}
	closeOnce   sync.Once
	closeError  error
}

func New(ctx context.Context, root string, options Options, report func(Status)) (*Endpoint, error) {
	endpoint, err := Start(ctx, root, options, report)
	if err != nil {
		return nil, err
	}
	if err := endpoint.Up(ctx); err != nil {
		_ = endpoint.Close()
		return nil, err
	}
	return endpoint, nil
}

// Enroll verifies enrollment and persists the node identity without publishing
// an HTTPS listener. The running Spool service is not restarted or replaced.
func Enroll(ctx context.Context, root string, options Options, report func(Status)) error {
	endpoint, err := Start(ctx, root, options, report)
	if err != nil {
		return err
	}
	defer endpoint.Close()
	_, err = endpoint.node.Up(ctx)
	return err
}

// Start exposes login controls before enrollment finishes. Up must succeed
// before Listener and Origin can be used to serve HTTPS.
func Start(ctx context.Context, root string, options Options, report func(Status)) (*Endpoint, error) {
	if proxyInitError != nil {
		return nil, fmt.Errorf("configure Tailscale proxy: %w", proxyInitError)
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "tsnet")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create tsnet state: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, fmt.Errorf("secure tsnet state: %w", err)
	}
	// tsnet reads TS_AUTHKEY for unattended enrollment. Without it, its user
	// logger prints an interactive login URL. Credentials stay out of SQLite.
	node := embeddedTailnet{&tsnet.Server{
		Dir: dir, Hostname: options.Hostname, AuthKey: options.AuthKey,
		UserLogf: func(format string, args ...any) {
			log.Print(RedactSecrets(fmt.Sprintf(format, args...), options.AuthKey))
		},
	}}
	return startTailnet(ctx, node, options.Port, report)
}

// RedactSecrets also covers the environment keys supported by tsnet itself.
func RedactSecrets(message, authKey string) string {
	for _, secret := range []string{authKey, os.Getenv("TS_AUTHKEY"), os.Getenv("TS_AUTH_KEY")} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return message
}

func openTailnet(ctx context.Context, node tailnetNode, port int, report func(Status)) (_ *Endpoint, err error) {
	endpoint, err := startTailnet(ctx, node, port, report)
	if err != nil {
		return nil, err
	}
	if err := endpoint.Up(ctx); err != nil {
		_ = endpoint.Close()
		return nil, err
	}
	return endpoint, nil
}

func startTailnet(ctx context.Context, node tailnetNode, port int, report func(Status)) (*Endpoint, error) {
	endpoint := &Endpoint{node: node, port: port}
	// Close is only safe after Start has returned, never concurrently with it.
	if err := node.Start(); err != nil {
		_ = endpoint.Close()
		return nil, fmt.Errorf("start tsnet: %w", err)
	}
	if report != nil {
		endpoint.watch(ctx, report)
	}
	return endpoint, nil
}

func (e *Endpoint) Up(ctx context.Context) error {
	status, err := e.node.Up(ctx)
	if err != nil {
		return fmt.Errorf("connect tsnet: %w", err)
	}
	host, err := tailnetHTTPSName(status)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	listener, err := e.node.Listen("tcp", ":"+strconv.Itoa(e.port))
	if err != nil {
		return fmt.Errorf("listen on tsnet: %w", err)
	}
	// Use Listen rather than ListenTLS: the latter waits in Up with a
	// background context, which cannot be interrupted during startup.
	listener = tls.NewListener(listener, &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if !strings.EqualFold(strings.TrimSuffix(hello.ServerName, "."), host) {
				return nil, fmt.Errorf("TLS server name must match the tsnet HTTPS hostname")
			}
			return e.node.GetCertificate(hello)
		},
	})
	authority := host
	if e.port != 443 {
		authority = net.JoinHostPort(host, strconv.Itoa(e.port))
	}
	e.listener, e.origin = listener, "https://"+authority
	return nil
}

// LoginURL explicitly requests interactive enrollment when tsnet has not yet
// supplied a link. Never substitute the generic Tailscale website: that would
// sign in to a browser account without authorizing this node.
func (e *Endpoint) LoginURL(ctx context.Context) (string, error) {
	status, err := e.node.Status(ctx)
	if err != nil {
		return "", err
	}
	if status != nil {
		if IsLoginURL(status.AuthURL) {
			return status.AuthURL, nil
		}
		if status.BackendState == "Running" {
			return "", fmt.Errorf("Tailscale is already connected. No login is required.")
		}
	}
	if err := e.node.StartLoginInteractive(ctx); err != nil {
		return "", err
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			status, err = e.node.Status(ctx)
			if err != nil {
				return "", err
			}
			if status == nil {
				continue
			}
			if IsLoginURL(status.AuthURL) {
				return status.AuthURL, nil
			}
			if status.BackendState == "Running" {
				return "", fmt.Errorf("Tailscale is already connected. No login is required.")
			}
		}
	}
}

func (e *Endpoint) Listener() net.Listener { return e.listener }
func (e *Endpoint) Origin() string         { return e.origin }

func (e *Endpoint) Close() error {
	e.closeOnce.Do(func() {
		if e.cancelWatch != nil {
			e.cancelWatch()
			<-e.watchDone
		}
		e.closeError = e.node.Close()
	})
	return e.closeError
}

func (e *Endpoint) watch(ctx context.Context, report func(Status)) {
	ctx, e.cancelWatch = context.WithCancel(ctx)
	e.watchDone = make(chan struct{})
	go func() {
		defer close(e.watchDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			probe, cancel := context.WithTimeout(ctx, 2*time.Second)
			status, err := e.node.Status(probe)
			cancel()
			if err == nil && status != nil {
				authURL := status.AuthURL
				if !IsLoginURL(authURL) {
					authURL = ""
				}
				report(Status{State: status.BackendState, AuthURL: authURL})
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func IsLoginURL(address string) bool {
	u, err := url.Parse(address)
	return err == nil && u.Scheme == "https" && u.Host == "login.tailscale.com" && u.User == nil && u.Fragment == ""
}

func tailnetHTTPSName(status *ipnstate.Status) (string, error) {
	if status == nil || status.CurrentTailnet == nil || !status.CurrentTailnet.MagicDNSEnabled {
		return "", fmt.Errorf("tsnet HTTPS requires MagicDNS; enable it in the Tailscale admin DNS settings")
	}
	if status.Self == nil {
		return "", fmt.Errorf("tsnet did not report this node's DNS name")
	}
	host := strings.ToLower(strings.TrimSuffix(status.Self.DNSName, "."))
	if len(host) > 253 || !strings.Contains(host, ".") {
		return "", fmt.Errorf("tsnet did not report a valid HTTPS DNS name")
	}
	for _, label := range strings.Split(host, ".") {
		if !validDNSLabel(label) {
			return "", fmt.Errorf("tsnet did not report a valid HTTPS DNS name")
		}
	}
	for _, domain := range status.CertDomains {
		if strings.EqualFold(strings.TrimSuffix(domain, "."), host) {
			return host, nil
		}
	}
	return "", fmt.Errorf("tsnet HTTPS certificates are unavailable for %s; enable HTTPS in the Tailscale admin DNS settings", host)
}

func validDNSLabel(label string) bool {
	if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, c := range label {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
