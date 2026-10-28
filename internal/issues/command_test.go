package issues

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRunFindsGitHubCLIWithoutShellPATH(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	name := "gh"
	if runtime.GOOS == "windows" {
		bin = filepath.Join(home, "Programs", "GitHub CLI")
		name += ".exe"
		t.Setenv("ProgramW6432", "")
		t.Setenv("ProgramFiles", filepath.Join(home, "Programs"))
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(bin, name)
	if err := os.Link(executable, fixture); err != nil {
		data, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture, data, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := t.TempDir()
	t.Setenv("PATH", path)
	t.Setenv("GH_HOST", "enterprise.example.com")
	t.Setenv("GH_PROMPT_DISABLED", "0")
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	t.Setenv("SPOOL_TEST_GH_PROCESS", "1")
	run := func(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
		args = append([]string{"-test.run=^TestGitHubCLIProcess$", "--"}, args...)
		return Run(ctx, directory, name, args...)
	}
	status := CheckCLI(context.Background(), run)
	if status != (CLIStatus{State: CLIReady, Login: "octocat"}) {
		t.Fatalf("GitHub CLI installed outside PATH was not detected: %+v", status)
	}
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out, err := run(context.Background(), directory, "gh", "argument with spaces")
	if err != nil {
		t.Fatal(err)
	}
	want := directory + "\ngithub.com\n1\n0\nargument with spaces\n"
	if string(out) != want {
		t.Fatalf("fallback changed the working directory, environment or arguments: got %q, want %q", out, want)
	}
	if os.Getenv("PATH") != path {
		t.Fatal("CLI discovery changed the process PATH")
	}
}

func TestGitHubCLIProcess(t *testing.T) {
	if os.Getenv("SPOOL_TEST_GH_PROCESS") != "1" {
		return
	}
	arg := os.Args[len(os.Args)-1]
	switch arg {
	case "--version":
		fmt.Println("gh version fixture")
	case "user":
		fmt.Print(`{"login":"octocat"}`)
	default:
		directory, err := os.Getwd()
		if err != nil {
			os.Exit(1)
		}
		fmt.Printf("%s\n%s\n%s\n%s\n%s\n", directory, os.Getenv("GH_HOST"), os.Getenv("GH_PROMPT_DISABLED"), os.Getenv("GIT_TERMINAL_PROMPT"), arg)
	}
	os.Exit(0)
}

func TestRunWithGitHubCLILeavesOtherCommandsUnchanged(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPOOL_TEST_GH_PROCESS", "1")
	run := WithGitHubCLI(filepath.Join(t.TempDir(), "missing-gh"))
	out, err := run(context.Background(), "", executable, "-test.run=^TestGitHubCLIProcess$", "--", "--version")
	if err != nil || string(out) != "gh version fixture\n" {
		t.Fatalf("gh override affected another command: %q, %v", out, err)
	}
}
