package netproxy

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func readSystemProxy(ctx context.Context) (systemProxy, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return systemProxy{}, fmt.Errorf("read macOS system proxy: %w", err)
	}
	return parseMacSystemProxy(string(output))
}

func parseMacSystemProxy(output string) (systemProxy, error) {
	var result systemProxy
	values := map[string]string{}
	var exceptions bool
	depth := 0
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "{") {
			if depth == 1 && strings.HasPrefix(line, "ExceptionsList :") {
				exceptions = true
			}
			depth++
			continue
		}
		if line == "}" {
			if depth == 2 {
				exceptions = false
			}
			depth--
			continue
		}
		key, value, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		if exceptions && depth == 2 {
			// Preserve macOS host globs; they are not NO_PROXY suffixes.
			result.exceptions = append(result.exceptions, value)
		} else if depth == 1 {
			values[key] = value
		}
	}
	result.excludeSimple = values["ExcludeSimpleHostnames"] == "1"
	if values["ProxyAutoConfigEnable"] == "1" || values["ProxyAutoDiscoveryEnable"] == "1" {
		return result, fmt.Errorf("Spool system proxy currently requires static HTTP/HTTPS settings, not PAC or auto-discovery.")
	}
	for _, entry := range []struct {
		prefix string
		target *string
	}{{"HTTP", &result.http}, {"HTTPS", &result.https}} {
		if values[entry.prefix+"Enable"] != "1" {
			continue
		}
		address, err := systemProxyAddress(values[entry.prefix+"Proxy"], values[entry.prefix+"Port"])
		if err != nil {
			return result, err
		}
		*entry.target = address
	}
	if result.http == "" && result.https == "" && values["SOCKSEnable"] == "1" {
		return result, fmt.Errorf("Spool system proxy requires an HTTP/HTTPS proxy; SOCKS-only settings are not supported.")
	}
	return result, nil
}
