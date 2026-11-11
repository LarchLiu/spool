package workspace

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"spool/relay"
	"strings"
	"testing"
	"time"
)

// A real HTTPS gateway with fixture-only DNS, CA and persistent public settings.
// No production TLS verification is disabled by this helper.
func relayWebPublicServer(t *testing.T, opts relay.ServerOptions, proxies ...string) (*relay.Server, *httptest.Server) {
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
	server := httptest.NewUnstartedServer(nil)
	opts.PublicURL = "https://relay.localhost:" + strings.Split(server.Listener.Addr().String(), ":")[1]
	if opts.AdminToken == "" {
		opts.AdminToken = strings.Repeat("a", 32)
	}
	if opts.DataDir == "" {
		opts.DataDir = t.TempDir()
	}
	broker, err := relay.NewServer(opts)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	server.Config.Handler = broker
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
	body, _ := json.Marshal(map[string]any{"trusted_proxies": proxies})
	r := httptest.NewRequest("PUT", "http://localhost/v1/settings", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	broker.AdminHandler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	return broker, server
}
func relayTenantURL(t *testing.T, base, id string) string {
	t.Helper()
	address, err := relay.WebURL(base, id)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(address)
	if u.Host == "" {
		t.Fatal("missing tenant host")
	}
	return address
}

func relayWebBroker(t *testing.T) (*relay.Server, *httptest.Server, string) {
	t.Helper()
	broker, public := relayWebPublicServer(t, relay.ServerOptions{})
	_, token, err := broker.CreateRegistration("workspace integration")
	if err != nil {
		t.Fatal(err)
	}
	return broker, public, token
}
