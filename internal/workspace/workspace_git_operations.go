package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
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

// Commit subjects and authors follow the same file boundaries as previews.
// Scan past commits that only touch protected files or nested private projects.
func workspaceGitLastCommit(ctx context.Context, directory string, allowed func(string) bool) *workspaceGitCommit {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	root, _ := gitWorkspace(directory)
	for offset := 0; ; offset += workspaceGitHistoryPage {
		body, err := readWorkspaceGit(ctx, directory, 256*1024, "log", "-"+strconv.Itoa(workspaceGitHistoryPage), "--skip="+strconv.Itoa(offset), "-z", "--format=%H%x00%s%x00%an%x00%at", "--", ".")
		if err != nil {
			return nil
		}
		fields := strings.Split(string(body), "\x00")
		count := 0
		for i := 0; i+3 < len(fields); i += 4 {
			count++
			id := strings.TrimSpace(fields[i])
			paths, err := readWorkspaceGit(ctx, directory, 4*1024*1024, "diff-tree", "--root", "--no-commit-id", "--diff-merges=first-parent", "--name-only", "--no-renames", "-r", "-z", id, "--", ".")
			if err != nil {
				return nil
			}
			visible := len(paths) == 0 && directory == root && (allowed == nil || allowed(root))
			for _, path := range strings.Split(strings.TrimSuffix(string(paths), "\x00"), "\x00") {
				if path == "" {
					continue
				}
				relative, err := filepath.Rel(directory, filepath.Join(root, path))
				if err == nil && workspaceGitPath(directory, filepath.ToSlash(relative), allowed) {
					visible = true
					break
				}
			}
			if visible {
				stamp, _ := strconv.ParseInt(fields[i+3], 10, 64)
				return &workspaceGitCommit{ID: id, Subject: fields[i+1], Author: fields[i+2], Time: stamp}
			}
		}
		if count < workspaceGitHistoryPage {
			return nil
		}
	}
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
	view.LastCommit = workspaceGitLastCommit(ctx, directory, allowed)
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
	// A fork may fetch from its upstream and push to a different remote. Keep
	// these targets separate, following Git's push-remote precedence.
	view.PushRemote, view.PushBranch = view.Remote, view.RemoteBranch
	pushRemote := run("config", "--get", "branch."+view.Branch+".pushRemote")
	if pushRemote == "" {
		pushRemote = run("config", "--get", "remote.pushDefault")
	}
	if pushRemote != "" {
		view.PushRemote = ""
		for _, remote := range remotes {
			if remote == pushRemote {
				view.PushRemote = remote
			}
		}
		if pushRemote != view.Remote {
			view.PushBranch = view.Branch
		}
	}
	if view.PushRemote == "" {
		view.PushBranch = ""
	} else if view.PushBranch != "" {
		tracking := "refs/remotes/" + view.PushRemote + "/" + view.PushBranch
		if run("rev-parse", "--verify", tracking) != "" {
			view.PushUpstream = view.PushRemote + "/" + view.PushBranch
			view.PushAhead, _ = strconv.Atoi(run("rev-list", "--count", tracking+"..HEAD"))
		}
	}
}

// Hold Git's normal index lock across validation and the write. Git commands
// operate on the lock file as an alternate index, then publish it by rename.
// Ordinary external add/commit/checkout commands therefore cannot invalidate a
// checked snapshot while metadata, filters or hooks are running.
type workspaceGitIndexLock struct{ path, target string }

func lockWorkspaceGitIndex(ctx context.Context, directory string) (*workspaceGitIndexLock, error) {
	body, err := readWorkspaceGit(ctx, directory, 4096, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return nil, fmt.Errorf("Git index is unavailable")
	}
	target := strings.TrimSpace(string(body))
	lock := &workspaceGitIndexLock{target + ".lock", target}
	file, err := os.OpenFile(lock.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
	if err != nil {
		return nil, fmt.Errorf("Git index is busy. Wait for other Git operations and refresh the panel.")
	}
	index, err := os.Open(target)
	missing := os.IsNotExist(err)
	if err == nil {
		_, err = io.Copy(file, index)
		_ = index.Close()
	}
	closeErr := file.Close()
	if missing {
		err = runWorkspaceGitOperationWithIndex(ctx, directory, "", lock.path, "read-tree", "--empty")
	}
	if err == nil {
		err = closeErr
	}
	if err != nil {
		lock.close()
		return nil, fmt.Errorf("Git index is unavailable: %w", err)
	}
	return lock, nil
}

func (lock *workspaceGitIndexLock) close() {
	if lock.path != "" {
		_ = os.Remove(lock.path + ".lock")
		_ = os.Remove(lock.path)
	}
}

func (lock *workspaceGitIndexLock) publish() error {
	if err := os.Rename(lock.path, lock.target); err != nil {
		return fmt.Errorf("Git index could not be updated. Refresh the Git panel: %w", err)
	}
	lock.path = ""
	return nil
}

// Literal pathspecs still match directory descendants in the index and HEAD.
// Validate their complete expansion, then allow physical directories only for
// exact gitlink entries (a submodule pointer is one index item).
func validateWorkspaceGitIndexPaths(ctx context.Context, directory, head string, paths []string) error {
	changed := fmt.Errorf("The selected files changed. Choose individual files and refresh the Git panel.")
	root, _ := gitWorkspace(directory)
	selected, gitlinks := map[string]bool{}, map[string]bool{}
	for _, path := range paths {
		selected[path] = true
	}
	index, err := readWorkspaceGit(ctx, directory, 16*1024*1024, append([]string{"ls-files", "--stage", "--full-name", "-z", "--"}, paths...)...)
	if err != nil {
		return changed
	}
	check := func(body []byte) error {
		for _, entry := range strings.Split(string(body), "\x00") {
			if entry == "" {
				continue
			}
			metadata, name, ok := strings.Cut(entry, "\t")
			fields := strings.Fields(metadata)
			relative, err := filepath.Rel(directory, filepath.Join(root, name))
			path := filepath.ToSlash(relative)
			if !ok || len(fields) != 3 || err != nil || !selected[path] {
				return changed
			}
			if fields[0] == "160000" {
				gitlinks[path] = true
			}
		}
		return nil
	}
	if err := check(index); err != nil {
		return err
	}
	if head != "" {
		body, err := readWorkspaceGit(ctx, directory, 16*1024*1024, append([]string{"ls-tree", "-r", "--full-name", "-z", head, "--"}, paths...)...)
		if err != nil {
			return changed
		}
		if err := check(body); err != nil {
			return err
		}
	}
	for _, path := range paths {
		info, err := os.Lstat(filepath.Join(directory, filepath.FromSlash(path)))
		if err != nil && !os.IsNotExist(err) || info != nil && info.IsDir() && !gitlinks[path] {
			return changed
		}
	}
	return nil
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
	return runWorkspaceGitOperationWithIndex(ctx, directory, message, "", args...)
}

func runWorkspaceGitOperationWithIndex(ctx context.Context, directory, message, index string, args ...string) error {
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
	if index != "" {
		cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+index)
	}
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
	var indexLock *workspaceGitIndexLock
	index := ""
	if input.Action != "fetch" && input.Action != "push" {
		indexLock, err = lockWorkspaceGitIndex(ctx, directory)
		if err != nil {
			apiError(w, 409, err.Error())
			return
		}
		defer indexLock.close()
		index = indexLock.path
	}
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
		// A staged deletion followed by a recreated untracked file produces two
		// status entries for one path. Keep both and select the requested side.
		files := map[string][]workspaceGitFile{}
		for _, file := range parseWorkspaceGitStatus(body) {
			path, _ := filepath.Rel(directory, filepath.Join(root, file.Path))
			file.Path = filepath.ToSlash(path)
			if file.PreviousPath != "" {
				previous, _ := filepath.Rel(directory, filepath.Join(root, file.PreviousPath))
				file.PreviousPath = filepath.ToSlash(previous)
			}
			files[file.Path] = append(files[file.Path], file)
		}
		for _, path := range input.Paths {
			entries, found := files[path]
			if !found || !workspaceGitPath(directory, path, allowed) {
				apiError(w, 403, "Choose an accessible changed file")
				return
			}
			matched := false
			for _, file := range entries {
				if input.Action == "unstage" && !file.Staged || input.Action != "unstage" && !file.Unstaged {
					continue
				}
				if file.PreviousPath != "" && !workspaceGitPath(directory, file.PreviousPath, allowed) {
					apiError(w, 403, "Choose an accessible changed file")
					return
				}
				matched = true
				names := []string{path}
				if strings.Contains(file.Status, "R") && (input.Action == "unstage" || input.Action == "stage" && !file.Staged) {
					names = append(names, file.PreviousPath)
				}
				for _, name := range names {
					if name != "" && !seen[name] {
						paths = append(paths, name)
						seen[name] = true
					}
				}
			}
			if input.Action == "discard" && !matched {
				apiError(w, 409, "The selected files changed. Refresh the Git panel.")
				return
			}
		}
		if len(paths) == 0 {
			apiError(w, 409, "The selected files changed. Refresh the Git panel.")
			return
		}
		if input.Action == "discard" {
			if err := discardWorkspaceGit(ctx, directory, root, index, paths); err != nil {
				apiError(w, 409, err.Error())
				return
			}
			apiJSON(w, 200, map[string]bool{"ok": true})
			return
		}
		if err := validateWorkspaceGitIndexPaths(ctx, directory, view.Commit, paths); err != nil {
			apiError(w, 409, err.Error())
			return
		}
		args = []string{"add", "--all", "--"}
		if input.Action == "unstage" {
			if view.Commit == "" {
				args = []string{"rm", "--cached", "--force", "-r", "--ignore-unmatch", "--"}
			} else {
				args = []string{"reset", "--quiet", view.Commit, "--"}
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
		remote, branch := view.Remote, view.RemoteBranch
		if input.Action == "push" {
			remote, branch = view.PushRemote, view.PushBranch
		}
		if remote == "" || input.Remote != remote || input.RemoteBranch != branch {
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
			args = []string{"push", "--porcelain", "--recurse-submodules=no", "--no-follow-tags", "--", remote, view.Commit + ":refs/heads/" + branch}
		}
	}
	// Metadata queries can be slow. Recheck refs after them; the index lock
	// protects ordinary checkouts, and push additionally pins its source SHA.
	_, branch, _ := workspaceGitState(directory)
	head, _ := readWorkspaceGit(ctx, directory, 1024, "rev-parse", "--verify", "HEAD")
	if branch != input.Branch || strings.TrimSpace(string(head)) != input.Head {
		apiError(w, 409, "The Git branch changed. Refresh the Git panel and try again.")
		return
	}
	if err := runWorkspaceGitOperationWithIndex(ctx, directory, input.Message, index, args...); err != nil {
		apiError(w, 409, err.Error())
		return
	}
	if indexLock != nil {
		if err := indexLock.publish(); err != nil {
			apiError(w, 409, err.Error())
			return
		}
	}
	if input.Action == "push" && view.Upstream == "" && view.PushRemote == view.Remote {
		// The source refspec is an immutable SHA, so --set-upstream cannot infer
		// the local branch. Name it explicitly, independent of the current HEAD.
		if err := runWorkspaceGitOperation(ctx, directory, "", "branch", "--set-upstream-to="+view.PushRemote+"/"+view.PushBranch, "--", input.Branch); err != nil {
			apiError(w, 409, "Push succeeded, but tracking could not be configured. Refresh the Git panel: "+err.Error())
			return
		}
	}
	apiJSON(w, 200, map[string]bool{"ok": true})
}

// Restore tracked files from the index, preserving staged changes. Remove only
// exact untracked files; never recurse through directories or nested repositories.
// Validate the entire batch before touching any working files.
func discardWorkspaceGit(ctx context.Context, directory, repository, index string, paths []string) error {
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
		if err := runWorkspaceGitOperationWithIndex(ctx, directory, "", index, append([]string{"restore", "--worktree", "--"}, restore...)...); err != nil {
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
