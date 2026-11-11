package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A real HTTPS gateway with fixture-only DNS, CA and persistent public settings.
// No production TLS verification is disabled by this helper.
func webPublicServer(t *testing.T, broker *Server) *httptest.Server {
	t.Helper()
	return webPublicServerWithHandler(t, broker, broker)
}

func webPublicServerWithHandler(t *testing.T, broker *Server, handler http.Handler) *httptest.Server {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "relay.localhost"}, DNSNames: []string{"relay.localhost", "*.web.relay.localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &private.PublicKey, private)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}}
	server.StartTLS()
	server.URL = "https://relay.localhost:" + strings.Split(server.Listener.Addr().String(), ":")[1]
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	pool := x509.NewCertPool()
	leaf, _ := x509.ParseCertificate(der)
	pool.AddCert(leaf)
	transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err == nil && (host == "relay.localhost" || strings.HasSuffix(host, ".relay.localhost")) {
			address = net.JoinHostPort("127.0.0.1", port)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	http.DefaultTransport = transport
	*server.Client() = http.Client{Transport: transport, Timeout: 5 * time.Second}
	t.Cleanup(func() {
		broker.Close()
		server.Close()
		transport.CloseIdleConnections()
		http.DefaultTransport = original
	})
	configureTestDeployment(t, broker, server.URL)
	return server
}
func testTenantURL(t *testing.T, base, id string) string {
	t.Helper()
	address, err := WebURL(base, id)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(address)
	if u.Host == "" {
		t.Fatal("missing tenant host")
	}
	return address
}

// Test-only startup wiring: choose the random listener URL before any host or
// verifier starts. Production addresses are supplied to NewServer by deployment.
func configureTestDeployment(t *testing.T, broker *Server, origin string) {
	t.Helper()
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.opts.PublicURL = origin
	if err := broker.loadSettings(); err != nil {
		t.Fatal(err)
	}
}
