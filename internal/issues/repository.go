package issues

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

type Checkout struct {
	Root      string `json:"root"`
	Branch    string `json:"branch"`
	Status    string `json:"status"`
	Worktrees string `json:"worktrees"`
}

func Inspect(ctx context.Context, run Command, directory, repo string) (Checkout, error) {
	var checkout Checkout
	get := func(args ...string) (string, error) {
		b, e := run(ctx, directory, "git", args...)
		return strings.TrimSpace(string(b)), e
	}
	root, err := get("rev-parse", "--show-toplevel")
	if err != nil {
		return checkout, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return checkout, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil || directory != root {
		return checkout, fmt.Errorf("project must point to a Git checkout root")
	}
	remote, err := get("remote", "get-url", "origin")
	if err != nil {
		return checkout, err
	}
	remote = strings.TrimSuffix(strings.TrimSuffix(remote, "/"), ".git")
	if remote != "git@github.com:"+repo && remote != "https://github.com/"+repo && remote != "ssh://git@github.com/"+repo {
		return checkout, fmt.Errorf("origin does not match configured GitHub repository %s", repo)
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
