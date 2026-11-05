//go:build !darwin && !linux && !windows

package voxl

import "os/exec"

func configureCommand(cmd *exec.Cmd) {}
