package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func workspaceGitIndex(ctx context.Context, directory string) string {
	if root, _ := gitWorkspace(directory); root != "" {
		directory = root
	}
	body, err := readWorkspaceGit(ctx, directory, 16*1024*1024, "ls-files", "--stage", "-z")
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func workspaceGitMetadata(ctx context.Context, directory string, view *workspaceGitView, allowed func(string) bool) {
	run := func(args ...string) string {
		body, err := readWorkspaceGit(ctx, directory, 256*1024, args...)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(body))
	}
	view.Index = workspaceGitIndex(ctx, directory)
	if body := run("log", "-1", "--format=%H%x00%s%x00%an%x00%at"); body != "" {
		parts := strings.Split(body, "\x00")
		if len(parts) == 4 {
			stamp, _ := strconv.ParseInt(parts[3], 10, 64)
			view.LastCommit = &workspaceGitCommit{ID: parts[0], Subject: parts[1], Author: parts[2], Time: stamp}
		}
	}
	root, _ := gitWorkspace(directory)
	view.RemoteAllowed = allowed == nil || allowed(root)
	remotes := strings.Fields(run("remote"))
	if body := run("for-each-ref", "--format=%(upstream:short)%00%(upstream:remotename)%00%(upstream:remoteref)", "refs/heads/"+view.Branch); view.Branch != "" && body != "" {
		parts := strings.Split(body, "\x00")
		if len(parts) == 3 && strings.HasPrefix(parts[2], "refs/heads/") {
			view.Upstream, view.Remote, view.RemoteBranch = parts[0], parts[1], strings.TrimPrefix(parts[2], "refs/heads/")
		}
	}
	if view.Upstream != "" {
		counts := strings.Fields(run("rev-list", "--left-right", "--count", "HEAD...@{upstream}"))
		if len(counts) == 2 {
			view.Ahead, _ = strconv.Atoi(counts[0])
			view.Behind, _ = strconv.Atoi(counts[1])
		}
	}
	if view.Remote == "" {
		for _, remote := range remotes {
			if remote == "origin" {
				view.Remote = remote
			}
		}
		if view.Remote == "" && len(remotes) == 1 {
			view.Remote = remotes[0]
		}
		view.RemoteBranch = view.Branch
	}
	found := false
	for _, remote := range remotes {
		if remote == view.Remote {
			found = true
		}
	}
	if !found {
		view.Remote = ""
		view.RemoteBranch = ""
	}
}

type workspaceGitOperation struct {
	Action       string   `json:"action"`
	Paths        []string `json:"paths"`
	Message      string   `json:"message"`
	Branch       string   `json:"branch"`
	Head         string   `json:"head"`
	Index        string   `json:"index"`
	Remote       string   `json:"remote"`
	RemoteBranch string   `json:"remote_branch"`
}

type workspaceGitLock struct {
	gate  chan struct{}
	users int
}

// Queue writes across linked worktrees, which share refs. Waiting requests can
// be canceled independently; idle entries are removed rather than accumulating.
func (a *App) beginWorkspaceGit(ctx context.Context, common string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.gitMu.Lock()
	if a.gitBusy == nil {
		a.gitBusy = make(map[string]*workspaceGitLock)
	}
	lock := a.gitBusy[common]
	if lock == nil {
		lock = &workspaceGitLock{gate: make(chan struct{}, 1)}
		a.gitBusy[common] = lock
	}
	lock.users++
	a.gitMu.Unlock()
	drop := func() {
		a.gitMu.Lock()
		defer a.gitMu.Unlock()
		lock.users--
		if lock.users == 0 {
			delete(a.gitBusy, common)
		}
	}
	select {
	case lock.gate <- struct{}{}:
		return func() { <-lock.gate; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

// Drain command output even when the retained diagnostic limit is reached: a
// successful write must not appear to fail merely because a hook is verbose.
type gitOperationOutput struct{ body []byte }

func (out *gitOperationOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 64*1024 - len(out.body)
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		out.body = append(out.body, p[:remaining]...)
	}
	return n, nil
}

var gitCredentialURL = regexp.MustCompile(`(https?://)[^\s/@]+(?::[^\s/@]*)?@`)

func runWorkspaceGitOperation(ctx context.Context, directory, message string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	base := []string{"-c", "color.ui=false", "-c", "credential.interactive=false"}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	configureWorkspaceGitCommand(cmd)
	cmd.Dir = directory
	cmd.WaitDelay = time.Second
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "GIT_") || name == "GIT_SSH" || name == "GIT_SSH_COMMAND" || name == "GIT_SSH_VARIANT" {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_LITERAL_PATHSPECS=1", "GIT_EDITOR=true", "SSH_ASKPASS_REQUIRE=never")
	if message != "" {
		cmd.Stdin = strings.NewReader(message)
	}
	out := &gitOperationOutput{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Git operation timed out; refresh the Git panel to check its state")
		}
		text := strings.TrimSpace(gitCredentialURL.ReplaceAllString(string(out.body), "${1}[redacted]@"))
		if text == "" {
			text = err.Error()
		}
		return fmt.Errorf("%s", text)
	}
	return nil
}

func (a *App) mutateWorkspaceGit(w http.ResponseWriter, r *http.Request, directory string, allowed func(string) bool) {
	var input workspaceGitOperation
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Action != "stage" && input.Action != "unstage" && input.Action != "discard" && input.Action != "commit" && input.Action != "fetch" && input.Action != "push" {
		apiError(w, 400, "Unknown Git operation")
		return
	}
	root, common := gitWorkspace(directory)
	if common == "" {
		apiError(w, 400, "This directory is not a Git repository.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 110*time.Second)
	defer cancel()
	release, err := a.beginWorkspaceGit(ctx, common)
	if err != nil {
		apiError(w, 409, "Git operation was canceled while waiting. Refresh the Git panel.")
		return
	}
	defer release()
	view := workspaceGitView{}
	_, view.Branch, _ = workspaceGitState(directory)
	if head, err := readWorkspaceGit(ctx, directory, 1024, "rev-parse", "--verify", "HEAD"); err == nil {
		view.Commit = strings.TrimSpace(string(head))
	}
	workspaceGitMetadata(ctx, directory, &view, allowed)
	if input.Branch != view.Branch || input.Head != view.Commit {
		apiError(w, 409, "The Git branch changed. Refresh the Git panel and try again.")
		return
	}
	var args []string
	switch input.Action {
	case "stage", "unstage", "discard":
		if len(input.Paths) == 0 || len(input.Paths) > maxDirectoryEntries {
			apiError(w, 400, "Choose files for the Git operation")
			return
		}
		if input.Action == "discard" && (view.Index == "" || input.Index != view.Index) {
			apiError(w, 409, "Staged changes changed. Review them before discarding.")
			return
		}
		paths := []string{}
		seen := map[string]bool{}
		body, err := readWorkspaceGit(ctx, directory, 4*1024*1024, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", ".")
		if err != nil {
			apiError(w, 500, "Git changes are unavailable")
			return
		}
		files := map[string]workspaceGitFile{}
		for _, file := range parseWorkspaceGitStatus(body) {
			path, _ := filepath.Rel(directory, filepath.Join(root, file.Path))
			file.Path = filepath.ToSlash(path)
			if file.PreviousPath != "" {
				previous, _ := filepath.Rel(directory, filepath.Join(root, file.PreviousPath))
				file.PreviousPath = filepath.ToSlash(previous)
			}
			files[file.Path] = file
		}
		for _, path := range input.Paths {
			file, found := files[path]
			if !found || !workspaceGitPath(directory, path, allowed) || file.PreviousPath != "" && !workspaceGitPath(directory, file.PreviousPath, allowed) {
				apiError(w, 403, "Choose an accessible changed file")
				return
			}
			if input.Action == "discard" && !file.Unstaged {
				apiError(w, 409, "The selected files changed. Refresh the Git panel.")
				return
			}
			if input.Action == "unstage" && !file.Staged || input.Action == "stage" && !file.Unstaged {
				continue
			}
			names := []string{path}
			if input.Action == "unstage" || input.Action == "stage" && !file.Staged {
				names = append(names, file.PreviousPath)
			}
			for _, name := range names {
				if name != "" && !seen[name] {
					paths = append(paths, name)
					seen[name] = true
				}
			}
		}
		if len(paths) == 0 {
			apiError(w, 409, "The selected files changed. Refresh the Git panel.")
			return
		}
		if input.Action == "discard" {
			if err := discardWorkspaceGit(ctx, directory, root, paths); err != nil {
				apiError(w, 409, err.Error())
				return
			}
			apiJSON(w, 200, map[string]bool{"ok": true})
			return
		}
		args = []string{"add", "--all", "--"}
		if input.Action == "unstage" {
			if view.Commit == "" {
				args = []string{"rm", "--cached", "--force", "-r", "--ignore-unmatch", "--"}
			} else {
				args = []string{"reset", "--quiet", "HEAD", "--"}
			}
		}
		args = append(args, paths...)
	case "commit":
		if strings.TrimSpace(input.Message) == "" || len(input.Message) > 32*1024 || strings.ContainsRune(input.Message, 0) {
			apiError(w, 400, "Enter a commit message")
			return
		}
		if view.Index == "" || input.Index != view.Index {
			apiError(w, 409, "Staged changes changed. Review them before committing.")
			return
		}
		body, err := readWorkspaceGit(ctx, root, 4*1024*1024, "diff", "--cached", "--name-only", "--no-renames", "-z")
		if err != nil || len(body) == 0 {
			apiError(w, 409, "No staged changes to commit")
			return
		}
		for _, path := range strings.Split(strings.TrimSuffix(string(body), "\x00"), "\x00") {
			relative, err := filepath.Rel(directory, filepath.Join(root, path))
			if err != nil || !workspaceGitPath(directory, filepath.ToSlash(relative), allowed) {
				apiError(w, 403, "Staged changes outside this directory must be reviewed in their project before committing.")
				return
			}
		}
		args = []string{"commit", "--file=-"}
	case "fetch", "push":
		if !view.RemoteAllowed {
			apiError(w, 403, "This project does not grant access to the repository root")
			return
		}
		if view.Remote == "" || input.Remote != view.Remote || input.RemoteBranch != view.RemoteBranch {
			apiError(w, 409, "The Git remote changed. Refresh the Git panel.")
			return
		}
		if input.Action == "fetch" {
			args = []string{"fetch", "--no-recurse-submodules", "--", view.Remote}
		} else {
			if view.Branch == "" || view.Commit == "" {
				apiError(w, 409, "Choose a branch with commits before pushing")
				return
			}
			args = []string{"push", "--porcelain", "--recurse-submodules=no", "--no-follow-tags"}
			if view.Upstream == "" {
				args = append(args, "--set-upstream")
			}
			args = append(args, "--", view.Remote, "HEAD:refs/heads/"+view.RemoteBranch)
		}
	}
	if err := runWorkspaceGitOperation(ctx, directory, input.Message, args...); err != nil {
		apiError(w, 409, err.Error())
		return
	}
	apiJSON(w, 200, map[string]bool{"ok": true})
}

// Restore tracked files from the index, preserving staged changes. Remove only
// exact untracked files; never recurse through directories or nested repositories.
// Validate the entire batch before touching any working files.
func discardWorkspaceGit(ctx context.Context, directory, repository string, paths []string) error {
	body, err := readWorkspaceGit(ctx, directory, 4*1024*1024, append([]string{"ls-files", "--stage", "--full-name", "-z", "--"}, paths...)...)
	if err != nil {
		return fmt.Errorf("Git index is unavailable")
	}
	tracked := map[string]bool{}
	for _, entry := range strings.Split(string(body), "\x00") {
		if entry == "" {
			continue
		}
		metadata, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(metadata)
		if !ok || len(fields) != 3 || fields[2] != "0" || fields[0] != "100644" && fields[0] != "100755" {
			return fmt.Errorf("Resolve conflicts and review submodules or special files before discarding")
		}
		relative, err := filepath.Rel(directory, filepath.Join(repository, name))
		if err != nil {
			return err
		}
		tracked[filepath.ToSlash(relative)] = true
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	var restore, remove []string
	for _, path := range paths {
		info, err := root.Lstat(filepath.FromSlash(path))
		if err != nil && !(tracked[path] && os.IsNotExist(err)) {
			return fmt.Errorf("The selected files changed. Refresh the Git panel.")
		}
		if info != nil && !info.Mode().IsRegular() {
			return fmt.Errorf("Only regular files can be discarded; review directories and special files separately")
		}
		if tracked[path] {
			restore = append(restore, path)
		} else {
			remove = append(remove, path)
		}
	}
	if len(restore) > 0 {
		if err := runWorkspaceGitOperation(ctx, directory, "", append([]string{"restore", "--worktree", "--"}, restore...)...); err != nil {
			return err
		}
	}
	for _, path := range remove {
		if err := root.Remove(filepath.FromSlash(path)); err != nil {
			return fmt.Errorf("Some files could not be discarded. Refresh the Git panel: %w", err)
		}
	}
	return nil
}
