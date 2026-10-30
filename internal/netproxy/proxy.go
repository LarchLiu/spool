// Package netproxy owns Spool's process-wide outbound HTTP proxy policy.
package netproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
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

type Options struct {
	Enabled        bool
	UseSystemProxy bool
	ProxyURL       string
	NoProxy        string
}

func (o Options) Validate() error {
	raw := strings.TrimSpace(o.ProxyURL)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || (u.Path != "" && u.Path != "/") || strings.ContainsAny(raw, " \t\r\n") {
		return fmt.Errorf("Proxy URL must be an http(s) or socks5(h) URL without a path, query or fragment.")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("Proxy URL must be an http(s) or socks5(h) URL without a path, query or fragment.")
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return fmt.Errorf("Proxy URL port must be between 1 and 65535.")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("Proxy URL port must be between 1 and 65535.")
	}
	return nil
}

var activeProxy atomic.Pointer[Policy]

func init() {
	activeProxy.Store(newPolicy(environmentConfig(), systemProxy{}))
	// Install before HTTP users (including the signed update wrapper) start.
	// The transport stays immutable; requests read an atomic policy snapshot.
	http.DefaultTransport.(*http.Transport).Proxy = Proxy
}

func environmentConfig() *httpproxy.Config {
	config := httpproxy.FromEnvironment()
	all := os.Getenv("ALL_PROXY")
	if all == "" {
		all = os.Getenv("all_proxy")
	}
	if config.HTTPProxy == "" {
		config.HTTPProxy = all
	}
	if config.HTTPSProxy == "" {
		config.HTTPSProxy = all
	}
	return config
}

// Prepare resolves the selected proxy mode without changing the active policy.
func Prepare(ctx context.Context, options Options) (*Policy, error) {
	return prepare(ctx, options, readSystemProxy)
}

func prepare(ctx context.Context, options Options, readSystem func(context.Context) (systemProxy, error)) (*Policy, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if !options.Enabled {
		return newPolicy(&httpproxy.Config{}, systemProxy{}), nil
	}
	config := environmentConfig()
	if options.UseSystemProxy {
		proxy, err := readSystem(ctx)
		if err != nil {
			return nil, err
		}
		return newPolicy(config, proxy), nil
	}
	if address := strings.TrimSpace(options.ProxyURL); address != "" {
		config.HTTPProxy, config.HTTPSProxy = address, address
		config.CGI = false
	}
	if bypass := strings.TrimSpace(options.NoProxy); bypass != "" {
		config.NoProxy = bypass
	}
	return newPolicy(config, systemProxy{}), nil
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
