package workspace

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

type SidebarPreferences struct {
	Pinned   []string `json:"pinned"`
	Revision int64    `json:"revision"`
}

// Operations change only the selected preference, so concurrent devices do not
// replace each other's unrelated pins. Initial is a one-time
// import from device storage; an established server record always wins.
type SidebarPreferenceChange struct {
	ChatID  string              `json:"chat_id,omitempty"`
	Pinned  *bool               `json:"pinned,omitempty"`
	Initial *SidebarPreferences `json:"initial,omitempty"`
}

func (c SidebarPreferenceChange) Validate() error {
	if c.Initial != nil && c.ChatID == "" && c.Pinned == nil {
		if len(c.Initial.Pinned) <= 1000 {
			return nil
		}
	}
	if c.Initial == nil && c.ChatID != "" && len(c.ChatID) <= 512 && c.Pinned != nil {
		return nil
	}
	return fmt.Errorf("invalid sidebar preference change")
}

func sidebarPreferenceKey(user string) string { return "sidebar:user:" + user }

type sidebarReader interface {
	QueryRow(string, ...any) *sql.Row
}

func readSidebarPreferences(db sidebarReader, user string) (SidebarPreferences, error) {
	p := SidebarPreferences{Pinned: []string{}}
	var body string
	err := db.QueryRow(`SELECT value FROM settings WHERE key=?`, sidebarPreferenceKey(user)).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(body), &p)
	return p, err
}

var errSidebarChatAccess = errors.New("chat access denied")

func (s *Store) ChangeSidebarPreferences(user string, c SidebarPreferenceChange) (SidebarPreferences, error) {
	if err := c.Validate(); err != nil {
		return SidebarPreferences{}, err
	}
	if c.Pinned != nil && *c.Pinned && !s.CanAccessChat(user, c.ChatID) {
		return SidebarPreferences{}, errSidebarChatAccess
	}
	// Resolve access before opening a transaction: the store has one connection.
	if c.Initial != nil {
		initial := *c.Initial
		initial.Pinned = []string{}
		seen := map[string]bool{}
		for _, id := range c.Initial.Pinned {
			if !seen[id] && s.CanAccessChat(user, id) {
				initial.Pinned = append(initial.Pinned, id)
				seen[id] = true
			}
		}
		c.Initial = &initial
	}
	tx, err := s.db.Begin()
	if err != nil {
		return SidebarPreferences{}, err
	}
	defer tx.Rollback()
	p, err := readSidebarPreferences(tx, user)
	if err != nil {
		return p, err
	}
	if c.Initial != nil {
		if p.Revision != 0 {
			return p, nil
		}
		p.Pinned = c.Initial.Pinned
	} else if c.Pinned != nil {
		if *c.Pinned && slices.Contains(p.Pinned, c.ChatID) {
			return p, nil
		}
		next := []string{}
		if *c.Pinned {
			next = append(next, c.ChatID)
		}
		for _, id := range p.Pinned {
			if id != c.ChatID {
				next = append(next, id)
			}
		}
		if len(next) > 1000 {
			return p, fmt.Errorf("too many pinned chats")
		}
		p.Pinned = next
	}
	p.Revision++
	body, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, sidebarPreferenceKey(user), string(body)); err != nil {
		return p, err
	}
	err = tx.Commit()
	s.notify(err, changeNavigation)
	return p, err
}
