//go:build !darwin && !freebsd && !linux && !netbsd && !openbsd && !windows

package workspace

import "os/exec"

func configureWorkspaceGitCommand(cmd *exec.Cmd) {}
