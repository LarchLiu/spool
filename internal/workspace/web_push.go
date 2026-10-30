package workspace

import (
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	webpush "github.com/SherClockHolmes/webpush-go"

	"spool/internal/i18n"
)

type pushKeys struct{ public, private string }

type pushSubscription struct {
	webpush.Subscription
	token, user, language string
}

type pushJob struct {
	chatID, turnID, approvalID, kind string
	messageFrom, messageTo           string
	recipients                       []string
	queuedAt                         time.Time
}

func (a *App) enqueueMessagePush(message directMessage, recipients []string) {
	a.enqueuePush(pushJob{kind: message.eventType(), messageFrom: message.From, messageTo: message.To, recipients: recipients})
}

func (job pushJob) isMessage() bool {
	return job.kind == "direct_message" || job.kind == "group_message"
}

type pushDispatcher struct {
	mu     sync.Mutex
	closed bool
	queue  chan pushJob
	cancel context.CancelFunc
	done   chan struct{}
}

// Keys live outside the settings API so management responses never expose them.
func (s *Store) webPushKeys() (pushKeys, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	var keys pushKeys
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM web_push_keys)`).Scan(&exists); err != nil {
		return keys, err
	}
	if !exists {
		private, public, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			return keys, err
		}
		if _, err = s.db.Exec(`INSERT OR IGNORE INTO web_push_keys VALUES(1,?,?)`, public, private); err != nil {
			return keys, err
		}
	}
	err := s.db.QueryRow(`SELECT public_key,private_key FROM web_push_keys WHERE id=1`).Scan(&keys.public, &keys.private)
	return keys, err
}

func validPushEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || len(endpoint) > 4096 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" || host == "web.push.apple.com" || strings.HasSuffix(host, ".notify.windows.com")
}

func validPushSubscription(sub webpush.Subscription) bool {
	if !validPushEndpoint(sub.Endpoint) || len(sub.Keys.P256dh) > 100 || len(sub.Keys.Auth) > 32 {
		return false
	}
	public, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(sub.Keys.P256dh, "="))
	if err != nil || len(public) != 65 {
		return false
	}
	x, _ := elliptic.Unmarshal(elliptic.P256(), public)
	auth, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(sub.Keys.Auth, "="))
	return x != nil && err == nil && len(auth) == 16
}

func publicPushAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}

func newPushHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Subscription endpoints are untrusted input. Do not follow redirects, use
	// proxy environment variables, or allow DNS to route a provider to a LAN host.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !publicPushAddress(ip) {
				return nil, errors.New("push provider resolved to a non-public address")
			}
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = errors.New("push provider has no addresses")
		}
		return nil, lastErr
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (a *App) pushRoute(w http.ResponseWriter, r *http.Request, id authIdentity, path string) {
	if path == "/api/push/config" && r.Method == http.MethodGet {
		keys, err := a.store.webPushKeys()
		if err != nil {
			apiError(w, 500, "Could not configure push notifications")
			return
		}
		apiJSON(w, 200, map[string]string{"public_key": keys.public})
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if path == "/api/push/subscribe" {
		var input struct {
			Subscription webpush.Subscription `json:"subscription"`
			Language     string               `json:"language"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if !validPushSubscription(input.Subscription) || (input.Language != "en" && input.Language != "zh-CN" && input.Language != "ja") {
			apiError(w, 400, "Invalid push subscription")
			return
		}
		a.store.mu.Lock()
		defer a.store.mu.Unlock()
		var count int
		err := a.store.db.QueryRow(`SELECT count(*) FROM web_push_subscriptions p JOIN auth_sessions a ON a.token_hash=p.token_hash WHERE a.user_id=? AND p.endpoint<>?`, id.User.ID, input.Subscription.Endpoint).Scan(&count)
		if err != nil {
			apiError(w, 500, "Could not save push subscription")
			return
		}
		if count >= 20 {
			apiError(w, 409, "Too many notification devices")
			return
		}
		_, err = a.store.db.Exec(`INSERT INTO web_push_subscriptions(endpoint,token_hash,p256dh,auth,language,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(endpoint) DO UPDATE SET token_hash=excluded.token_hash,p256dh=excluded.p256dh,auth=excluded.auth,language=excluded.language,updated_at=excluded.updated_at`, input.Subscription.Endpoint, id.TokenHash, input.Subscription.Keys.P256dh, input.Subscription.Keys.Auth, input.Language, time.Now().UnixMilli())
		if err != nil {
			apiError(w, 500, "Could not save push subscription")
			return
		}
		apiJSON(w, 200, map[string]bool{"subscribed": true})
		return
	}
	if path != "/api/push/status" && path != "/api/push/refresh" && path != "/api/push/unsubscribe" && path != "/api/push/test" {
		http.NotFound(w, r)
		return
	}
	var input struct {
		Endpoint string `json:"endpoint"`
		Language string `json:"language,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validPushEndpoint(input.Endpoint) {
		apiError(w, 400, "Invalid push subscription")
		return
	}
	if path == "/api/push/refresh" {
		if input.Language != "en" && input.Language != "zh-CN" && input.Language != "ja" {
			apiError(w, 400, "Invalid push subscription")
			return
		}
		if _, err := a.store.db.Exec(`UPDATE web_push_subscriptions SET language=?,updated_at=? WHERE endpoint=? AND token_hash=?`, input.Language, time.Now().UnixMilli(), input.Endpoint, id.TokenHash); err != nil {
			apiError(w, 500, "Could not save push subscription")
			return
		}
		apiJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if path == "/api/push/unsubscribe" {
		if _, err := a.store.db.Exec(`DELETE FROM web_push_subscriptions WHERE endpoint=? AND token_hash=?`, input.Endpoint, id.TokenHash); err != nil {
			apiError(w, 500, "Could not save push subscription")
			return
		}
		apiJSON(w, 200, map[string]bool{"subscribed": false})
		return
	}
	var sub pushSubscription
	sub.Endpoint, sub.token, sub.user = input.Endpoint, id.TokenHash, id.User.ID
	err := a.store.db.QueryRow(`SELECT p256dh,auth,language FROM web_push_subscriptions WHERE endpoint=? AND token_hash=?`, input.Endpoint, id.TokenHash).Scan(&sub.Keys.P256dh, &sub.Keys.Auth, &sub.language)
	if path == "/api/push/status" {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			apiError(w, 500, "Could not save push subscription")
			return
		}
		apiJSON(w, 200, map[string]bool{"subscribed": err == nil})
		return
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			apiError(w, 404, "Push subscription not found")
		} else {
			apiError(w, 500, "Could not send push notification")
		}
		return
	}
	result, err := a.store.db.Exec(`UPDATE web_push_subscriptions SET last_test_at=? WHERE endpoint=? AND token_hash=? AND last_test_at<?`, time.Now().UnixMilli(), input.Endpoint, id.TokenHash, time.Now().Add(-30*time.Second).UnixMilli())
	if err != nil {
		apiError(w, 500, "Could not send push notification")
		return
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		apiError(w, 429, "Wait before sending another test notification")
		return
	}
	if err := a.sendPush(r.Context(), sub, pushJob{kind: "test"}); err != nil {
		apiError(w, 502, "Could not send push notification")
		return
	}
	apiJSON(w, 200, map[string]bool{"sent": true})
}

func (a *App) enqueuePush(job pushJob) {
	job.queuedAt = time.Now()
	select {
	case <-a.shutdown:
		return
	default:
	}
	a.store.desktopNotifications.publish(a, job)
	var subscribed bool
	if err := a.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM web_push_subscriptions)`).Scan(&subscribed); err != nil || !subscribed {
		return
	}
	a.push.mu.Lock()
	defer a.push.mu.Unlock()
	if a.push.closed {
		return
	}
	if a.push.queue == nil {
		a.push.queue, a.push.done = make(chan pushJob, 128), make(chan struct{})
		var ctx context.Context
		ctx, a.push.cancel = context.WithCancel(context.Background())
		go func() {
			defer close(a.push.done)
			for {
				if ctx.Err() != nil {
					return
				}
				select {
				case <-ctx.Done():
					return
				case job := <-a.push.queue:
					a.deliverPushJob(ctx, job)
				}
			}
		}()
	}
	select {
	case a.push.queue <- job:
	default:
		log.Print("event=web_push_queue_full")
	}
}

func (a *App) closePush() {
	a.push.mu.Lock()
	a.push.closed = true
	if a.push.cancel != nil {
		a.push.cancel()
	}
	done := a.push.done
	a.push.mu.Unlock()
	if done != nil {
		<-done
	}
	if a.pushClient != nil {
		a.pushClient.CloseIdleConnections()
	}
}

func (a *App) pushJobCurrent(job pushJob) bool {
	if job.kind != "approval" {
		return true
	}
	a.liveMu.Lock()
	defer a.liveMu.Unlock()
	pending := a.approvals[job.approvalID]
	return pending != nil && !pending.decided && pending.chatID == job.chatID && pending.turnID == job.turnID
}

func (a *App) deliverPushJob(ctx context.Context, job pushJob) {
	if ctx.Err() != nil || (!job.queuedAt.IsZero() && time.Since(job.queuedAt) > 5*time.Minute) || !a.pushJobCurrent(job) {
		return
	}
	query := `SELECT ps.endpoint,ps.token_hash,ps.p256dh,ps.auth,ps.language,au.user_id FROM web_push_subscriptions ps JOIN auth_sessions au ON au.token_hash=ps.token_hash JOIN users u ON u.id=au.user_id `
	var args []any
	if job.isMessage() {
		if len(job.recipients) == 0 {
			return
		}
		query += `WHERE u.disabled=0 AND au.expires_at>? AND u.id<>? AND EXISTS(SELECT 1 FROM users sender WHERE sender.id=? AND sender.disabled=0) AND u.id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(job.recipients)), ",") + `)`
		args = []any{time.Now().UnixMilli(), job.messageFrom, job.messageFrom}
		for _, user := range job.recipients {
			args = append(args, user)
		}
	} else {
		query += `JOIN sessions s ON s.id=? WHERE u.disabled=0 AND au.expires_at>? AND (s.user_id=u.id OR EXISTS(SELECT 1 FROM chat_collaborators WHERE session_id=s.id AND user_id=u.id) OR EXISTS(SELECT 1 FROM turn_actors WHERE session_id=s.id AND turn_id=? AND user_id=u.id))`
		args = []any{job.chatID, time.Now().UnixMilli(), job.turnID}
	}
	rows, err := a.store.db.Query(query, args...)
	if err != nil {
		log.Print("event=web_push_subscriptions_failed")
		return
	}
	var subscriptions []pushSubscription
	for rows.Next() {
		var sub pushSubscription
		if err = rows.Scan(&sub.Endpoint, &sub.token, &sub.Keys.P256dh, &sub.Keys.Auth, &sub.language, &sub.user); err != nil {
			break
		}
		subscriptions = append(subscriptions, sub)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		log.Print("event=web_push_subscriptions_failed")
		return
	}
	for _, sub := range subscriptions {
		if ctx.Err() != nil || !a.pushJobCurrent(job) {
			return
		}
		if !a.canReceivePush(sub.user, job) {
			continue
		}
		if err := a.sendPush(ctx, sub, job); err != nil && ctx.Err() == nil {
			// Endpoints and provider errors can contain subscription credentials.
			log.Print("event=web_push_delivery_failed")
		}
	}
}

func (a *App) canReceivePush(user string, job pushJob) bool {
	if job.kind == "direct_message" {
		return user == job.messageTo && user != job.messageFrom
	}
	if job.kind == "group_message" {
		return a.directMessages.canNotifyGroup(job.messageFrom, user, job.messageTo)
	}
	return a.store.CanAccessChat(user, job.chatID) && (job.kind != "approval" || a.store.CanControlChat(user, job.chatID))
}

func pushDisplayName(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.Map(func(r rune) rune {
		// Names belong on one line, without hidden control/bidi formatting.
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, value)
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 80 {
		return string(runes[:77]) + "..."
	}
	return string(runes)
}

func (a *App) pushNotificationText(language string, job pushJob) (string, string) {
	title, message := "Spool - Task succeeded", "A task has finished. Open Spool to view it."
	var sender, titleName, bodyName string
	if job.isMessage() {
		_ = a.store.db.QueryRow(`SELECT nickname FROM users WHERE id=? AND disabled=0`, job.messageFrom).Scan(&sender)
		sender = pushDisplayName(sender)
	}
	switch job.kind {
	case "failed":
		title, message = "Spool - Task failed", "A task has failed. Open Spool to view it."
	case "approval":
		title, message = "Spool - Approval required", "A task needs your approval. Open Spool to respond."
	case "test":
		title, message = "Spool - Test notification", "Push notifications are working on this device."
	case "direct_message":
		title, message = "Spool - New private message", "You have a new private message. Open Spool to view it."
		if sender != "" {
			title, titleName = "Spool - Message from {name}", sender
		}
	case "group_message":
		group := a.directMessages.groupName(job.messageTo)
		if job.messageTo == onlineChatRoom {
			group = i18n.Translate(language, "Online group chat")
		} else if group == "Group chat" {
			group = i18n.Translate(language, "Group chat")
		}
		title, message = "Spool - New group message", "You have a new group message. Open Spool to view it."
		if group = pushDisplayName(group); group != "" {
			title, titleName = "Spool - Group: {name}", group
		}
		if sender != "" {
			message, bodyName = "{name} sent a message in this group. Open Spool to view it.", sender
		}
	}
	return i18n.Translate(language, title, "name", titleName), i18n.Translate(language, message, "name", bodyName)
}

func (a *App) sendPush(ctx context.Context, sub pushSubscription, job pushJob) error {
	var active bool
	err := a.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM web_push_subscriptions p JOIN auth_sessions a ON a.token_hash=p.token_hash JOIN users u ON u.id=a.user_id WHERE p.endpoint=? AND p.token_hash=? AND a.expires_at>? AND u.disabled=0)`, sub.Endpoint, sub.token, time.Now().UnixMilli()).Scan(&active)
	if err != nil || !active {
		return err
	}
	if !validPushSubscription(sub.Subscription) {
		return errors.New("invalid push subscription")
	}
	keys, err := a.store.webPushKeys()
	if err != nil {
		return err
	}
	title, message := a.pushNotificationText(sub.language, job)
	target := "/"
	tag := "spool-" + job.chatID + "-" + job.turnID + "-" + job.kind
	topic := ""
	if job.chatID != "" {
		target += "?chat=" + url.QueryEscape(job.chatID)
	}
	if job.isMessage() {
		peer := job.messageFrom
		if job.kind == "group_message" {
			peer = job.messageTo
		}
		target = "/?" + url.Values{"inbox": {peer}, "recipient": {sub.user}}.Encode()
		tag = "spool-message-" + peer
		hash := sha256.Sum256([]byte(tag))
		topic = base64.RawURLEncoding.EncodeToString(hash[:16])
	}
	payload, _ := json.Marshal(map[string]string{"title": title, "body": message, "url": target, "tag": tag})
	subject := a.settings.PublicURL
	if !strings.HasPrefix(subject, "https://") || strings.Contains(subject, "localhost") {
		subject = "https://github.com/LarchLiu/spool"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, err := webpush.SendNotificationWithContext(ctx, payload, &sub.Subscription, &webpush.Options{HTTPClient: a.pushClient, Subscriber: subject, VAPIDPublicKey: keys.public, VAPIDPrivateKey: keys.private, TTL: 300, Urgency: webpush.UrgencyNormal, Topic: topic})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		_, _ = a.store.db.Exec(`DELETE FROM web_push_subscriptions WHERE endpoint=? AND token_hash=? AND p256dh=? AND auth=?`, sub.Endpoint, sub.token, sub.Keys.P256dh, sub.Keys.Auth)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("push provider returned HTTP %d", response.StatusCode)
	}
	return nil
}
