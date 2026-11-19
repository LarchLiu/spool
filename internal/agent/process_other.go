//go:build !darwin && !freebsd && !linux && !netbsd && !openbsd && !windows

package agent

import "os/exec"

func configureAgentCommand(cmd *exec.Cmd) {}
