package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"text/tabwriter"

	"spool"
	"spool/internal/diagnostics"
	"spool/internal/workspace"
)

type usageError struct{ message string }

func (e *usageError) Error() string          { return e.message }
func usage(format string, args ...any) error { return &usageError{fmt.Sprintf(format, args...)} }

type command struct {
	path, summary, details string
	flags                  *flag.FlagSet
	children               []*command
	run                    func(context.Context, *workspace.Service) error
	validate               func() error
}

func newCommand(path, summary, details string) *command {
	c := &command{path: path, summary: summary, details: details}
	c.flags = flag.NewFlagSet(path, flag.ContinueOnError)
	return c
}

func (c *command) add(name, summary, details string) *command {
	n := newCommand(c.path+" "+name, summary, details)
	c.children = append(c.children, n)
	return n
}

func (c *command) help(w io.Writer) {
	fmt.Fprintf(w, "%s\n\nUsage: %s", c.summary, c.path)
	if len(c.children) > 0 {
		fmt.Fprint(w, " <command>")
	}
	fmt.Fprintln(w, " [options]")
	if c.details != "" {
		fmt.Fprintf(w, "\n%s\n", c.details)
	}
	if len(c.children) > 0 {
		fmt.Fprintln(w, "\nCommands:")
		t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, child := range c.children {
			fmt.Fprintf(t, "  %s\t%s\n", strings.TrimPrefix(child.path, c.path+" "), child.summary)
		}
		_ = t.Flush()
	}
	fmt.Fprintln(w, "\nGlobal options (accepted before or after the command):\n  --data-dir PATH  Data directory; overrides SPOOL_HOME\n  -h, --help       Show help without opening the database")
	if c.flags.NFlag() > 0 || c.flagsHasOptions() {
		fmt.Fprintln(w, "\nOptions (boolean values use --name=true or --name=false):")
		c.flags.SetOutput(w)
		c.flags.PrintDefaults()
	}
	fmt.Fprintf(w, "\nUse '%s --help' for options, or 'spool-server help <command> [subcommand]'.\n", c.path)
}

func (c *command) flagsHasOptions() bool {
	found := false
	c.flags.VisitAll(func(*flag.Flag) { found = true })
	return found
}

func changed(c *command, name string) bool {
	found := false
	c.flags.Visit(func(f *flag.Flag) { found = found || f.Name == name })
	return found
}

func required(values ...string) error {
	for i := 0; i < len(values); i += 2 {
		if strings.TrimSpace(values[i+1]) == "" {
			return usage("--%s is required", values[i])
		}
	}
	return nil
}

// extractDataDir makes the one persistent global option usable at every depth.
// Stop at '--' so option-looking values are never interpreted as globals.
func extractDataDir(args []string) (string, []string, error) {
	root := workspace.DefaultDataDir(production != "1")
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if a == "--data-dir" || a == "-data-dir" {
			i++
			if i == len(args) {
				return "", nil, usage("--data-dir requires a path")
			}
			root = args[i]
		} else if strings.HasPrefix(a, "--data-dir=") || strings.HasPrefix(a, "-data-dir=") {
			root = strings.SplitN(a, "=", 2)[1]
		} else {
			rest = append(rest, a)
		}
	}
	if strings.TrimSpace(root) == "" {
		return "", nil, usage("--data-dir must not be empty")
	}
	return root, rest, nil
}

func runCLI(ctx context.Context, args []string, out, errOut io.Writer) (runErr error) {
	root, args, err := extractDataDir(args)
	if err != nil {
		return err
	}
	cli := newCLI(out)
	if len(args) == 0 {
		cli.help(out)
		return nil
	}
	if args[0] == "--version" || args[0] == "-version" {
		args[0] = "version"
	}
	if args[0] == "legacy" {
		return runLegacy(ctx, append([]string{"-data-dir", root}, args[1:]...))
	}
	// Keep existing flat-flag invocations working; top-level help belongs to the new CLI.
	if strings.HasPrefix(args[0], "-") && args[0] != "--help" && args[0] != "-h" && args[0] != "-help" {
		return runLegacy(ctx, append([]string{"-data-dir", root}, args...))
	}
	help := args[0] == "help"
	if help {
		args = args[1:]
	}
	c := cli
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		var next *command
		for _, child := range c.children {
			if child.path == c.path+" "+args[0] {
				next = child
				break
			}
		}
		if next == nil {
			return usage("unknown command %q; use '%s --help'", args[0], c.path)
		}
		c, args = next, args[1:]
	}
	if help {
		if len(args) != 0 {
			return usage("help accepts command names only")
		}
		c.help(out)
		return nil
	}
	c.flags.SetOutput(errOut)
	c.flags.Usage = func() { c.help(out) }
	if err := c.flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return usage("%v; use '%s --help'", err, c.path)
	}
	if c.flags.NArg() != 0 {
		return usage("unexpected arguments: %s; use '%s --help'", strings.Join(c.flags.Args(), " "), c.path)
	}
	if c.run == nil {
		c.help(out)
		return nil
	}
	if c.validate != nil {
		if err := c.validate(); err != nil {
			return err
		}
	}
	if c.path == "spool-server version" {
		return c.run(ctx, nil)
	}
	open := workspace.OpenAdministration
	foreground := c.path == "spool-server serve" || c.path == "spool-server relay pair" || c.path == "spool-server tailscale login"
	if foreground {
		open = workspace.OpenService
	}
	service, err := open(root)
	if err != nil {
		return fmt.Errorf("open local workspace (stop any Desktop/server using this data directory first): %w", err)
	}
	if foreground {
		logs, err := diagnostics.Start(service.Store.Root(), "server", spool.Version(), production != "1")
		if err != nil {
			log.Printf("event=file_logging_unavailable error=%q", err)
		}
		if logs != nil {
			defer logs.Close()
		}
	}
	defer func() { runErr = errors.Join(runErr, service.Close()) }()
	return c.run(ctx, service)
}

func writeJSON(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}

func newCLI(out io.Writer) *command {
	c := newCommand("spool-server", "Spool headless server and local administration", "Administration commands require the service to be stopped and run locally as the service OS user.\nResults are JSON; passwords and registration keys are read from environment variables.\nServe runs in the foreground; use systemd to supervise it. No GUI or voxl is required.\nData defaults to ~/.spool in production and ~/.spool-dev in development (SPOOL_HOME overrides).\nExisting flat flags remain available through 'spool-server legacy --help'.\n\nExample:\n  SPOOL_NEW_PASSWORD='a-long-unique-password' spool-server user add --username alice\n  spool-server project add --name Example --directory /srv/example --members alice\n  spool-server serve --host 127.0.0.1 --port 8787")
	v := c.add("version", "Print the embedded release version", "")
	v.run = func(context.Context, *workspace.Service) error {
		_, err := fmt.Fprintln(out, spool.Version())
		return err
	}
	addServe(c, out)
	addConfig(c, out)
	addUsers(c, out)
	addProjects(c, out)
	addAgents(c, out)
	addIssues(c, out)
	addGitHub(c, out)
	addRelay(c, out)
	addTailscale(c, out)
	return c
}

func envRequired(name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", usage("set %s in the environment", name)
	}
	return v, nil
}
