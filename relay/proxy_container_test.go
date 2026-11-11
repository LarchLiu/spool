package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in actual proxy binaries: persistent policy, on-demand certificate gate,
// HTTP provider discovery, TLS termination and exact Host routing. All runtime
// storage lives in t.TempDir and owned containers are removed before cleanup.
func TestProxyContainers(t *testing.T) {
	if os.Getenv("SPOOL_PROXY_CONTAINER_TEST") != "1" {
		t.Skip("set SPOOL_PROXY_CONTAINER_TEST=1; needs Docker and the bundled Caddy image")
	}
	for _, kind := range []string{"caddy", "traefik-individual", "traefik-wildcard"} {
		t.Run(kind, func(t *testing.T) {
			s := testBroker(t)
			s.opts.TraefikToken = strings.Repeat("p", 32)
			s.opts.TraefikResolver = "fixture"
			public := httptest.NewServer(s)
			t.Cleanup(public.Close)
			integration := httptest.NewServer(s.ProxyHandler())
			t.Cleanup(integration.Close)
			upstream := "http://host.docker.internal:" + strings.Split(public.Listener.Addr().String(), ":")[1]
			policy := "http://host.docker.internal:" + strings.Split(integration.Listener.Addr().String(), ":")[1]
			s.opts.ProxyUpstream = upstream
			mode := "individual"
			if kind == "traefik-wildcard" {
				mode = "wildcard"
			}
			if mode == "wildcard" {
				s.opts.TraefikResolver = "none"
			}
			configureTestDeployment(t, s, "https://relay.example.com")
			id, _ := NewIdentity()
			registration, token := createViaAPI(t, s, "container Web")
			host, err := NewHost(t.Context(), public.URL, token, id, nil)
			if err != nil {
				t.Fatal(err)
			}
			host.WebEnabled = true
			ready := make(chan struct{}, 1)
			host.OnWebURL = func(string) {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
			cfg, _ := id.TLSConfig()
			app := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Host+" "+r.URL.Path) })}
			served, done := make(chan struct{}), make(chan struct{})
			go func() { app.Serve(tls.NewListener(host, cfg)); close(served) }()
			go func() { host.Run(); host.Wait(); close(done) }()
			t.Cleanup(func() { host.Close(); app.Close(); <-done; <-served })
			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				t.Fatal("host not ready")
			}
			name := "spool-proxy-test-" + strings.ToLower(randomID()[:12])
			directory := t.TempDir()
			run := func(args ...string) string {
				t.Helper()
				command := exec.CommandContext(t.Context(), "docker", args...)
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("docker %s: %v\n%s", args[0], err, output)
				}
				return string(output)
			}
			args := []string{"run", "-d", "--name", name, "--add-host=host.docker.internal:host-gateway", "-p", "127.0.0.1::443"}
			if kind == "caddy" {
				config := map[string]any{"admin": map[string]any{"disabled": true}, "apps": map[string]any{
					"tls":  map[string]any{"automation": map[string]any{"on_demand": map[string]any{"ask": policy + "/v1/tls/allow"}, "policies": []any{map[string]any{"on_demand": true, "issuers": []any{map[string]any{"module": "internal"}}}}}},
					"http": map[string]any{"servers": map[string]any{"web": map[string]any{"listen": []string{":443"}, "tls_connection_policies": []any{map[string]any{}}, "routes": []any{map[string]any{"handle": []any{map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]string{"dial": strings.TrimPrefix(upstream, "http://")}}}}}}}}}}}
				data, _ := json.Marshal(config)
				path := filepath.Join(directory, "caddy.json")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "-v", path+":/etc/caddy/test.json:ro", "--entrypoint", "caddy", "spool-relay-caddy:local", "run", "--config", "/etc/caddy/test.json")
			} else {
				writeProxyTestCertificate(t, directory)
				// A loaded matching test certificate prevents public ACME calls.
				// Inspect the effective router through a private, per-run API to
				// verify Relay's explicit policy without entrypoint TLS defaults,
				// and confirm unrelated routes retain their own TLS policy.
				acme := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Error("loaded test certificate unexpectedly triggered ACME", r.URL.Path)
					http.Error(w, "test does not issue public certificates", 503)
				}))
				t.Cleanup(acme.Close)
				acmeURL := "http://host.docker.internal:" + strings.Split(acme.Listener.Addr().String(), ":")[1]
				args = append(args, "-p", "127.0.0.1::8080", "-v", directory+":/etc/spool-proxy:ro", "traefik:v3.6", "--entrypoints.https.address=:443", "--entrypoints.http.address=:80", "--certificatesresolvers.fixture.acme.caserver="+acmeURL, "--certificatesresolvers.fixture.acme.storage=/tmp/acme.json", "--certificatesresolvers.fixture.acme.tlschallenge=true", "--api.insecure=true", "--providers.file.filename=/etc/spool-proxy/tls.yaml", "--providers.http.endpoint="+policy+"/v1/traefik", "--providers.http.pollinterval=250ms", "--providers.http.headers.Authorization=Bearer "+s.opts.TraefikToken)
			}
			t.Cleanup(func() {
				if t.Failed() {
					logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
					t.Log(string(logs))
				}
				exec.Command("docker", "rm", "-f", name).Run()
			})
			run(args...)
			binding := strings.TrimSpace(run("port", name, "443/tcp"))
			transport := &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, binding)
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
			address := "https://" + id.ID() + ".web.relay.example.com/"
			check := func(target string) (int, string, error) {
				response, err := client.Get(target)
				if err != nil {
					return 0, "", err
				}
				defer response.Body.Close()
				if kind != "caddy" && target == address && (response.TLS == nil || len(response.TLS.PeerCertificates) == 0 || response.TLS.PeerCertificates[0].VerifyHostname(id.ID()+".web.relay.example.com") != nil) {
					return 0, "", fmt.Errorf("Traefik has not loaded the matching test certificate")
				}
				data, _ := io.ReadAll(response.Body)
				return response.StatusCode, string(data), nil
			}
			deadline := time.Now().Add(25 * time.Second)
			for {
				code, body, err := check(address)
				if err == nil && code == 200 {
					if body != id.ID()+".web.relay.example.com "+WebPath(id.ID()) {
						t.Fatal("Host or route changed", body)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("proxy did not load route", code, body, err)
				}
				time.Sleep(100 * time.Millisecond)
			}
			if kind != "caddy" {
				apiBinding := strings.TrimSpace(run("port", name, "8080/tcp"))
				response, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + apiBinding + "/api/http/routers")
				if err != nil {
					t.Fatal(err)
				}
				var routers []struct {
					Name string
					TLS  *struct{ CertResolver string }
				}
				err = json.NewDecoder(response.Body).Decode(&routers)
				response.Body.Close()
				if err != nil || response.StatusCode != 200 {
					t.Fatal("could not inspect effective Traefik router", response.StatusCode, err)
				}
				found, foundPlain, foundTLS := false, false, false
				for _, router := range routers {
					if router.Name == "unrelated-plain@file" {
						foundPlain = true
						if router.TLS != nil {
							t.Fatal("Relay changed unrelated route's TLS policy", router)
						}
					}
					if router.Name == "unrelated-tls@file" {
						foundTLS = true
						if router.TLS == nil || router.TLS.CertResolver != "" {
							t.Fatal("Relay changed unrelated route's certificate policy", router)
						}
					}
					if router.Name != "spool-web-"+id.ID()+"@http" {
						continue
					}
					found = true
					want := ""
					if mode == "individual" {
						want = "fixture"
					}
					if router.TLS == nil || router.TLS.CertResolver != want {
						t.Fatal("wrong effective certificate policy", mode, router)
					}
				}
				if !found || !foundPlain || !foundTLS {
					t.Fatal("route missing from Traefik runtime", found, foundPlain, foundTLS)
				}
			}
			if code, _, err := check("https://unknown.web.relay.example.com/"); err == nil && code == 200 {
				t.Fatal("unknown domain served tenant")
			}
			if err := s.RevokeRegistration(registration.ID); err != nil {
				t.Fatal(err)
			}
			if code, _, err := check(address); err == nil && code == 200 {
				t.Fatal("revoked tenant still reachable")
			}
		})
	}
}

func writeProxyTestCertificate(t *testing.T, directory string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "relay.example.com"}, DNSNames: []string{"*.web.relay.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"certificate.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		"private.pem":     pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}),
		"tls.yaml": []byte(`tls:
  certificates:
    - certFile: /etc/spool-proxy/certificate.pem
      keyFile: /etc/spool-proxy/private.pem
http:
  routers:
    unrelated-plain:
      rule: Host("unrelated.example.org")
      entryPoints: [https]
      service: noop@internal
    unrelated-tls:
      rule: Host("unrelated-tls.example.org")
      entryPoints: [https]
      service: noop@internal
      tls: {}
`),
	} {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
