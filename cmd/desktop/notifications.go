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
)

type notificationTarget struct {
	URL  string `json:"url"`
	User string `json:"user"`
}

var desktopNotificationOpen = mygo.NewEvent[notificationTarget]("spool:notification-open")

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

func (p *panel) desktopSessionCookies(address string) ([]mygo.Cookie, error) {
	if p.notificationCookies != nil {
		return p.notificationCookies(address)
	}
	return mygo.Cookies.List(mygo.CookieFilter{URL: address, Name: "spool_session", Path: "/"})
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
	if p.notificationEvents != nil {
		p.notificationEvents.SetFocused(focused)
	}
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

func (p *panel) pollDesktopNotifications() {
	if p.quitting || p.notificationEvents == nil || p.notificationPolling {
		return
	}
	p.notificationPolling = true
	defer func() { p.notificationPolling = false }()
	p.setDesktopNotificationFocused(p.desktopNotificationFocused())
	var token, account string
	if p.notificationsEnabled && p.notificationsSupported {
		address, err := p.service.LocalWebURL()
		if err == nil {
			cookies, err := p.desktopSessionCookies(address)
			if err == nil && len(cookies) == 1 && cookies[0].HTTPOnly {
				if p.quitting {
					return
				}
				token = cookies[0].Value
				account = p.service.DesktopNotificationAccount(token)
			}
		}
	}
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
			if account == "" || p.desktopNotificationFocused() {
				continue
			}
			note, allowed := event.Resolve(token, mygo.App.Locale())
			if !allowed || p.desktopNotificationFocused() {
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
