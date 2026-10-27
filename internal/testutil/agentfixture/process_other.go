//go:build !darwin && !freebsd && !linux && !netbsd && !openbsd

package agentfixture

import "testing"

func WaitExit(t *testing.T, pid int) {
	t.Helper()
	t.Skip("process-exit observation requires Unix process signals")
}
