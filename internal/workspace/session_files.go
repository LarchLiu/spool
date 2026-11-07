package workspace

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"spool/internal/issues"
	chatmodel "spool/internal/model"
)

type sessionWorkspace struct {
	Path      string `json:"path"`
	Branch    string `json:"branch,omitempty"`
	Commit    string `json:"commit,omitempty"`
	Kind      string `json:"kind"`
	Available bool   `json:"available"`
}

type sessionWorkspaceView struct {
	sessionWorkspace
	Workspaces []sessionWorkspace `json:"workspaces"`
	TurnID     string             `json:"turn_id,omitempty"`
}

// File viewing follows the latest directory recorded in the native session,
// without changing the directory used to start/resume an agent or its permissions.
func (s *Store) sessionFileDirectory(chat chatmodel.Session) (string, error) {
	if chat.NativeSessionPath != "" {
		var directory string
		err := s.historyCache.read(chat.Backend, chat.NativeSessionPath, func(file *nativeHistory) error {
			directory = file.directory
			return nil
		})
		if err == nil && directory != "" {
			return directory, nil
		}
	}
	// Older sessions may not contain cwd metadata. Only then use the issue
	// task's checkout or the session's configured execution directory.
	if chat.AutomationChat {
		if worktree := s.issueFileDirectory(chat); worktree != "" {
			return worktree, nil
		}
	}
	return s.sessionDirectory(chat)
}

// Issue agents already record their actual checkout in the task ledger. Read
// that record, including an active run's progress, rather than changing it.
func (s *Store) issueFileDirectory(chat chatmodel.Session) string {
	project, err := s.Project(chat.ProjectID)
	if err != nil {
		return ""
	}
	_, common := gitWorkspace(project.Path)
	if filepath.Base(common) != ".git" {
		return ""
	}
	main := filepath.Dir(common)
	directory := issues.Directory(s.Root(), main)
	identity, err := os.ReadFile(filepath.Join(filepath.Dir(directory), ".project-directory"))
	if err != nil || string(identity) != main {
		return ""
	}
	ledger, err := issues.Load(directory)
	if err != nil {
		return ""
	}
	for _, record := range ledger.Records {
		if record.Owner.Automation != "issues" || record.Owner.Chat != chat.ID {
			continue
		}
		if run, err := issues.TaskDirectory(directory, record.Owner.Run); err == nil {
			if task, err := issues.Load(run); err == nil && len(task.Records) == 1 {
				if progress := task.Records[record.Number]; progress != nil && progress.Owner == record.Owner {
					return progress.Worktree
				}
			}
		}
		return record.Worktree
	}
	return ""
}

func registeredWorktrees(ctx context.Context, project string) []string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain", "-z")
	cmd.Dir = project
	cmd.WaitDelay = time.Second
	// An inherited Git override must not redirect this authorization check.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	var roots []string
	for _, field := range strings.Split(string(output), "\x00") {
		if path, ok := strings.CutPrefix(field, "worktree "); ok {
			if root := canonicalDirectory(path); root != "" {
				roots = append(roots, root)
			}
		}
	}
	return roots
}

func describeSessionWorkspace(directory string) sessionWorkspace {
	workspace := sessionWorkspace{Path: filepath.Clean(directory), Kind: "directory"}
	if path := canonicalDirectory(directory); path != "" {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			workspace.Path, workspace.Available = path, true
			workspace.Kind, workspace.Branch, workspace.Commit = workspaceGitState(path)
		}
	}
	return workspace
}

func workspaceGitState(directory string) (kind, branch, commit string) {
	root, common := gitWorkspace(directory)
	if common == "" {
		return "directory", "", ""
	}
	kind = "main"
	git := filepath.Join(root, ".git")
	if body, err := os.ReadFile(git); err == nil {
		git = strings.TrimSpace(strings.TrimPrefix(string(body), "gitdir: "))
		if !filepath.IsAbs(git) {
			git = filepath.Join(root, git)
		}
		if canonicalDirectory(git) != common {
			kind = "worktree"
		}
	}
	if body, err := os.ReadFile(filepath.Join(git, "HEAD")); err == nil {
		head := strings.TrimSpace(string(body))
		if ref, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
			branch = ref
		} else if len(head) >= 7 && !strings.ContainsAny(head, " \r\n") {
			commit = head[:7]
		}
	}
	return kind, branch, commit
}

func (a *App) sessionFilesResponse(w http.ResponseWriter, r *http.Request, chat chatmodel.Session, kind string) {
	directory, err := a.store.sessionFileDirectory(chat)
	if err != nil || !filepath.IsAbs(directory) {
		apiError(w, 404, "Session directory is unavailable")
		return
	}
	projects, err := a.store.Projects()
	if err != nil {
		apiError(w, 404, "Session directory is unavailable")
		return
	}
	mappings := localSessionProjects(projects)
	project, err := a.store.Project(chat.ProjectID)
	if err != nil {
		apiError(w, 404, "Session directory is unavailable")
		return
	}
	roots := registeredWorktrees(r.Context(), project.Path)
	projectPath := canonicalDirectory(project.Path)
	allowed := func(path string) bool {
		if matchLocalSessionProject(path, mappings).ID != chat.ProjectID {
			return false
		}
		if directoryWithin(path, projectPath) {
			return true
		}
		root, _ := gitWorkspace(path)
		for _, registered := range roots {
			if root == registered {
				return true
			}
		}
		return false
	}
	workspace := describeSessionWorkspace(directory)
	if workspace.Available && !allowed(workspace.Path) && kind != "file" {
		projectFileError(w, os.ErrPermission)
		return
	}
	options := []sessionWorkspace{workspace}
	seen := map[string]bool{workspace.Path: true}
	projectRoot, _ := gitWorkspace(project.Path)
	currentRoot, _ := gitWorkspace(workspace.Path)
	relative, _ := filepath.Rel(projectRoot, projectPath)
	for _, path := range append([]string{project.Path}, roots...) {
		if path != project.Path && relative != "" && relative != "." {
			path = filepath.Join(path, relative)
		}
		option := describeSessionWorkspace(path)
		root, _ := gitWorkspace(option.Path)
		// A cwd inside one checkout is not another worktree choice.
		if workspace.Available && (currentRoot != "" && root == currentRoot || len(roots) == 0) {
			continue
		}
		if !seen[option.Path] && option.Available && allowed(option.Path) {
			seen[option.Path] = true
			options = append(options, option)
		}
	}
	if selected := r.URL.Query().Get("workspace"); selected != "" {
		if !filepath.IsAbs(selected) || strings.ContainsAny(selected, "\x00\r\n") {
			projectFileError(w, os.ErrPermission)
			return
		}
		path := filepath.Clean(selected)
		if canonical := canonicalDirectory(path); canonical != "" {
			path = canonical
		}
		if !seen[path] {
			projectFileError(w, os.ErrPermission)
			return
		}
		for _, option := range options {
			if option.Path == path {
				workspace = option
				break
			}
		}
	}
	directory = workspace.Path
	if kind == "file" && a.externalSessionFile(w, r, chat, directory) {
		return
	}
	if workspace.Available && !allowed(directory) {
		projectFileError(w, os.ErrPermission)
		return
	}
	if kind == "workspace" {
		view := sessionWorkspaceView{sessionWorkspace: workspace, Workspaces: options}
		if chat.NativeSessionPath != "" {
			_ = a.store.historyCache.read(chat.Backend, chat.NativeSessionPath, func(file *nativeHistory) error {
				if len(file.index) > 0 {
					view.TurnID = nativeIdentity(chat.NativeSessionID, file.index[len(file.index)-1])
				}
				return nil
			})
		}
		apiJSON(w, 200, view)
		return
	}
	if !workspace.Available {
		apiError(w, 404, "Session directory is unavailable")
		return
	}
	a.serveProjectFiles(w, r, directory, kind, func(path string) bool {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return false
		}
		if info, err := os.Stat(resolved); err != nil {
			return false
		} else if !info.IsDir() {
			resolved = filepath.Dir(resolved)
		}
		return matchLocalSessionProject(resolved, mappings).ID == chat.ProjectID
	})
}
