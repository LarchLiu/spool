//go:build darwin || freebsd || linux || netbsd || openbsd

package agentfixture

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

// WaitExit observes the subprocess rather than reaching into runner internals.
func WaitExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("fixture CLI process %d was not reclaimed", pid)
}
