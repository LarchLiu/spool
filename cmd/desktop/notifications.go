package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"strings"

	"github.com/egoist/mygo"

	chatmodel "spool/internal/model"
	"spool/internal/workspace"
)

type notificationTarget struct {
	URL  string `json:"url"`
	User string `json:"user"`
}

var desktopNotificationOpen = mygo.NewEvent[notificationTarget]("spool:notification-open")

type sessionNotification struct {
	Chat string `json:"chat"`
	User string `json:"user"`
	Kind string `json:"kind"`
}

var desktopSessionNotification = mygo.NewEvent[sessionNotification]("spool:session-notification")
var desktopSessionView = mygo.NewEvent[string]("spool:session-view")

func notificationID(target notificationTarget) string {
	body, _ := json.Marshal(target)
	return "spool-native-" + base64.RawURLEncoding.EncodeToString(body)
}

func notificationTargetFromID(id string) (notificationTarget, bool) {
	var target notificationTarget
	if !strings.HasPrefix(id, "spool-native-") || len(id) > 1024 {
		return target, false
	}
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, "spool-native-"))
	if err != nil || json.Unmarshal(body, &target) != nil || !chatmodel.ValidID(target.User) {
		return notificationTarget{}, false
	}
	u, err := url.Parse(target.URL)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Path != "/" || u.RawPath != "" || u.Fragment != "" {
		return notificationTarget{}, false
	}
	params, err := url.ParseQuery(u.RawQuery)
	chat, inbox := params.Get("chat"), params.Get("inbox")
	valid := err == nil && len(params["chat"]) == 1 && len(params) == 1 && chatmodel.ValidID(chat)
	valid = valid || err == nil && len(params) == 2 && len(params["inbox"]) == 1 && len(params["recipient"]) == 1 && chatmodel.ValidID(inbox) && inbox != target.User && params.Get("recipient") == target.User
	return target, valid
}

func (p *panel) systemNotification(opts mygo.NotificationOptions) error {
	if p.notificationShow != nil {
		return p.notificationShow(opts)
	}
	return mygo.NewNotification(opts).Show()
}

func (p *panel) clearSystemNotifications() {
	if p.notificationClear != nil {
		p.notificationClear()
		return
	}
	mygo.ClearNotifications()
}

func desktopSessionCookies(address string) ([]mygo.Cookie, error) {
	return mygo.Cookies.List(mygo.CookieFilter{URL: address, Name: "spool_session", Path: "/"})
}

func (p *panel) desktopSessionCookies(address string) ([]mygo.Cookie, error) {
	if p.notificationCookies != nil {
		return p.notificationCookies(address)
	}
	return desktopSessionCookies(address)
}

type desktopNotificationSession struct{ token, account string }

// pending and apply belong to the UI thread. Only the copied lookup runs on a
// worker: waiting for WebKit cookies on the UI thread pumps a nested native
// event loop, which can consume events from an ongoing window resize.
type desktopNotificationReader struct{ pending bool }

func (r *desktopNotificationReader) read(lookup func() desktopNotificationSession, dispatch func(func()), apply func(desktopNotificationSession)) bool {
	if r.pending {
		return false
	}
	r.pending = true
	go func() {
		session := lookup()
		dispatch(func() {
			r.pending = false
			apply(session)
		})
	}()
	return true
}

func lookupDesktopNotificationSession(address string, cookies func(string) ([]mygo.Cookie, error), account func(string) string) desktopNotificationSession {
	values, err := cookies(address)
	if err != nil || len(values) != 1 || !values[0].HTTPOnly {
		return desktopNotificationSession{}
	}
	token := values[0].Value
	user := account(token)
	if user == "" {
		return desktopNotificationSession{}
	}
	return desktopNotificationSession{token, user}
}

func (p *panel) desktopNotificationFocused() bool {
	if p.notificationFocused != nil {
		return p.notificationFocused()
	}
	return p.window != nil && p.window.IsFocused()
}

func (p *panel) setDesktopNotificationFocused(focused bool) {
	if p.notificationFocused != nil {
		focused = p.notificationFocused()
	}
	chat := p.viewedDesktopNotificationChat(focused)
	if p.notificationEvents != nil {
		p.notificationEvents.SetView(focused, chat)
	}
	if !p.quitting && p.chatView != nil && p.chatLoaded && !p.chatFailed {
		_ = desktopSessionView.EmitPage(p.chatView.Page(), chat)
	}
}

func (p *panel) viewedDesktopNotificationChat(focused bool) string {
	if focused && p.tab == "Chat" && p.chatLoaded && !p.chatFailed {
		return p.notificationChat
	}
	return ""
}

func (p *panel) suppressDesktopNotification(note workspace.DesktopNotification) bool {
	if !p.desktopNotificationFocused() {
		return false
	}
	return note.ChatID == "" || note.ChatID == p.viewedDesktopNotificationChat(true)
}

func showSystemTest(supported bool, opts mygo.NotificationOptions, show func(mygo.NotificationOptions) error) error {
	if !supported {
		return errors.New("System notifications are unavailable. On macOS, use the packaged desktop app.")
	}
	err := show(opts)
	if errors.Is(err, mygo.ErrNotificationsDenied) {
		return errors.New("System notifications are blocked. Allow Spool in your system notification settings.")
	}
	if err != nil {
		log.Print("event=desktop_notification_failed")
		return errors.New("Could not show system notification")
	}
	return nil
}

func (p *panel) testSystemNotification(ctx context.Context) error {
	type configuration struct {
		supported bool
		opts      mygo.NotificationOptions
		show      func(mygo.NotificationOptions) error
	}
	c, err := settingsUI(ctx, p, func() (configuration, error) {
		show := p.notificationShow
		if show == nil {
			show = func(opts mygo.NotificationOptions) error { return mygo.NewNotification(opts).Show() }
		}
		return configuration{p.notificationsSupported, mygo.NotificationOptions{ID: "spool-notification-test", Title: p.tr("Spool - Test notification"), Body: p.tr("System notifications are working on this desktop.")}, show}, nil
	})
	if err != nil {
		return err
	}
	return showSystemTest(c.supported, c.opts, c.show)
}

func (p *panel) watchDesktopNotifications(done <-chan struct{}) {
	ready, dispatch := p.notificationEvents.Ready(), p.window.Update
	go func() {
		for {
			select {
			case <-done:
				return
			case _, open := <-ready:
				if !open {
					return
				}
				dispatch(func() {
					// Focus may have discarded the event before UI dispatch.
					if len(p.notificationEvents.Events()) != 0 {
						p.processDesktopNotifications()
					}
				})
			}
		}
	}()
}

func (p *panel) refreshDesktopNotificationSession() {
	if p.quitting {
		return
	}
	// Invalidate a cookie lookup that predates login, logout, a page reload or a
	// preference change. Clear old-account notifications without waiting for it.
	p.notificationRevision++
	p.clearSystemNotifications()
	p.notificationSession = [32]byte{}
	p.notificationRefresh = true
	p.processDesktopNotifications()
}

func (p *panel) processDesktopNotifications() {
	if p.quitting || p.notificationEvents == nil {
		return
	}
	if p.notificationReader.pending {
		return
	}
	p.notificationRefresh = false
	p.setDesktopNotificationFocused(p.desktopNotificationFocused())
	address, err := p.service.LocalWebURL()
	if err == nil {
		cookies := p.notificationCookies
		if cookies == nil {
			cookies = desktopSessionCookies
		}
		account := p.service.DesktopNotificationAccount
		revision := p.notificationRevision
		p.notificationReader.read(func() desktopNotificationSession {
			return lookupDesktopNotificationSession(address, cookies, account)
		}, p.window.Update, func(identity desktopNotificationSession) {
			if p.quitting {
				return
			}
			if revision == p.notificationRevision {
				p.applyDesktopNotificationSession(identity)
			}
			if p.notificationRefresh || len(p.notificationEvents.Events()) != 0 {
				p.processDesktopNotifications()
			}
		})
		return
	}
	p.applyDesktopNotificationSession(desktopNotificationSession{})
	if len(p.notificationEvents.Events()) != 0 {
		p.processDesktopNotifications()
	}
}

func (p *panel) applyDesktopNotificationSession(identity desktopNotificationSession) {
	if p.quitting {
		return
	}
	token, account := identity.token, identity.account
	var session [32]byte
	if account != "" {
		session = sha256.Sum256([]byte(token))
	}
	if session != p.notificationSession {
		p.clearSystemNotifications()
		p.notificationSession = session
	}
	for range 16 {
		if p.quitting {
			return
		}
		select {
		case event, open := <-p.notificationEvents.Events():
			if !open {
				return
			}
			if account == "" {
				continue
			}
			note, allowed := event.Resolve(token, mygo.App.Locale())
			if !allowed || p.suppressDesktopNotification(note) {
				continue
			}
			if note.ChatID != "" {
				p.sessionNotifications = append(p.sessionNotifications, sessionNotification{note.ChatID, note.UserID, note.Kind})
				if len(p.sessionNotifications) > 128 {
					p.sessionNotifications = p.sessionNotifications[len(p.sessionNotifications)-128:]
				}
				p.sendSessionNotifications()
			}
			// Session indicators remain useful when OS notifications are disabled,
			// unsupported or denied. These preferences only control the OS popup.
			if !p.notificationsEnabled || !p.notificationsSupported {
				continue
			}
			id := notificationID(notificationTarget{note.URL, note.UserID})
			if err := p.systemNotification(mygo.NotificationOptions{ID: id, Group: id, Title: note.Title, Body: note.Body}); err != nil {
				// Never log names, session cookies or notification contents.
				log.Print("event=desktop_notification_failed")
			}
		default:
			return
		}
	}
}

func (p *panel) sendSessionNotifications() {
	if p.quitting || p.chatView == nil || !p.chatLoaded || p.chatFailed {
		return
	}
	for len(p.sessionNotifications) > 0 {
		if desktopSessionNotification.EmitPage(p.chatView.Page(), p.sessionNotifications[0]) != nil {
			return
		}
		p.sessionNotifications = p.sessionNotifications[1:]
	}
	p.sessionNotifications = nil
}

func (p *panel) openSystemNotification(id string) {
	target, valid := notificationTargetFromID(id)
	if !valid || p.quitting {
		return
	}
	p.pendingNotification = &target
	if p.window == nil {
		return
	}
	p.openChat()
	p.sendPendingNotification()
}

func (p *panel) sendPendingNotification() {
	if p.pendingNotification == nil || p.chatView == nil || p.chatFailed || !p.chatLoaded {
		return
	}
	if desktopNotificationOpen.EmitPage(p.chatView.Page(), *p.pendingNotification) == nil {
		p.pendingNotification = nil
	}
}
