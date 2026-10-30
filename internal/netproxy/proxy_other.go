//go:build !darwin

package netproxy

import "context"

func readSystemProxy(context.Context) (systemProxy, error) {
	// The SDK handles Windows WinHTTP discovery; headless Unix uses the
	// standard HTTP_PROXY / HTTPS_PROXY / NO_PROXY environment variables.
	return systemProxy{}, nil
}
