package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"spool/internal/workspace"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	root := flag.String("data-dir", workspace.DefaultDataDir(), "SQLite data directory")
	user := flag.String("add-user", "", "create a local user (password from SPOOL_NEW_PASSWORD)")
	nickname := flag.String("nickname", "", "display name for the new user")
	projectName := flag.String("add-project", "", "create a project and grant it to --grant username(s)")
	directory := flag.String("directory", "", "existing local project directory")
	grant := flag.String("grant", "", "comma-separated usernames for new project")
	port := flag.Int("port", 0, "persist a new Web port")
	host := flag.String("host", "", "persist a listen IP (default 127.0.0.1)")
	publicURL := flag.String("public-url", "", "persist reverse proxy public origin (HTTPS for remote PWA)")
	flag.Parse()
	service, err := workspace.OpenService(*root)
	if err != nil {
		return err
	}
	defer service.Close()
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
	if err = service.Store.SaveSettings(settings); err != nil {
		return err
	}
	if err = service.Start(); err != nil {
		return err
	}
	log.Printf("Spool Web: %s", service.WebURL())
	log.Printf("SQLite: %s/spool.sqlite", service.Store.Root())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
	log.Print("Stopping service and saving active turns")
	return nil
}
