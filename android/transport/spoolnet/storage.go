package spoolnet

import (
	"os"
	"path/filepath"
)

func prepareLogStorage(dataDir string) error {
	logsDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		return err
	}
	// tsnet.Dir does not cover the backend's socket-stat logger. On Android,
	// its default directory discovery panics without HOME or a writable /tmp.
	return os.Setenv("TS_LOGS_DIR", logsDir)
}
