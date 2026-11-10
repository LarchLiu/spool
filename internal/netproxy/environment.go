package netproxy

import (
	"fmt"
	"os"
	"strings"
)

// Environment returns a child's environment using the same prepared policy as
// Spool's HTTP clients. A nil input inherits the process environment. Call this
// at launch time for external tools and plugin hosts; never modify os.Environ.
// An unavailable policy is an error, rather than permission to launch with a
// stale inherited proxy or direct access. Existing subprocesses keep their env.
// System hostname globs and ExcludeSimpleHostnames have no portable NO_PROXY
// equivalent; external tools interpret NO_PROXY according to their own runtime.
func Environment(env []string) ([]string, error) {
	policy := activeProxy.Load()
	if policy.unavailable != nil {
		return nil, fmt.Errorf("proxy settings unavailable: %w", policy.unavailable)
	}
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+8)
	nodeProxySet := false
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		case "NODE_USE_ENV_PROXY":
			nodeProxySet = true
		}
		out = append(out, entry)
	}
	config := policy.config
	if config.HTTPProxy == "" && config.HTTPSProxy == "" {
		return out, nil
	}
	set := func(key, value string) {
		if value != "" {
			out = append(out, key+"="+value, strings.ToLower(key)+"="+value)
		}
	}
	set("HTTP_PROXY", config.HTTPProxy)
	set("HTTPS_PROXY", config.HTTPSProxy)
	// Only export an all-protocol fallback when both HTTP schemes agree.
	// Otherwise it could proxy a scheme which was intended to be direct.
	if config.HTTPProxy == config.HTTPSProxy {
		set("ALL_PROXY", config.HTTPProxy)
	}
	no := []string{"localhost", ".localhost", "127.0.0.0/8", "127.0.0.1", "::1"}
	if config.NoProxy != "" {
		no = append(no, config.NoProxy)
	}
	// A fully explicit environment proxy ignores OS exceptions, as Resolve
	// does. With mixed environment/system routes, NO_PROXY is necessarily a
	// shared bypass list for clients that only accept environment variables.
	no = append(no, policy.system.exceptions...)
	set("NO_PROXY", strings.Join(no, ","))
	// Supported Node versions otherwise ignore proxy env for built-in fetch.
	if !nodeProxySet {
		out = append(out, "NODE_USE_ENV_PROXY=1")
	}
	return out, nil
}
