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
	AutomaticChecks      bool `json:"automatic_checks"`
	AutomaticInstalls    bool `json:"automatic_installs"`
	CheckIntervalMinutes int  `json:"check_interval_minutes"`
}

const DefaultUpdateCheckIntervalMinutes = 12 * 60

func (p UpdatePreferences) Validate() error {
	switch p.CheckIntervalMinutes {
	case 30, 60, 360, 720, 1440:
		return nil
	default:
		return errors.New("Invalid update check interval")
	}
}

func (s *Store) UpdatePreferences() (UpdatePreferences, error) {
	p := UpdatePreferences{AutomaticChecks: true, CheckIntervalMinutes: DefaultUpdateCheckIntervalMinutes}
	var body string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key='updates:desktop'`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(body), &p); err != nil {
		return p, err
	}
	if p.CheckIntervalMinutes == 0 {
		p.CheckIntervalMinutes = DefaultUpdateCheckIntervalMinutes
	}
	return p, p.Validate()
}

func (s *Store) SaveUpdatePreferences(p UpdatePreferences) error {
	if p.CheckIntervalMinutes == 0 {
		p.CheckIntervalMinutes = DefaultUpdateCheckIntervalMinutes
	}
	if err := p.Validate(); err != nil {
		return err
	}
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
