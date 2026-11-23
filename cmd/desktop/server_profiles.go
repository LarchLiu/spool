package main

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"spool/android/transport/spoolnet"
	chatmodel "spool/internal/model"
	"spool/relay"
)

const localDesktopServer = "local"

type desktopServer struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ServerName string `json:"server_name"`
	Address    string `json:"address"`
	Enabled    bool   `json:"enabled"`
	Direct     string `json:"direct"`
	Tailscale  string `json:"tailscale"`
	Relay      string `json:"relay"`
	Mode       string `json:"mode"`
	Automatic  bool   `json:"automatic"`
	// A trusted sign-in owns its profile and cookie mount. The route may use
	// an existing device connection, but never that connection's login session.
	FederationSource  string `json:"federation_source,omitempty"`
	FederationTarget  string `json:"federation_target,omitempty"`
	FederationAccount string `json:"federation_account,omitempty"`
	ConnectionID      string `json:"connection_id,omitempty"`
}

type desktopServers struct {
	Entries      []desktopServer `json:"entries"`
	Selected     string          `json:"selected"`
	SidebarWidth int             `json:"sidebar_width,omitempty"`
}

func defaultDesktopServers() desktopServers {
	return desktopServers{Entries: []desktopServer{{ID: localDesktopServer, Enabled: true}}, Selected: localDesktopServer}
}

func (server desktopServer) displayName() string {
	if name := strings.TrimSpace(server.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(server.ServerName); name != "" {
		return name
	}
	return "Home"
}

// Addresses identify one service, including a public relay's stable mount.
// Credentials belong to that service's HTTPOnly session, never the profile.
func desktopServerAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	u, err := url.Parse(address)
	if err != nil || u.Opaque != "" || u.Hostname() == "" || u.User != nil ||
		(u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.Contains(address, "#") || u.RawPath != "" {
		return "", errors.New("Enter an HTTP or HTTPS server address without credentials, query or fragment.")
	}
	port, portErr := strconv.Atoi(u.Port())
	if u.Port() != "" && (portErr != nil || port < 1 || port > 65535) || strings.ContainsAny(u.Host, "\\\r\n\t ") {
		return "", errors.New("Enter an HTTP or HTTPS server address without credentials, query or fragment.")
	}
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/") + "/"
	if u.Path != "/" {
		id := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/web/"), "/")
		if len(id) != 52 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyz234567") != "" || u.Path != "/web/"+id+"/" {
			return "", errors.New("Use a server origin or a public relay server URL.")
		}
	}
	return u.String(), nil
}

func readDesktopServers(path string) (desktopServers, error) {
	profiles := defaultDesktopServers()
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return profiles, nil
	}
	if err != nil {
		return profiles, err
	}
	var saved desktopServers
	if err := json.Unmarshal(body, &saved); err != nil {
		return profiles, fmt.Errorf("read desktop servers: %w", err)
	}
	seen := map[string]bool{localDesktopServer: true}
	for _, server := range saved.Entries {
		if server.ID == localDesktopServer {
			continue
		}
		if !chatmodel.ValidID(server.ID) || seen[server.ID] {
			return profiles, errors.New("invalid desktop server ID")
		}
		server, err = validateDesktopServer(server)
		if err != nil {
			return defaultDesktopServers(), err
		}
		seen[server.ID] = true
		profiles.Entries = append(profiles.Entries, server)
	}
	if server, ok := profiles.find(saved.Selected); ok {
		profiles.Selected = server.ID
	}
	if saved.SidebarWidth >= 220 && saved.SidebarWidth <= 480 {
		profiles.SidebarWidth = saved.SidebarWidth
	}
	return profiles, nil
}

func validateDesktopServer(server desktopServer) (desktopServer, error) {
	if server.FederationTarget != "" || server.FederationSource != "" || server.FederationAccount != "" || server.ConnectionID != "" {
		if !validDesktopFederationID(server.FederationTarget) ||
			(server.FederationSource != localDesktopServer && !chatmodel.ValidID(server.FederationSource)) ||
			len(server.FederationAccount) != 43 || strings.Trim(server.FederationAccount, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" ||
			(server.ConnectionID != "" && server.ConnectionID != localDesktopServer && !chatmodel.ValidID(server.ConnectionID)) {
			return server, errors.New("Invalid trusted server profile.")
		}
	}
	server.Name = strings.TrimSpace(server.Name)
	if utf8.RuneCountInString(server.Name) > 80 {
		return server, errors.New("Use a display name of up to 80 characters.")
	}
	if server.Mode == "" && server.Direct == "" && server.Tailscale == "" && server.Relay == "" && server.Address != "" {
		server.Direct = server.Address
	}
	var err error
	for _, address := range []*string{&server.Direct, &server.Tailscale} {
		*address = strings.TrimSpace(*address)
		if *address != "" {
			*address, err = desktopServerAddress(*address)
			if err != nil {
				return server, err
			}
		}
	}
	if server.Tailscale != "" {
		origin, e := spoolnet.HTTPSOrigin(server.Tailscale)
		if e != nil {
			return server, e
		}
		server.Tailscale = origin + "/"
	}
	server.Relay = strings.TrimSpace(server.Relay)
	if server.Relay != "" {
		p, e := relay.ParsePairing(server.Relay)
		if e != nil {
			return server, e
		}
		server.Relay, err = p.Encode()
		if err != nil {
			return server, err
		}
	}
	if server.Mode == "" {
		server.Mode = "direct"
	}
	if server.Mode != "direct" && server.Mode != "tailscale" && server.Mode != "relay" {
		return server, errors.New("Invalid connection mode.")
	}
	server.Address = server.endpoint(server.Mode)
	if server.Enabled && server.Address == "" {
		return server, errors.New("Configure the selected connection before connecting.")
	}
	return server, nil
}

func (server desktopServer) endpoint(mode string) string {
	switch mode {
	case "direct":
		if server.Mode == "" && server.Direct == "" && server.Tailscale == "" && server.Relay == "" {
			return server.Address
		}
		return server.Direct
	case "tailscale":
		return server.Tailscale
	case "relay":
		if p, err := relay.ParsePairing(server.Relay); err == nil {
			return relay.Origin(p.ID()) + "/"
		}
	}
	return ""
}

func (server desktopServer) connection(mode string) desktopServer {
	server.Mode, server.Address = mode, server.endpoint(mode)
	return server
}

func (profiles desktopServers) find(id string) (desktopServer, bool) {
	for _, server := range profiles.Entries {
		if server.ID == id {
			return server, true
		}
	}
	return desktopServer{}, false
}

func (profiles desktopServers) save(server desktopServer) (desktopServers, error) {
	if server.ID == localDesktopServer {
		return profiles, errors.New("The local server is managed in Service settings.")
	}
	server, err := validateDesktopServer(server)
	if err != nil {
		return profiles, err
	}
	next := profiles
	next.Entries = append([]desktopServer(nil), profiles.Entries...)
	if server.ID == "" {
		server.ServerName = ""
		server.ID = chatmodel.NewID()
		next.Entries = append(next.Entries, server)
	} else {
		found := false
		for i, old := range next.Entries {
			if old.ID == server.ID {
				// The connection editor does not edit the sign-in identity.
				if old.FederationTarget != "" && server.FederationTarget == "" {
					server.FederationSource, server.FederationTarget = old.FederationSource, old.FederationTarget
					server.FederationAccount, server.ConnectionID = old.FederationAccount, old.ConnectionID
				}
				// Metadata comes from the server, independently of an editor draft.
				server.ServerName = old.ServerName
				if old.Address != server.Address && old.Mode == server.Mode {
					server.ServerName = ""
				}
				next.Entries[i], found = server, true
			}
		}
		if !found {
			return profiles, errors.New("Server not found.")
		}
		// Trusted sessions keep their own cookies while following the device's
		// connection settings. A shared native node must have one active route.
		for i, shared := range next.Entries {
			if shared.ID != server.ID && shared.connectionID() == server.connectionID() {
				shared.Direct, shared.Tailscale, shared.Relay = server.Direct, server.Tailscale, server.Relay
				shared.Mode, shared.Address, shared.Automatic = server.Mode, server.Address, server.Automatic
				next.Entries[i] = shared
			}
		}
	}
	return next, nil
}

func (profiles desktopServers) remove(id string) (desktopServers, error) {
	if id == localDesktopServer {
		return profiles, errors.New("The local server is managed in Service settings.")
	}
	if _, ok := profiles.find(id); !ok {
		return profiles, errors.New("Server not found.")
	}
	next := profiles
	next.Entries = nil
	for _, server := range profiles.Entries {
		if server.ID != id {
			next.Entries = append(next.Entries, server)
		}
	}
	if next.Selected == id {
		next.Selected = localDesktopServer
	}
	return next, nil
}

func writeDesktopServers(path string, profiles desktopServers) error {
	body, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".desktop-servers-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// The mount and cookie namespace change when a profile's destination changes,
// so editing an address cannot send the previous server's credentials there.
func desktopServerMount(server desktopServer) string {
	mode := server.Mode
	if mode == "" {
		mode = "direct"
	}
	identity := server.ID + "\x00" + mode + "\x00" + server.Address
	if server.FederationTarget != "" {
		identity += "\x00" + server.FederationSource + "\x00" + server.FederationTarget + "\x00" + server.FederationAccount
	}
	hash := sha256.Sum256([]byte(identity))
	id := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(hash[:]))
	return "/web/" + id + "/"
}
