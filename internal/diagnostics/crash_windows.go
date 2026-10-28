package diagnostics

import (
	"os/exec"
	"syscall"
)

func hideMonitorWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
