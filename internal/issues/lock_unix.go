//go:build darwin || freebsd || linux || netbsd || openbsd

package issues

import (
	"errors"
	"os"
	"syscall"
)

func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrLocked
	}
	return err
}
func unlockFile(f *os.File)     { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
func processEnded(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }
