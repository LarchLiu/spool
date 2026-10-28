package issues

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ResolveGitHubCLI honors an explicit executable without falling back to another
// installation. An empty executable selects automatic discovery.
func ResolveGitHubCLI(executable string) (string, error) {
	if executable == "" {
		return findGitHubCLI(commonGitHubCLIPaths())
	}
	if !filepath.IsAbs(executable) {
		return "", errors.New("GitHub CLI path must be an absolute executable path.")
	}
	return exec.LookPath(executable)
}

func findGitHubCLI(candidates []string) (string, error) {
	path, err := exec.LookPath("gh")
	if err == nil || !errors.Is(err, exec.ErrNotFound) {
		return path, err
	}
	// Desktop launchers do not necessarily inherit the user's shell PATH.
	// Keep PATH precedence and ErrDot protection; never run a shell to find gh.
	for _, candidate := range candidates {
		if !filepath.IsAbs(candidate) {
			continue
		}
		if path, candidateErr := exec.LookPath(candidate); candidateErr == nil {
			return path, nil
		}
	}
	return "", err
}

func commonGitHubCLIPaths() []string {
	home, _ := os.UserHomeDir()
	return githubCLIPaths(runtime.GOOS, home, os.Getenv)
}

func githubCLIPaths(goos, home string, getenv func(string) string) []string {
	var paths []string
	name := "gh"
	if goos == "windows" {
		name += ".exe"
	}
	add := func(root string, parts ...string) {
		if root != "" {
			parts = append(append([]string{root}, parts...), name)
			paths = append(paths, filepath.Join(parts...))
		}
	}
	switch goos {
	case "windows":
		add(getenv("ProgramW6432"), "GitHub CLI")
		add(getenv("ProgramFiles"), "GitHub CLI")
		add(getenv("ProgramFiles(x86)"), "GitHub CLI")
		add(getenv("LOCALAPPDATA"), "Programs", "GitHub CLI")
		add(getenv("LOCALAPPDATA"), "Microsoft", "WinGet", "Links")
		add(getenv("SCOOP"), "shims")
		add(home, "scoop", "shims")
		add(getenv("ChocolateyInstall"), "bin")
		add(getenv("ProgramData"), "chocolatey", "bin")
	case "darwin":
		add(home, ".local", "bin")
		add("/opt/homebrew/bin")
		add("/usr/local/bin")
		add("/opt/local/bin")
	case "linux":
		add(home, ".local", "bin")
		add(home, ".linuxbrew", "bin")
		add(home, ".nix-profile", "bin")
		add("/home/linuxbrew/.linuxbrew/bin")
		add("/usr/local/bin")
		add("/usr/bin")
		add("/bin")
		add("/snap/bin")
		add("/run/current-system/sw/bin")
	}
	return paths
}
