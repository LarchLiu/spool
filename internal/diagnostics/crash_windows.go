package diagnostics

import (
	"os/exec"

	"spool/internal/executil"
)

func hideMonitorWindow(cmd *exec.Cmd) {
	executil.HideWindow(cmd)
}
