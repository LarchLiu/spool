package netproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"golang.org/x/net/http/httpproxy"
)

func TestPolicySystemFallbackAndEnvironmentPrecedence(t *testing.T) {
	system := systemProxy{http: "http://system:8889", https: "http://secure:8889", exceptions: []string{"*.internal", "10.*", "172.2*", "192.168.0.0/16"}, excludeSimple: true}
	for _, tc := range []struct {
		name, target, want string
		config             httpproxy.Config
	}{
		{"HTTP", "http://example.com", "http://system:8889", httpproxy.Config{}},
		{"HTTPS CONNECT", "https://example.com", "http://secure:8889", httpproxy.Config{}},
		{"environment first", "https://example.com", "http://env:80", httpproxy.Config{HTTPSProxy: "http://env:80"}},
		{"NO_PROXY", "https://example.com", "", httpproxy.Config{NoProxy: "example.com"}},
		{"domain exception", "https://api.internal", "", httpproxy.Config{}},
		{"IP glob", "https://10.20.30.40", "", httpproxy.Config{}},
		{"partial IP glob", "https://172.20.0.1", "", httpproxy.Config{}},
		{"CIDR", "https://192.168.12.1", "", httpproxy.Config{}},
		{"simple host", "https://printer", "", httpproxy.Config{}},
		{"environment ignores OS exception", "https://api.internal", "http://env:80", httpproxy.Config{HTTPSProxy: "http://env:80"}},
		{"localhost", "https://localhost:1234", "", httpproxy.Config{HTTPSProxy: "http://env:80"}},
		{"localhost subdomain", "http://app.localhost", "", httpproxy.Config{}},
		{"IPv4 loopback", "http://127.0.0.2:8787", "", httpproxy.Config{}},
		{"IPv6 loopback", "https://[::1]:8787", "", httpproxy.Config{}},
		{"unrelated", "https://not-internal.example.com", "http://secure:8889", httpproxy.Config{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, _ := url.Parse(tc.target)
			proxy, err := newPolicy(&tc.config, system).resolve(target)
			got := ""
			if proxy != nil {
				got = proxy.String()
			}
			if err != nil || got != tc.want {
				t.Fatalf("proxy = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestDisabledSystemProxyPreservesEnvironment(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTPS_PROXY", "http://env:8889")
	policy, err := Prepare(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse("https://example.com")
	proxy, err := policy.resolve(target)
	if err != nil || proxy == nil || proxy.Host != "env:8889" {
		t.Fatal(proxy, err)
	}
	t.Setenv("HTTPS_PROXY", "")
	policy, err = Prepare(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err = policy.resolve(target)
	if err != nil || proxy != nil {
		t.Fatal(proxy, err)
	}
}

func TestSharedHTTPPolicyCanChangeWithoutReplacingDefaultClient(t *testing.T) {
	previous := activeProxy.Load()
	t.Cleanup(func() { Activate(previous) })
	client := http.DefaultClient
	for _, body := range []string{"first", "second"} {
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Host != "spool-proxy-test.invalid" {
				t.Errorf("unexpected target %q", r.URL.Host)
			}
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(proxy.Close)
		Activate(newPolicy(&httpproxy.Config{}, systemProxy{http: proxy.URL}))
		response, err := client.Get("http://spool-proxy-test.invalid/resource")
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(data) != body {
			t.Fatal(string(data), err)
		}
		if client != http.DefaultClient {
			t.Fatal("replaced the signed update client")
		}
	}
}

func TestSystemProxyAddress(t *testing.T) {
	for _, tc := range []struct{ host, port, want string }{
		{"127.0.0.1", "8889", "http://127.0.0.1:8889"}, {"::1", "8080", "http://[::1]:8080"},
		{"proxy.example", "80", "http://proxy.example:80"}, {"", "80", ""}, {"bad/host", "80", ""},
		{"host", "0", ""}, {"host", "65536", ""}, {"host", "oops", ""}, {"host:1234", "80", ""},
	} {
		got, err := systemProxyAddress(tc.host, tc.port)
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Errorf("%s:%s = %q, %v", tc.host, tc.port, got, err)
		}
	}
}
