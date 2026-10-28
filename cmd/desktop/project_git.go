package main

import (
	"context"

	"spool/internal/issues"
)

type githubRepositoryCheck struct {
	directory  string
	repository string
	pending    bool
	generation uint64
	cancel     context.CancelFunc
}

func (g *githubRepositoryCheck) ready(directory string) bool {
	return directory != "" && g.directory == directory && !g.pending && g.repository != ""
}

func (g *githubRepositoryCheck) refresh(ctx context.Context, directory string, run issues.Command, update func(func())) {
	if ctx.Err() != nil {
		return
	}
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
	g.generation++
	generation := g.generation
	g.directory, g.repository, g.pending = directory, "", directory != ""
	if directory == "" {
		return
	}
	request, cancel := context.WithCancel(ctx)
	g.cancel = cancel
	go func() {
		defer cancel()
		repository, err := issues.DetectRepository(request, run, directory)
		if request.Err() != nil {
			return
		}
		update(func() {
			// A directory edit or recheck may supersede an already queued result.
			if ctx.Err() == nil && g.generation == generation {
				g.pending = false
				g.cancel = nil
				if err == nil {
					g.repository = repository
				}
			}
		})
	}()
}

func (g *githubRepositoryCheck) message(directory string) string {
	if directory == "" {
		return "Select a project directory to detect its GitHub repository."
	}
	if g.directory != directory || g.pending {
		return "Reading GitHub repository from project Git..."
	}
	if g.ready(directory) {
		return "GitHub repository (from origin): {repository}"
	}
	return "No GitHub repository found. Use a Git checkout root with a GitHub.com origin to enable issue automation."
}

func (p *panel) refreshProjectGit() {
	p.projectGit.refresh(p.github.ctx, p.project.Path, issues.Run, p.window.Update)
}
