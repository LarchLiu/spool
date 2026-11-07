package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Desktop launchers may find a CLI by its absolute fallback path while its
// shebang or launcher still needs node (or another runtime) on PATH. Supply
// these directories only to the child, keeping the inherited PATH first.
func configureAgentEnvironment(cmd *exec.Cmd) {
	env := cmd.Environ()
	path, index := "", -1
	for i, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			path, index = value, i
			break
		}
	}
	directories := filepath.SplitList(path)
	seen := make(map[string]bool, len(directories))
	for _, directory := range directories {
		seen[filepath.Clean(directory)] = true
	}
	// Include the chosen launcher's directory and the same standard install
	// locations already searched for agent executables. Never add a relative
	// directory or source a user's shell startup files.
	candidates := append([]string{cmd.Path}, commonAgentPaths("node")...)
	changed := false
	for _, binary := range candidates {
		directory := filepath.Dir(binary)
		if !filepath.IsAbs(directory) || seen[directory] {
			continue
		}
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[directory] = true
		directories = append(directories, directory)
		changed = true
	}
	if !changed {
		return
	}
	entry := "PATH=" + strings.Join(directories, string(os.PathListSeparator))
	if index < 0 {
		env = append(env, entry)
	} else {
		env[index] = entry
	}
	cmd.Env = env
}
