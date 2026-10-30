package workspace

import (
	"context"
	"crypto/ecdh"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type messagePushRecord struct {
	endpoint string
	topic    string
	payload  map[string]string
}

type messagePushFixture struct {
	app        *App
	users      map[string]User
	identities map[string]authIdentity
	clients    map[string]*directMessageClient
	subs       map[string]webpush.Subscription
	private    map[string]*ecdh.PrivateKey
	records    []messagePushRecord
}

func newMessagePushFixture(t *testing.T) *messagePushFixture {
	t.Helper()
	s := testStore(t)
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	// Capture production hook jobs without racing the dispatcher; deliver each
	// explicitly to exercise permission changes between admission and sending.
	a.push.queue = make(chan pushJob, 16)
	f := &messagePushFixture{app: a, users: map[string]User{}, identities: map[string]authIdentity{}, clients: map[string]*directMessageClient{}, subs: map[string]webpush.Subscription{}, private: map[string]*ecdh.PrivateKey{}}
	for _, name := range []string{"alice", "bob", "carol", "david"} {
		u := testUser(t, s, name)
		token, identity, err := s.login(name, "test-password-2026")
		if err != nil {
			t.Fatal(err)
		}
		a.presence.Touch(identity, "message-push-test", false)
		client := a.directMessages.subscribe(u.ID)
		t.Cleanup(func() { a.directMessages.unsubscribe(client) })
		sub, private := testPushSubscription(t, name)
		subscribePush(t, a, &http.Cookie{Name: authCookie, Value: token}, identity.CSRF, sub, "en")
		f.users[name], f.identities[name], f.clients[name], f.subs[name], f.private[sub.Endpoint] = u, identity, client, sub, private
	}
	a.pushClient = &http.Client{Transport: pushTransport(func(r *http.Request) (*http.Response, error) {
		var sub webpush.Subscription
		for _, v := range f.subs {
			if v.Endpoint == r.URL.String() {
				sub = v
			}
		}
		if sub.Endpoint == "" {
			t.Fatal("push leaked to an unknown subscription")
		}
		payload := decryptPush(t, r, f.private[sub.Endpoint], sub)
		f.records = append(f.records, messagePushRecord{r.URL.String(), r.Header.Get("Topic"), payload})
		return pushResponse(201), nil
	})}
	return f
}

func (f *messagePushFixture) nextJob(t *testing.T) pushJob {
	t.Helper()
	select {
	case job := <-f.app.push.queue:
		return job
	default:
		t.Fatal("successful message did not enqueue a notification")
		return pushJob{}
	}
}

func (f *messagePushFixture) expectNoJob(t *testing.T) {
	t.Helper()
	select {
	case <-f.app.push.queue:
		t.Fatal("rejected or undelivered message enqueued a notification")
	default:
	}
}

func TestMessagePushPrivateRecipientsPrivacyAndMultiDevice(t *testing.T) {
	f := newMessagePushFixture(t)
	token, identity, err := f.app.store.login("bob", "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	secondSub, private := testPushSubscription(t, "bob-phone")
	subscribePush(t, f.app, &http.Cookie{Name: authCookie, Value: token}, identity.CSRF, secondSub, "en")
	f.subs["bob-phone"], f.private[secondSub.Endpoint] = secondSub, private
	second := f.app.directMessages.subscribe(f.users["bob"].ID)
	t.Cleanup(func() { f.app.directMessages.unsubscribe(second) })
	message, code := f.app.publishDirectMessage(f.clients["alice"], realtimeInput{Type: "direct_message", RequestID: "private-request", To: f.users["bob"].ID, Text: "private message body must not appear"})
	if code != "" {
		t.Fatalf("send private message: %s", code)
	}
	job := f.nextJob(t)
	if !reflect.DeepEqual(job.recipients, []string{f.users["bob"].ID}) || job.kind != "direct_message" {
		t.Fatalf("unexpected private recipients: %+v", job)
	}
	f.app.deliverPushJob(context.Background(), job)
	if len(f.records) != 2 {
		t.Fatalf("expected one notification per recipient device, got %d", len(f.records))
	}
	for _, record := range f.records {
		u, _ := url.Parse(record.payload["url"])
		if u.Query().Get("inbox") != message.From || u.Query().Get("recipient") != f.users["bob"].ID || record.payload["body"] != "You have a new private message. Open Spool to view it." || record.payload["title"] != "Spool - Message from alice" {
			t.Fatalf("bad private notification: %v", record.payload)
		}
		if record.endpoint != f.subs["bob"].Endpoint && record.endpoint != secondSub.Endpoint {
			t.Fatal("sender or third party received private notification")
		}
		body, _ := json.Marshal(record.payload)
		if strings.Contains(string(body), message.Text) || strings.Contains(string(body), "bob") {
			t.Fatal("message text or recipient name leaked onto lock screen")
		}
		if len(record.topic) != 22 || record.payload["tag"] != "spool-message-"+message.From {
			t.Fatal("private messages do not coalesce by conversation")
		}
	}
	f.expectNoJob(t)
}

func TestMessagePushIgnoresRejectedOfflineAndStalledMessages(t *testing.T) {
	f := newMessagePushFixture(t)
	input := realtimeInput{Type: "direct_message", RequestID: "rejected-request", To: f.users["bob"].ID, Text: ""}
	if _, code := f.app.publishDirectMessage(f.clients["alice"], input); code != "invalid_message" {
		t.Fatalf("invalid message accepted: %s", code)
	}
	f.expectNoJob(t)
	input.Text = "hello"
	f.app.presence.Remove(f.identities["bob"].TokenHash)
	if _, code := f.app.publishDirectMessage(f.clients["alice"], input); code != "offline" {
		t.Fatalf("offline message accepted: %s", code)
	}
	f.expectNoJob(t)
	f.app.presence.Touch(f.identities["bob"], "message-push-test", false)
	for len(f.clients["bob"].messages) < cap(f.clients["bob"].messages) {
		f.clients["bob"].messages <- directMessage{}
	}
	if _, code := f.app.publishDirectMessage(f.clients["alice"], input); code != "offline" {
		t.Fatalf("stalled recipient accepted message: %s", code)
	}
	f.expectNoJob(t)
}

func TestMessagePushSelectedGroupFreezesDeliveredMembers(t *testing.T) {
	f := newMessagePushFixture(t)
	group, code := f.app.changePeopleChatGroup(f.clients["alice"], realtimeInput{Type: "group_create", RequestID: "create-private-group", Name: "Launch team", Members: []string{f.users["bob"].ID}})
	if code != "" {
		t.Fatalf("create group: %s", code)
	}
	second := f.app.directMessages.subscribe(f.users["bob"].ID)
	t.Cleanup(func() { f.app.directMessages.unsubscribe(second) })
	input := realtimeInput{Type: "group_message", RequestID: "group-request", To: group.ID, Text: "secret group text"}
	if _, code := f.app.publishDirectMessage(f.clients["carol"], input); code != "forbidden" {
		t.Fatal("non-member could send to selected group")
	}
	f.expectNoJob(t)
	if _, code := f.app.publishDirectMessage(f.clients["alice"], input); code != "" {
		t.Fatalf("group send failed: %s", code)
	}
	job := f.nextJob(t)
	if !reflect.DeepEqual(job.recipients, []string{f.users["bob"].ID}) {
		t.Fatalf("sender, outsider or duplicate socket in group push: %v", job.recipients)
	}
	// A new member must not learn that earlier messages were sent.
	if _, code := f.app.changePeopleChatGroup(f.clients["alice"], realtimeInput{Type: "group_invite", RequestID: "invite-after-send", To: group.ID, Members: []string{f.users["carol"].ID}}); code != "" {
		t.Fatal(code)
	}
	f.app.deliverPushJob(context.Background(), job)
	if len(f.records) != 1 || f.records[0].endpoint != f.subs["bob"].Endpoint {
		t.Fatalf("late invitee or nonmember was notified: %v", f.records)
	}
	u, _ := url.Parse(f.records[0].payload["url"])
	if u.Query().Get("inbox") != group.ID || u.Query().Get("recipient") != f.users["bob"].ID || f.records[0].payload["title"] != "Spool - Group: Launch team" || f.records[0].payload["body"] != "alice sent a message in this group. Open Spool to view it." {
		t.Fatalf("group notification did not link to selected group: %v", f.records[0].payload)
	}
	if strings.Contains(f.records[0].payload["body"], input.Text) {
		t.Fatal("group message text leaked onto lock screen")
	}
	f.records = nil
	f.app.directMessages.mu.Lock()
	delete(f.app.directMessages.groups, group.ID)
	f.app.directMessages.mu.Unlock()
	f.app.deliverPushJob(context.Background(), job)
	if len(f.records) != 0 {
		t.Fatal("closed group was notified")
	}
}

func TestMessagePushOnlineRoomSnapshotAndRevokedSessions(t *testing.T) {
	f := newMessagePushFixture(t)
	// David is not connected when the message is accepted.
	f.app.directMessages.unsubscribe(f.clients["david"])
	_, code := f.app.publishDirectMessage(f.clients["alice"], realtimeInput{Type: "group_message", RequestID: "online-group-request", To: onlineChatRoom, Text: "online room text"})
	if code != "" {
		t.Fatal(code)
	}
	job := f.nextJob(t)
	want := []string{f.users["bob"].ID, f.users["carol"].ID}
	slices.Sort(want)
	if !reflect.DeepEqual(job.recipients, want) {
		t.Fatalf("online room snapshot wrong: %v", job.recipients)
	}
	reconnected := f.app.directMessages.subscribe(f.users["david"].ID)
	t.Cleanup(func() { f.app.directMessages.unsubscribe(reconnected) })
	f.app.store.db.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, f.users["bob"].ID)
	f.app.store.db.Exec(`UPDATE auth_sessions SET expires_at=0 WHERE user_id=?`, f.users["carol"].ID)
	f.app.deliverPushJob(context.Background(), job)
	if len(f.records) != 0 {
		t.Fatal("late joiner or revoked session received online group push")
	}
	f.app.store.db.Exec(`UPDATE auth_sessions SET expires_at=? WHERE user_id=?`, job.queuedAt.AddDate(0, 0, 1).UnixMilli(), f.users["carol"].ID)
	f.app.deliverPushJob(context.Background(), job)
	if len(f.records) != 1 || f.records[0].endpoint != f.subs["carol"].Endpoint {
		t.Fatal("valid online room recipient did not receive push")
	}
	u, _ := url.Parse(f.records[0].payload["url"])
	if u.Query().Get("inbox") != onlineChatRoom || f.records[0].payload["title"] != "Spool - Group: Online group chat" {
		t.Fatal("online group notification linked to wrong conversation")
	}
	f.records = nil
	f.app.store.db.Exec(`UPDATE users SET disabled=1 WHERE id=?`, f.users["carol"].ID)
	f.app.deliverPushJob(context.Background(), job)
	if len(f.records) != 0 {
		t.Fatal("disabled recipient received group push")
	}
}

func TestMessagePushNamesAndLocales(t *testing.T) {
	f := newMessagePushFixture(t)
	if _, err := f.app.store.db.Exec(`UPDATE users SET nickname=? WHERE id=?`, "Alice {name}", f.users["alice"].ID); err != nil {
		t.Fatal(err)
	}
	group, code := f.app.changePeopleChatGroup(f.clients["alice"], realtimeInput{Type: "group_create", RequestID: "localized-push-group", Name: "Launch {name}", Members: []string{f.users["bob"].ID}})
	if code != "" {
		t.Fatal(code)
	}
	for _, test := range []struct {
		language, kind, target, title, body string
	}{
		{"en", "direct_message", f.users["bob"].ID, "Spool - Message from Alice {name}", "You have a new private message. Open Spool to view it."},
		{"zh-CN", "direct_message", f.users["bob"].ID, "Spool · Alice {name} 发来消息", "你收到一条新私聊消息，打开 Spool 查看。"},
		{"ja", "direct_message", f.users["bob"].ID, "Spool · Alice {name} からのメッセージ", "新しい個人メッセージがあります。Spool を開いて確認してください。"},
		{"en", "group_message", group.ID, "Spool - Group: Launch {name}", "Alice {name} sent a message in this group. Open Spool to view it."},
		{"zh-CN", "group_message", group.ID, "Spool · 群聊：Launch {name}", "Alice {name} 在群内发送了消息，打开 Spool 查看。"},
		{"ja", "group_message", group.ID, "Spool · グループ：Launch {name}", "Alice {name} がグループにメッセージを送信しました。Spool を開いて確認してください。"},
		{"zh-CN", "group_message", onlineChatRoom, "Spool · 群聊：在线群聊", "Alice {name} 在群内发送了消息，打开 Spool 查看。"},
	} {
		t.Run(test.language+"/"+test.target, func(t *testing.T) {
			title, body := f.app.pushNotificationText(test.language, pushJob{kind: test.kind, messageFrom: f.users["alice"].ID, messageTo: test.target})
			if title != test.title || body != test.body {
				t.Fatalf("unexpected named message notification: %q %q", title, body)
			}
		})
	}
	// Empty or unavailable labels use generic text, never account login names or IDs.
	if _, err := f.app.store.db.Exec(`UPDATE users SET nickname='' WHERE id=?`, f.users["alice"].ID); err != nil {
		t.Fatal(err)
	}
	title, _ := f.app.pushNotificationText("en", pushJob{kind: "direct_message", messageFrom: f.users["alice"].ID})
	if title != "Spool - New private message" {
		t.Fatal("empty sender label did not fall back to generic private title")
	}
	title, body := f.app.pushNotificationText("en", pushJob{kind: "group_message", messageFrom: f.users["alice"].ID, messageTo: "unknown-group-123"})
	if title != "Spool - New group message" || body != "You have a new group message. Open Spool to view it." {
		t.Fatal("unavailable labels did not fall back to generic group text")
	}
	f.app.directMessages.mu.Lock()
	group.Name = "Group chat"
	f.app.directMessages.groups[group.ID] = group
	f.app.directMessages.mu.Unlock()
	title, _ = f.app.pushNotificationText("zh-CN", pushJob{kind: "group_message", messageTo: group.ID})
	if title != "Spool · 群聊：群聊" {
		t.Fatalf("default group name was not translated: %q", title)
	}
}
