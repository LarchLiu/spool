package workspace

import (
	"database/sql"
	"errors"
	"net/url"
	"slices"
	"sync"
	"time"
)

// The native host resolves notifications with its HTTPOnly session cookie.
// Web session indicators use the same events through an authenticated socket;
// neither path trusts a renderer-provided user ID.
type DesktopNotification struct {
	Title, Body, URL, UserID string
	ChatID, Kind             string
}

type DesktopNotificationEvent struct {
	app *App
	job pushJob
}

type notificationHub struct {
	mu   sync.Mutex
	subs map[*DesktopNotificationSubscription]struct{}
}

type DesktopNotificationSubscription struct {
	hub      *notificationHub
	events   chan DesktopNotificationEvent
	ready    chan struct{}
	focused  bool
	chatID   string
	webToken string
	viewAt   time.Time
}

func (s *Service) SubscribeDesktopNotifications() *DesktopNotificationSubscription {
	return s.Store.notifications.subscribe("")
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

func (h *notificationHub) subscribe(webToken string) *DesktopNotificationSubscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[*DesktopNotificationSubscription]struct{}{}
	}
	s := &DesktopNotificationSubscription{hub: h, events: make(chan DesktopNotificationEvent, 128), ready: make(chan struct{}, 1), webToken: webToken}
	h.subs[s] = struct{}{}
	return s
}

func (s *DesktopNotificationSubscription) Events() <-chan DesktopNotificationEvent { return s.events }

// Ready wakes the native host when events arrive. Wakes coalesce; the host must
// drain Events before waiting again. It closes with the subscription.
func (s *DesktopNotificationSubscription) Ready() <-chan struct{} { return s.ready }

// SetView suppresses the session being read in the foreground. Other sessions
// still notify, while message notifications retain their background-only policy.
// Discard suppressed events now so losing focus never replays them later.
func (s *DesktopNotificationSubscription) SetView(focused bool, chatID string) {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if _, open := s.hub.subs[s]; !open {
		return
	}
	s.focused, s.chatID = focused, chatID
	s.viewAt = time.Now()
	if focused {
		var pending []DesktopNotificationEvent
		for {
			select {
			case event := <-s.events:
				if !s.suppresses(event.job) {
					pending = append(pending, event)
				}
			default:
				for _, event := range pending {
					s.events <- event
				}
				return
			}
		}
	}
}

func (s *DesktopNotificationSubscription) suppresses(job pushJob) bool {
	return s.focused && (job.isMessage() || job.chatID != "" && job.chatID == s.chatID)
}

func (s *DesktopNotificationSubscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if _, ok := s.hub.subs[s]; ok {
		delete(s.hub.subs, s)
		close(s.events)
		close(s.ready)
	}
}

// A focused view suppresses pushes for its browser auth session only. Other
// devices remain independent. Expire stale focus hints if a browser suspends
// without a blur event.
func (h *notificationHub) suppressesWebPush(token string, job pushJob) bool {
	if job.chatID == "" || job.isMessage() {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		if sub.webToken == token && sub.focused && sub.chatID == job.chatID && time.Since(sub.viewAt) < 45*time.Second {
			return true
		}
	}
	return false
}

func (h *notificationHub) publish(a *App, job pushJob) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) == 0 {
		return
	}
	if job.isMessage() {
		enabled, err := a.store.DesktopNotificationsEnabled()
		if err != nil || !enabled {
			return
		}
	}
	for sub := range h.subs {
		if sub.suppresses(job) {
			continue
		}
		select {
		case sub.events <- DesktopNotificationEvent{a, job}:
			select {
			case sub.ready <- struct{}{}:
			default:
			}
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
	if a == nil || token == "" || len(token) > 128 {
		return DesktopNotification{}, false
	}
	if job.isMessage() {
		enabled, err := a.store.DesktopNotificationsEnabled()
		if err != nil || !enabled {
			return DesktopNotification{}, false
		}
	}
	identity, err := a.store.authenticate(token)
	if err != nil || !event.authorized(identity.User.ID) {
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
	note := DesktopNotification{Title: title, Body: body, URL: target, UserID: identity.User.ID}
	if !job.isMessage() {
		note.ChatID, note.Kind = job.chatID, job.kind
	}
	return note, true
}

func (event DesktopNotificationEvent) authorized(user string) bool {
	a, job := event.app, event.job
	if a == nil || time.Since(job.queuedAt) > 5*time.Minute || !a.pushJobCurrent(job) {
		return false
	}
	select {
	case <-a.shutdown:
		return false
	default:
	}
	return a.desktopNotificationRecipient(user, job)
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
