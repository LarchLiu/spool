//go:build !windows

package diagnostics

import "os/exec"

func hideMonitorWindow(*exec.Cmd) {}
