package tailnet

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
)

func tailnetTestStatus() *ipnstate.Status {
	return &ipnstate.Status{
		CurrentTailnet: &ipnstate.TailnetStatus{MagicDNSEnabled: true},
		Self:           &ipnstate.PeerStatus{DNSName: "spool.example.ts.net."},
		CertDomains:    []string{"spool.example.ts.net"},
	}
}

func TestTailnetOptions(t *testing.T) {
	for _, hostname := range []string{"spool-server", "Spool", "node-2", strings.Repeat("a", 63)} {
		if err := (Options{Hostname: hostname, Port: 443}).Validate(); err != nil {
			t.Fatalf("valid hostname %q: %v", hostname, err)
		}
	}
	for _, hostname := range []string{"", "-node", "node-", "two.nodes", "node/name", "node:443", "node name", strings.Repeat("a", 64)} {
		if err := (Options{Hostname: hostname, Port: 443}).Validate(); err == nil {
			t.Fatalf("accepted hostname %q", hostname)
		}
	}
	for _, port := range []int{-1, 0, 65536} {
		if err := (Options{Hostname: "spool", Port: port}).Validate(); err == nil {
			t.Fatalf("accepted port %d", port)
		}
	}
}

func TestTailnetHTTPSNameRequiresMagicDNSAndOwnCertificate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ipnstate.Status)
	}{
		{"missing tailnet", func(s *ipnstate.Status) { s.CurrentTailnet = nil }},
		{"MagicDNS disabled", func(s *ipnstate.Status) { s.CurrentTailnet.MagicDNSEnabled = false }},
		{"no self", func(s *ipnstate.Status) { s.Self = nil }},
		{"HTTPS disabled", func(s *ipnstate.Status) { s.CertDomains = nil }},
		{"other node certificate", func(s *ipnstate.Status) { s.CertDomains = []string{"other.example.ts.net"} }},
		{"empty hostname", func(s *ipnstate.Status) { s.Self.DNSName = "" }},
		{"bare hostname", func(s *ipnstate.Status) { s.Self.DNSName = "spool" }},
		{"path in hostname", func(s *ipnstate.Status) { s.Self.DNSName = "spool.example.ts.net/path" }},
		{"bad DNS label", func(s *ipnstate.Status) { s.Self.DNSName = "-spool.example.ts.net" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := tailnetTestStatus()
			tc.change(status)
			if _, err := tailnetHTTPSName(status); err == nil {
				t.Fatal("accepted invalid HTTPS status")
			}
		})
	}
	if _, err := tailnetHTTPSName(nil); err == nil {
		t.Fatal("accepted nil status")
	}
	status := tailnetTestStatus()
	status.Self.DNSName = "Spool.Example.TS.NET."
	if host, err := tailnetHTTPSName(status); err != nil || host != "spool.example.ts.net" {
		t.Fatal(host, err)
	}
}

type fakeTailnet struct {
	status                   *ipnstate.Status
	startErr, upErr, bindErr error
	waitForCancel            bool
	listener                 net.Listener
	network, address         string
	certificate              *tls.Certificate
	starts, closes, certs    atomic.Int32
	logins                   atomic.Int32
	statusProbe              func(context.Context) (*ipnstate.Status, error)
	login                    func(context.Context) error
}

func (n *fakeTailnet) Start() error {
	n.starts.Add(1)
	return n.startErr
}

func (n *fakeTailnet) Status(ctx context.Context) (*ipnstate.Status, error) {
	if n.statusProbe != nil {
		return n.statusProbe(ctx)
	}
	return n.status, nil
}

func (n *fakeTailnet) StartLoginInteractive(ctx context.Context) error {
	n.logins.Add(1)
	if n.login != nil {
		return n.login(ctx)
	}
	return nil
}

func (n *fakeTailnet) Up(ctx context.Context) (*ipnstate.Status, error) {
	if n.waitForCancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return n.status, n.upErr
}

func (n *fakeTailnet) Listen(network, address string) (net.Listener, error) {
	n.network, n.address = network, address
	if n.bindErr != nil {
		return nil, n.bindErr
	}
	var err error
	n.listener, err = net.Listen("tcp", "127.0.0.1:0")
	return n.listener, err
}

func (n *fakeTailnet) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	n.certs.Add(1)
	return n.certificate, nil
}

func (n *fakeTailnet) Close() error {
	n.closes.Add(1)
	if n.listener != nil {
		return n.listener.Close()
	}
	return nil
}

func TestTailnetStartupFailureClosesNode(t *testing.T) {
	broken := errors.New("test startup failure")
	for _, tc := range []struct {
		name string
		node *fakeTailnet
	}{
		{"start", &fakeTailnet{startErr: broken}},
		{"up", &fakeTailnet{upErr: broken}},
		{"HTTPS settings", &fakeTailnet{}},
		{"listen", &fakeTailnet{status: tailnetTestStatus(), bindErr: broken}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := tc.node
			if endpoint, err := openTailnet(context.Background(), node, 443, nil); err == nil || endpoint != nil {
				t.Fatal("startup failure was ignored", endpoint, err)
			}
			if node.starts.Load() != 1 || node.closes.Load() != 1 {
				t.Fatal("failed startup did not clean up the started node")
			}
		})
	}
}

func TestTailnetStartupHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	node := &fakeTailnet{waitForCancel: true}
	if _, err := openTailnet(ctx, node, 443, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("login did not honor cancellation", err)
	}
	if node.closes.Load() != 1 || node.listener != nil {
		t.Fatal("canceled login leaked a node or bound a listener")
	}
}

func TestTailnetLoginStatusIsAvailableBeforeEnrollment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := tailnetTestStatus()
	status.BackendState, status.AuthURL = "NeedsLogin", "https://login.tailscale.com/a/test"
	node := &fakeTailnet{status: status, waitForCancel: true}
	updates := make(chan Status, 2)
	done := make(chan error, 1)
	go func() {
		_, err := openTailnet(ctx, node, 443, func(status Status) {
			select {
			case updates <- status:
			default:
			}
		})
		done <- err
	}()
	select {
	case update := <-updates:
		if update.State != "NeedsLogin" || update.AuthURL != status.AuthURL {
			t.Fatal("missing native login state", update)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("enrollment blocked login status reporting")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || node.closes.Load() != 1 {
		t.Fatal("cancellation did not close the watcher and node", err)
	}
}

func TestTailnetRedactsExplicitAndEnvironmentKeys(t *testing.T) {
	t.Setenv("TS_AUTHKEY", "tskey-auth-env-one")
	t.Setenv("TS_AUTH_KEY", "tskey-auth-env-two")
	message := RedactSecrets("error tskey-auth-explicit / tskey-auth-env-one / tskey-auth-env-two", "tskey-auth-explicit")
	if strings.Contains(message, "tskey-auth-") || strings.Count(message, "[redacted]") != 3 {
		t.Fatal("did not redact all enrollment credentials")
	}
}

func TestTailnetLoginURLRequiresOfficialHTTPSOrigin(t *testing.T) {
	if !IsLoginURL("https://login.tailscale.com/a/test") {
		t.Fatal("official login URL was rejected")
	}
	for _, address := range []string{"", "http://login.tailscale.com/a/test", "https://evil.example/a", "https://login.tailscale.com.evil.example/a", "https://user@login.tailscale.com/a", "https://login.tailscale.com:8443/a", "javascript:alert(1)"} {
		if IsLoginURL(address) {
			t.Fatal("untrusted browser login URL accepted", address)
		}
	}
}

func TestTailnetLoginURLCanBeRequestedBeforeEnrollment(t *testing.T) {
	const address = "https://login.tailscale.com/a/test"
	var status atomic.Pointer[ipnstate.Status]
	status.Store(&ipnstate.Status{BackendState: "NeedsLogin"})
	node := &fakeTailnet{
		statusProbe: func(context.Context) (*ipnstate.Status, error) { return status.Load(), nil },
		login: func(context.Context) error {
			status.Store(&ipnstate.Status{BackendState: "NeedsLogin", AuthURL: address})
			return nil
		},
	}
	endpoint, err := startTailnet(context.Background(), node, 443, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	if endpoint.Listener() != nil || node.logins.Load() != 0 {
		t.Fatal("Start unexpectedly waited for enrollment or requested interactive login")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if got, err := endpoint.LoginURL(ctx); err != nil || got != address || node.logins.Load() != 1 {
		t.Fatal("explicit login did not obtain this node's link", err)
	}
	if got, err := endpoint.LoginURL(ctx); err != nil || got != address || node.logins.Load() != 1 {
		t.Fatal("an existing login link was unnecessarily regenerated", err)
	}
}

func TestTailnetLoginURLDoesNotReturnUntrustedLinksOrWaitForever(t *testing.T) {
	for _, address := range []string{"", "https://evil.example/a/test"} {
		node := &fakeTailnet{status: &ipnstate.Status{BackendState: "NeedsLogin", AuthURL: address}}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		got, err := (&Endpoint{node: node}).LoginURL(ctx)
		cancel()
		if got != "" || !errors.Is(err, context.DeadlineExceeded) || node.logins.Load() != 1 {
			t.Fatal("unavailable or untrusted login link was accepted", err)
		}
	}
	broken := errors.New("test login request failed")
	node := &fakeTailnet{login: func(context.Context) error { return broken }}
	if _, err := (&Endpoint{node: node}).LoginURL(context.Background()); !errors.Is(err, broken) {
		t.Fatal("login request failure was hidden", err)
	}
	node = &fakeTailnet{status: &ipnstate.Status{BackendState: "Running"}}
	if _, err := (&Endpoint{node: node}).LoginURL(context.Background()); err == nil || node.logins.Load() != 0 {
		t.Fatal("interactive login was requested for an already connected node")
	}
}

func TestTailnetEndpointUsesTLSAndRestrictsServerName(t *testing.T) {
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := seed.TLS.Certificates[0]
	transport := seed.Client().Transport.(*http.Transport).Clone()
	seed.Close()
	transport.TLSClientConfig.ServerName = "example.com"
	t.Cleanup(transport.CloseIdleConnections)
	status := tailnetTestStatus()
	status.Self.DNSName, status.CertDomains = "example.com.", []string{"example.com"}
	node := &fakeTailnet{status: status, certificate: &certificate}
	endpoint, err := openTailnet(context.Background(), node, 8443, nil)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Origin() != "https://example.com:8443" || node.network != "tcp" || node.address != ":8443" {
		t.Fatal("wrong advertised origin or tailnet bind", endpoint.Origin(), node.network, node.address)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			t.Error("TLS was terminated before HTTP ingress")
		}
		_, _ = io.WriteString(w, "TLS reached Spool")
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(endpoint.Listener()) }()
	t.Cleanup(func() {
		_ = server.Close()
		<-done
		_ = endpoint.Close()
	})
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get("https://" + endpoint.Listener().Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "TLS reached Spool" || node.certs.Load() == 0 {
		t.Fatal("TLS did not reach the app through the certificate callback", string(body), err)
	}
	for _, name := range []string{"other.example.com", ""} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		dialer := tls.Dialer{Config: &tls.Config{RootCAs: transport.TLSClientConfig.RootCAs, ServerName: name}}
		conn, err := dialer.DialContext(ctx, "tcp", endpoint.Listener().Addr().String())
		cancel()
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			t.Fatal("accepted foreign/missing TLS server name", name)
		}
	}
}
