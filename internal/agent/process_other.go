//go:build !darwin && !freebsd && !linux && !netbsd && !openbsd

package agent

import "os/exec"

func configureAgentCommand(cmd *exec.Cmd) {}
