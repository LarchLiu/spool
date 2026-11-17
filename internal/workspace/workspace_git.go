package workspace

import (
	"bytes"
	"context"
	"errors"
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
	"unicode/utf8"
)

type workspaceGitFile struct {
	Path              string `json:"path"`
	PreviousPath      string `json:"previous_path,omitempty"`
	Status            string `json:"status"`
	Staged            bool   `json:"staged"`
	Unstaged          bool   `json:"unstaged"`
	StagedAdditions   int    `json:"staged_additions"`
	StagedDeletions   int    `json:"staged_deletions"`
	UnstagedAdditions int    `json:"unstaged_additions"`
	UnstagedDeletions int    `json:"unstaged_deletions"`
	Untracked         bool   `json:"untracked"`
	Additions         int    `json:"additions"`
	Deletions         int    `json:"deletions"`
	Binary            bool   `json:"binary,omitempty"`
}

type workspaceGitCommit struct {
	ID       string   `json:"id"`
	Subject  string   `json:"subject"`
	Message  string   `json:"message,omitempty"`
	Author   string   `json:"author"`
	Time     int64    `json:"time"`
	Tags     []string `json:"tags,omitempty"`
	Unpushed bool     `json:"unpushed,omitempty"`
}

type workspaceGitView struct {
	Repository     bool                 `json:"repository"`
	Branch         string               `json:"branch,omitempty"`
	Commit         string               `json:"commit,omitempty"`
	Files          []workspaceGitFile   `json:"files"`
	Commits        []workspaceGitCommit `json:"commits"`
	Truncated      bool                 `json:"truncated,omitempty"`
	Writable       bool                 `json:"writable"`
	Index          string               `json:"index,omitempty"`
	Remote         string               `json:"remote,omitempty"`
	RemoteBranch   string               `json:"remote_branch,omitempty"`
	Upstream       string               `json:"upstream,omitempty"`
	Ahead          int                  `json:"ahead"`
	Behind         int                  `json:"behind"`
	RemoteAllowed  bool                 `json:"remote_allowed"`
	LastCommit     *workspaceGitCommit  `json:"last_commit,omitempty"`
	SelectedCommit *workspaceGitCommit  `json:"selected_commit,omitempty"`
	NextCursor     string               `json:"next_cursor,omitempty"`
}

var gitCommitID = regexp.MustCompile(`^[a-f0-9]{40,64}$`)
var errGitOutputLimit = errors.New("Git output exceeded the preview limit")

type gitOutput struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *gitOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.Buffer.Write(p[:b.limit-b.Len()])
		b.exceeded = true
		return 0, errGitOutputLimit
	}
	return b.Buffer.Write(p)
}

// Keep Git reads bounded and independent of inherited repository overrides,
// user diff programs, hooks, fsmonitor and textconv filters.
func readWorkspaceGit(ctx context.Context, directory string, limit int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	base := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=", "-c", "core.quotePath=false", "-c", "color.ui=false"}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = directory
	cmd.WaitDelay = time.Second
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_LITERAL_PATHSPECS=1")
	out := &gitOutput{limit: limit}
	cmd.Stdout = out
	err := cmd.Run()
	if out.exceeded {
		err = errGitOutputLimit
	}
	return out.Bytes(), err
}

func parseWorkspaceGitStatus(body []byte) []workspaceGitFile {
	files := []workspaceGitFile{}
	fields := strings.Split(string(body), "\x00")
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if len(field) < 4 {
			continue
		}
		x, y := field[0], field[1]
		file := workspaceGitFile{Path: field[3:], Status: strings.TrimSpace(field[:2]), Staged: x != ' ' && x != '?', Unstaged: y != ' ' || x == '?', Untracked: x == '?'}
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++
			if i < len(fields) {
				file.PreviousPath = fields[i]
			}
		}
		files = append(files, file)
	}
	return files
}

func parseWorkspaceGitNameStatus(body []byte) []workspaceGitFile {
	files := []workspaceGitFile{}
	fields := strings.Split(string(body), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		file := workspaceGitFile{Status: fields[i], Path: fields[i+1]}
		if strings.HasPrefix(file.Status, "R") || strings.HasPrefix(file.Status, "C") {
			if i+2 >= len(fields) {
				break
			}
			file.PreviousPath, file.Path = file.Path, fields[i+2]
			file.Status = file.Status[:1]
			i++
		}
		files = append(files, file)
	}
	return files
}

func addWorkspaceGitNumstat(files []workspaceGitFile, directory, root string, body []byte, side string) {
	fields := strings.Split(string(body), "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" {
			// With -z, a rename has an empty path followed by old and new paths.
			if i+2 >= len(fields) {
				break
			}
			path = fields[i+2]
			i += 2
		}
		relative, _ := filepath.Rel(directory, filepath.Join(root, path))
		path = filepath.ToSlash(relative)
		add, _ := strconv.Atoi(parts[0])
		del, _ := strconv.Atoi(parts[1])
		for i := range files {
			file := &files[i]
			if file.Path != path && file.PreviousPath != path {
				continue
			}
			switch side {
			case "staged":
				file.StagedAdditions += add
				file.StagedDeletions += del
			case "unstaged":
				file.UnstagedAdditions += add
				file.UnstagedDeletions += del
			default:
				file.Additions += add
				file.Deletions += del
				file.Binary = file.Binary || parts[0] == "-"
			}
		}
	}
}

// Missing/deleted paths can still be diffed, but existing symlink components
// and protected/private subprojects must obey the file browser's boundaries.
func workspaceGitPath(directory, path string, allowed func(string) bool) bool {
	if !filepath.IsLocal(path) || path == "." || strings.ContainsAny(path, "\x00\r\n") || protectedProjectPath(path) {
		return false
	}
	current := directory
	for _, component := range strings.Split(filepath.FromSlash(path), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		check := current
		if err == nil && !info.IsDir() {
			check = filepath.Dir(current)
		}
		if allowed != nil && !allowed(check) {
			return false
		}
	}
	return true
}

func (a *App) serveWorkspaceGit(w http.ResponseWriter, r *http.Request, directory string, writable bool, allowed func(string) bool) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPost {
		if !writable {
			apiError(w, 403, "Project is read-only")
			return
		}
		a.mutateWorkspaceGit(w, r, directory, allowed)
		return
	}
	view := workspaceGitView{Files: []workspaceGitFile{}, Commits: []workspaceGitCommit{}}
	if _, common := gitWorkspace(directory); common == "" {
		apiJSON(w, 200, view)
		return
	}
	view.Repository = true
	view.Writable = writable
	_, view.Branch, view.Commit = workspaceGitState(directory)
	ctx := r.Context()
	run := func(args ...string) ([]byte, error) { return readWorkspaceGit(ctx, directory, 4*1024*1024, args...) }
	commit := r.URL.Query().Get("commit")
	if commit != "" && !gitCommitID.MatchString(commit) {
		apiError(w, 400, "Invalid commit")
		return
	}
	// Require a reachable commit, rather than accepting arbitrary object IDs.
	if commit != "" {
		if _, err := run("merge-base", "--is-ancestor", commit, "HEAD"); err != nil {
			apiError(w, 404, "Commit is unavailable")
			return
		}
	}
	head, headErr := run("rev-parse", "--verify", "HEAD")
	if headErr == nil {
		view.Commit = strings.TrimSpace(string(head))
	}
	workspaceGitMetadata(ctx, directory, &view, allowed)
	mode := r.URL.Query().Get("view")
	if mode == "history" {
		if headErr != nil {
			apiJSON(w, 200, view)
			return
		}
		anchor, offset := view.Commit, 0
		if cursor := r.URL.Query().Get("cursor"); cursor != "" {
			parts := strings.Split(cursor, ":")
			if len(parts) != 2 || !gitCommitID.MatchString(parts[0]) {
				apiError(w, 400, "Invalid history cursor")
				return
			}
			var err error
			offset, err = strconv.Atoi(parts[1])
			if err != nil || offset < 1 {
				apiError(w, 400, "Invalid history cursor")
				return
			}
			anchor = parts[0]
			if _, err := run("merge-base", "--is-ancestor", anchor, "HEAD"); err != nil {
				apiError(w, 409, "Git history changed; refresh the list")
				return
			}
		}
		if err := workspaceGitHistory(ctx, directory, &view, anchor, offset); err != nil {
			apiError(w, 500, "Git history is unavailable")
			return
		}
		apiJSON(w, 200, view)
		return
	}
	var files []workspaceGitFile
	var err error
	if commit == "" {
		var body []byte
		body, err = run("status", "--porcelain=v1", "-z", "--untracked-files=all", "--", ".")
		files = parseWorkspaceGitStatus(body)
	} else {
		var body []byte
		// Compare merges to their first parent in the list, statistics and patch.
		body, err = run("diff-tree", "--root", "--no-commit-id", "--diff-merges=first-parent", "--name-status", "-r", "-z", "--find-renames", commit, "--", ".")
		files = parseWorkspaceGitNameStatus(body)
	}
	if err != nil {
		apiError(w, 500, "Git changes are unavailable")
		return
	}
	root, _ := gitWorkspace(directory)
	for _, file := range files {
		// Git status and diff-tree paths are repository relative.
		path, _ := filepath.Rel(directory, filepath.Join(root, file.Path))
		file.Path = filepath.ToSlash(path)
		if file.PreviousPath != "" {
			previous, _ := filepath.Rel(directory, filepath.Join(root, file.PreviousPath))
			file.PreviousPath = filepath.ToSlash(previous)
		}
		if !workspaceGitPath(directory, file.Path, allowed) || file.PreviousPath != "" && !workspaceGitPath(directory, file.PreviousPath, allowed) {
			continue
		}
		view.Files = append(view.Files, file)
	}
	if mode == "diff" {
		path := r.URL.Query().Get("path")
		side := r.URL.Query().Get("side")
		if side != "" && side != "staged" && side != "unstaged" {
			apiError(w, 400, "Invalid diff side")
			return
		}
		found := false
		for _, file := range view.Files {
			if file.Path != path {
				continue
			}
			found = true
			if commit == "" && (side == "staged" && !file.Staged || side == "unstaged" && !file.Unstaged) {
				// A staged deletion and its recreated untracked file share a path.
				// Continue to the status entry for the requested side.
				continue
			}
			patch, truncated, binary, err := workspaceGitDiffSide(ctx, directory, file, commit, headErr == nil, side)
			if err != nil {
				apiError(w, 500, "Git diff is unavailable")
				return
			}
			apiJSON(w, 200, map[string]any{"path": path, "patch": patch, "binary": binary, "truncated": truncated})
			return
		}
		if found {
			apiError(w, 404, "Git changes are unavailable")
			return
		}
		projectFileError(w, os.ErrPermission)
		return
	}
	var stats []byte
	if commit != "" {
		stats, err = run("diff-tree", "--root", "--no-commit-id", "--diff-merges=first-parent", "-r", "--numstat", "-z", "--find-renames", commit, "--", ".")
	} else if headErr == nil {
		stats, err = run("diff", "--no-ext-diff", "--no-textconv", "--numstat", "-z", "--find-renames", "HEAD", "--", ".")
	} else {
		stats, err = run("diff", "--cached", "--no-ext-diff", "--no-textconv", "--numstat", "-z", "--find-renames", "--", ".")
	}
	if err != nil {
		apiError(w, 500, "Git statistics are unavailable")
		return
	}
	if commit == "" {
		for _, side := range []string{"staged", "unstaged"} {
			args := []string{"diff", "--no-ext-diff", "--no-textconv", "--numstat", "-z", "--find-renames"}
			if side == "staged" {
				args = append(args, "--cached")
			}
			body, readErr := run(append(args, "--", ".")...)
			if readErr != nil {
				apiError(w, 500, "Git statistics are unavailable")
				return
			}
			addWorkspaceGitNumstat(view.Files, directory, root, body, side)
		}
	}
	addWorkspaceGitNumstat(view.Files, directory, root, stats, "")
	if len(view.Files) > maxDirectoryEntries {
		view.Files = view.Files[:maxDirectoryEntries]
		view.Truncated = true
	}
	if commit != "" {
		view.SelectedCommit, err = workspaceGitCommitDetail(ctx, directory, &view, commit)
		if err != nil {
			apiError(w, 500, "Git commit information is unavailable")
			return
		}
	}
	apiJSON(w, 200, view)
}

func workspaceGitDiff(ctx context.Context, directory string, file workspaceGitFile, commit string, hasHead bool) (string, bool, bool, error) {
	return workspaceGitDiffSide(ctx, directory, file, commit, hasHead, "")
}

func workspaceGitDiffSide(ctx context.Context, directory string, file workspaceGitFile, commit string, hasHead bool, side string) (string, bool, bool, error) {
	if file.Untracked {
		root, err := os.OpenRoot(directory)
		if err != nil {
			return "", false, false, err
		}
		defer root.Close()
		f, err := root.Open(file.Path)
		if err != nil {
			return "", false, false, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return "", false, false, os.ErrPermission
		}
		body, err := io.ReadAll(io.LimitReader(f, maxFilePreviewBytes+1))
		if err != nil {
			return "", false, false, err
		}
		if bytes.IndexByte(body, 0) >= 0 || !utf8.Valid(body) {
			return "", false, true, nil
		}
		truncated := len(body) > maxFilePreviewBytes
		if truncated {
			body = body[:maxFilePreviewBytes]
			if at := bytes.LastIndexByte(body, '\n'); at >= 0 {
				body = body[:at+1]
			}
		}
		lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
		if len(body) == 0 {
			return "", truncated, false, nil
		}
		var patch strings.Builder
		fmt.Fprintf(&patch, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", file.Path, len(lines))
		for _, line := range lines {
			patch.WriteString("+" + line + "\n")
		}
		if !truncated && body[len(body)-1] != '\n' {
			patch.WriteString("\\ No newline at end of file\n")
		}
		return patch.String(), truncated, false, nil
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--find-renames", "--unified=3"}
	if commit != "" {
		args = []string{"show", "--format=", "--diff-merges=first-parent", "--no-ext-diff", "--no-textconv", "--no-color", "--find-renames", "--unified=3", commit}
	} else if side == "staged" {
		args = append(args, "--cached")
	} else if side == "unstaged" {
		// Default diff compares the worktree to the index, including unborn HEAD.
	} else if hasHead {
		args = append(args, "HEAD")
	} else {
		args = append(args, "--cached")
	}
	args = append(args, "--", file.Path)
	if file.PreviousPath != "" {
		args = append(args, file.PreviousPath)
	}
	body, err := readWorkspaceGit(ctx, directory, maxFilePreviewBytes, args...)
	truncated := errors.Is(err, errGitOutputLimit)
	if truncated {
		err = nil
		if at := bytes.LastIndexByte(body, '\n'); at >= 0 {
			body = body[:at+1]
		}
	}
	binary := false
	for line := range bytes.SplitSeq(body, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("Binary files ")) && bytes.HasSuffix(line, []byte(" differ")) {
			binary = true
			break
		}
	}
	return string(body), truncated, binary, err
}
