package netproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTPS_PROXY", "http://env:8889")
	policy, err := Prepare(context.Background(), Options{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse("https://example.com")
	proxy, err := policy.resolve(target)
	if err != nil || proxy == nil || proxy.Host != "env:8889" {
		t.Fatal(proxy, err)
	}
	t.Setenv("HTTPS_PROXY", "")
	policy, err = Prepare(context.Background(), Options{Enabled: true})
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
		policy, err := Prepare(context.Background(), Options{Enabled: true, ProxyURL: proxy.URL, NoProxy: "localhost"})
		if err != nil {
			t.Fatal(err)
		}
		Activate(policy)
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

func TestCustomProxyOverridesEnvironmentAndKeepsBypassRules(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://environment:8080")
	t.Setenv("HTTPS_PROXY", "http://environment:8081")
	t.Setenv("ALL_PROXY", "socks5://environment:1080")
	t.Setenv("NO_PROXY", "inherited.example")
	t.Setenv("REQUEST_METHOD", "GET")
	for _, tc := range []struct{ name, bypass, target, want string }{
		{"HTTP", "", "http://example.com", "http://custom:8889"},
		{"HTTPS", "", "https://example.com", "http://custom:8889"},
		{"inherit bypass", "", "https://inherited.example", ""},
		{"replace inherited bypass", "internal.example", "https://inherited.example", "http://custom:8889"},
		{"custom bypass", "internal.example", "https://api.internal.example", ""},
		{"CIDR bypass", "192.168.0.0/16", "https://192.168.12.1", ""},
		{"IPv6 bypass", "2001:db8::/32", "https://[2001:db8::1]", ""},
		{"bypass everything", "*", "https://example.com", ""},
		{"localhost", "example.com", "http://localhost", ""},
		{"IPv4 loopback", "example.com", "http://127.0.0.2", ""},
		{"IPv6 loopback", "example.com", "http://[::1]", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := prepare(context.Background(), Options{Enabled: true, ProxyURL: " http://custom:8889 ", NoProxy: tc.bypass}, func(context.Context) (systemProxy, error) {
				t.Fatal("custom mode attempted system discovery")
				return systemProxy{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			target, _ := url.Parse(tc.target)
			proxy, err := policy.resolve(target)
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

func TestAllProxyIsOnlyAFallbackForSchemeSpecificEnvironment(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "https_proxy", "all_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTPS_PROXY", "http://https-environment:8080")
	t.Setenv("ALL_PROXY", "socks5://all-environment:1080")
	policy, err := Prepare(context.Background(), Options{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]string{
		"http://example.com": "socks5://all-environment:1080", "https://example.com": "http://https-environment:8080",
	} {
		u, _ := url.Parse(target)
		proxy, err := policy.resolve(u)
		if err != nil || proxy == nil || proxy.String() != want {
			t.Fatalf("%s = %v, %v; want %s", target, proxy, err, want)
		}
	}
}

func TestProxyMasterSwitchAndSystemMode(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct {
		name, environment, target, want string
		enabled, system                 bool
	}{
		{"disabled custom", "http://environment:8080", "https://external.test", "", false, false},
		{"disabled system", "http://environment:8080", "https://external.test", "", false, true},
		{"disabled HTTP", "http://environment:8080", "http://external.test", "", false, true},
		{"custom mode", "http://environment:8080", "https://external.test", "http://custom:8889", true, false},
		{"custom bypass", "http://environment:8080", "https://example.com", "", true, false},
		{"system ignores saved custom and bypass", "", "https://example.com", "http://system:8889", true, true},
		{"system keeps its exceptions", "", "https://api.internal", "", true, true},
		{"system preserves environment priority", "http://environment:8080", "https://example.com", "http://environment:8080", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HTTP_PROXY", tc.environment)
			t.Setenv("HTTPS_PROXY", tc.environment)
			t.Setenv("ALL_PROXY", tc.environment)
			discoveries := 0
			policy, err := prepare(context.Background(), Options{
				Enabled: tc.enabled, UseSystemProxy: tc.system, ProxyURL: "http://custom:8889", NoProxy: "example.com",
			}, func(context.Context) (systemProxy, error) {
				discoveries++
				return systemProxy{https: "http://system:8889", exceptions: []string{"*.internal"}}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if (discoveries == 1) != (tc.enabled && tc.system) {
				t.Fatalf("unexpected system discovery count: %d", discoveries)
			}
			target, _ := url.Parse(tc.target)
			proxy, err := policy.resolve(target)
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

func TestCustomProxyValidationDoesNotDiscloseCredentials(t *testing.T) {
	for _, address := range []string{"", "  ", "http://127.0.0.1:8889", "https://proxy.example", "socks5://[::1]:1080", "socks5h://user:secret@proxy.example:1080/"} {
		if err := (Options{ProxyURL: address}).Validate(); err != nil {
			t.Fatalf("valid proxy URL rejected: %v", err)
		}
	}
	for _, address := range []string{"proxy.example:8889", "ftp://proxy.example", "http://", "http://proxy.example:0", "http://proxy.example:65536", "http://proxy.example:", "http://proxy.example:bad", "http://user:secret@proxy.example/path", "http://user:secret@proxy.example?x=y", "http://proxy.example#fragment", "http://proxy.example?", "http://proxy.example#", "http://proxy.example\n" + "x"} {
		_, err := Prepare(context.Background(), Options{ProxyURL: address})
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), address) {
			t.Fatalf("invalid proxy was accepted or error disclosed the URL: %v", err)
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
