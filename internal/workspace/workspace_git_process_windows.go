package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"spool/internal/executil"
)

func configureWorkspaceGitCommand(cmd *exec.Cmd) {
	executil.HideWindow(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		kill := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe"), "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		executil.HideWindow(kill)
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
