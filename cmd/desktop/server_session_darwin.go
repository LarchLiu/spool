package main

import (
	"strconv"
	"strings"
	"syscall"
)

func persistentDesktopSessions() bool {
	version, err := syscall.Sysctl("kern.osrelease")
	major, parseErr := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	return err == nil && parseErr == nil && major >= 23 // macOS 14
}
