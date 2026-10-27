//go:build !darwin && !freebsd && !linux && !netbsd && !openbsd && !windows

package issues

import (
	"fmt"
	"os"
)

func lockFile(*os.File) error {
	return fmt.Errorf("issue automation locking is unavailable on this platform")
}
func unlockFile(*os.File)   {}
func processEnded(int) bool { return false }
