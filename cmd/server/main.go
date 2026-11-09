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

	"spool/internal/diagnostics"
	chatmodel "spool/internal/model"
	"spool/internal/workspace"
)

// Set by production builds with -ldflags "-X main.production=1".
var production string

func main() {
	if diagnostics.RunCrashMonitor() {
		return
	}
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() (runErr error) {
	root := flag.String("data-dir", workspace.DefaultDataDir(production != "1"), "Data directory (database in db/spool.sqlite)")
	user := flag.String("add-user", "", "create a local user (password from SPOOL_NEW_PASSWORD)")
	nickname := flag.String("nickname", "", "display name for the new user")
	projectName := flag.String("add-project", "", "create a project and grant it to --grant username(s)")
	directory := flag.String("directory", "", "existing local project directory")
	grant := flag.String("grant", "", "comma-separated usernames for new project")
	port := flag.Int("port", 0, "persist a new Web port")
	webEnabled := flag.Bool("web", true, "persist local Web listener enablement (desktop, Tailscale and relay access are independent)")
	host := flag.String("host", "", "persist a listen IP (default 127.0.0.1)")
	publicURL := flag.String("public-url", "", "persist reverse proxy public origin (HTTPS for remote PWA)")
	tailnetEnabled := flag.Bool("tsnet", false, "persist embedded Tailscale HTTPS enablement (no system VPN or Funnel)")
	tailnetHostname := flag.String("tsnet-hostname", "spool-server", "embedded Tailscale node hostname (state persists in data-dir/tsnet)")
	tailnetPort := flag.Int("tsnet-port", 443, "HTTPS port on the embedded Tailscale node (does not bind a host TCP port)")
	relayEnabled := flag.Bool("relay", false, "persist outbound Spool relay and secure LAN listener enablement")
	relayWeb := flag.Bool("relay-web", false, "persist public Web/PWA access through the registered relay")
	relayURL := flag.String("relay-url", "", "public HTTPS origin of the independent Spool relay")
	relayLANPort := flag.Int("relay-lan-port", 28082, "secure LAN listener and discovery port")
	relayPair := flag.Bool("relay-pair", false, "start the service, print a five-minute invitation and approve one phone interactively")
	relayDevices := flag.Bool("relay-devices", false, "list paired devices locally and exit (service must be stopped)")
	relayRevoke := flag.String("relay-revoke", "", "revoke a paired device fingerprint locally and exit (service must be stopped)")
	relayRegister := flag.Bool("relay-register", false, "register with the relay using SPOOL_RELAY_REGISTRATION_TOKEN (empty for automatic registration), save the credential, and exit")
	relayName := flag.String("relay-name", "", "optional server name used with -relay-register")
	systemProxy := flag.Bool("system-proxy", false, "persist use of system HTTP/HTTPS proxy settings for Spool (macOS)")
	proxyEnabled := flag.Bool("proxy-enabled", true, "persist whether Spool uses outbound proxies (false forces direct connections)")
	proxyURL := flag.String("proxy-url", "", "persist a custom outbound proxy URL (used when proxies are enabled and system proxy is off; blank inherits environment)")
	noProxy := flag.String("no-proxy", "", "persist comma-separated proxy bypass hosts (blank inherits NO_PROXY)")
	historyCache := flag.Int("history-cache-mib", 0, "persist shared history cache target in MiB (default 64)")
	installVoxl := flag.Bool("install-voxl", false, "copy or build voxl locally in data-dir/voxl, then exit (macOS: Homebrew; Linux: system build libraries; Windows: MSVC, LLVM, esbuild and vcpkg)")
	voxlBinary := flag.String("voxl-binary", "", "existing local voxl executable to copy with -install-voxl (defaults to ~/.cargo/bin/voxl or PATH)")
	remoteDesktop := flag.Bool("remote-desktop", false, "persist local voxl enablement; no remote installation/configuration API")
	remoteUser := flag.String("remote-desktop-user", "", "set remote desktop authorization for a local username, then exit (use -remote-desktop=true/false)")
	issuesProject := flag.String("issues-project", "", "configure issue automation for an existing project ID (service must be stopped)")
	listProjects := flag.Bool("list-projects", false, "list local project IDs/names as JSON while service is stopped")
	issuesEnabled := flag.Bool("issues-enabled", true, "enable issue automation; false pauses future checks")
	issuesRepo := flag.String("issues-repo", "", "GitHub owner/repo for issue automation")
	issuesInterval := flag.Int("issues-interval", 60, "issue check interval in minutes (1-10080)")
	var issueLabels []string
	flag.Func("issues-label", "eligible issue label (repeat once per label; default: no filter, one task per project)", func(label string) error {
		issueLabels = append(issueLabels, label)
		return nil
	})
	issuesAgent := flag.String("issues-agent", "codex", "issue automation agent: codex or claude")
	issuesOwner := flag.String("issues-owner", "", "project member username owning each new automation chat")
	issuesAssignees := flag.String("issues-assignees", "", "comma-separated project member usernames allowed to control task chats")
	issuesModel := flag.String("issues-model", "", "issue automation model (blank uses native default)")
	issuesEffort := flag.String("issues-effort", "", "issue automation reasoning level")
	issuesPermission := flag.String("issues-permission", "", "native permission mode for issue automation")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logs, err := diagnostics.Start(*root, "server", "", production != "1")
	if err != nil {
		log.Printf("event=file_logging_unavailable error=%q", err)
	}
	defer func() {
		if runErr != nil {
			log.Printf("event=server_failed error=%q", runErr)
		} else {
			log.Print("event=server_exited")
		}
		if logs != nil {
			_ = logs.Close()
		}
	}()
	service, err := workspace.OpenService(*root)
	if err != nil {
		return err
	}
	defer func() {
		if err := service.Close(); err != nil {
			log.Printf("event=service_shutdown_failed error=%q", err)
			if runErr == nil {
				runErr = err
			}
		}
	}()
	if *installVoxl {
		return service.InstallVoxl(ctx, *voxlBinary)
	}
	if *remoteUser != "" {
		users, e := service.Store.Users()
		if e != nil {
			return e
		}
		for _, u := range users {
			if strings.EqualFold(u.Username, *remoteUser) {
				_, e = service.Store.SaveUser(workspace.UserInput{ID: u.ID, Username: u.Username, Nickname: u.Nickname, Disabled: u.Disabled, RemoteDesktop: *remoteDesktop})
				return e
			}
		}
		return fmt.Errorf("unknown username %q", *remoteUser)
	}
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
					p.Issues = workspace.IssuesAutomation{Enabled: true, IntervalMinutes: *issuesInterval, Labels: issueLabels, Repository: *issuesRepo, UserID: owner, AssigneeIDs: assignees, Backend: chatmodel.Backend(*issuesAgent), Model: *issuesModel, Effort: *issuesEffort, Permission: *issuesPermission}
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
		_, err = service.Store.SaveUser(workspace.UserInput{Username: *user, Nickname: name, Password: os.Getenv("SPOOL_NEW_PASSWORD"), RemoteDesktop: *remoteDesktop})
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
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "web":
			settings.WebEnabled = *webEnabled
		case "relay":
			settings.RelayEnabled = *relayEnabled
		case "relay-web":
			settings.RelayWebEnabled = *relayWeb
		case "relay-url":
			settings.RelayURL = *relayURL
		case "relay-lan-port":
			settings.RelayLANPort = *relayLANPort
		case "public-url":
			settings.PublicURL = *publicURL
		case "tsnet":
			settings.TSNetEnabled = *tailnetEnabled
		case "tsnet-hostname":
			settings.TSNetHostname = *tailnetHostname
		case "tsnet-port":
			settings.TSNetPort = *tailnetPort
		case "system-proxy":
			settings.UseSystemProxy = *systemProxy
		case "proxy-enabled":
			settings.ProxyEnabled = *proxyEnabled
		case "proxy-url":
			settings.ProxyURL = *proxyURL
		case "no-proxy":
			settings.NoProxy = *noProxy
		case "history-cache-mib":
			settings.HistoryCacheMiB = *historyCache
		case "remote-desktop":
			settings.RemoteDesktopEnabled = *remoteDesktop
		}
	})
	if err = settings.Validate(); err != nil {
		return err
	}
	wantRelayEnabled := settings.RelayEnabled
	if *relayRegister {
		settings.RelayEnabled = false
	}
	if err = service.SaveSettings(settings); err != nil {
		return err
	}
	if *relayRegister {
		if err = service.RegisterRelayNamed(ctx, settings.RelayURL, os.Getenv("SPOOL_RELAY_REGISTRATION_TOKEN"), *relayName); err != nil {
			return err
		}
		settings.RelayEnabled = wantRelayEnabled
		if err = service.SaveSettings(settings); err != nil {
			return err
		}
		if !*relayPair {
			return nil
		}
	}
	if *relayDevices {
		devices, err := service.RelayDevices()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(devices.Devices)
	}
	if *relayRevoke != "" {
		return service.RevokeRelayDevice(*relayRevoke)
	}
	if err = service.Start(); err != nil {
		return err
	}
	if *relayPair {
		if err := approveRelayPhone(ctx, service); err != nil {
			return err
		}
		log.Print("Phone paired. Service remains running until interrupted.")
	}
	if settings.TSNetEnabled {
		log.Print("Embedded Tailscale is starting; follow its login URL and approve the node if required")
	}
	log.Printf("Spool Web: %s", service.WebURL())
	log.Printf("SQLite: %s", service.Store.DatabasePath())
	<-ctx.Done()
	log.Print("Stopping service and saving active turns")
	return nil
}
