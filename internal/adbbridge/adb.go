package adbbridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"spool/internal/executil"
)

// ADB is the part of the host's adb client the bridge depends on. Every call
// honours ctx; the bridge never talks to the adb server's socket directly.
type ADB interface {
	// Connect returns adb's own verdict, such as "connected to 127.0.0.1:1".
	Connect(ctx context.Context, serial string) (string, error)
	Disconnect(ctx context.Context, serial string)
	// WaitForDevice blocks until the transport is authorized and online.
	WaitForDevice(ctx context.Context, serial string) error
	Pair(ctx context.Context, address, code string) (string, error)
}

// CLI runs the adb executable against the adb server its environment selects,
// which is the host's default one unless the caller overrides it.
type CLI struct {
	Path string
	// Env entries are appended to the inherited environment.
	Env []string
}

// Locate finds adb the way a developer's shell would, then falls back to the
// standard SDK locations because desktop launchers start with a minimal PATH.
func Locate() (string, bool) {
	name := "adb"
	if runtime.GOOS == "windows" {
		name = "adb.exe"
	}
	if path, err := exec.LookPath(name); err == nil {
		if absolute, err := filepath.Abs(path); err == nil {
			return absolute, true
		}
	}
	var candidates []string
	for _, variable := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if root := os.Getenv(variable); root != "" {
			candidates = append(candidates, filepath.Join(root, "platform-tools", name))
		}
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if home != "" {
			candidates = append(candidates, filepath.Join(home, "Library", "Android", "sdk", "platform-tools", name))
		}
		candidates = append(candidates, "/opt/homebrew/bin/adb", "/usr/local/bin/adb")
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			candidates = append(candidates, filepath.Join(local, "Android", "Sdk", "platform-tools", name))
		}
	default:
		if home != "" {
			candidates = append(candidates, filepath.Join(home, "Android", "Sdk", "platform-tools", name))
		}
		candidates = append(candidates, "/usr/local/bin/adb", "/usr/bin/adb")
	}
	for _, candidate := range candidates {
		if !filepath.IsAbs(candidate) {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return candidate, true
	}
	return "", false
}

func (c CLI) run(ctx context.Context, timeout time.Duration, stdin string, args ...string) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.Path, args...)
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = strings.NewReader(stdin)
	// The first adb command may start the adb server, which outlives it. Never
	// wait for a daemon that still holds the output pipe.
	cmd.WaitDelay = 2 * time.Second
	executil.HideWindow(cmd)
	output, err := cmd.CombinedOutput()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	return strings.TrimSpace(string(output)), err
}

func (c CLI) Connect(ctx context.Context, serial string) (string, error) {
	// adb itself waits up to ten seconds for the transport handshake.
	return c.run(ctx, 25*time.Second, "", "connect", serial)
}

func (c CLI) Disconnect(ctx context.Context, serial string) {
	_, _ = c.run(ctx, 10*time.Second, "", "disconnect", serial)
}

func (c CLI) WaitForDevice(ctx context.Context, serial string) error {
	_, err := c.run(ctx, 0, "", "-s", serial, "wait-for-device")
	return err
}

// Pair passes the code on standard input so it never appears in the host's
// process list.
func (c CLI) Pair(ctx context.Context, address, code string) (string, error) {
	return c.run(ctx, 40*time.Second, code+"\n", "pair", address)
}
