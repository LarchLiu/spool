package agent

import (
	"os/exec"

	"spool/internal/executil"
)

func configureAgentCommand(cmd *exec.Cmd) {
	executil.HideWindow(cmd)
}
