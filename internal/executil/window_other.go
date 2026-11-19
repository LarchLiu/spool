//go:build !windows

package executil

import "os/exec"

// HideWindow is a no-op on platforms where background commands do not allocate
// terminal windows.
func HideWindow(cmd *exec.Cmd) {}
