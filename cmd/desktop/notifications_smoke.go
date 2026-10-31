package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/egoist/mygo"

	"spool/internal/workspace"
)

// System WebViews may share cookies with an installed app. Keep their page
// signed out; use isolated HTTP clients for both chat identities, inject only
// the cookie lookup, OS display and focus policy, and exercise real native event
// routing. CI desktops may deny foreground activation after hiding a window, so
// drive notification focus with real visibility instead of OS focus stealing.
func (p *panel) probeDesktopNotifications(ctx context.Context, report string) error {
	address, err := p.service.LocalWebURL()
	if err != nil {
		return err
	}
	people, err := p.service.Store.Users()
	if err != nil {
		return err
	}
	var receiver string
	for _, person := range people {
		if person.Username == "smoke-person" {
			receiver = person.ID
		}
	}
	sender, err := p.service.Store.SaveUser(workspace.UserInput{Username: "notification-sender", Nickname: "Notification Sender", Password: "smoke-sender-password"})
	if err != nil {
		return err
	}
	receiverConn, token, err := smokeNotificationSocket(ctx, address, "smoke-person", "smoke-password-123")
	if err != nil {
		return err
	}
	defer receiverConn.CloseNow()
	conn, _, err := smokeNotificationSocket(ctx, address, "notification-sender", "smoke-sender-password")
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	shown := make(chan mygo.NotificationOptions, 16)
	ready := make(chan struct{}, 1)
	p.window.Update(func() {
		p.showMainWindow()
		p.notificationFocused = p.window.IsVisible
		p.setDesktopNotificationFocused(true)
		p.notificationsSupported = true
		p.notificationShow = func(opts mygo.NotificationOptions) error { shown <- opts; return nil }
		p.notificationCookies = func(string) ([]mygo.Cookie, error) {
			return []mygo.Cookie{{Name: "spool_session", Value: token, HTTPOnly: true, Path: "/"}}, nil
		}
		ready <- struct{}{}
	})
	defer p.window.Update(func() {
		p.notificationFocused = nil
		p.setDesktopNotificationFocused(p.desktopNotificationFocused())
	})
	select {
	case <-ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := p.waitNotificationWindow(ctx, true, true); err != nil {
		return err
	}
	settings := p.settingsView.Page()
	if err := smokeSettingsCommand(ctx, settings, settingsCommand{Kind: "save-notifications", Enabled: true}); err != nil {
		return err
	}
	select {
	case test := <-shown:
		if test.ID != "spool-notification-test" {
			return fmt.Errorf("enabling did not send a test notification")
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := smokeSettingsCommand(ctx, settings, settingsCommand{Kind: "navigate", Section: "Appearance"}); err != nil {
		return err
	}
	if err := waitChatScript(ctx, settings, `return smokeSettingsReady('Appearance', '[role="switch"]') && document.querySelector('[data-page="Appearance"] [role="switch"]')?.getAttribute('aria-checked') === 'true';`); err != nil {
		return err
	}
	if _, err := settings.EvalContext(ctx, `await new Promise(resolve => setTimeout(resolve, 200));`); err != nil {
		return err
	}
	if image, err := p.settingsView.CapturePage(); err != nil {
		return err
	} else if err := os.WriteFile(report+"-notifications.png", image, 0600); err != nil {
		return err
	}
	page := p.chatView.Page()
	if _, err := page.EvalContext(ctx, smokeNotificationObserverScript); err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"type":"direct_message","request_id":"native-foreground-smoke","to":%q,"text":"foreground native message body"}`, receiver))); err != nil {
		return err
	}
	if _, err := smokeNotificationResult(ctx, conn, "native-foreground-smoke"); err != nil {
		return err
	}
	checked := make(chan error, 1)
	p.window.Update(func() {
		if !p.desktopNotificationFocused() || len(p.notificationEvents.Events()) != 0 || len(shown) != 0 {
			checked <- fmt.Errorf("focused desktop queued or showed an automatic notification")
			return
		}
		p.window.Hide()
		checked <- nil
	})
	select {
	case err := <-checked:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := p.waitNotificationWindow(ctx, false, false); err != nil {
		return err
	}
	p.window.Update(func() {
		p.pollDesktopNotifications()
		if len(shown) != 0 {
			checked <- fmt.Errorf("foreground notification replayed after hiding the window")
			return
		}
		checked <- nil
	})
	select {
	case err := <-checked:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"type":"direct_message","request_id":"native-private-smoke","to":%q,"text":"hidden native message body"}`, receiver))); err != nil {
		return err
	}
	if _, err := smokeNotificationResult(ctx, conn, "native-private-smoke"); err != nil {
		return err
	}
	var private mygo.NotificationOptions
	select {
	case private = <-shown:
	case <-ctx.Done():
		return ctx.Err()
	}
	target, valid := notificationTargetFromID(private.ID)
	if !valid || target.User != receiver || !strings.Contains(private.Title, sender.Nickname) || strings.Contains(private.Body, "hidden native message body") {
		return fmt.Errorf("native private notification metadata/content incorrect")
	}
	p.window.Update(func() { p.openSystemNotification(private.ID) })
	if err := waitSmokeNotification(ctx, page, target); err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"type":"group_create","request_id":"native-group-smoke","name":"Native notification group","members":[%q]}`, receiver))); err != nil {
		return err
	}
	result, err := smokeNotificationResult(ctx, conn, "native-group-smoke")
	if err != nil {
		return err
	}
	group, _ := result["group"].(map[string]any)
	groupID, _ := group["id"].(string)
	if groupID == "" {
		return fmt.Errorf("native group fixture returned no ID")
	}
	p.window.Update(func() { p.window.Hide() })
	if err := p.waitNotificationWindow(ctx, false, false); err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"type":"group_message","request_id":"native-group-msg-smoke","to":%q,"text":"hidden native group body"}`, groupID))); err != nil {
		return err
	}
	if _, err := smokeNotificationResult(ctx, conn, "native-group-msg-smoke"); err != nil {
		return err
	}
	var groupNote mygo.NotificationOptions
	select {
	case groupNote = <-shown:
	case <-ctx.Done():
		return ctx.Err()
	}
	if !strings.Contains(groupNote.Title, "Native notification group") || !strings.Contains(groupNote.Body, sender.Nickname) || strings.Contains(groupNote.Body, "hidden native group body") {
		return fmt.Errorf("native group notification metadata/content incorrect")
	}
	groupTarget, valid := notificationTargetFromID(groupNote.ID)
	if !valid {
		return fmt.Errorf("native group click target invalid")
	}
	p.window.Update(func() { p.openSystemNotification(groupNote.ID) })
	if err := waitSmokeNotification(ctx, page, groupTarget); err != nil {
		return err
	}
	return smokeSettingsCommand(ctx, settings, settingsCommand{Kind: "save-notifications", Enabled: false})
}

// The frontend consumes and deletes spoolNotificationTarget synchronously.
// Observing that buffer from a later DOM listener races even with capture=true
// on window. Subscribe to the native payload without consuming the UI buffer.
// Keep the Document itself as the reload sentinel, not a clock reading.
const smokeNotificationObserverScript = `
	window.spoolSmokeNotification = null;
	window.mygo.on('spool:notification-open', target => {
		window.spoolSmokeNotification = target;
	});
	window.spoolSmokeDocument = document;
`

func waitSmokeNotification(ctx context.Context, page *mygo.Page, target notificationTarget) error {
	return waitChatChecks(ctx, page, fmt.Sprintf(`
		const checks = {
			target: window.spoolSmokeNotification?.url === %q,
			account: window.spoolSmokeNotification?.user === %q,
			same_page: document === window.spoolSmokeDocument,
			consumed: !('spoolNotificationTarget' in window),
			signed_out: !!document.querySelector('input[name="password"]')
		};
		return {ready: Object.values(checks).every(Boolean), checks};
	`, target.URL, target.User))
}

func (p *panel) waitNotificationWindow(ctx context.Context, focused, visible bool) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		state := make(chan bool, 1)
		p.window.Update(func() {
			matches := p.desktopNotificationFocused() == focused && p.window.IsVisible() == visible
			if matches {
				log.Printf("event=smoke_notification_focus native=%v policy=%v visible=%v", p.window.IsFocused(), p.desktopNotificationFocused(), p.window.IsVisible())
			}
			state <- matches
		})
		select {
		case matches := <-state:
			if matches {
				return nil
			}
		case <-ctx.Done():
			return fmt.Errorf("notification window focus policy=%v visible=%v: %w", focused, visible, ctx.Err())
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("notification window focus policy=%v visible=%v: %w", focused, visible, ctx.Err())
		}
	}
}

func smokeNotificationResult(ctx context.Context, conn *websocket.Conn, request string) (map[string]any, error) {
	for {
		_, body, err := conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		var event struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &event) != nil {
			continue
		}
		if event.Type == "ping" {
			_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"pong"}`))
			continue
		}
		if request == "ready" && event.Type == "ready" {
			return nil, nil
		}
		var data map[string]any
		if json.Unmarshal(event.Data, &data) != nil {
			continue
		}
		if data["request_id"] == request {
			if event.Type == "direct_message_error" {
				return nil, fmt.Errorf("notification fixture message rejected: %v", data["code"])
			}
			return data, nil
		}
	}
}

func smokeNotificationSocket(ctx context.Context, address, username, password string) (*websocket.Conn, string, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req, _ := http.NewRequestWithContext(ctx, "POST", address+"/api/login", bytes.NewReader(body))
	req.Header.Set("Origin", address)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	var login struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(response.Body).Decode(&login); err != nil || response.StatusCode != 200 {
		return nil, "", fmt.Errorf("notification fixture login failed")
	}
	var token string
	for _, cookie := range response.Cookies() {
		if cookie.Name == "spool_session" {
			token = cookie.Value
		}
	}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(address, "http")+"/api/events", &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Origin": {address}}})
	if err != nil {
		return nil, "", err
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"type":"hello","csrf":%q,"visible":true}`, login.CSRF))); err != nil {
		conn.CloseNow()
		return nil, "", err
	}
	if _, err := smokeNotificationResult(ctx, conn, "ready"); err != nil {
		conn.CloseNow()
		return nil, "", err
	}
	return conn, token, nil
}
