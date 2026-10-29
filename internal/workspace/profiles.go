package workspace

import (
	"database/sql"
	"fmt"
	"strings"
)

type UserProfile struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	Avatar   Avatar `json:"avatar"`
}

type ProfileSnapshot struct {
	Revision int64         `json:"revision"`
	People   []UserProfile `json:"people"`
}

func bumpProfileRevision(tx *sql.Tx) error {
	_, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('profile_revision','1') ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1`)
	return err
}

// Use the same read transaction for the revision and profiles, so an old HTTP
// response cannot claim the revision of a newer avatar or account edit.
func (s *Store) userProfiles(user string, online []PresenceUser) (ProfileSnapshot, error) {
	v := ProfileSnapshot{People: []UserProfile{}}
	tx, err := s.db.Begin()
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow(`SELECT COALESCE((SELECT CAST(value AS INTEGER) FROM meta WHERE key='profile_revision'),0)`).Scan(&v.Revision); err != nil {
		return v, err
	}
	// Historical operators are visible only through authorized chats. Online
	// users are already public to signed-in clients through the presence list.
	args := []any{user, user, user, user}
	allowed := `SELECT ? AS user_id UNION SELECT user_id FROM operators`
	if len(online) > 0 {
		ids := make([]string, len(online))
		for i, person := range online {
			ids[i] = "?"
			args = append(args, person.ID)
		}
		allowed += ` UNION SELECT id FROM users WHERE id IN (` + strings.Join(ids, ",") + `)`
	}
	rows, err := tx.Query(`WITH selected AS (
		SELECT s.id,s.user_id FROM sessions s
		JOIN project_members member ON member.project_id=s.project_id AND member.user_id=?
		JOIN projects p ON p.id=s.project_id LEFT JOIN users u ON u.id=s.user_id
		WHERE (s.user_id=? OR p.shared_chats=1 OR EXISTS(SELECT 1 FROM chat_collaborators WHERE session_id=s.id AND user_id=?))
		AND p.archived=0 AND `+visibleSessionSQL+` AND (u.disabled=0 OR EXISTS(SELECT 1 FROM automation_chats WHERE session_id=s.id) OR EXISTS(SELECT 1 FROM local_sessions WHERE session_id=s.id))
	), operators AS (
		SELECT user_id FROM selected s WHERE COALESCE((SELECT source_offset FROM turn_actors WHERE session_id=s.id ORDER BY started_at,turn_id LIMIT 1),-1)<>0
		UNION SELECT a.user_id FROM turn_actors a JOIN selected s ON s.id=a.session_id
		UNION SELECT a.user_id FROM turn_actions a JOIN selected s ON s.id=a.session_id
	), allowed AS (`+allowed+`)
	SELECT u.id,u.nickname,COALESCE(a.style,?),COALESCE(a.seed,'') FROM users u
	LEFT JOIN user_avatars a ON a.user_id=u.id WHERE u.id IN (SELECT user_id FROM allowed) ORDER BY u.id`, append(args, DefaultAvatar().Style)...)
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var person UserProfile
		if err = rows.Scan(&person.ID, &person.Nickname, &person.Avatar.Style, &person.Avatar.Seed); err != nil {
			break
		}
		v.People = append(v.People, person)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return v, fmt.Errorf("read user profiles: %w", err)
	}
	return v, tx.Commit()
}
