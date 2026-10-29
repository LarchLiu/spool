package workspace

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	chatmodel "spool/internal/model"
)

type UIPreferences struct {
	Theme    string `json:"theme"`
	Language string `json:"language"`
}

func DefaultUIPreferences() UIPreferences {
	return UIPreferences{Theme: "system", Language: "system"}
}

func (p UIPreferences) Validate() error {
	if p.Theme != "system" && p.Theme != "light" && p.Theme != "dark" {
		return fmt.Errorf("invalid theme preference")
	}
	if p.Language != "system" && p.Language != "en" && p.Language != "zh-CN" && p.Language != "ja" {
		return fmt.Errorf("invalid language preference")
	}
	return nil
}

func uiPreferenceKey(user string) string {
	if user == "" {
		return "ui:desktop"
	}
	return "ui:user:" + user
}

// The empty user selects local desktop preferences; Web callers can only use
// their authenticated user ID, never the desktop or another user's settings.
func (s *Store) UIPreferences(user string) (UIPreferences, error) {
	p := DefaultUIPreferences()
	var body string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, uiPreferenceKey(user)).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(body), &p); err != nil {
		return p, err
	}
	return p, p.Validate()
}

func (s *Store) SaveUIPreferences(user string, p UIPreferences) error {
	if err := p.Validate(); err != nil {
		return err
	}
	body, _ := json.Marshal(p)
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, uiPreferenceKey(user), string(body))
	return err
}

type Navigation struct {
	Projects []Project           `json:"projects"`
	Chats    []chatmodel.Session `json:"chats"`
}

func (s *Store) UserNavigation(user string) (Navigation, error) {
	if err := s.syncLocalSessions(false); err != nil {
		return Navigation{}, err
	}
	projects, err := s.UserProjects(user)
	if err != nil {
		return Navigation{}, err
	}
	// Return indexed metadata only: grouped projects and cross-project recents
	// must not load every turn. Project sharing controls whose chats are listed.
	rows, err := s.db.Query(`SELECT `+qualifiedSessionColumns+` FROM sessions s
		JOIN project_members member ON member.project_id=s.project_id AND member.user_id=?
		JOIN projects p ON p.id=s.project_id
		LEFT JOIN users u ON u.id=s.user_id
		WHERE (s.user_id=? OR p.shared_chats=1 OR EXISTS(SELECT 1 FROM chat_collaborators WHERE session_id=s.id AND user_id=?)) AND p.archived=0 AND `+visibleSessionSQL+` AND (u.disabled=0 OR EXISTS(SELECT 1 FROM automation_chats WHERE session_id=s.id) OR EXISTS(SELECT 1 FROM local_sessions WHERE session_id=s.id)) ORDER BY s.updated_at DESC,s.id`, user, user, user)
	if err != nil {
		return Navigation{}, err
	}
	defer rows.Close()
	chats := []chatmodel.Session{}
	for rows.Next() {
		chat, e := scanSession(rows)
		if e != nil {
			return Navigation{}, e
		}
		chat.NativeSessionID = ""
		chats = append(chats, chat)
	}
	err = rows.Err()
	rows.Close()
	if err == nil {
		err = populateParticipants(s.db, chats)
		s.setSessionControls(user, chats)
	}
	return Navigation{Projects: projects, Chats: chats}, err
}

const qualifiedSessionColumns = `s.id,COALESCE(s.user_id,''),COALESCE(s.project_id,''),s.title,s.backend,s.native_session_id,s.native_session_path,s.model,s.reasoning_effort,s.permission_mode,s.status,s.created_at,s.updated_at,EXISTS(SELECT 1 FROM local_sessions WHERE session_id=s.id)`

const visibleSessionSQL = `(NOT EXISTS(SELECT 1 FROM local_sessions WHERE session_id=s.id) OR (p.shared_chats=1 AND EXISTS(SELECT 1 FROM project_local_sharing WHERE project_id=p.id AND enabled=1) AND EXISTS(SELECT 1 FROM local_sessions WHERE session_id=s.id AND visible=1) AND NOT EXISTS(SELECT 1 FROM spool_native_sessions owned WHERE owned.backend=s.backend AND owned.native_session_id=s.native_session_id)))`

// Ownership selects the independent personal-chat permission, so project-wide
// sharing cannot bypass a personal read-only policy. Task assignment is separate.
const controllableSessionSQL = `(CASE WHEN s.user_id=u.id AND NOT EXISTS(SELECT 1 FROM automation_chats WHERE session_id=s.id) THEN COALESCE((SELECT access FROM project_member_own_access WHERE project_id=p.id AND user_id=u.id),'create')='create' ELSE EXISTS(SELECT 1 FROM chat_collaborators WHERE session_id=s.id AND user_id=u.id) OR (p.shared_chats=1 AND COALESCE((SELECT access FROM project_member_access WHERE project_id=p.id AND user_id=u.id),'control')='control') END)`

func (s *Store) setSessionControls(user string, chats []chatmodel.Session) {
	for i := range chats {
		chats[i].CanControl = new(bool)
	}
	for start := 0; start < len(chats); start += 200 {
		batch := chats[start:min(start+200, len(chats))]
		args := []any{user}
		positions := map[string]int{}
		for i := range batch {
			args = append(args, batch[i].ID)
			positions[batch[i].ID] = i
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := s.db.Query(`SELECT s.id FROM sessions s JOIN projects p ON p.id=s.project_id JOIN project_members m ON m.project_id=p.id AND m.user_id=? JOIN users u ON u.id=m.user_id WHERE p.archived=0 AND u.disabled=0 AND `+controllableSessionSQL+` AND `+visibleSessionSQL+` AND s.id IN (`+placeholders+`)`, args...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				*batch[positions[id]].CanControl = true
			}
		}
		rows.Close()
	}
}
