package workspace

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrChatsRunning     = errors.New("Chats are running. Wait for all responses to finish before updating.")
	ErrUpdateInProgress = errors.New("An update is in progress. Try again shortly.")
)

type UpdatePreferences struct {
	AutomaticChecks   bool `json:"automatic_checks"`
	AutomaticInstalls bool `json:"automatic_installs"`
}

func (s *Store) UpdatePreferences() (UpdatePreferences, error) {
	p := UpdatePreferences{AutomaticChecks: true}
	var body string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key='updates:desktop'`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(body), &p)
	return p, err
}

func (s *Store) SaveUpdatePreferences(p UpdatePreferences) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings(key,value) VALUES('updates:desktop',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(body))
	return err
}

// AcquireUpdate serializes installation/relaunch with turn admission, not just
// a snapshot of chat statuses. Stopped turns still count until cleanup ends.
func (s *Service) AcquireUpdate() (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("service is closed")
	}
	if s.updating {
		return nil, ErrUpdateInProgress
	}
	app := s.app
	if app != nil {
		app.turnMu.Lock()
		defer app.turnMu.Unlock()
		if app.runningTurns != 0 {
			return nil, ErrChatsRunning
		}
		app.updating = true
	}
	s.updating = true
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if app != nil {
				app.turnMu.Lock()
				app.updating = false
				app.turnMu.Unlock()
			}
			s.updating = false
		})
	}, nil
}
