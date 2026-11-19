package voxl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"spool/internal/executil"
)

func configureCommand(cmd *exec.Cmd) {
	executil.HideWindow(cmd)
	cmd.Cancel = func() error {
		// cargo can own compilers/linkers. Reap only this owned process tree
		// before the installer removes its staging directory.
		kill := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe"), "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		executil.HideWindow(kill)
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
