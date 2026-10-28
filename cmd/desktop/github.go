package main

import (
	"context"
	"errors"
	"strings"

	"spool/internal/issues"
	"spool/internal/workspace"
)

type githubCLICheck struct {
	ctx     context.Context
	pending bool
	status  issues.CLIStatus
}

func (g *githubCLICheck) ready() bool {
	return !g.pending && g.status.State == issues.CLIReady
}

func (g *githubCLICheck) refresh(run issues.Command, update func(func())) {
	if g.pending || g.ctx.Err() != nil {
		return
	}
	g.pending = true
	go func() {
		status := issues.CheckCLI(g.ctx, run)
		if g.ctx.Err() != nil {
			return
		}
		update(func() {
			if g.ctx.Err() == nil {
				g.pending = false
				g.status = status
			}
		})
	}()
}

func (g *githubCLICheck) message() string {
	if g.pending || g.status.State == "" {
		return "Checking GitHub CLI installation and sign-in..."
	}
	switch g.status.State {
	case issues.CLIReady:
		return "GitHub CLI is ready (signed in as {user})."
	case issues.CLIMissing:
		return "GitHub CLI (gh) was not found. Check the configured executable path or install gh, then recheck."
	case issues.CLIUnavailable:
		return "GitHub CLI could not run. Check its installation and permissions, then recheck."
	case issues.CLILoginRequired:
		return "GitHub CLI is not signed in to github.com. Run gh auth login --hostname github.com, then recheck."
	case issues.CLIInvalidCredentials:
		return "GitHub rejected the credentials. Run gh auth login --hostname github.com, then recheck."
	case issues.CLINetworkUnavailable:
		return "GitHub could not be reached. Check your network or proxy, then recheck; signing in again may not help."
	default:
		return "GitHub CLI could not verify access. Run gh api --hostname github.com user to diagnose, then recheck."
	}
}

func (p *panel) refreshGitHubCLI() {
	p.github.refresh(p.service.Store.RunIssueCommand, p.window.Update)
}

func (p *panel) saveGitHubCLIPath(update func(func())) {
	if p.busy || p.github.pending || p.github.ctx.Err() != nil {
		return
	}
	path := strings.TrimSpace(p.githubPath)
	p.busy = true
	p.message = "Working…"
	go func() {
		err := p.service.Store.SaveGitHubCLIPath(path)
		if p.github.ctx.Err() != nil {
			return
		}
		update(func() {
			if p.github.ctx.Err() != nil {
				return
			}
			p.busy = false
			if err != nil {
				p.message = err.Error()
				return
			}
			p.githubPath = path
			p.message = "Saved"
			p.github.refresh(p.service.Store.RunIssueCommand, update)
		})
	}()
}

func (p *panel) validateIssueEnablement(project workspace.Project) error {
	if !project.Issues.Enabled {
		return nil
	}
	if !p.projectGit.ready(project.Path) {
		return errors.New("Issue automation requires a detected GitHub repository from the project Git origin.")
	}
	if p.github.ready() {
		return nil
	}
	// A failed CLI check must not prevent editing an existing schedule.
	for _, saved := range p.projects {
		if saved.ID == project.ID && saved.Issues.Enabled {
			return nil
		}
	}
	return errors.New("Issue automation cannot be enabled until the GitHub CLI check succeeds.")
}
