//go:build darwin || linux

package screencapture

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func scratchDir() (string, error) {
	parent := filepath.Join(os.TempDir(), fmt.Sprintf("spool-%d", os.Getuid()))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&0o077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		return "", os.ErrPermission
	}
	base := filepath.Join(parent, "spool")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, "screenshot.")
}
