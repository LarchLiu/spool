//go:build darwin || freebsd || linux || netbsd || openbsd

package workspace

import (
	"os"
	"syscall"
)

func lockDatabaseLayout(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}
