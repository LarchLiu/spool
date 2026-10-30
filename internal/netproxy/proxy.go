// Package netproxy owns Spool's process-wide outbound HTTP proxy policy.
package netproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync/atomic"

	"golang.org/x/net/http/httpproxy"
)

type systemProxy struct {
	http, https   string
	exceptions    []string
	excludeSimple bool
}

type Policy struct {
	resolve func(*url.URL) (*url.URL, error)
}

var activeProxy atomic.Pointer[Policy]

func init() {
	activeProxy.Store(newPolicy(httpproxy.FromEnvironment(), systemProxy{}))
	// Install before HTTP users (including the signed update wrapper) start.
	// The transport stays immutable; requests read an atomic policy snapshot.
	http.DefaultTransport.(*http.Transport).Proxy = Proxy
}

// Prepare resolves system configuration without changing the active policy.
// Explicit environment variables remain authoritative, even when disabled.
func Prepare(ctx context.Context, enabled bool) (*Policy, error) {
	config := httpproxy.FromEnvironment()
	var proxy systemProxy
	if enabled {
		var err error
		proxy, err = readSystemProxy(ctx)
		if err != nil {
			return nil, err
		}
	}
	return newPolicy(config, proxy), nil
}

func newPolicy(config *httpproxy.Config, proxy systemProxy) *Policy {
	merged := *config
	if merged.HTTPProxy == "" {
		merged.HTTPProxy = proxy.http
	}
	if merged.HTTPSProxy == "" {
		merged.HTTPSProxy = proxy.https
	}
	resolve := merged.ProxyFunc()
	return &Policy{resolve: func(target *url.URL) (*url.URL, error) {
		host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
		ip := net.ParseIP(host)
		if host == "localhost" || strings.HasSuffix(host, ".localhost") || ip != nil && ip.IsLoopback() {
			return nil, nil
		}
		// macOS exceptions apply only to the macOS fallback, not to an
		// explicitly configured environment proxy and its own NO_PROXY list.
		usesSystem := target.Scheme == "http" && config.HTTPProxy == "" || target.Scheme == "https" && config.HTTPSProxy == ""
		if usesSystem {
			if proxy.excludeSimple && !strings.Contains(host, ".") && ip == nil {
				return nil, nil
			}
			for _, exception := range proxy.exceptions {
				if matchesException(host, exception) {
					return nil, nil
				}
			}
		}
		return resolve(target)
	}}
}

func matchesException(host, exception string) bool {
	exception = strings.ToLower(strings.TrimSpace(exception))
	if _, block, err := net.ParseCIDR(exception); err == nil {
		return block.Contains(net.ParseIP(host))
	}
	if strings.HasPrefix(exception, ".") {
		return host == exception[1:] || strings.HasSuffix(host, exception)
	}
	matched, _ := path.Match(exception, host)
	return matched
}

// Activate applies a prepared policy to subsequent built-in HTTP requests.
// Existing connections and independently configured subprocesses are untouched.
func Activate(policy *Policy) {
	activeProxy.Store(policy)
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
}

func Proxy(req *http.Request) (*url.URL, error) {
	return Resolve(req.URL)
}

// Resolve is also used by the embedded Tailscale SDK's proxy hook.
func Resolve(target *url.URL) (*url.URL, error) {
	return activeProxy.Load().resolve(target)
}

func systemProxyAddress(host, port string) (string, error) {
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 || host == "" || strings.ContainsAny(host, " \t\r\n/@?#") {
		return "", fmt.Errorf("invalid system proxy host or port")
	}
	if strings.Contains(host, ":") && net.ParseIP(strings.Trim(host, "[]")) == nil {
		return "", fmt.Errorf("invalid system proxy host or port")
	}
	return "http://" + net.JoinHostPort(strings.Trim(host, "[]"), port), nil
}
