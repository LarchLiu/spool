package main

import (
	"context"
	"errors"

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
		return "GitHub CLI (gh) was not found. Install it and ensure it is on the application's PATH, then recheck."
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
	p.github.refresh(issues.Run, p.window.Update)
}

func (p *panel) validateIssueEnablement(project workspace.Project) error {
	if !project.Issues.Enabled || p.github.ready() {
		return nil
	}
	// A failed check must not prevent editing or disabling an existing schedule.
	for _, saved := range p.projects {
		if saved.ID == project.ID && saved.Issues.Enabled {
			return nil
		}
	}
	return errors.New("Issue automation cannot be enabled until the GitHub CLI check succeeds.")
}
