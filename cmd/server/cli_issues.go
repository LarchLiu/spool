package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"spool/internal/issues"
	chatmodel "spool/internal/model"
	"spool/internal/workspace"
)

func addIssues(root *command, out io.Writer) {
	c := root.add("issues", "Inspect/configure scheduled GitHub Issue automation", "The repository is derived from the project's Git origin. A new enabled schedule\nrequires a working authenticated gh CLI. Administration requires the service stopped.")
	show := c.add("show", "Print schedule settings and last-run diagnostics", "")
	ref := show.flags.String("project", "", "Project name or ID (required)")
	show.validate = func() error { return required("project", *ref) }
	show.run = func(_ context.Context, s *workspace.Service) error {
		p, err := findProject(s.Store, *ref)
		if err != nil {
			return err
		}
		return writeJSON(out, struct {
			workspace.IssuesAutomation
			NextRun   int64  `json:"next_run_at"`
			LastRun   int64  `json:"last_run_at"`
			LastError string `json:"last_error"`
			LastChat  string `json:"last_chat_id"`
		}{p.Issues, p.Issues.NextRunAt, p.Issues.LastRunAt, p.Issues.LastError, p.Issues.LastChatID})
	}
	set := c.add("set", "Update selected schedule fields", "Only supplied options change. --enabled=false pauses future checks.\nLabels are comma-separated; --labels='' removes the filter. Owner and assignees\nmust be active project members; owner must be among assignees. Empty assignees\nfall back to the owner. Model/effort/permission accept empty values for native defaults.\nExample: spool-server issues set --project Example --owner alice --enabled=true")
	project := set.flags.String("project", "", "Project name or ID (required)")
	enabled := set.flags.Bool("enabled", false, "Enable scheduled checks")
	interval := set.flags.Int("interval", 60, "Check interval in minutes (1-10080)")
	labels := set.flags.String("labels", "", "Comma-separated eligible issue labels; empty means no filter")
	var exactLabels []string
	set.flags.Func("label", "Exact eligible label; repeat for multiple labels (supports commas in a label); replaces saved labels", func(v string) error {
		exactLabels = append(exactLabels, v)
		return nil
	})
	owner := set.flags.String("owner", "", "Username/ID owning automation chats")
	assignees := set.flags.String("assignees", "", "Comma-separated controller usernames/IDs; empty uses owner")
	backend := set.flags.String("backend", "codex", "Agent: codex or claude")
	model := set.flags.String("model", "", "Native model identifier")
	effort := set.flags.String("effort", "", "Native reasoning effort")
	permission := set.flags.String("permission", "", "Native permission mode")
	set.validate = func() error {
		if err := required("project", *project); err != nil {
			return err
		}
		if set.flags.NFlag() < 2 {
			return usage("supply at least one schedule field")
		}
		if changed(set, "label") && changed(set, "labels") {
			return usage("use either --label or --labels")
		}
		if !chatmodel.Backend(*backend).Valid() {
			return usage("--backend must be codex or claude")
		}
		if *interval < 1 || *interval > 10080 {
			return usage("--interval must be between 1 and 10080")
		}
		return nil
	}
	set.run = func(ctx context.Context, s *workspace.Service) error {
		p, err := findProject(s.Store, *project)
		if err != nil {
			return err
		}
		wasEnabled := p.Issues.Enabled
		v := &p.Issues
		if changed(set, "enabled") {
			v.Enabled = *enabled
		}
		if changed(set, "interval") {
			v.IntervalMinutes = *interval
		}
		if changed(set, "labels") {
			v.Labels = nil
			if strings.TrimSpace(*labels) != "" {
				v.Labels = strings.Split(*labels, ",")
			}
		}
		if changed(set, "label") {
			v.Labels = exactLabels
		}
		if changed(set, "owner") {
			u, e := findUser(s.Store, *owner)
			if e != nil {
				return e
			}
			if u.Disabled {
				return fmt.Errorf("owner is disabled")
			}
			v.UserID = u.ID
		}
		if changed(set, "assignees") {
			v.AssigneeIDs, err = userIDs(s.Store, *assignees, true)
			if err != nil {
				return err
			}
		}
		if changed(set, "backend") {
			v.Backend = chatmodel.Backend(*backend)
		}
		if changed(set, "model") {
			v.Model = *model
		}
		if changed(set, "effort") {
			v.Effort = *effort
		}
		if changed(set, "permission") {
			v.Permission = *permission
		}
		if v.Enabled {
			for _, id := range v.Controllers() {
				u, err := findUser(s.Store, id)
				if err != nil {
					return err
				}
				if u.Disabled {
					return fmt.Errorf("automation controller %q is disabled", u.Username)
				}
			}
		}
		if v.Enabled && !wasEnabled {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if status := issues.CheckCLI(ctx, s.Store.RunIssueCommand); status.State != issues.CLIReady {
				return fmt.Errorf("GitHub CLI must be ready before enabling automation (run 'github check')")
			}
		}
		p, err = s.Store.SaveProject(p)
		if err != nil {
			return err
		}
		return writeJSON(out, p.Issues)
	}
}
