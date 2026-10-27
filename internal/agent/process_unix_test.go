//go:build darwin || freebsd || linux || netbsd || openbsd

package agent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestPersistentAgentCleansUpProcessGroup(t *testing.T) {
	for _, backend := range []chatmodel.Backend{chatmodel.BackendCodex, chatmodel.BackendClaude} {
		t.Run(string(backend), func(t *testing.T) {
			request := newAgentFixture(t, backend)
			request.Prompt = "spawn-child"
			runner := newTestRunner(t)
			runner.IdleTimeout = 100 * time.Millisecond
			runFixture(t, runner, request)
			process := currentProcess(t, runner, request.ChatID)
			data, err := os.ReadFile(filepath.Join(request.WorkingDirectory, "child-pid.txt"))
			if err != nil {
				t.Fatal(err)
			}
			child, err := strconv.Atoi(string(data))
			if err != nil || child <= 1 {
				t.Fatalf("invalid child PID: %q", data)
			}
			t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
			if !processIsRunning(child) {
				t.Fatal("child exited before idle cleanup")
			}
			waitProcessExit(t, process)
			for _, pid := range []int{process.cmd.Process.Pid, child} {
				deadline := time.Now().Add(2 * time.Second)
				for processIsRunning(pid) {
					if time.Now().After(deadline) {
						t.Fatalf("process %d survived chat cleanup", pid)
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
		})
	}
}

func processIsRunning(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return false
	}
	// An orphaned child may briefly be a zombie until the OS's reaper collects it.
	state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}
	status := strings.TrimSpace(string(state))
	return status != "" && !strings.HasPrefix(status, "Z")
}
