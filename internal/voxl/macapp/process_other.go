//go:build !darwin

package macapp

import (
	"errors"
	"os/exec"
)

func exchangeBundles(staged, installed string) error {
	return errors.New("Voxl Helper upgrades require macOS")
}

func configureToolCommand(cmd *exec.Cmd) {}
