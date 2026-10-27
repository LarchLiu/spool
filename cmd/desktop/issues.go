package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	chatmodel "spool/internal/model"
)

func (p *panel) projectIssuesView(c *ui.Context) {
	v := &p.project.Issues
	ui.Text(c, p.tr("Scheduled GitHub issues")).Bold()
	ui.Checkbox(c, &v.Enabled, p.tr("Enable issue automation"))
	if !v.Enabled {
		return
	}
	p.field(c, "GitHub repository (owner/repo)", &v.Repository)
	if p.issueInterval == "" {
		p.issueInterval = "60"
	}
	p.field(c, "Run interval (minutes, 1–10080)", &p.issueInterval)
	agents := []string{"Codex", "Claude Code"}
	selected := v.Backend.Label()
	ui.Select(c, &selected, agents).Label(p.tr("Automation agent"))
	previous := v.Backend
	if selected == "Claude Code" {
		v.Backend = chatmodel.BackendClaude
	} else {
		v.Backend = chatmodel.BackendCodex
	}
	if previous != v.Backend {
		v.Permission = ""
		v.Model = ""
		v.Effort = ""
	}
	ui.Text(c, p.tr("Task assignees (all can control task chats)")).Bold()
	current := v.Controllers()
	assignees := []string{}
	for _, u := range p.users {
		if u.Disabled || !p.members[u.ID] {
			continue
		}
		checked := false
		for _, id := range current {
			checked = checked || id == u.ID
		}
		c.Key("issue-assignee-" + u.ID)
		ui.Checkbox(c, &checked, u.Nickname+" · "+u.Username)
		if checked {
			assignees = append(assignees, u.ID)
		}
	}
	v.AssigneeIDs = assignees
	found := false
	for _, id := range assignees {
		found = found || id == v.UserID
	}
	if !found {
		v.UserID = ""
		if len(assignees) > 0 {
			v.UserID = assignees[0]
		}
	}
	ui.Text(c, p.tr("Assignees can send messages, stop responses and answer approvals. Changes also apply to existing task chats; normal chats stay owner-only.")).Wrap().FontSize(12)
	p.field(c, "Automation model (blank = agent default)", &v.Model)
	p.field(c, "Automation reasoning level (blank = default)", &v.Effort)
	modes := []string{"Default", "ask", "auto"}
	if v.Backend == chatmodel.BackendClaude {
		modes = []string{"Default", "manual", "auto", "acceptEdits", "plan", "dontAsk", "bypassPermissions"}
	}
	selected = v.Permission
	if selected == "" {
		selected = "Default"
	}
	ui.Select(c, &selected, modes).Label(p.tr("Automation permissions"))
	v.Permission = selected
	if selected == "Default" {
		v.Permission = ""
	}
	ui.Text(c, p.tr("Each actionable run starts a new chat. Go synchronizes and claims issues; the agent fixes one issue against dev. No catch-up runs while stopped.")).Wrap().FontSize(12)
	ui.Text(c, p.tr("Requires gh login and a matching origin. Only exact web/server/app labels are eligible. Shared local ledger: main checkout docs/issues.md.")).Wrap().FontSize(12)
	status := *v
	for _, project := range p.projects {
		if project.ID == p.project.ID {
			status = project.Issues
			break
		}
	}
	if status.NextRunAt > 0 {
		ui.Text(c, p.tr("Next issue check: {time}", "time", time.UnixMilli(status.NextRunAt).Format("2006-01-02 15:04:05"))).FontSize(12)
	}
	if status.LastChatID != "" {
		ui.Text(c, p.tr("Last automation chat: {id}", "id", status.LastChatID)).Wrap().FontSize(12)
	}
	if status.LastError != "" {
		ui.Text(c, status.LastError).Wrap().FontSize(12)
	}
}
