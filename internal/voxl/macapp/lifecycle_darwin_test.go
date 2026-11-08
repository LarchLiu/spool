package macapp_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"spool/internal/testutil/voxlfixture"
	"spool/internal/voxl/macapp"

	"github.com/ebitengine/purego"
)

func TestMain(m *testing.M) {
	if os.Getenv("SPOOL_TEST_VOXL_OWNER") == "1" {
		cmd, cleanup, err := macapp.Command(context.Background(), os.Getenv("SPOOL_TEST_VOXL_ROOT"), "--port="+os.Getenv("SPOOL_TEST_VOXL_PORT"))
		if err != nil {
			os.Exit(2)
		}
		cmd.Dir, cmd.Stdout = os.Getenv("SPOOL_TEST_VOXL_ROOT"), os.Stdout
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "SPOOL_TEST_VOXL_OWNER=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		err = cmd.Run()
		cleanup()
		if err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	if voxlfixture.Run() {
		return
	}
	os.Exit(m.Run())
}

type processes struct {
	Helper int `json:"helper_pid"`
	Voxl   int `json:"voxl_pid"`
}

func port(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	value := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	return value
}

func start(t *testing.T, cmd *exec.Cmd) (processes, <-chan error) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "engine-started")
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, "SPOOL_TEST_VOXL_STARTED="+marker)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Always stop and wait before TempDir cleanup, including failed assertions.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	var p processes
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = cmd.Process.Signal(syscall.SIGCONT)
			if cmd.Cancel != nil {
				_ = cmd.Cancel()
			} else {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
			select {
			case <-done:
			case <-time.After(12 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Error("launch client did not exit")
			}
		}
		if p.Helper > 0 && p.Voxl > 0 {
			waitStopped(t, p)
		}
	})
	result := make(chan processes, 1)
	go func() {
		var p processes
		_ = json.NewDecoder(out).Decode(&p)
		result <- p
	}()
	select {
	case p = <-result:
	case <-time.After(15 * time.Second):
		t.Fatal("no native app handshake")
	}
	if p.Helper <= 0 || p.Voxl <= 0 {
		t.Fatal("invalid native app handshake", p)
	}
	// The PID acknowledgement precedes exec. Exercise the actual engine, not
	// merely the waiting gate, in the lifetime and TCC responsibility tests.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return p, done
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("acknowledged process did not execute the engine")
	return p, done
}

func waitStopped(t *testing.T, p processes) {
	t.Helper()
	deadline := time.Now().Add(9 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(p.Helper, 0) != nil && syscall.Kill(p.Voxl, 0) != nil {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	if output, err := exec.Command("/bin/ps", "-p", fmt.Sprintf("%d,%d", p.Helper, p.Voxl), "-o", "pid=,ppid=,state=,comm=").CombinedOutput(); err == nil {
		t.Logf("remaining owned processes:\n%s", output)
	}
	t.Errorf("owned app/voxl survived shutdown: %+v", p)
}

func TestNativeAppLifetimeAndFailureCleanup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data with spaces", "voxl")
	voxlfixture.Install(t, root)
	for _, mode := range []string{"cancel", "launcher-crash", "helper-crash", "helper-stall", "engine-exit", "heartbeat-timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cmd, cleanup, err := macapp.Command(ctx, root, "--port="+port(t))
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			cmd.Dir, cmd.Env = root, append(os.Environ(), "VOXL_PASSWORD=isolated-test-secret")
			p, done := start(t, cmd)
			switch mode {
			case "cancel":
				cancel()
			case "launcher-crash":
				_ = cmd.Process.Kill()
			case "helper-crash":
				_ = syscall.Kill(p.Helper, syscall.SIGKILL)
			case "helper-stall":
				_ = syscall.Kill(p.Helper, syscall.SIGSTOP)
				cancel()
			case "engine-exit":
				_ = syscall.Kill(p.Voxl, syscall.SIGTERM)
			case "heartbeat-timeout":
				_ = cmd.Process.Signal(syscall.SIGSTOP)
				waitStopped(t, p)
				_ = cmd.Process.Signal(syscall.SIGCONT)
			}
			select {
			case <-done:
			case <-time.After(12 * time.Second):
				t.Fatal("launch client left running")
			}
			waitStopped(t, p)
		})
	}
}

func TestOwnerCrashRevokesCapture(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voxl")
	voxlfixture.Install(t, root)
	exe, _ := os.Executable()
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "SPOOL_TEST_VOXL_OWNER=1", "SPOOL_TEST_VOXL_ROOT="+root, "SPOOL_TEST_VOXL_PORT="+port(t), "VOXL_PASSWORD=isolated-owner-secret")
	p, done := start(t, cmd)
	_ = cmd.Process.Kill()
	<-done
	waitStopped(t, p)
}

func TestStartupFailureBeforePIDAcknowledgement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voxl")
	voxlfixture.Install(t, root)
	source, err := filepath.Abs("native/helper.m")
	if err != nil {
		t.Fatal(err)
	}
	hook, err := filepath.Abs("testdata/startup_fault.h")
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(macapp.Bundle(root), "Contents", "MacOS", "voxl-helper")
	commands := [][]string{
		{"/usr/bin/xcrun", "--sdk", "macosx", "clang", "-O2", "-fobjc-arc", "-Wall", "-Wextra", "-Werror", "-mmacosx-version-min=12.0", "-framework", "AppKit", "-framework", "Foundation", "-include", hook, source, "-o", launcher},
		{"/usr/bin/codesign", "--force", "--sign", "-", macapp.Bundle(root)},
	}
	for _, args := range commands {
		if output, err := exec.CommandContext(t.Context(), args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatal("prepare startup fault helper:", err, string(output))
		}
	}
	for _, mode := range []string{"helper-crash", "cancel", "handshake-timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cmd, cleanup, err := macapp.Command(ctx, root, "--port="+port(t))
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			record := filepath.Join(t.TempDir(), "processes.json")
			marker := filepath.Join(t.TempDir(), "engine-started")
			fault := "wait"
			if mode == "helper-crash" {
				fault = "exit"
			}
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "SPOOL_TEST_STARTUP_FAULT="+fault, "SPOOL_TEST_STARTUP_PIDS="+record, "SPOOL_TEST_VOXL_STARTED="+marker)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait(); close(done) }()
			var p processes
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(12 * time.Second):
					_ = cmd.Process.Kill()
					<-done
				}
				// A regression must not leave the test's own mock processes alive.
				if p.Helper > 0 {
					_ = syscall.Kill(p.Helper, syscall.SIGKILL)
				}
				if p.Voxl > 0 {
					_ = syscall.Kill(p.Voxl, syscall.SIGKILL)
				}
				if p.Helper > 0 && p.Voxl > 0 {
					waitStopped(t, p)
				}
			})
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				data, err := os.ReadFile(record)
				if err == nil && json.Unmarshal(data, &p) == nil && p.Helper > 0 && p.Voxl > 0 {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if p.Helper <= 0 || p.Voxl <= 0 {
				t.Fatal("helper did not reach the spawn boundary")
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Error("failed startup was reported as successful")
				}
			case <-time.After(12 * time.Second):
				t.Fatal("failed startup left the launcher running")
			}
			waitStopped(t, p)
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Error("engine ran before its PID was acknowledged", err)
			}
		})
	}
}

func TestIndependentApplicationResponsibility(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".spool", "voxl")
	voxlfixture.Install(t, root)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, cleanup, err := macapp.Command(ctx, root, "--port="+port(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	cmd.Dir, cmd.Env = root, append(os.Environ(), "VOXL_PASSWORD=isolated-test-secret")
	p, done := start(t, cmd)
	// Diagnostic only, matching MyGo's existing process-memory diagnostics.
	// Production does not call this private API or alter responsibility.
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(lib)
	if _, err := purego.Dlsym(lib, "responsibility_get_pid_responsible_for_pid"); err != nil {
		cancel()
		<-done
		t.Skip("responsibility diagnostic unavailable on this macOS")
	}
	var responsible func(int32) int32
	purego.RegisterLibFunc(&responsible, lib, "responsibility_get_pid_responsible_for_pid")
	appResponsibility := responsible(int32(p.Helper))
	engineResponsibility := responsible(int32(p.Voxl))
	parentResponsibility := responsible(int32(os.Getpid()))
	t.Logf("responsible processes: helper=%d voxl=%d launcher=%d parent=%d", appResponsibility, engineResponsibility, responsible(int32(cmd.Process.Pid)), parentResponsibility)
	if appResponsibility != int32(p.Helper) || engineResponsibility != int32(p.Helper) || appResponsibility == parentResponsibility {
		t.Errorf("capture is not attributed to the independent app: %+v", p)
	}
	cancel()
	<-done
}

func TestInstallationPreservesIdentityAndSharedData(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voxl")
	voxlfixture.Install(t, root)
	shared := filepath.Join(root, "shared", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(shared), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]string {
		files := map[string]string{}
		err := filepath.WalkDir(macapp.Bundle(root), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files[path] = fmt.Sprintf("%x/%d", sha256.Sum256(data), info.ModTime().UnixNano())
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := snapshot()
	if err := macapp.Install(t.Context(), root, "missing-source-after-Spool-upgrade"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("reinstall rewrote an authorized application")
	}
	if data, err := os.ReadFile(shared); err != nil || string(data) != "unchanged" {
		t.Fatal("install changed shared data", err)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", macapp.Bundle(root)).CombinedOutput(); err != nil {
		t.Fatal("invalid application signature", err, string(output))
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".helper-install-") {
			t.Fatal("installation left scratch data")
		}
	}
}

func TestHelperRefusesCaptureWithoutAuthenticatedStart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voxl")
	voxlfixture.Install(t, root)
	launcher := filepath.Join(macapp.Bundle(root), "Contents", "MacOS", "voxl-helper")
	for _, mode := range []string{"manual-open", "missing-control", "wrong-start", "missing-gate"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			args := []string{}
			if mode != "manual-open" {
				args = []string{"--run"}
			}
			if mode == "missing-gate" {
				args = []string{"--exec"}
			}
			cmd := exec.CommandContext(ctx, launcher, args...)
			cmd.Env = os.Environ()
			var handshake chan error
			if mode == "wrong-start" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(4 * time.Second))
				token := strings.Repeat("a", 64)
				cmd.Env = append(cmd.Env, "SPOOL_VOXL_CONTROL_TOKEN="+token, "SPOOL_VOXL_CONTROL_PORT="+strconv.Itoa(listener.Addr().(*net.TCPAddr).Port), "SPOOL_VOXL_DIRECTORY="+root)
				handshake = make(chan error, 1)
				go func() {
					conn, err := listener.Accept()
					if err != nil {
						handshake <- err
						return
					}
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
					reader := bufio.NewReader(conn)
					line, err := reader.ReadString('\n')
					if err != nil || line != token+"\n" {
						handshake <- fmt.Errorf("invalid authentication: %w", err)
						return
					}
					_, err = fmt.Fprintln(conn, "WRONG")
					if err == nil {
						line, err = reader.ReadString('\n')
						if line != "" {
							handshake <- fmt.Errorf("helper started without authorization: %s", line)
							return
						}
					}
					if !errors.Is(err, io.EOF) {
						handshake <- fmt.Errorf("helper did not close invalid control channel: %w", err)
						return
					}
					handshake <- nil
				}()
			}
			if err := cmd.Run(); err == nil || ctx.Err() != nil {
				t.Fatal("helper accepted an unauthorized start or failed to exit", err)
			}
			if handshake != nil {
				if err := <-handshake; err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// Opt-in real-binary smoke test. --version returns before voxl initializes
// desktop capture, so this checks native loading without requesting access to
// the developer's screen or injecting input.
func TestRealVoxlApplicationVersion(t *testing.T) {
	binary := os.Getenv("SPOOL_VOXL_SMOKE_BINARY")
	if binary == "" {
		t.Skip("set SPOOL_VOXL_SMOKE_BINARY to a locally built voxl executable")
	}
	root := filepath.Join(t.TempDir(), "voxl")
	if err := macapp.Install(t.Context(), root, binary); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd, cleanup, err := macapp.Command(ctx, root, "--version")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	cmd.Dir, cmd.Env = root, os.Environ()
	output, err := cmd.Output()
	if err != nil {
		t.Fatal("real voxl did not finish --version through LaunchServices", err)
	}
	var p processes
	if err := json.Unmarshal(output, &p); err != nil || p.Helper <= 0 || p.Voxl <= 0 {
		t.Fatal("real voxl was not launched", string(output), err)
	}
	waitStopped(t, p)
}
