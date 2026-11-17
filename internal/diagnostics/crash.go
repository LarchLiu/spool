package diagnostics

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"sync"
	"time"
)

const monitorArgument = "--spool-crash-monitor"

type crashMonitor struct {
	done chan struct{}
	err  error
}

// The runtime crash output is process-wide. Serialize replacement and shutdown
// so every detached monitor is reaped before its owner can remove its logs.
var crashCapture struct {
	sync.Mutex
	monitor *crashMonitor
}

// StopCrashCapture disables diagnostics crash output and waits for its monitor
// to exit. Call it before deleting disposable application data. Session.Close
// deliberately leaves capture installed for panics reported after deferred
// cleanup; this explicit stop is for callers that have finished crash capture.
func StopCrashCapture() error {
	crashCapture.Lock()
	defer crashCapture.Unlock()
	if crashCapture.monitor == nil {
		return nil
	}
	if err := debug.SetCrashOutput(nil, debug.CrashOptions{}); err != nil {
		return err
	}
	monitor := crashCapture.monitor
	<-monitor.done
	crashCapture.monitor = nil
	return monitor.err
}

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
	crashCapture.Lock()
	defer crashCapture.Unlock()
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
	monitor := &crashMonitor{done: make(chan struct{})}
	go func() {
		monitor.err = cmd.Wait()
		close(monitor.done)
	}()
	if err := debug.SetCrashOutput(writer, debug.CrashOptions{}); err != nil {
		// No runtime-owned duplicate keeps the child alive on setup failure.
		writer.Close()
		<-monitor.done
		return err
	}
	if previous := crashCapture.monitor; previous != nil {
		// SetCrashOutput closed the previous pipe. Wait for EOF handling and
		// file closure before returning control to either session's owner.
		<-previous.done
	}
	crashCapture.monitor = monitor
	return nil
}
