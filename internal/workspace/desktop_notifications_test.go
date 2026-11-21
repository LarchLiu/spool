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

func TestDesktopNotificationsSuppressOnlyViewedForegroundSession(t *testing.T) {
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
	a.enqueuePush(pushJob{chatID: "viewed-chat", kind: "completed", turnID: "before-focus"})
	a.enqueuePush(pushJob{chatID: "other-chat", kind: "completed", turnID: "other-before-focus"})
	sub.SetView(true, "viewed-chat")
	if event := nextDesktopEvent(t, sub); event.job.turnID != "other-before-focus" {
		t.Fatal("focusing the viewed session discarded another session's notification")
	}
	for _, kind := range []string{"completed", "failed", "approval"} {
		a.enqueuePush(pushJob{chatID: "viewed-chat", kind: kind, turnID: "viewed-" + kind})
		a.enqueuePush(pushJob{chatID: "other-chat", kind: kind, turnID: "other-" + kind})
		if event := nextDesktopEvent(t, sub); event.job.turnID != "other-"+kind {
			t.Fatalf("foreground session notification not routed independently: %+v", event.job)
		}
	}
	if len(sub.Events()) != 0 || len(other.Events()) != 8 {
		t.Fatal("viewed session notified or another subscriber lost its events")
	}
	for _, kind := range []string{"direct_message", "group_message"} {
		a.enqueuePush(pushJob{kind: kind})
	}
	if len(sub.Events()) != 0 {
		t.Fatal("foreground message notifications changed their background-only policy")
	}
	sub.SetView(false, "viewed-chat")
	select {
	case <-sub.Events():
		t.Fatal("viewed foreground events replayed after blur")
	default:
	}
	a.enqueuePush(pushJob{chatID: "viewed-chat", kind: "completed", turnID: "after-blur"})
	if event := nextDesktopEvent(t, sub); event.job.turnID != "after-blur" {
		t.Fatal("background notification did not resume after blur")
	}
	sub.SetView(true, "other-chat")
	a.enqueuePush(pushJob{chatID: "viewed-chat", kind: "failed", turnID: "after-switch"})
	if event := nextDesktopEvent(t, sub); event.job.turnID != "after-switch" {
		t.Fatal("switching sessions did not allow the previous session to notify")
	}
	a.enqueuePush(pushJob{chatID: "viewed-chat", kind: "approval"})
	sub.SetView(true, "viewed-chat")
	if len(sub.Events()) != 0 {
		t.Fatal("viewing a session did not discard its pending notification")
	}
	sub.SetView(true, "")
	a.enqueuePush(pushJob{chatID: "viewed-chat", kind: "approval"})
	if len(sub.Events()) != 1 {
		t.Fatal("foreground settings or a new draft suppressed a session notification")
	}
	sub.Close()
	sub.SetView(true, "viewed-chat")
	sub.Close()
}

func TestDesktopNotificationsWakeOnEventsAndClose(t *testing.T) {
	s := testStore(t)
	if err := s.SaveDesktopNotificationsEnabled(true); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	sub := (&Service{Store: s}).SubscribeDesktopNotifications()
	t.Cleanup(sub.Close)
	select {
	case <-sub.Ready():
		t.Fatal("an idle subscription woke without an event")
	default:
	}
	// More than one UI batch must survive coalesced wakeups. Publishing must
	// also remain nonblocking when the event queue is full.
	for range 150 {
		a.enqueuePush(pushJob{kind: "completed", turnID: "queued"})
	}
	select {
	case <-sub.Ready():
	default:
		t.Fatal("queued events did not wake the native host")
	}
	if len(sub.Events()) != 128 {
		t.Fatalf("queue length = %d, want its bounded capacity", len(sub.Events()))
	}
	for len(sub.Events()) != 0 {
		nextDesktopEvent(t, sub)
	}
	select {
	case <-sub.Ready():
		t.Fatal("wakeups did not coalesce")
	default:
	}
	a.enqueuePush(pushJob{kind: "completed", turnID: "next"})
	select {
	case <-sub.Ready():
	default:
		t.Fatal("the next event did not wake an idle host")
	}
	sub.Close()
	if _, open := <-sub.Ready(); open {
		t.Fatal("closing the subscription did not stop the native watcher")
	}
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
	if note, ok := nextDesktopEvent(t, sub).Resolve(tokens["alice"], "en"); !ok || note.ChatID != chat.ID || note.Kind != "completed" {
		t.Fatal("session indicators depend on OS notification opt-in")
	}
	if err := s.SaveDesktopNotificationsEnabled(true); err != nil {
		t.Fatal(err)
	}
	a.enqueuePush(pushJob{chatID: chat.ID, turnID: "turn-12345678", kind: "completed"})
	event := nextDesktopEvent(t, sub)
	if _, err := s.db.Exec(`UPDATE sessions SET title=? WHERE id=?`, "修复通知标题", chat.ID); err != nil {
		t.Fatal(err)
	}
	note, ok := event.Resolve(tokens["alice"], "zh-CN")
	if !ok || note.UserID != alice.ID || note.URL != "/?chat="+chat.ID || note.Title != "Spool · 任务成功：修复通知标题" || note.ChatID != chat.ID || note.Kind != "completed" {
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
	if _, ok := event.Resolve(tokens["alice"], "en"); !ok {
		t.Fatal("disabling OS popups discarded a session indicator")
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
	if note.ChatID != "" || note.Kind != "" {
		t.Fatal("a message notification must not mark a session")
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
	if _, err := s.db.Exec(`UPDATE sessions SET title=? WHERE id=?`, "审核任务", chat.ID); err != nil {
		t.Fatal(err)
	}
	a := NewApp(s, &fakeRunner{}, "", "")
	t.Cleanup(func() { a.Close() })
	s.SaveDesktopNotificationsEnabled(true)
	token, _, _ := s.login("alice", "test-password-2026")
	event := DesktopNotificationEvent{a, pushJob{chatID: chat.ID, turnID: "turn-12345678", approvalID: "native-approval", kind: "approval", queuedAt: time.Now()}}
	a.approvals[event.job.approvalID] = &pendingApproval{chatID: chat.ID, turnID: event.job.turnID}
	if note, ok := event.Resolve(token, "zh-CN"); !ok || note.Title != "Spool · 需要审核：审核任务" || note.ChatID != chat.ID || note.Kind != "approval" {
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
	event.job.kind = "failed"
	if note, ok := event.Resolve(token, "en"); !ok || note.ChatID != chat.ID || note.Kind != "failed" {
		t.Fatal("failed task lost session notification metadata")
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
