//go:build !darwin

package macapp

import "os/exec"

func configureToolCommand(cmd *exec.Cmd) {}
