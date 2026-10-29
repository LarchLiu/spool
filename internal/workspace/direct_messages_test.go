package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func readDirectMessage(t *testing.T, conn *websocket.Conn) directMessage {
	t.Helper()
	message := realtimeUntil(t, conn, func(m realtimeMessage) bool { return m.Type == "direct_message" })
	var value directMessage
	if err := json.Unmarshal(message.Data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func assertNoDirectMessageUntil(t *testing.T, conn *websocket.Conn, kind string) {
	t.Helper()
	realtimeUntil(t, conn, func(m realtimeMessage) bool {
		if m.Type == "direct_message" || m.Type == "group_message" {
			t.Fatal("private message leaked or was replayed", string(m.Data))
		}
		return m.Type == kind
	})
}

func readDirectError(t *testing.T, conn *websocket.Conn, request, code string) {
	t.Helper()
	message := realtimeUntil(t, conn, func(m realtimeMessage) bool { return m.Type == "direct_message_error" })
	var value directMessageError
	if err := json.Unmarshal(message.Data, &value); err != nil || value.RequestID != request || value.Code != code {
		t.Fatalf("error = %s, want request=%s code=%s: %v", message.Data, request, code, err)
	}
}

func TestRealtimeDirectMessagesArePrivateEphemeralAndNotStored(t *testing.T) {
	a, server, alice, _, cookie, csrf := realtimeFixture(t, func(a *App) { a.realtimePing = 25 * time.Millisecond })
	bob, charlie := testUser(t, a.store, "bob"), testUser(t, a.store, "charlie")
	bobCookie, bobCSRF := loginRequest(t, a, bob.Username)
	charlieCookie, charlieCSRF := loginRequest(t, a, charlie.Username)
	aliceFirst := realtimeConnect(t, server, cookie, csrf, true)
	realtimeReady(t, aliceFirst)
	aliceSecond := realtimeConnect(t, server, cookie, csrf, false)
	realtimeReady(t, aliceSecond)
	bobFirst := realtimeConnect(t, server, bobCookie, bobCSRF, true)
	realtimeReady(t, bobFirst)
	bobSecond := realtimeConnect(t, server, bobCookie, bobCSRF, false)
	realtimeReady(t, bobSecond)
	thirdParty := realtimeConnect(t, server, charlieCookie, charlieCSRF, true)
	realtimeReady(t, thirdParty)
	// The entire exchange must work with SQLite writes forbidden.
	if _, err := a.store.db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.store.db.Exec(`PRAGMA query_only=OFF`) })
	var before int
	if err := a.store.db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	realtimeWrite(t, aliceFirst, realtimeInput{Type: "direct_message", RequestID: "request-first", To: bob.ID, Text: "  hello, Bob <script>not HTML</script>  ", Visible: true})
	message := readDirectMessage(t, aliceFirst)
	if message.From != alice.ID || message.To != bob.ID || message.ID == "" || message.SentAt == 0 || message.Text != "hello, Bob <script>not HTML</script>" || message.RequestID != "request-first" {
		t.Fatalf("unexpected message: %#v", message)
	}
	for _, conn := range []*websocket.Conn{aliceSecond, bobFirst, bobSecond} {
		if got := readDirectMessage(t, conn); got != message {
			t.Fatalf("device received %#v, want %#v", got, message)
		}
	}
	assertNoDirectMessageUntil(t, thirdParty, "ping")
	realtimeWrite(t, bobFirst, realtimeInput{Type: "direct_message", RequestID: "request-reply", To: alice.ID, Text: strings.Repeat("你", maxDirectMessageCharacters), Visible: true})
	for _, conn := range []*websocket.Conn{bobFirst, bobSecond, aliceFirst, aliceSecond} {
		if got := readDirectMessage(t, conn); got.From != bob.ID || got.To != alice.ID || got.Text != strings.Repeat("你", maxDirectMessageCharacters) {
			t.Fatalf("invalid reply: %#v", got)
		}
	}
	var after int
	if err := a.store.db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatalf("chat wrote to SQLite: before=%d after=%d err=%v", before, after, err)
	}
	bobFirst.CloseNow()
	bobSecond.CloseNow()
	deadline := time.Now().Add(3 * time.Second)
	for {
		present := false
		for _, person := range a.presence.List(a.store) {
			present = present || person.ID == bob.ID
		}
		if !present {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Bob stayed online after closing both sockets")
		}
		time.Sleep(time.Millisecond)
	}
	realtimeWrite(t, aliceFirst, realtimeInput{Type: "direct_message", RequestID: "request-offline", To: bob.ID, Text: "not queued", Visible: true})
	readDirectError(t, aliceFirst, "request-offline", "offline")
	reconnected := realtimeConnect(t, server, bobCookie, bobCSRF, true)
	assertNoDirectMessageUntil(t, reconnected, "ready")
	assertNoDirectMessageUntil(t, reconnected, "ping")
	assertNoDirectMessageUntil(t, aliceSecond, "ping")
}

func TestRealtimeDirectMessageValidationAndRateLimit(t *testing.T) {
	a, server, alice, _, cookie, csrf := realtimeFixture(t)
	conn := realtimeConnect(t, server, cookie, csrf, true)
	realtimeReady(t, conn)
	inputs := []realtimeInput{
		{RequestID: "request-empty", To: "offline-user", Text: " \n "},
		{RequestID: "request-large", To: "offline-user", Text: strings.Repeat("🙂", maxDirectMessageCharacters+1)},
		{RequestID: "request-self", To: alice.ID, Text: "self"},
		{RequestID: "request-target", To: "../invalid", Text: "invalid target"},
		{RequestID: "bad", To: "offline-user", Text: "invalid request"},
	}
	for _, input := range inputs {
		input.Type, input.Visible = "direct_message", true
		realtimeWrite(t, conn, input)
		readDirectError(t, conn, input.RequestID, "invalid_message")
	}
	// Repeated errors must not be suppressed by snapshot deduplication.
	input := realtimeInput{Type: "direct_message", RequestID: "request-repeat", To: "offline-user", Text: "hello", Visible: true}
	for i := 0; i < 5; i++ {
		realtimeWrite(t, conn, input)
		readDirectError(t, conn, input.RequestID, "offline")
	}
	realtimeWrite(t, conn, input)
	readDirectError(t, conn, input.RequestID, "rate_limited")
	// Revoked recipients cannot receive messages even if still connected.
	bob := testUser(t, a.store, "bob")
	bobCookie, bobCSRF := loginRequest(t, a, bob.Username)
	bobConn := realtimeConnect(t, server, bobCookie, bobCSRF, true)
	realtimeReady(t, bobConn)
	if err := a.store.DeleteUser(bob.ID); err != nil {
		t.Fatal(err)
	}
	fresh := realtimeConnect(t, server, cookie, csrf, true)
	realtimeReady(t, fresh)
	realtimeWrite(t, fresh, realtimeInput{Type: "direct_message", RequestID: "request-disabled", To: bob.ID, Text: "secret", Visible: true})
	readDirectError(t, fresh, "request-disabled", "offline")
}

func TestDirectMessageHubDisconnectsSlowClientsAndHasNoReplay(t *testing.T) {
	var hub directMessageHub
	origin := hub.subscribe("alice")
	defer hub.unsubscribe(origin)
	otherDevice := hub.subscribe("alice")
	defer hub.unsubscribe(otherDevice)
	slow := hub.subscribe("bob")
	fast := hub.subscribe("bob")
	defer hub.unsubscribe(fast)
	thirdParty := hub.subscribe("charlie")
	defer hub.unsubscribe(thirdParty)
	message := directMessage{From: "alice", To: "bob", Text: "hello"}
	for i := 0; i < 65; i++ {
		if !hub.publish(origin, message) {
			t.Fatal("fast recipient did not accept message")
		}
		<-fast.messages
		<-otherDevice.messages
	}
	select {
	case <-slow.done:
	default:
		t.Fatal("slow recipient was not disconnected")
	}
	if len(origin.messages) != 0 || len(thirdParty.messages) != 0 {
		t.Fatal("message was duplicated or leaked")
	}
	hub.unsubscribe(slow)
	hub.unsubscribe(fast)
	if hub.publish(origin, message) {
		t.Fatal("offline message was accepted")
	}
	if len(otherDevice.messages) != 0 {
		t.Fatal("failed message was echoed to sender's other device")
	}
	reconnected := hub.subscribe("bob")
	defer hub.unsubscribe(reconnected)
	if len(reconnected.messages) != 0 {
		t.Fatal("hub retained message history")
	}
}

func TestRealtimeGroupMessagesReachEveryoneWithoutHistory(t *testing.T) {
	a, server, alice, _, cookie, csrf := realtimeFixture(t, func(a *App) { a.realtimePing = 25 * time.Millisecond })
	bob, charlie := testUser(t, a.store, "bob"), testUser(t, a.store, "charlie")
	bobCookie, bobCSRF := loginRequest(t, a, bob.Username)
	charlieCookie, charlieCSRF := loginRequest(t, a, charlie.Username)
	first := realtimeConnect(t, server, cookie, csrf, true)
	realtimeReady(t, first)
	second := realtimeConnect(t, server, cookie, csrf, false)
	realtimeReady(t, second)
	bobConn := realtimeConnect(t, server, bobCookie, bobCSRF, true)
	realtimeReady(t, bobConn)
	charlieConn := realtimeConnect(t, server, charlieCookie, charlieCSRF, false)
	realtimeReady(t, charlieConn)
	if _, err := a.store.db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.store.db.Exec(`PRAGMA query_only=OFF`) })
	readGroup := func(conn *websocket.Conn) directMessage {
		t.Helper()
		event := realtimeUntil(t, conn, func(m realtimeMessage) bool {
			if m.Type == "direct_message_error" {
				t.Fatal("group send failed", string(m.Data))
			}
			return m.Type == "group_message"
		})
		var message directMessage
		if err := json.Unmarshal(event.Data, &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	realtimeWrite(t, first, realtimeInput{Type: "group_message", RequestID: "group-request", To: onlineChatRoom, Text: "hello everyone", Visible: true})
	message := readGroup(first)
	if message.From != alice.ID || message.To != onlineChatRoom || message.Text != "hello everyone" || message.ID == "" {
		t.Fatalf("invalid group message: %#v", message)
	}
	for _, conn := range []*websocket.Conn{second, bobConn, charlieConn} {
		if got := readGroup(conn); got != message {
			t.Fatalf("group message differed: %#v", got)
		}
	}
	realtimeWrite(t, bobConn, realtimeInput{Type: "group_message", RequestID: "group-reply", To: onlineChatRoom, Text: "hello Alice", Visible: true})
	for _, conn := range []*websocket.Conn{first, second, bobConn, charlieConn} {
		if got := readGroup(conn); got.From != bob.ID || got.Text != "hello Alice" {
			t.Fatalf("invalid group reply: %#v", got)
		}
	}
	realtimeWrite(t, first, realtimeInput{Type: "direct_message", RequestID: "private-request", To: bob.ID, Text: "not for the group", Visible: true})
	for _, conn := range []*websocket.Conn{first, second, bobConn} {
		readDirectMessage(t, conn)
	}
	assertNoDirectMessageUntil(t, charlieConn, "ping")
	charlieConn.CloseNow()
	reconnected := realtimeConnect(t, server, charlieCookie, charlieCSRF, true)
	assertNoDirectMessageUntil(t, reconnected, "ready")
	assertNoDirectMessageUntil(t, reconnected, "ping")
	for _, input := range []realtimeInput{
		{Type: "direct_message", RequestID: "invalid-broadcast", To: onlineChatRoom, Text: "not a person"},
	} {
		realtimeWrite(t, first, input)
		readDirectError(t, first, input.RequestID, "invalid_message")
	}
}

func readGroupResult(t *testing.T, conn *websocket.Conn) peopleChatGroup {
	t.Helper()
	event := realtimeUntil(t, conn, func(m realtimeMessage) bool { return m.Type == "group_result" })
	var result struct {
		Group peopleChatGroup `json:"group"`
	}
	if err := json.Unmarshal(event.Data, &result); err != nil || result.Group.ID == "" {
		t.Fatalf("invalid group result: %s, %v", event.Data, err)
	}
	return result.Group
}

func TestRealtimeSelectedGroupsEnforceMembershipInvitesAndCleanup(t *testing.T) {
	// Leave enough time to read all four clients; artificially fast heartbeats
	// otherwise build up old pongs behind later test commands under -race.
	a, server, alice, _, cookie, csrf := realtimeFixture(t, func(a *App) { a.realtimePing = 500 * time.Millisecond })
	bob, charlie, david := testUser(t, a.store, "bob"), testUser(t, a.store, "charlie"), testUser(t, a.store, "david")
	first := realtimeConnect(t, server, cookie, csrf, true)
	realtimeReady(t, first)
	bobCookie, bobCSRF := loginRequest(t, a, bob.Username)
	bobConn := realtimeConnect(t, server, bobCookie, bobCSRF, true)
	realtimeReady(t, bobConn)
	charlieCookie, charlieCSRF := loginRequest(t, a, charlie.Username)
	charlieConn := realtimeConnect(t, server, charlieCookie, charlieCSRF, true)
	realtimeReady(t, charlieConn)
	davidCookie, davidCSRF := loginRequest(t, a, david.Username)
	davidConn := realtimeConnect(t, server, davidCookie, davidCSRF, true)
	realtimeReady(t, davidConn)
	if _, err := a.store.db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.store.db.Exec(`PRAGMA query_only=OFF`) })
	readGroupMessage := func(conn *websocket.Conn, text string) {
		t.Helper()
		event := realtimeUntil(t, conn, func(m realtimeMessage) bool {
			if m.Type == "direct_message_error" {
				t.Fatal("group send failed", string(m.Data))
			}
			return m.Type == "group_message"
		})
		var message directMessage
		if err := json.Unmarshal(event.Data, &message); err != nil || !message.Group || message.Text != text {
			t.Fatalf("unexpected group message: %s %v", event.Data, err)
		}
	}
	noPrivateGroup := func(conn *websocket.Conn) {
		t.Helper()
		realtimeUntil(t, conn, func(m realtimeMessage) bool {
			if m.Type == "chat_groups" || m.Type == "group_message" {
				t.Fatal("selected group leaked to a non-member", string(m.Data))
			}
			return m.Type == "ping"
		})
	}
	realtimeWrite(t, first, realtimeInput{Type: "group_create", RequestID: "create-pair", Name: "Alice and Bob", Members: []string{bob.ID, bob.ID}, Visible: true})
	group := readGroupResult(t, first)
	if group.Name != "Alice and Bob" || len(group.Members) != 2 || group.CreatedBy != alice.ID {
		t.Fatalf("invalid selected group: %#v", group)
	}
	realtimeUntil(t, bobConn, func(m realtimeMessage) bool {
		return m.Type == "chat_groups" && strings.Contains(string(m.Data), group.ID)
	})
	noPrivateGroup(charlieConn)
	noPrivateGroup(davidConn)
	realtimeWrite(t, first, realtimeInput{Type: "group_message", RequestID: "group-secret", To: group.ID, Text: "members only", Visible: true})
	readGroupMessage(first, "members only")
	readGroupMessage(bobConn, "members only")
	noPrivateGroup(charlieConn)
	realtimeWrite(t, charlieConn, realtimeInput{Type: "group_message", RequestID: "unauthorized-send", To: group.ID, Text: "intrusion", Visible: true})
	readDirectError(t, charlieConn, "unauthorized-send", "forbidden")
	realtimeWrite(t, charlieConn, realtimeInput{Type: "group_invite", RequestID: "unauthorized-invite", To: group.ID, Members: []string{david.ID}, Visible: true})
	readDirectError(t, charlieConn, "unauthorized-invite", "forbidden")
	realtimeWrite(t, first, realtimeInput{Type: "group_invite", RequestID: "invite-charlie", To: group.ID, Members: []string{charlie.ID}, Visible: true})
	updated := readGroupResult(t, first)
	if len(updated.Members) != 3 {
		t.Fatalf("invite did not add member: %#v", updated)
	}
	realtimeUntil(t, charlieConn, func(m realtimeMessage) bool {
		if m.Type == "group_message" {
			t.Fatal("new member received history")
		}
		return m.Type == "chat_groups" && strings.Contains(string(m.Data), group.ID)
	})
	assertNoDirectMessageUntil(t, charlieConn, "ping")
	realtimeWrite(t, bobConn, realtimeInput{Type: "group_message", RequestID: "welcome-charlie", To: group.ID, Text: "welcome", Visible: true})
	for _, conn := range []*websocket.Conn{first, bobConn, charlieConn} {
		readGroupMessage(conn, "welcome")
	}
	noPrivateGroup(davidConn)
	realtimeWrite(t, first, realtimeInput{Type: "group_create", RequestID: "create-other", Members: []string{david.ID}, Visible: true})
	other := readGroupResult(t, first)
	if other.ID == group.ID {
		t.Fatal("groups share identity")
	}
	realtimeUntil(t, davidConn, func(m realtimeMessage) bool {
		return m.Type == "chat_groups" && strings.Contains(string(m.Data), other.ID)
	})
	realtimeWrite(t, davidConn, realtimeInput{Type: "group_message", RequestID: "other-secret", To: other.ID, Text: "separate group", Visible: true})
	readGroupMessage(davidConn, "separate group")
	readGroupMessage(first, "separate group")
	assertNoDirectMessageUntil(t, bobConn, "ping")
	assertNoDirectMessageUntil(t, charlieConn, "ping")
	first.CloseNow()
	bobConn.CloseNow()
	charlieConn.CloseNow()
	deadline := time.Now().Add(3 * time.Second)
	for {
		a.directMessages.mu.Lock()
		_, exists := a.directMessages.groups[group.ID]
		a.directMessages.mu.Unlock()
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("empty group was retained")
		}
		time.Sleep(time.Millisecond)
	}
	if groups := a.directMessages.groupsFor(alice.ID); len(groups) != 1 || groups[0].ID != other.ID {
		t.Fatalf("wrong group lifecycle: %#v", groups)
	}
	aliceReconnected := realtimeConnect(t, server, cookie, csrf, true)
	realtimeUntil(t, aliceReconnected, func(m realtimeMessage) bool {
		if m.Type == "group_message" {
			t.Fatal("group history replayed on reconnect")
		}
		return m.Type == "chat_groups" && strings.Contains(string(m.Data), other.ID)
	})
	reconnected := realtimeConnect(t, server, bobCookie, bobCSRF, true)
	realtimeUntil(t, reconnected, func(m realtimeMessage) bool {
		if m.Type == "chat_groups" && string(m.Data) != "[]" {
			t.Fatal("closed group replayed")
		}
		if m.Type == "group_message" {
			t.Fatal("group history replayed")
		}
		return m.Type == "ready"
	})
}
