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

// File viewing follows provider metadata without changing the directory used
// to start/resume an agent or granting the agent additional permissions.
func (s *Store) sessionFileDirectory(chat chatmodel.Session) (string, error) {
	directory, err := s.sessionDirectory(chat)
	if err != nil {
		return "", err
	}
	if chat.NativeSessionPath != "" {
		_ = s.historyCache.read(chat.Backend, chat.NativeSessionPath, func(file *nativeHistory) error {
			if file.directory != "" {
				directory = file.directory
			}
			return nil
		})
	}
	if chat.AutomationChat {
		if worktree := s.issueFileDirectory(chat); worktree != "" {
			directory = worktree
		}
	}
	return directory, nil
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

func registeredWorktree(ctx context.Context, project, root string) bool {
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
		return false
	}
	for _, field := range strings.Split(string(output), "\x00") {
		if path, ok := strings.CutPrefix(field, "worktree "); ok && canonicalDirectory(path) == root {
			return true
		}
	}
	return false
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
	if err == nil && kind == "file" && a.externalSessionFile(w, r, chat, directory) {
		return
	}
	if err != nil || !filepath.IsAbs(directory) {
		apiError(w, 404, "Session directory is unavailable")
		return
	}
	workspace := sessionWorkspace{Path: filepath.Clean(directory), Kind: "directory"}
	directory = canonicalDirectory(directory)
	if directory == "" {
		if kind == "workspace" {
			apiJSON(w, 200, workspace)
		} else {
			apiError(w, 404, "Session directory is unavailable")
		}
		return
	}
	projects, err := a.store.Projects()
	if err != nil {
		apiError(w, 404, "Session directory is unavailable")
		return
	}
	mappings := localSessionProjects(projects)
	if matchLocalSessionProject(directory, mappings).ID != chat.ProjectID {
		projectFileError(w, os.ErrPermission)
		return
	}
	project, err := a.store.Project(chat.ProjectID)
	if err != nil {
		apiError(w, 404, "Session directory is unavailable")
		return
	}
	if !directoryWithin(directory, canonicalDirectory(project.Path)) {
		root, _ := gitWorkspace(directory)
		if !registeredWorktree(r.Context(), project.Path, root) {
			projectFileError(w, os.ErrPermission)
			return
		}
	}
	workspace.Path, workspace.Available = directory, true
	workspace.Kind, workspace.Branch, workspace.Commit = workspaceGitState(directory)
	if kind == "workspace" {
		apiJSON(w, 200, workspace)
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
