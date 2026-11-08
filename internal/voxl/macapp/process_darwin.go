package macapp

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func exchangeBundles(staged, installed string) error {
	if err := unix.RenamexNp(staged, installed, unix.RENAME_SWAP); err != nil {
		return &os.LinkError{Op: "exchange", Old: staged, New: installed, Err: err}
	}
	return nil
}

func configureToolCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
