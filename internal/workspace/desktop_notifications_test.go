package workspace

import (
	"net/url"
	"strings"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func nextDesktopEvent(t *testing.T, sub *DesktopNotificationSubscription) DesktopNotificationEvent {
	t.Helper()
	select {
	case event := <-sub.Events():
		return event
	default:
		t.Fatal("no desktop notification event")
		return DesktopNotificationEvent{}
	}
}

func TestDesktopNotificationsDefaultOffAndPersist(t *testing.T) {
	root := t.TempDir()
	s, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := s.DesktopNotificationsEnabled()
	if err != nil || enabled {
		t.Fatal("new desktop silently opted in")
	}
	if err := s.SaveDesktopNotificationsEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if enabled, err = s.DesktopNotificationsEnabled(); err != nil || !enabled {
		t.Fatal("desktop opt-in was not persisted")
	}
}

func TestDesktopNotificationsDropForegroundEventsWithoutReplay(t *testing.T) {
	s := testStore(t)
	if err := s.SaveDesktopNotificationsEnabled(true); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	service := &Service{Store: s}
	sub, other := service.SubscribeDesktopNotifications(), service.SubscribeDesktopNotifications()
	t.Cleanup(sub.Close)
	t.Cleanup(other.Close)
	a.enqueuePush(pushJob{kind: "completed", turnID: "before-focus"})
	sub.SetFocused(true)
	for range 150 {
		a.enqueuePush(pushJob{kind: "completed", turnID: "while-focused"})
	}
	if len(sub.Events()) != 0 || len(other.Events()) == 0 {
		t.Fatal("foreground events queued or another subscriber was suppressed")
	}
	sub.SetFocused(false)
	select {
	case <-sub.Events():
		t.Fatal("foreground events replayed after blur")
	default:
	}
	a.enqueuePush(pushJob{kind: "completed", turnID: "after-blur"})
	if event := nextDesktopEvent(t, sub); event.job.turnID != "after-blur" {
		t.Fatal("background notification did not resume after blur")
	}
	sub.Close()
	sub.SetFocused(true)
	sub.Close()
}

func TestDesktopTaskNotificationsWithoutPushSubscriptions(t *testing.T) {
	s := testStore(t)
	alice, bob, carol := testUser(t, s, "alice"), testUser(t, s, "bob"), testUser(t, s, "carol")
	p := testProject(t, s, alice, bob, carol)
	p.SharedChats = true
	if _, err := s.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	chat, _ := s.CreateChat(alice.ID, p.ID, chatmodel.BackendCodex)
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	sub := (&Service{Store: s}).SubscribeDesktopNotifications()
	t.Cleanup(sub.Close)
	tokens := map[string]string{}
	for _, name := range []string{"alice", "bob", "carol"} {
		token, _, err := s.login(name, "test-password-2026")
		if err != nil {
			t.Fatal(err)
		}
		tokens[name] = token
	}
	a.enqueuePush(pushJob{chatID: chat.ID, turnID: "turn-12345678", kind: "completed"})
	select {
	case <-sub.Events():
		t.Fatal("disabled desktop queued a notification")
	default:
	}
	if err := s.SaveDesktopNotificationsEnabled(true); err != nil {
		t.Fatal(err)
	}
	a.enqueuePush(pushJob{chatID: chat.ID, turnID: "turn-12345678", kind: "completed"})
	event := nextDesktopEvent(t, sub)
	note, ok := event.Resolve(tokens["alice"], "zh-CN")
	if !ok || note.UserID != alice.ID || note.URL != "/?chat="+chat.ID || note.Title != "Spool · 任务成功" {
		t.Fatalf("bad task notification: %+v %v", note, ok)
	}
	for _, token := range []string{"", "invalid-token", tokens["bob"], tokens["carol"]} {
		if _, ok := event.Resolve(token, "en"); ok {
			t.Fatal("invalid session or project-only member received native notification")
		}
	}
	if _, err := s.db.Exec(`INSERT INTO chat_collaborators VALUES(?,?)`, chat.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := event.Resolve(tokens["bob"], "en"); !ok {
		t.Fatal("collaborator did not receive native notification")
	}
	if _, err := s.db.Exec(`DELETE FROM project_members WHERE project_id=? AND user_id=?`, p.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := event.Resolve(tokens["bob"], "en"); ok {
		t.Fatal("revoked native recipient still notified")
	}
	s.SaveDesktopNotificationsEnabled(false)
	if _, ok := event.Resolve(tokens["alice"], "en"); ok {
		t.Fatal("queued notification survived disable")
	}
	s.SaveDesktopNotificationsEnabled(true)
	s.db.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, alice.ID)
	if _, ok := event.Resolve(tokens["alice"], "en"); ok {
		t.Fatal("signed-out native session still notified")
	}
}

func TestDesktopMessageNotificationsFollowDeliveredRecipients(t *testing.T) {
	f := newMessagePushFixture(t)
	s := f.app.store
	if err := s.SaveDesktopNotificationsEnabled(true); err != nil {
		t.Fatal(err)
	}
	sub := (&Service{Store: s}).SubscribeDesktopNotifications()
	t.Cleanup(sub.Close)
	// Native notifications work independently of any Web Push provider opt-in.
	s.db.Exec(`DELETE FROM web_push_subscriptions`)
	token, _, err := s.login("bob", "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	senderToken, _, _ := s.login("alice", "test-password-2026")
	message, code := f.app.publishDirectMessage(f.clients["alice"], realtimeInput{Type: "direct_message", RequestID: "native-message", To: f.users["bob"].ID, Text: "private desktop body"})
	if code != "" {
		t.Fatal(code)
	}
	event := nextDesktopEvent(t, sub)
	note, ok := event.Resolve(token, "en")
	u, _ := url.Parse(note.URL)
	if !ok || note.Title != "Spool - Message from alice" || strings.Contains(note.Body, message.Text) || u.Query().Get("inbox") != message.From || u.Query().Get("recipient") != f.users["bob"].ID {
		t.Fatalf("bad private native notification: %+v %v", note, ok)
	}
	if _, ok := event.Resolve(senderToken, "en"); ok {
		t.Fatal("sender received own message notification")
	}
	group, code := f.app.changePeopleChatGroup(f.clients["alice"], realtimeInput{Type: "group_create", RequestID: "native-group", Name: "Native team", Members: []string{f.users["bob"].ID}})
	if code != "" {
		t.Fatal(code)
	}
	if _, code := f.app.publishDirectMessage(f.clients["alice"], realtimeInput{Type: "group_message", RequestID: "native-group-msg", To: group.ID, Text: "private group body"}); code != "" {
		t.Fatal(code)
	}
	event = nextDesktopEvent(t, sub)
	if note, ok = event.Resolve(token, "en"); !ok || note.Title != "Spool - Group: Native team" || !strings.Contains(note.Body, "alice") || strings.Contains(note.Body, "private group body") {
		t.Fatal("native group metadata/content incorrect")
	}
	f.app.directMessages.mu.Lock()
	delete(f.app.directMessages.groups, group.ID)
	f.app.directMessages.mu.Unlock()
	if _, ok := event.Resolve(token, "en"); ok {
		t.Fatal("closed native group notified")
	}
}

func TestDesktopNotificationLifetimeAndApprovalChecks(t *testing.T) {
	s := testStore(t)
	u := testUser(t, s, "alice")
	p := testProject(t, s, u)
	chat, _ := s.CreateChat(u.ID, p.ID, chatmodel.BackendCodex)
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	s.SaveDesktopNotificationsEnabled(true)
	token, _, _ := s.login("alice", "test-password-2026")
	event := DesktopNotificationEvent{a, pushJob{chatID: chat.ID, turnID: "turn-12345678", approvalID: "native-approval", kind: "approval", queuedAt: time.Now()}}
	a.approvals[event.job.approvalID] = &pendingApproval{chatID: chat.ID, turnID: event.job.turnID}
	if note, ok := event.Resolve(token, "zh-CN"); !ok || note.Title != "Spool · 需要审核" {
		t.Fatal("native approval not delivered")
	}
	if _, err := s.db.Exec(`INSERT INTO project_member_own_access VALUES(?,?,'read-only') ON CONFLICT(project_id,user_id) DO UPDATE SET access='read-only'`, p.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := event.Resolve(token, "en"); ok {
		t.Fatal("read-only native account received approval")
	}
	s.db.Exec(`DELETE FROM project_member_own_access`)
	a.approvals[event.job.approvalID].decided = true
	if _, ok := event.Resolve(token, "en"); ok {
		t.Fatal("resolved native approval delivered")
	}
	event.job.kind, event.job.queuedAt = "failed", time.Now().Add(-6*time.Minute)
	if _, ok := event.Resolve(token, "en"); ok {
		t.Fatal("stale native event delivered")
	}
	event.job.queuedAt = time.Now()
	a.Close()
	if _, ok := event.Resolve(token, "en"); ok {
		t.Fatal("stopped service delivered native event")
	}
}
