// Package executil configures background commands without changing their transport or lifecycle.
package executil

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// HideWindow prevents a background command from allocating or showing a Windows
// console. Existing process attributes, pipes and cancellation remain intact.
func HideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
