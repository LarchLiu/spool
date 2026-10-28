package diagnostics

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"time"
)

const monitorArgument = "--spool-crash-monitor"

// RunCrashMonitor must run at the start of each executable's main, before flag
// parsing or GUI startup. A separate process can drain and rotate a fatal Go
// crash report even after the crashing process's goroutines have stopped.
func RunCrashMonitor() bool {
	if len(os.Args) != 2 || os.Args[1] != monitorArgument || os.Getenv("SPOOL_CRASH_MONITOR") != "1" {
		return false
	}
	path := os.Getenv("SPOOL_CRASH_LOG")
	file, err := privateLogFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "crash monitor:", err)
		return true
	}
	writer := &rotatingWriter{path: path, file: file}
	defer writer.Close()
	buffer := make([]byte, 16<<10)
	first := true
	for {
		n, readErr := os.Stdin.Read(buffer)
		if n != 0 {
			if first {
				_, err = fmt.Fprintf(writer, "time=%s process=%s\n", time.Now().UTC().Format(time.RFC3339Nano), os.Getenv("SPOOL_CRASH_PROCESS"))
				first = false
			}
			if err == nil {
				_, err = writer.Write(buffer[:n])
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "crash monitor:", err)
				return true
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				fmt.Fprintln(os.Stderr, "crash monitor:", readErr)
			}
			return true
		}
	}
}

func captureCrashes(path, component, version string) error {
	file, err := privateLogFile(path)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	defer writer.Close()
	cmd := exec.Command(executable, monitorArgument)
	cmd.Env = append(os.Environ(), "SPOOL_CRASH_MONITOR=1", "SPOOL_CRASH_LOG="+path,
		fmt.Sprintf("SPOOL_CRASH_PROCESS=%s pid=%d version=%s", component, os.Getpid(), version))
	cmd.Stdin = reader
	cmd.Stderr = os.Stderr
	hideMonitorWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return debug.SetCrashOutput(writer, debug.CrashOptions{})
}
