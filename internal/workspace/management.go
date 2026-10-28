package workspace

import (
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
	chatmodel "spool/internal/model"
)

type User struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Nickname  string `json:"nickname"`
	Disabled  bool   `json:"disabled"`
	CreatedAt int64  `json:"created_at"`
	Avatar    Avatar `json:"avatar"`
}
type UserInput struct {
	ID       string
	Username string
	Nickname string
	Password string
	Disabled bool
}
type Project struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Path        string           `json:"-"`
	Archived    bool             `json:"archived"`
	SharedChats bool             `json:"shared_chats"`
	CreatedAt   int64            `json:"created_at"`
	Members     []string         `json:"-"`
	Issues      IssuesAutomation `json:"-"`
}
type Settings struct {
	Host            string `json:"host"`
	Port            int    `json:"port"`
	PublicURL       string `json:"public_url"`
	IdleMinutes     int    `json:"idle_minutes"`
	AutoStart       bool   `json:"auto_start"`
	HistoryCacheMiB int    `json:"history_cache_mib"`
}

func DefaultSettings() Settings {
	return Settings{Host: "127.0.0.1", Port: 8787, IdleMinutes: 15, AutoStart: true, HistoryCacheMiB: 64}
}
func (s Settings) Address() string { return net.JoinHostPort(s.Host, fmt.Sprint(s.Port)) }
func (s Settings) Validate() error {
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
	return v, err
}
func (s *Store) SaveSettings(v Settings) error {
	if err := v.Validate(); err != nil {
		return err
	}
	v.PublicURL = strings.TrimSuffix(v.PublicURL, "/")
	b, _ := json.Marshal(v)
	// Keep persistence and the live cache target ordered across concurrent saves.
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	_, err := s.db.Exec(`INSERT INTO settings VALUES('server',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(b))
	if err == nil {
		s.historyCache.setMaxBytes(v.HistoryCacheMiB * 1024 * 1024)
	}
	return err
}
func (s *Store) Users() ([]User, error) {
	rows, err := s.db.Query(`SELECT u.id,u.username,u.nickname,u.disabled,u.created_at,COALESCE(a.style,?),COALESCE(a.seed,'') FROM users u LEFT JOIN user_avatars a ON a.user_id=u.id ORDER BY u.username`, DefaultAvatar().Style)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var v User
		if err = rows.Scan(&v.ID, &v.Username, &v.Nickname, &v.Disabled, &v.CreatedAt, &v.Avatar.Style, &v.Avatar.Seed); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) SaveUser(v UserInput) (User, error) {
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
		b, err := bcrypt.GenerateFromPassword([]byte(v.Password), 12)
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
		if err = tx.QueryRow(`SELECT password_hash,created_at FROM users WHERE id=?`, v.ID).Scan(&existing, &now); err != nil {
			return User{}, err
		}
		if hash == "" {
			hash = existing
		}
		_, err = tx.Exec(`UPDATE users SET username=?,nickname=?,password_hash=?,disabled=? WHERE id=?`, v.Username, v.Nickname, hash, v.Disabled, v.ID)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, v.ID)
		}
	}
	if err != nil {
		return User{}, err
	}
	avatar := DefaultAvatar()
	if err = tx.QueryRow(`SELECT style,seed FROM user_avatars WHERE user_id=?`, v.ID).Scan(&avatar.Style, &avatar.Seed); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	err = tx.Commit()
	return User{ID: v.ID, Username: v.Username, Nickname: v.Nickname, Disabled: v.Disabled, CreatedAt: now, Avatar: avatar}, err
}
func (s *Store) DeleteUser(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE users SET disabled=1 WHERE id=?`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id,name,path,archived,shared_chats,created_at FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out := []Project{}
	for rows.Next() {
		var v Project
		if err = rows.Scan(&v.ID, &v.Name, &v.Path, &v.Archived, &v.SharedChats, &v.CreatedAt); err != nil {
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
		members, e := s.db.Query(`SELECT user_id FROM project_members WHERE project_id=?`, out[i].ID)
		if e != nil {
			return nil, e
		}
		for members.Next() {
			var id string
			if e = members.Scan(&id); e != nil {
				members.Close()
				return nil, e
			}
			out[i].Members = append(out[i].Members, id)
		}
		members.Close()
	}
	return out, nil
}
func (s *Store) Project(id string) (Project, error) {
	var v Project
	err := s.db.QueryRow(`SELECT id,name,path,archived,shared_chats,created_at FROM projects WHERE id=?`, id).Scan(&v.ID, &v.Name, &v.Path, &v.Archived, &v.SharedChats, &v.CreatedAt)
	return v, err
}
func (s *Store) SaveProject(v Project) (Project, error) {
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
	if !v.Issues.Enabled {
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
	if _, err = tx.Exec(`DELETE FROM project_members WHERE project_id=?`, v.ID); err != nil {
		return v, err
	}
	for _, id := range v.Members {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO project_members VALUES(?,?)`, v.ID, id); err != nil {
			return v, err
		}
	}
	if err = saveIssuesAutomation(tx, v.ID, v.Issues); err != nil {
		return v, err
	}
	return v, tx.Commit()
}
func (s *Store) CanAccessProject(user, project string) bool {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM project_members m JOIN projects p ON p.id=m.project_id JOIN users u ON u.id=m.user_id WHERE m.user_id=? AND m.project_id=? AND p.archived=0 AND u.disabled=0`, user, project).Scan(&n)
	return err == nil && n == 1
}

// Task assignees can read/control their task chats without enabling project-wide
// sharing. Regular shared chats remain read-only for non-owners.
func (s *Store) CanAccessChat(user, chat string) bool {
	var owner, project string
	var shared bool
	err := s.db.QueryRow(`SELECT s.user_id,s.project_id,p.shared_chats FROM sessions s JOIN projects p ON p.id=s.project_id JOIN users u ON u.id=s.user_id WHERE s.id=? AND p.archived=0 AND (u.disabled=0 OR EXISTS(SELECT 1 FROM automation_chats WHERE session_id=s.id))`, chat).Scan(&owner, &project, &shared)
	if err != nil || !s.CanAccessProject(user, project) {
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
	err := s.db.QueryRow(`SELECT count(*) FROM sessions s WHERE s.id=? AND ((s.user_id=? AND NOT EXISTS(SELECT 1 FROM automation_chats WHERE session_id=s.id)) OR EXISTS(SELECT 1 FROM chat_collaborators WHERE session_id=s.id AND user_id=?))`, chat, user, user).Scan(&n)
	return err == nil && n == 1
}
func (s *Store) CreateChat(user, project string, backend chatmodel.Backend) (chatmodel.Session, error) {
	if !s.CanAccessProject(user, project) {
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
			out = append(out, p)
		}
	}
	return out, nil
}
func (s *Store) UserSessions(user, project string) ([]chatmodel.Session, error) {
	if !s.CanAccessProject(user, project) {
		return nil, os.ErrPermission
	}
	p, err := s.Project(project)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + sessionColumns + ` FROM sessions WHERE project_id=?`
	args := []any{project}
	if !p.SharedChats {
		query += ` AND (user_id=? OR EXISTS(SELECT 1 FROM chat_collaborators WHERE session_id=sessions.id AND user_id=?))`
		args = append(args, user, user)
	}
	query += ` ORDER BY updated_at DESC`
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
		out = append(out, v)
	}
	return out, rows.Err()
}
