package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"spool/internal/agent"
	"spool/internal/issues"
	chatmodel "spool/internal/model"
	"spool/internal/netproxy"
	"spool/internal/tailnet"
	"spool/relay"
)

type User struct {
	ID               string `json:"id"`
	Username         string `json:"username"`
	Nickname         string `json:"nickname"`
	Disabled         bool   `json:"disabled"`
	CreatedAt        int64  `json:"created_at"`
	Avatar           Avatar `json:"avatar"`
	RemoteDesktop    bool   `json:"remote_desktop"`
	ADBBridge        bool   `json:"adb_bridge"`
	SourceServerID   string `json:"source_server_id,omitempty"`
	SourceServerName string `json:"source_server_name,omitempty"`
	SourceUserID     string `json:"source_user_id,omitempty"`
}
type UserInput struct {
	ID            string
	Username      string
	Nickname      string
	Password      string
	Disabled      bool
	RemoteDesktop bool
	ADBBridge     bool
}

const productionBcryptCost = 12

type Project struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Path               string            `json:"-"`
	Archived           bool              `json:"archived"`
	SharedChats        bool              `json:"shared_chats"`
	SharedLocalChats   bool              `json:"shared_local_chats"`
	AllowExternalFiles bool              `json:"allow_external_files"`
	CreatedAt          int64             `json:"created_at"`
	Members            []string          `json:"-"`
	MemberAccess       map[string]string `json:"-"`
	MemberOwnAccess    map[string]string `json:"-"`
	CanCreate          *bool             `json:"can_create,omitempty"`
	Issues             IssuesAutomation  `json:"-"`
}
type Settings struct {
	ServerName           string `json:"server_name"`
	WebEnabled           bool   `json:"web_enabled"`
	Host                 string `json:"host"`
	Port                 int    `json:"port"`
	PublicURL            string `json:"public_url"`
	WebTrustedProxies    string `json:"web_trusted_proxies"`
	WebDetectedProxy     string `json:"web_detected_proxy,omitempty"`
	WebDetectedOrigin    string `json:"web_detected_origin,omitempty"`
	IdleMinutes          int    `json:"idle_minutes"`
	AutoStart            bool   `json:"auto_start"`
	HistoryCacheMiB      int    `json:"history_cache_mib"`
	TSNetEnabled         bool   `json:"tsnet_enabled"`
	TSNetHostname        string `json:"tsnet_hostname"`
	TSNetPort            int    `json:"tsnet_port"`
	ProxyEnabled         bool   `json:"proxy_enabled"`
	UseSystemProxy       bool   `json:"use_system_proxy"`
	ProxyURL             string `json:"proxy_url"`
	NoProxy              string `json:"no_proxy"`
	RemoteDesktopEnabled bool   `json:"remote_desktop_enabled"`
	ADBBridgeEnabled     bool   `json:"adb_bridge_enabled"`
	RelayEnabled         bool   `json:"relay_enabled"`
	RelayWebEnabled      bool   `json:"relay_web_enabled"`
	RelayURL             string `json:"relay_url"`
	RelayLANPort         int    `json:"relay_lan_port"`
}

func DefaultSettings() Settings {
	return Settings{ServerName: "Home", WebEnabled: true, Host: "127.0.0.1", Port: 59001, WebTrustedProxies: "127.0.0.0/8,::1/128", IdleMinutes: 15, AutoStart: true, HistoryCacheMiB: 64, TSNetHostname: "spool-server", TSNetPort: 443, ProxyEnabled: true, RelayLANPort: 10095}
}

func normalizeServerName(name string) string {
	if name = strings.TrimSpace(name); name == "" {
		return "Home"
	}
	return name
}
func (s Settings) Address() string { return net.JoinHostPort(s.Host, fmt.Sprint(s.Port)) }
func (s Settings) Validate() error {
	if err := s.validateLocal(); err != nil {
		return err
	}
	return (netproxy.Options{ProxyURL: s.ProxyURL, NoProxy: s.NoProxy}).Validate()
}

// An unavailable saved proxy must not prevent serving the local UI for repair.
// New settings still validate the proxy through Validate before being saved.
func (s Settings) validateLocal() error {
	if _, err := parseWebTrustedProxies(s.WebTrustedProxies); err != nil {
		return err
	}
	if s.RelayEnabled {
		if _, err := relay.ParseRelayURL(s.RelayURL); err != nil {
			return err
		}
		if s.RelayLANPort < 1 || s.RelayLANPort > 65535 {
			return fmt.Errorf("relay LAN port must be between 1 and 65535")
		}
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if s.Host != "localhost" && net.ParseIP(s.Host) == nil {
		return fmt.Errorf("listen host must be localhost or an IP address")
	}
	if s.IdleMinutes < 1 || s.IdleMinutes > 10080 {
		return fmt.Errorf("idle minutes must be between 1 and 10080")
	}
	if err := validateHistoryCacheMiB(s.HistoryCacheMiB); err != nil {
		return err
	}
	if s.TSNetEnabled {
		if err := (tailnet.Options{Hostname: s.TSNetHostname, Port: s.TSNetPort}).Validate(); err != nil {
			return err
		}
	}
	if s.PublicURL != "" {
		u, e := url.Parse(s.PublicURL)
		if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("public URL must be an http(s) origin without a path")
		}
	}
	return nil
}

func validateHistoryCacheMiB(size int) error {
	if size < 1 || size > int(^uint(0)>>1)/(1024*1024) {
		return fmt.Errorf("history cache size must be a positive whole number of MiB within the platform range")
	}
	return nil
}
func (s *Store) Settings() (Settings, error) {
	v := DefaultSettings()
	var body string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key='server'`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(body), &v)
	v.ServerName = normalizeServerName(v.ServerName)
	return v, err
}
func (s *Store) SaveSettings(v Settings) error {
	v.ServerName = normalizeServerName(v.ServerName)
	v.ProxyURL = strings.TrimSpace(v.ProxyURL)
	v.NoProxy = strings.TrimSpace(v.NoProxy)
	if !v.TSNetEnabled {
		if v.TSNetHostname == "" {
			v.TSNetHostname = DefaultSettings().TSNetHostname
		}
		if v.TSNetPort == 0 {
			v.TSNetPort = DefaultSettings().TSNetPort
		}
	}
	if err := v.Validate(); err != nil {
		return err
	}
	v.PublicURL = strings.TrimSuffix(v.PublicURL, "/")
	// Keep persistence and the live cache target ordered across concurrent saves.
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	return s.saveSettingsLocked(v)
}

func (s *Store) saveSettingsLocked(v Settings) error {
	b, _ := json.Marshal(v)
	_, err := s.db.Exec(`INSERT INTO settings VALUES('server',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(b))
	if err == nil {
		s.historyCache.setMaxBytes(v.HistoryCacheMiB * 1024 * 1024)
		s.codexHistory.setMaxBytes(v.HistoryCacheMiB * 1024 * 1024)
	}
	return s.notify(err, changeServer)
}
func (s *Store) Users() ([]User, error) {
	rows, err := s.db.Query(`SELECT u.id,u.username,u.nickname,u.disabled,u.created_at,COALESCE(a.style,?),COALESCE(a.seed,''),COALESCE(rd.enabled,0),COALESCE(ab.enabled,0),COALESCE(fu.peer_id,''),COALESCE(json_extract(fp.config,'$.name'),''),COALESCE(fu.subject,'') FROM users u LEFT JOIN user_avatars a ON a.user_id=u.id LEFT JOIN user_remote_desktop rd ON rd.user_id=u.id LEFT JOIN user_adb_bridge ab ON ab.user_id=u.id LEFT JOIN federation_users fu ON fu.user_id=u.id LEFT JOIN federation_peers fp ON fp.id=fu.peer_id WHERE NOT EXISTS(SELECT 1 FROM deleted_users WHERE user_id=u.id) ORDER BY u.username`, DefaultAvatar().Style)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var v User
		if err = rows.Scan(&v.ID, &v.Username, &v.Nickname, &v.Disabled, &v.CreatedAt, &v.Avatar.Style, &v.Avatar.Seed, &v.RemoteDesktop, &v.ADBBridge, &v.SourceServerID, &v.SourceServerName, &v.SourceUserID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) SaveUser(v UserInput) (User, error) {
	return s.saveUserWithPasswordCost(v, productionBcryptCost)
}

func (s *Store) saveUserWithPasswordCost(v UserInput, passwordCost int) (User, error) {
	if v.ID != "" {
		var deleted bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM deleted_users WHERE user_id=?)`, v.ID).Scan(&deleted); err != nil {
			return User{}, err
		} else if deleted {
			return User{}, errors.New("Deleted accounts cannot be restored")
		}
		source, err := s.federationSource(v.ID)
		if err != nil {
			return User{}, err
		}
		if source != "" {
			return s.saveFederationUser(v, source)
		}
	}
	v.Username = strings.TrimSpace(v.Username)
	v.Nickname = strings.TrimSpace(v.Nickname)
	if v.Username == "" || len(v.Username) > 64 || len(v.Nickname) > 100 || v.Nickname == "" {
		return User{}, fmt.Errorf("username and nickname are required (64/100 byte maximum)")
	}
	for _, c := range v.Username {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return User{}, fmt.Errorf("username may only contain letters, digits, dot, dash and underscore")
		}
	}
	var hash string
	if v.Password != "" {
		if len(v.Password) < 12 || len(v.Password) > 72 {
			return User{}, fmt.Errorf("password must be between 12 and 72 bytes")
		}
		b, err := bcrypt.GenerateFromPassword([]byte(v.Password), passwordCost)
		if err != nil {
			return User{}, err
		}
		hash = string(b)
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if v.ID == "" {
		if hash == "" {
			return User{}, fmt.Errorf("password is required for new users")
		}
		v.ID = chatmodel.NewID()
		_, err = tx.Exec(`INSERT INTO users VALUES(?,?,?,?,?,?)`, v.ID, v.Username, v.Nickname, hash, v.Disabled, now)
	} else {
		if !chatmodel.ValidID(v.ID) {
			return User{}, fmt.Errorf("invalid user ID")
		}
		var existing string
		if err = tx.QueryRow(`SELECT password_hash,created_at FROM users WHERE id=? AND NOT EXISTS(SELECT 1 FROM deleted_users WHERE user_id=users.id)`, v.ID).Scan(&existing, &now); err != nil {
			return User{}, err
		}
		if hash == "" {
			hash = existing
		}
		_, err = tx.Exec(`UPDATE users SET username=?,nickname=?,password_hash=?,disabled=? WHERE id=?`, v.Username, v.Nickname, hash, v.Disabled, v.ID)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM web_push_subscriptions WHERE user_id=?`, v.ID)
		}
		if err == nil {
			_, err = tx.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, v.ID)
		}
	}
	if err != nil {
		return User{}, err
	}
	if _, err = tx.Exec(`INSERT INTO user_remote_desktop(user_id,enabled) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET enabled=excluded.enabled`, v.ID, v.RemoteDesktop); err != nil {
		return User{}, err
	}
	if _, err = tx.Exec(`INSERT INTO user_adb_bridge(user_id,enabled) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET enabled=excluded.enabled`, v.ID, v.ADBBridge); err != nil {
		return User{}, err
	}
	if v.Disabled {
		if err := disableManagedUsers(tx, `SELECT id FROM users WHERE id=?`, v.ID); err != nil {
			return User{}, err
		}
	}
	avatar := DefaultAvatar()
	if err = tx.QueryRow(`SELECT style,seed FROM user_avatars WHERE user_id=?`, v.ID).Scan(&avatar.Style, &avatar.Seed); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	if err = bumpProfileRevision(tx); err != nil {
		return User{}, err
	}
	err = tx.Commit()
	s.notify(err, changeNavigation|changePresence|changeAuth|changeProfiles)
	return User{ID: v.ID, Username: v.Username, Nickname: v.Nickname, Disabled: v.Disabled, CreatedAt: now, Avatar: avatar, RemoteDesktop: v.RemoteDesktop, ADBBridge: v.ADBBridge}, err
}
func (s *Store) DeleteUser(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = disableManagedUsers(tx, `SELECT id FROM users WHERE id=?`, id); err != nil {
		return err
	}
	return s.notify(tx.Commit(), managedUsersChanged)
}

// RemoveDisabledUser hides a disabled account from management while retaining
// its immutable ID for historical chats and source identity bindings.
func (s *Store) RemoveDisabledUser(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var disabled bool
	if err := tx.QueryRow(`SELECT disabled FROM users WHERE id=?`, id).Scan(&disabled); err != nil {
		return err
	}
	if !disabled {
		return errors.New("Disable this account before deleting it")
	}
	if err := disableManagedUsers(tx, `SELECT id FROM users WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO deleted_users VALUES(?)`, id); err != nil {
		return err
	}
	return s.notify(tx.Commit(), managedUsersChanged)
}
func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id,name,path,archived,shared_chats,created_at,COALESCE((SELECT enabled FROM project_local_sharing WHERE project_id=projects.id),0),COALESCE((SELECT allow_external_files FROM project_file_access WHERE project_id=projects.id),1) FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out := []Project{}
	for rows.Next() {
		var v Project
		if err = rows.Scan(&v.ID, &v.Name, &v.Path, &v.Archived, &v.SharedChats, &v.CreatedAt, &v.SharedLocalChats, &v.AllowExternalFiles); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Issues, err = s.IssuesAutomation(out[i].ID); err != nil {
			return nil, err
		}
		members, e := s.db.Query(`SELECT m.user_id,COALESCE(a.access,'control'),COALESCE(own.access,'create') FROM project_members m JOIN users u ON u.id=m.user_id LEFT JOIN project_member_access a ON a.project_id=m.project_id AND a.user_id=m.user_id LEFT JOIN project_member_own_access own ON own.project_id=m.project_id AND own.user_id=m.user_id WHERE m.project_id=? AND u.disabled=0`, out[i].ID)
		if e != nil {
			return nil, e
		}
		out[i].MemberAccess = map[string]string{}
		out[i].MemberOwnAccess = map[string]string{}
		for members.Next() {
			var id, access, own string
			if e = members.Scan(&id, &access, &own); e != nil {
				members.Close()
				return nil, e
			}
			out[i].Members = append(out[i].Members, id)
			out[i].MemberAccess[id] = access
			out[i].MemberOwnAccess[id] = own
		}
		e = members.Err()
		members.Close()
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (s *Store) Project(id string) (Project, error) {
	var v Project
	err := s.db.QueryRow(`SELECT id,name,path,archived,shared_chats,created_at,COALESCE((SELECT enabled FROM project_local_sharing WHERE project_id=projects.id),0),COALESCE((SELECT allow_external_files FROM project_file_access WHERE project_id=projects.id),1) FROM projects WHERE id=?`, id).Scan(&v.ID, &v.Name, &v.Path, &v.Archived, &v.SharedChats, &v.CreatedAt, &v.SharedLocalChats, &v.AllowExternalFiles)
	return v, err
}
func (s *Store) SaveProject(v Project) (Project, error) {
	s.localMu.Lock()
	defer s.localMu.Unlock()
	v.Name = strings.TrimSpace(v.Name)
	if v.Name == "" || len(v.Name) > 120 {
		return v, fmt.Errorf("project name is required (120 byte maximum)")
	}
	if strings.TrimSpace(v.Path) == "" {
		return v, fmt.Errorf("project directory is required")
	}
	path, err := agent.WorkingDirectory(v.Path)
	if err != nil {
		return v, err
	}
	v.Path = path
	v.Issues.Labels = v.Issues.IssueLabels()
	if v.Issues.Enabled {
		v.Issues.Repository, err = issues.DetectRepository(context.Background(), issues.Run, v.Path)
		if err != nil {
			return v, fmt.Errorf("cannot enable issue automation: %w", err)
		}
	} else {
		if v.Issues.IntervalMinutes == 0 {
			v.Issues.IntervalMinutes = 60
		}
		if v.Issues.Backend == "" {
			v.Issues.Backend = chatmodel.BackendCodex
		}
	}
	if err = v.Issues.Validate(v.Members); err != nil {
		return v, err
	}
	access := make(map[string]string, len(v.Members))
	ownAccess := make(map[string]string, len(v.Members))
	for _, id := range v.Members {
		mode := v.MemberAccess[id]
		if mode == "" {
			mode = "control"
		}
		if mode != "control" && mode != "read-only" {
			return v, fmt.Errorf("invalid project member access")
		}
		access[id] = mode
		own := v.MemberOwnAccess[id]
		if own == "" {
			own = "create"
		}
		if own != "create" && own != "read-only" {
			return v, fmt.Errorf("invalid project member own chat access")
		}
		ownAccess[id] = own
	}
	v.MemberAccess = access
	v.MemberOwnAccess = ownAccess
	tx, err := s.db.Begin()
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if v.ID == "" {
		v.ID = chatmodel.NewID()
		v.CreatedAt = time.Now().UnixMilli()
		_, err = tx.Exec(`INSERT INTO projects(id,name,path,archived,shared_chats,created_at) VALUES(?,?,?,?,?,?)`, v.ID, v.Name, v.Path, v.Archived, v.SharedChats, v.CreatedAt)
	} else {
		var oldPath string
		var count int
		if err = tx.QueryRow(`SELECT path,created_at FROM projects WHERE id=?`, v.ID).Scan(&oldPath, &v.CreatedAt); err != nil {
			return v, err
		}
		if err = tx.QueryRow(`SELECT count(*) FROM sessions WHERE project_id=?`, v.ID).Scan(&count); err != nil {
			return v, err
		}
		if count > 0 && oldPath != v.Path {
			return v, fmt.Errorf("a project directory cannot change after chats exist; create a new project")
		}
		_, err = tx.Exec(`UPDATE projects SET name=?,path=?,archived=?,shared_chats=? WHERE id=?`, v.Name, v.Path, v.Archived, v.SharedChats, v.ID)
	}
	if err != nil {
		return v, err
	}
	if _, err = tx.Exec(`INSERT INTO project_local_sharing(project_id,enabled) VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET enabled=excluded.enabled`, v.ID, v.SharedLocalChats); err != nil {
		return v, err
	}
	if _, err = tx.Exec(`INSERT INTO project_file_access(project_id,allow_external_files) VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET allow_external_files=excluded.allow_external_files`, v.ID, v.AllowExternalFiles); err != nil {
		return v, err
	}
	if _, err = tx.Exec(`DELETE FROM project_members WHERE project_id=?`, v.ID); err != nil {
		return v, err
	}
	var enabledMembers []string
	for _, id := range v.Members {
		var disabled bool
		if err := tx.QueryRow(`SELECT disabled FROM users WHERE id=?`, id).Scan(&disabled); err != nil {
			return v, err
		}
		if disabled {
			delete(v.MemberAccess, id)
			delete(v.MemberOwnAccess, id)
			continue
		}
		enabledMembers = append(enabledMembers, id)
		if _, err = tx.Exec(`INSERT OR IGNORE INTO project_members VALUES(?,?)`, v.ID, id); err != nil {
			return v, err
		}
		if _, err = tx.Exec(`INSERT OR REPLACE INTO project_member_access(project_id,user_id,access) VALUES(?,?,?)`, v.ID, id, v.MemberAccess[id]); err != nil {
			return v, err
		}
		if _, err = tx.Exec(`INSERT OR REPLACE INTO project_member_own_access(project_id,user_id,access) VALUES(?,?,?)`, v.ID, id, v.MemberOwnAccess[id]); err != nil {
			return v, err
		}
	}
	v.Members = enabledMembers
	if err := v.Issues.Validate(v.Members); err != nil {
		return v, err
	}
	if err = saveIssuesAutomation(tx, v.ID, v.Issues); err != nil {
		return v, err
	}
	if err = tx.Commit(); err != nil {
		return v, err
	}
	s.localScanAt = time.Time{}
	s.changes.publish(changeNavigation)
	return v, nil
}
func (s *Store) CanAccessProject(user, project string) bool {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM project_members m JOIN projects p ON p.id=m.project_id JOIN users u ON u.id=m.user_id WHERE m.user_id=? AND m.project_id=? AND p.archived=0 AND u.disabled=0`, user, project).Scan(&n)
	return err == nil && n == 1
}

// Task assignees retain access without project-wide sharing. Local sessions are
// only available while explicitly shared and in the latest discovery window.
func (s *Store) CanAccessChat(user, chat string) bool {
	var owner, project string
	var shared bool
	var backend chatmodel.Backend
	var nativeID string
	err := s.db.QueryRow(`SELECT COALESCE(s.user_id,''),s.project_id,p.shared_chats,s.backend,s.native_session_id FROM sessions s JOIN projects p ON p.id=s.project_id LEFT JOIN users u ON u.id=s.user_id WHERE s.id=? AND p.archived=0 AND `+visibleSessionSQL+` AND (u.disabled=0 OR EXISTS(SELECT 1 FROM automation_chats WHERE session_id=s.id) OR EXISTS(SELECT 1 FROM local_sessions WHERE session_id=s.id))`, chat).Scan(&owner, &project, &shared, &backend, &nativeID)
	if err != nil || s.nativeSessionArchived(backend, nativeID) || !s.CanAccessProject(user, project) {
		return false
	}
	if owner == user || shared {
		return true
	}
	var n int
	err = s.db.QueryRow(`SELECT count(*) FROM chat_collaborators WHERE session_id=? AND user_id=?`, chat, user).Scan(&n)
	return err == nil && n == 1
}

func (s *Store) CanControlChat(user, chat string) bool {
	if !s.CanAccessChat(user, chat) {
		return false
	}
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM sessions s JOIN projects p ON p.id=s.project_id JOIN users u ON u.id=? WHERE s.id=? AND u.disabled=0 AND `+controllableSessionSQL, user, chat).Scan(&n)
	return err == nil && n == 1
}

func (s *Store) CanCreateChat(user, project string) bool {
	if !s.CanAccessProject(user, project) {
		return false
	}
	var access string
	err := s.db.QueryRow(`SELECT COALESCE((SELECT access FROM project_member_own_access WHERE project_id=? AND user_id=?),'create')`, project, user).Scan(&access)
	return err == nil && access == "create"
}

func (s *Store) CreateChat(user, project string, backend chatmodel.Backend) (chatmodel.Session, error) {
	if !s.CanCreateChat(user, project) {
		return chatmodel.Session{}, os.ErrPermission
	}
	return s.createSession(user, project, backend)
}
func (s *Store) UserProjects(user string) ([]Project, error) {
	all, err := s.Projects()
	if err != nil {
		return nil, err
	}
	out := []Project{}
	for _, p := range all {
		if !p.Archived && s.CanAccessProject(user, p.ID) {
			allowed := p.MemberOwnAccess[user] != "read-only"
			p.CanCreate = &allowed
			out = append(out, p)
		}
	}
	return out, nil
}
func (s *Store) UserSessions(user, project string) ([]chatmodel.Session, error) {
	if !s.CanAccessProject(user, project) {
		return nil, os.ErrPermission
	}
	if err := s.syncLocalSessions(false); err != nil {
		return nil, err
	}
	p, err := s.Project(project)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + qualifiedSessionColumns + ` FROM sessions s JOIN projects p ON p.id=s.project_id LEFT JOIN users u ON u.id=s.user_id WHERE s.project_id=? AND ` + visibleSessionSQL + ` AND (u.disabled=0 OR EXISTS(SELECT 1 FROM automation_chats WHERE session_id=s.id) OR EXISTS(SELECT 1 FROM local_sessions WHERE session_id=s.id))`
	args := []any{project}
	if !p.SharedChats {
		query += ` AND (s.user_id=? OR EXISTS(SELECT 1 FROM chat_collaborators WHERE session_id=s.id AND user_id=?))`
		args = append(args, user, user)
	}
	query += ` ORDER BY s.updated_at DESC,s.id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []chatmodel.Session{}
	for rows.Next() {
		v, e := scanSession(rows)
		if e != nil {
			return nil, e
		}
		if s.nativeSessionArchived(v.Backend, v.NativeSessionID) {
			continue
		}
		out = append(out, v)
	}
	err = rows.Err()
	rows.Close()
	if err == nil {
		err = populateParticipants(s.db, out)
		s.setSessionControls(user, out)
	}
	return out, err
}
