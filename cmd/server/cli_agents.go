package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"spool/internal/issues"
	chatmodel "spool/internal/model"
	"spool/internal/workspace"
)

type localAgent struct {
	workspace.AgentInfo
	Executable   string `json:"executable"`
	ResolvedPath string `json:"resolved_path"`
}

func printAgents(out io.Writer, s *workspace.Store) error {
	all, err := s.Agents()
	if err != nil {
		return err
	}
	v := []localAgent{}
	for _, a := range all {
		v = append(v, localAgent{a, a.Executable, a.ResolvedPath})
	}
	return writeJSON(out, v)
}

func addAgents(root *command, out io.Writer) {
	c := root.add("agent", "Inspect/configure Codex and Claude CLI discovery", "Install and authenticate agent CLIs as the same OS user running Spool.\nPath precedence: saved --path, SPOOL_CODEX_BIN/SPOOL_CLAUDE_BIN, PATH, common locations.")
	list := c.add("list", "Print saved agent paths and discovery results", "Discovery results may be cached; use 'agent refresh' to probe now.")
	list.run = func(_ context.Context, s *workspace.Service) error { return printAgents(out, s.Store) }
	set := c.add("set", "Update an agent executable path or enablement", "Omitted fields keep their saved values. --path='' clears the saved override.\nExample: spool-server agent set --backend codex --path /usr/local/bin/codex --enabled=true")
	backend := set.flags.String("backend", "", "Agent: codex or claude (required)")
	path := set.flags.String("path", "", "Executable path/name; empty restores automatic discovery")
	enabled := set.flags.Bool("enabled", true, "Enable this agent")
	set.validate = func() error {
		if !chatmodel.Backend(*backend).Valid() {
			return usage("--backend must be codex or claude")
		}
		if !changed(set, "path") && !changed(set, "enabled") {
			return usage("supply --path or --enabled")
		}
		return nil
	}
	set.run = func(_ context.Context, s *workspace.Service) error {
		all, err := s.Store.Agents()
		if err != nil {
			return err
		}
		for _, a := range all {
			if string(a.Backend) != *backend {
				continue
			}
			if changed(set, "path") {
				a.Executable = *path
			}
			if changed(set, "enabled") {
				a.Enabled = *enabled
			}
			if err := s.Store.ConfigureAgent(a.Backend, a.Enabled, a.Executable); err != nil {
				return err
			}
		}
		return printAgents(out, s.Store)
	}
	refresh := c.add("refresh", "Probe installed agents and refresh model catalogs", "Invokes local agent CLIs without sending a model prompt. Results include discovery errors.")
	timeout := refresh.flags.Duration("timeout", time.Minute, "Maximum discovery duration (e.g. 30s or 2m)")
	refresh.validate = func() error {
		if *timeout <= 0 {
			return usage("--timeout must be positive")
		}
		return nil
	}
	refresh.run = func(ctx context.Context, s *workspace.Service) error {
		ctx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		if err := s.Registry.Refresh(ctx); err != nil {
			return err
		}
		return printAgents(out, s.Store)
	}
}

func addGitHub(root *command, out io.Writer) {
	c := root.add("github", "Configure/check the GitHub CLI used by Issue automation", "Authenticate gh as the service OS user before enabling automation.")
	show := c.add("show", "Print the saved GitHub executable override", "")
	show.run = func(_ context.Context, s *workspace.Service) error {
		v, err := s.Store.GitHubCLIPath()
		if err != nil {
			return err
		}
		return writeJSON(out, map[string]string{"path": v})
	}
	set := c.add("set", "Set or clear the GitHub CLI executable override", "Example: spool-server github set --path /usr/local/bin/gh")
	path := set.flags.String("path", "", "Absolute executable path; empty restores automatic discovery (required)")
	set.validate = func() error {
		if !changed(set, "path") {
			return usage("--path is required (use --path='' to clear)")
		}
		return nil
	}
	set.run = func(_ context.Context, s *workspace.Service) error {
		if err := s.Store.SaveGitHubCLIPath(*path); err != nil {
			return err
		}
		return writeJSON(out, map[string]string{"path": *path})
	}
	check := c.add("check", "Check gh installation and authentication", "Prints diagnostic JSON and exits nonzero unless GitHub CLI is ready.")
	check.run = func(ctx context.Context, s *workspace.Service) error {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		v := issues.CheckCLI(ctx, s.Store.RunIssueCommand)
		if err := writeJSON(out, v); err != nil {
			return err
		}
		if v.State != issues.CLIReady {
			return fmt.Errorf("GitHub CLI is not ready: %s", v.State)
		}
		return nil
	}
}
