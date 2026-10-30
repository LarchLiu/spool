package workspace

import (
	"database/sql"
	"errors"
	"net/url"
	"slices"
	"sync"
	"time"
)

// Desktop notifications never expose an HTTP API or trust a renderer-provided
// user ID. The native host resolves each event with its HTTPOnly session cookie.
type DesktopNotification struct {
	Title, Body, URL, UserID string
}

type DesktopNotificationEvent struct {
	app *App
	job pushJob
}

type desktopNotificationHub struct {
	mu   sync.Mutex
	subs map[*DesktopNotificationSubscription]struct{}
}

type DesktopNotificationSubscription struct {
	hub     *desktopNotificationHub
	events  chan DesktopNotificationEvent
	focused bool
}

func (s *Service) SubscribeDesktopNotifications() *DesktopNotificationSubscription {
	return s.Store.desktopNotifications.subscribe()
}

func (s *Service) DesktopNotificationAccount(token string) string {
	if token == "" || len(token) > 128 {
		return ""
	}
	identity, err := s.Store.authenticate(token)
	if err != nil {
		return ""
	}
	return identity.User.ID
}

func (h *desktopNotificationHub) subscribe() *DesktopNotificationSubscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[*DesktopNotificationSubscription]struct{}{}
	}
	s := &DesktopNotificationSubscription{hub: h, events: make(chan DesktopNotificationEvent, 128)}
	h.subs[s] = struct{}{}
	return s
}

func (s *DesktopNotificationSubscription) Events() <-chan DesktopNotificationEvent { return s.events }

// SetFocused drops pending and future foreground events instead of replaying
// them when the desktop loses focus. It does not affect other subscribers.
func (s *DesktopNotificationSubscription) SetFocused(focused bool) {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if _, open := s.hub.subs[s]; !open {
		return
	}
	s.focused = focused
	if focused {
		for {
			select {
			case <-s.events:
			default:
				return
			}
		}
	}
}

func (s *DesktopNotificationSubscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if _, ok := s.hub.subs[s]; ok {
		delete(s.hub.subs, s)
		close(s.events)
	}
}

func (h *desktopNotificationHub) publish(a *App, job pushJob) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) == 0 {
		return
	}
	enabled, err := a.store.DesktopNotificationsEnabled()
	if err != nil || !enabled {
		return
	}
	for sub := range h.subs {
		if sub.focused {
			continue
		}
		select {
		case sub.events <- DesktopNotificationEvent{a, job}:
		default: // Best effort; never block message or task execution on native UI.
		}
	}
}

func (s *Store) DesktopNotificationsEnabled() (bool, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key='desktop:notifications'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return value == "true", err
}

func (s *Store) SaveDesktopNotificationsEnabled(enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES('desktop:notifications',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, value)
	return err
}

func (event DesktopNotificationEvent) Resolve(token, language string) (DesktopNotification, bool) {
	a, job := event.app, event.job
	if a == nil || time.Since(job.queuedAt) > 5*time.Minute || !a.pushJobCurrent(job) {
		return DesktopNotification{}, false
	}
	select {
	case <-a.shutdown:
		return DesktopNotification{}, false
	default:
	}
	enabled, err := a.store.DesktopNotificationsEnabled()
	if err != nil || !enabled || token == "" || len(token) > 128 {
		return DesktopNotification{}, false
	}
	identity, err := a.store.authenticate(token)
	if err != nil || !a.desktopNotificationRecipient(identity.User.ID, job) {
		return DesktopNotification{}, false
	}
	title, body := a.pushNotificationText(language, job)
	target := "/?" + url.Values{"chat": {job.chatID}}.Encode()
	if job.isMessage() {
		peer := job.messageFrom
		if job.kind == "group_message" {
			peer = job.messageTo
		}
		target = "/?" + url.Values{"inbox": {peer}, "recipient": {identity.User.ID}}.Encode()
	}
	return DesktopNotification{title, body, target, identity.User.ID}, true
}

func (a *App) desktopNotificationRecipient(user string, job pushJob) bool {
	if !a.canReceivePush(user, job) {
		return false
	}
	if job.isMessage() {
		var senderEnabled bool
		err := a.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND disabled=0)`, job.messageFrom).Scan(&senderEnabled)
		return err == nil && senderEnabled && slices.Contains(job.recipients, user)
	}
	var participant bool
	err := a.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions s WHERE s.id=? AND (s.user_id=? OR EXISTS(SELECT 1 FROM chat_collaborators WHERE session_id=s.id AND user_id=?) OR EXISTS(SELECT 1 FROM turn_actors WHERE session_id=s.id AND turn_id=? AND user_id=?)))`, job.chatID, user, user, job.turnID, user).Scan(&participant)
	return err == nil && participant
}
