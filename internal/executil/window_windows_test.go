package executil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestHideWindowRunsWithoutConsoleAndPreservesIO(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"default attributes", "existing attributes"} {
		t.Run(name, func(t *testing.T) {
			existing := name == "existing attributes"
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestHiddenCommandProcess$", "--", "argument with spaces")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "SPOOL_TEST_HIDDEN_COMMAND=1")
			cmd.Stdin = strings.NewReader("pipe input")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if existing {
				cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
			}
			HideWindow(cmd)
			if existing && cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
				t.Fatal("existing process flags were lost")
			}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("hidden command failed: %v; stderr: %s", err, &stderr)
			}
			directory, err := filepath.EvalSymlinks(cmd.Dir)
			if err != nil {
				t.Fatal(err)
			}
			want := directory + "\nargument with spaces\npipe input"
			if string(out) != want || stderr.String() != "pipe error" {
				t.Fatalf("command transport changed: stdout=%q stderr=%q", out, &stderr)
			}
		})
	}
}

func TestHiddenCommandProcess(t *testing.T) {
	if os.Getenv("SPOOL_TEST_HIDDEN_COMMAND") != "1" {
		return
	}
	// CREATE_NO_WINDOW must prevent console allocation, including when the
	// parent test runner itself has a console. HideWindow alone is insufficient.
	if cp, _ := windows.GetConsoleCP(); cp != 0 {
		fmt.Fprintln(os.Stderr, "background command has a console")
		os.Exit(1)
	}
	directory, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		os.Exit(1)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "%s\n%s\n%s", directory, os.Args[len(os.Args)-1], input)
	fmt.Fprint(os.Stderr, "pipe error")
	os.Exit(0)
}
