package diagnostics

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if RunCrashMonitor() {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestApplicationLogsArePersistentPrivateAndRestoreLogger(t *testing.T) {
	root := t.TempDir()
	output, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	session, err := Start(root, "desktop", "0.1.2", true)
	if err != nil {
		t.Fatal(err)
	}
	defer debug.SetCrashOutput(nil, debug.CrashOptions{})
	log.Print("event=startup_failed error=unsupported_schema")
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if log.Writer() != output || log.Flags() != flags || log.Prefix() != prefix {
		t.Fatal("application logger was not restored")
	}
	if session.Path != filepath.Join(root, "log", "desktop.log") {
		t.Fatal("log escaped the log/ directory", session.Path)
	}
	data, err := os.ReadFile(session.Path)
	if err != nil || !bytes.Contains(data, []byte("event=startup_failed")) || !bytes.Contains(data, []byte("version=\"0.1.2\"")) {
		t.Fatalf("diagnostics missing: %s, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{filepath.Join(root, "log"): 0o700, session.Path: 0o600} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != want {
				t.Fatalf("private permissions missing for %s: %v, %v", path, info, err)
			}
		}
	}
}

func TestLogRotationCapsEvenOversizedWrites(t *testing.T) {
	session, err := openSession(t.TempDir(), "server")
	if err != nil {
		t.Fatal(err)
	}
	defer session.writer.Close()
	data := bytes.Repeat([]byte("x"), maxLogBytes*6+17)
	if n, err := session.writer.Write(data); err != nil || n != len(data) {
		t.Fatalf("oversized write failed: %d, %v", n, err)
	}
	files, err := filepath.Glob(session.Path + "*")
	if err != nil || len(files) != 4 {
		t.Fatalf("unbounded rotation: %v, %v", files, err)
	}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxLogBytes {
			t.Fatalf("file exceeds size cap: %s, %v, %v", path, info, err)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrPermission }

func TestMissingConsoleOrBrokenFileDoesNotSuppressOtherDestination(t *testing.T) {
	var file, console bytes.Buffer
	if _, err := (mirroredWriter{file: &file, console: failingWriter{}}).Write([]byte("file-log")); err != nil || file.String() != "file-log" {
		t.Fatal("missing GUI stderr suppressed persistent logging", err)
	}
	if _, err := (mirroredWriter{file: failingWriter{}, console: &console}).Write([]byte("console-log")); err == nil || console.String() != "console-log" {
		t.Fatal("broken disk logging suppressed stderr", err)
	}
}

func TestUnwritableDataRootFallsBackWithoutDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	t.Setenv("LocalAppData", home)
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := Start(root, "desktop", "test", true)
	if err != nil {
		t.Fatal(err)
	}
	defer debug.SetCrashOutput(nil, debug.CrashOptions{})
	defer session.Close()
	if session.Warning == nil || filepath.Base(filepath.Dir(session.Path)) != "log" || strings.HasPrefix(session.Path, root) {
		t.Fatalf("fallback log unavailable: %+v", session)
	}
	data, err := os.ReadFile(session.Path)
	if err != nil || !bytes.Contains(data, []byte("event=log_directory_fallback")) {
		t.Fatalf("fallback was silent: %s, %v", data, err)
	}
	content, err := os.ReadFile(root)
	if err != nil || string(content) != "keep" {
		t.Fatal("diagnostics changed the unusable data root")
	}
}

func TestFatalPanicsAreRecordedAfterDeferredCleanup(t *testing.T) {
	for _, mode := range []string{"main-panic", "worker-panic"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable, "-test.run=^TestDiagnosticsCrashProcess$")
			cmd.Env = append(os.Environ(), "SPOOL_TEST_CRASH_ROOT="+root, "SPOOL_TEST_CRASH_MODE="+mode)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("panic did not terminate the helper: %s", output)
			}
			path := filepath.Join(root, "log", "desktop-crash.log")
			deadline := time.Now().Add(5 * time.Second)
			for {
				data, _ := os.ReadFile(path)
				if bytes.Contains(data, []byte("panic: diagnostic-crash-"+mode)) && bytes.Contains(data, []byte("goroutine")) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("crash report missing: %s", data)
				}
				time.Sleep(10 * time.Millisecond)
			}
			session, err := openSession(filepath.Join(root, "log"), "desktop")
			if err != nil {
				t.Fatal(err)
			}
			defer session.writer.Close()
			if session.PreviousCrashPath != path+".1" {
				t.Fatal("previous crash was not retained for the UI", session.PreviousCrashPath)
			}
		})
	}
}

func TestDiagnosticsCrashProcess(t *testing.T) {
	root := os.Getenv("SPOOL_TEST_CRASH_ROOT")
	if root == "" {
		return
	}
	session, err := Start(root, "desktop", "test", true)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	mode := os.Getenv("SPOOL_TEST_CRASH_MODE")
	if mode == "worker-panic" {
		go func() { panic("diagnostic-crash-" + mode) }()
		select {}
	}
	panic("diagnostic-crash-" + mode)
}

func TestCrashMonitorAlsoEnforcesSizeCaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop-crash.log")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, monitorArgument)
	cmd.Env = append(os.Environ(), "SPOOL_CRASH_MONITOR=1", "SPOOL_CRASH_LOG="+path)
	cmd.Stdin = bytes.NewReader(bytes.Repeat([]byte("fatal stack\n"), maxLogBytes/2))
	if output, err := cmd.CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatalf("crash monitor failed: %s, %v", output, err)
	}
	files, err := filepath.Glob(path + "*")
	if err != nil || len(files) != 4 {
		t.Fatalf("unbounded crash logs: %v, %v", files, err)
	}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxLogBytes {
			t.Fatal(fmt.Sprintf("oversized crash log %s: %v, %v", path, info, err))
		}
	}
}
