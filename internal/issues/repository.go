package issues

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type Checkout struct {
	Root       string `json:"root"`
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Status     string `json:"status"`
	Worktrees  string `json:"worktrees"`
}

// DetectRepository only reads local Git metadata; it never contacts the remote.
func DetectRepository(ctx context.Context, run Command, directory string) (string, error) {
	if strings.TrimSpace(directory) == "" {
		return "", fmt.Errorf("project directory is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	get := func(args ...string) (string, error) {
		b, e := run(ctx, directory, "git", args...)
		return strings.TrimSpace(string(b)), e
	}
	root, err := get("rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("project must point to a Git checkout root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil || absolute != root {
		return "", fmt.Errorf("project must point to a Git checkout root")
	}
	remote, err := get("remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("project requires a GitHub.com origin remote: %w", err)
	}
	remote = strings.TrimSuffix(strings.TrimSuffix(remote, "/"), ".git")
	for _, prefix := range []string{"git@github.com:", "https://github.com/", "ssh://git@github.com/"} {
		if repo, ok := strings.CutPrefix(remote, prefix); ok && ValidRepository(repo) {
			return repo, nil
		}
	}
	return "", fmt.Errorf("project origin must be a GitHub.com repository")
}

func Inspect(ctx context.Context, run Command, directory, repo string) (Checkout, error) {
	var checkout Checkout
	repository, err := DetectRepository(ctx, run, directory)
	if err != nil {
		return checkout, err
	}
	if repo != "" && !strings.EqualFold(repository, repo) {
		return checkout, fmt.Errorf("origin does not match configured GitHub repository %s", repo)
	}
	checkout.Repository = repository
	get := func(args ...string) (string, error) {
		b, e := run(ctx, directory, "git", args...)
		return strings.TrimSpace(string(b)), e
	}
	worktrees, err := get("worktree", "list", "--porcelain")
	if err != nil {
		return checkout, err
	}
	first, _, _ := strings.Cut(worktrees, "\n")
	if !strings.HasPrefix(first, "worktree ") {
		return checkout, fmt.Errorf("cannot identify main checkout")
	}
	checkout.Root, err = filepath.EvalSymlinks(strings.TrimPrefix(first, "worktree "))
	if err != nil {
		return checkout, err
	}
	checkout.Worktrees = worktrees
	checkout.Branch, err = get("symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		checkout.Branch = "(detached)"
	}
	checkout.Status, err = get("status", "--porcelain=v1", "--untracked-files=all")
	return checkout, err
}
