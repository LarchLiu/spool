package issues

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func writeGitHubCLIFixture(t *testing.T, directory string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("fixture"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindGitHubCLIPrefersPATH(t *testing.T) {
	bin := t.TempDir()
	want := writeGitHubCLIFixture(t, bin, 0o700)
	fallback := writeGitHubCLIFixture(t, t.TempDir(), 0o700)
	t.Setenv("PATH", bin)
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	got, err := findGitHubCLI([]string{fallback})
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want PATH executable %q", got, err, want)
	}
}

func TestFindGitHubCLIFallback(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	missing := filepath.Join(t.TempDir(), "missing-gh")
	directory := t.TempDir()
	want := writeGitHubCLIFixture(t, t.TempDir(), 0o700)
	got, err := findGitHubCLI([]string{missing, directory, want})
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want fallback executable %q", got, err, want)
	}
}

func TestFindGitHubCLISkipsNonExecutableFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use Unix executable permission bits")
	}
	t.Setenv("PATH", t.TempDir())
	nonExecutable := writeGitHubCLIFixture(t, t.TempDir(), 0o600)
	want := writeGitHubCLIFixture(t, t.TempDir(), 0o700)
	got, err := findGitHubCLI([]string{nonExecutable, want})
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want executable fallback %q", got, err, want)
	}
}

func TestFindGitHubCLIPreservesMissingError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	got, err := findGitHubCLI([]string{"relative/gh", filepath.Join(t.TempDir(), "missing-gh")})
	if got != "" || !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("got %q, %v; want exec.ErrNotFound", got, err)
	}
}

func TestFindGitHubCLIPreservesErrDot(t *testing.T) {
	bin := t.TempDir()
	writeGitHubCLIFixture(t, bin, 0o700)
	t.Chdir(bin)
	t.Setenv("PATH", ".")
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	t.Setenv("GODEBUG", "execerrdot=1")
	fallback := writeGitHubCLIFixture(t, t.TempDir(), 0o700)
	if _, err := findGitHubCLI([]string{fallback}); !errors.Is(err, exec.ErrDot) {
		t.Fatalf("fallback bypassed unsafe current-directory lookup: %v", err)
	}
}

func TestResolveGitHubCLIOverride(t *testing.T) {
	bin := t.TempDir()
	writeGitHubCLIFixture(t, bin, 0o700)
	t.Setenv("PATH", bin)
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	want := writeGitHubCLIFixture(t, filepath.Join(t.TempDir(), "custom installation"), 0o700)
	got, err := ResolveGitHubCLI(want)
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want configured executable %q", got, err, want)
	}
	for _, path := range []string{"gh", "relative/gh", filepath.Join(t.TempDir(), "missing-gh"), t.TempDir()} {
		if got, err := ResolveGitHubCLI(path); err == nil || got != "" {
			t.Fatalf("invalid override %q fell back to PATH: %q, %v", path, got, err)
		}
	}
}

func TestGitHubCLIPaths(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	env := map[string]string{}
	for _, key := range []string{"ProgramW6432", "ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA", "SCOOP", "ChocolateyInstall", "ProgramData"} {
		env[key] = filepath.Join(t.TempDir(), key)
	}
	for _, tc := range []struct {
		goos string
		want []string
	}{
		{goos: "darwin", want: []string{
			filepath.Join(home, ".local", "bin", "gh"),
			filepath.Join("/opt/homebrew/bin", "gh"),
			filepath.Join("/usr/local/bin", "gh"),
			filepath.Join("/opt/local/bin", "gh"),
		}},
		{goos: "linux", want: []string{
			filepath.Join(home, ".local", "bin", "gh"),
			filepath.Join(home, ".linuxbrew", "bin", "gh"),
			filepath.Join(home, ".nix-profile", "bin", "gh"),
			filepath.Join("/home/linuxbrew/.linuxbrew/bin", "gh"),
			filepath.Join("/usr/local/bin", "gh"),
			filepath.Join("/usr/bin", "gh"),
			filepath.Join("/bin", "gh"),
			filepath.Join("/snap/bin", "gh"),
			filepath.Join("/run/current-system/sw/bin", "gh"),
		}},
		{goos: "windows", want: []string{
			filepath.Join(env["ProgramW6432"], "GitHub CLI", "gh.exe"),
			filepath.Join(env["ProgramFiles"], "GitHub CLI", "gh.exe"),
			filepath.Join(env["ProgramFiles(x86)"], "GitHub CLI", "gh.exe"),
			filepath.Join(env["LOCALAPPDATA"], "Programs", "GitHub CLI", "gh.exe"),
			filepath.Join(env["LOCALAPPDATA"], "Microsoft", "WinGet", "Links", "gh.exe"),
			filepath.Join(env["SCOOP"], "shims", "gh.exe"),
			filepath.Join(home, "scoop", "shims", "gh.exe"),
			filepath.Join(env["ChocolateyInstall"], "bin", "gh.exe"),
			filepath.Join(env["ProgramData"], "chocolatey", "bin", "gh.exe"),
		}},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			got := githubCLIPaths(tc.goos, home, func(key string) string { return env[key] })
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got paths %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGitHubCLIPathsSkipsMissingRoots(t *testing.T) {
	paths := githubCLIPaths("windows", "", func(string) string { return "" })
	if len(paths) != 0 {
		t.Fatalf("missing environment produced relative search paths: %v", paths)
	}
	for _, goos := range []string{"darwin", "linux"} {
		for _, path := range githubCLIPaths(goos, "", func(string) string { return "" }) {
			if runtime.GOOS != "windows" && !filepath.IsAbs(path) {
				t.Fatalf("%s: missing home produced relative search path %q", goos, path)
			}
		}
	}
}
