package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	chatmodel "spool/internal/model"
	"spool/internal/workspace"
)

// Set by production builds with -ldflags "-X main.production=1".
var production string

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	root := flag.String("data-dir", workspace.DefaultDataDir(production != "1"), "Data directory (database in db/spool.sqlite)")
	user := flag.String("add-user", "", "create a local user (password from SPOOL_NEW_PASSWORD)")
	nickname := flag.String("nickname", "", "display name for the new user")
	projectName := flag.String("add-project", "", "create a project and grant it to --grant username(s)")
	directory := flag.String("directory", "", "existing local project directory")
	grant := flag.String("grant", "", "comma-separated usernames for new project")
	port := flag.Int("port", 0, "persist a new Web port")
	host := flag.String("host", "", "persist a listen IP (default 127.0.0.1)")
	publicURL := flag.String("public-url", "", "persist reverse proxy public origin (HTTPS for remote PWA)")
	historyCache := flag.Int("history-cache-mib", 0, "persist shared history cache target in MiB (default 64)")
	issuesProject := flag.String("issues-project", "", "configure issue automation for an existing project ID (service must be stopped)")
	listProjects := flag.Bool("list-projects", false, "list local project IDs/names as JSON while service is stopped")
	issuesEnabled := flag.Bool("issues-enabled", true, "enable issue automation; false pauses future checks")
	issuesRepo := flag.String("issues-repo", "", "GitHub owner/repo for issue automation")
	issuesInterval := flag.Int("issues-interval", 60, "issue check interval in minutes (1-10080)")
	issuesAgent := flag.String("issues-agent", "codex", "issue automation agent: codex or claude")
	issuesOwner := flag.String("issues-owner", "", "project member username owning each new automation chat")
	issuesAssignees := flag.String("issues-assignees", "", "comma-separated project member usernames allowed to control task chats")
	issuesModel := flag.String("issues-model", "", "issue automation model (blank uses native default)")
	issuesEffort := flag.String("issues-effort", "", "issue automation reasoning level")
	issuesPermission := flag.String("issues-permission", "", "native permission mode for issue automation")
	flag.Parse()
	service, err := workspace.OpenService(*root)
	if err != nil {
		return err
	}
	defer service.Close()
	if *listProjects {
		projects, e := service.Store.Projects()
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(projects)
	}
	if *issuesProject != "" {
		projects, e := service.Store.Projects()
		if e != nil {
			return e
		}
		for _, p := range projects {
			if p.ID == *issuesProject {
				if !*issuesEnabled {
					p.Issues.Enabled = false
				} else {
					users, e := service.Store.Users()
					if e != nil {
						return e
					}
					owner := ""
					assignees := []string{}
					for _, name := range strings.Split(*issuesAssignees, ",") {
						if strings.TrimSpace(name) == "" {
							continue
						}
						id := ""
						for _, u := range users {
							if !u.Disabled && strings.EqualFold(u.Username, strings.TrimSpace(name)) {
								id = u.ID
							}
						}
						if id == "" {
							return fmt.Errorf("unknown or disabled assignee %q", name)
						}
						assignees = append(assignees, id)
					}
					for _, u := range users {
						if !u.Disabled && strings.EqualFold(u.Username, *issuesOwner) {
							owner = u.ID
						}
					}
					if owner == "" && *issuesOwner == "" && len(assignees) > 0 {
						owner = assignees[0]
					}
					p.Issues = workspace.IssuesAutomation{Enabled: true, IntervalMinutes: *issuesInterval, Repository: *issuesRepo, UserID: owner, AssigneeIDs: assignees, Backend: chatmodel.Backend(*issuesAgent), Model: *issuesModel, Effort: *issuesEffort, Permission: *issuesPermission}
				}
				_, e = service.Store.SaveProject(p)
				if e == nil {
					log.Print("Issue automation configuration saved")
				}
				return e
			}
		}
		return fmt.Errorf("unknown project ID %q", *issuesProject)
	}
	if *user != "" {
		name := *nickname
		if name == "" {
			name = *user
		}
		_, err = service.Store.SaveUser(workspace.UserInput{Username: *user, Nickname: name, Password: os.Getenv("SPOOL_NEW_PASSWORD")})
		if err == nil {
			log.Print("User created")
		}
		return err
	}
	if *projectName != "" {
		users, e := service.Store.Users()
		if e != nil {
			return e
		}
		members := []string{}
		for _, name := range strings.Split(*grant, ",") {
			found := false
			for _, u := range users {
				if strings.EqualFold(strings.TrimSpace(name), u.Username) {
					members = append(members, u.ID)
					found = true
				}
			}
			if !found {
				return fmt.Errorf("unknown username %q", name)
			}
		}
		_, err = service.Store.SaveProject(workspace.Project{Name: *projectName, Path: *directory, Members: members})
		if err == nil {
			log.Print("Project created")
		}
		return err
	}
	settings, err := service.Store.Settings()
	if err != nil {
		return err
	}
	if *port != 0 {
		settings.Port = *port
	}
	if *host != "" {
		settings.Host = *host
	}
	if *publicURL != "" {
		settings.PublicURL = *publicURL
	}
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "history-cache-mib" {
			settings.HistoryCacheMiB = *historyCache
		}
	})
	if err = service.Store.SaveSettings(settings); err != nil {
		return err
	}
	if err = service.Start(); err != nil {
		return err
	}
	log.Printf("Spool Web: %s", service.WebURL())
	log.Printf("SQLite: %s", service.Store.DatabasePath())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
	log.Print("Stopping service and saving active turns")
	return nil
}
