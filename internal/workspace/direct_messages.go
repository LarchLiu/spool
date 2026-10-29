package workspace

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	chatmodel "spool/internal/model"
)

const (
	maxDirectMessageCharacters = 2000
	onlineChatRoom             = "online-room"
)

type directMessage struct {
	ID        string `json:"id"`
	RequestID string `json:"request_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Text      string `json:"text"`
	SentAt    int64  `json:"sent_at"`
	Group     bool   `json:"group,omitempty"`
}

type peopleChatGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Members   []string `json:"members"`
	CreatedBy string   `json:"created_by"`
}

type directMessageError struct {
	RequestID string `json:"request_id"`
	Code      string `json:"code"`
}

type directMessageClient struct {
	user     string
	messages chan directMessage
	done     chan struct{}
	groups   chan struct{}
}

// The hub owns live queues and temporary group membership, never message history.
type directMessageHub struct {
	mu      sync.Mutex
	clients map[*directMessageClient]struct{}
	groups  map[string]peopleChatGroup
}

func (h *directMessageHub) subscribe(user string) *directMessageClient {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients == nil {
		h.clients = make(map[*directMessageClient]struct{})
	}
	c := &directMessageClient{user, make(chan directMessage, 64), make(chan struct{}), make(chan struct{}, 1)}
	h.clients[c] = struct{}{}
	return c
}

func (h *directMessageHub) removeLocked(c *directMessageClient) {
	if _, exists := h.clients[c]; exists {
		delete(h.clients, c)
		close(c.done)
	}
}

func (h *directMessageHub) unsubscribe(c *directMessageClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(c)
	for id, group := range h.groups {
		connected := false
		for client := range h.clients {
			if slices.Contains(group.Members, client.user) {
				connected = true
				break
			}
		}
		if !connected {
			delete(h.groups, id)
		}
	}
}

func (h *directMessageHub) enqueueLocked(c *directMessageClient, message directMessage) bool {
	select {
	case c.messages <- message:
		return true
	default:
		// Disconnect stalled consumers instead of silently dropping messages.
		h.removeLocked(c)
		return false
	}
}

func (h *directMessageHub) publish(origin *directMessageClient, message directMessage) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	delivered := false
	for client := range h.clients {
		if client.user == message.To && h.enqueueLocked(client, message) {
			delivered = true
		}
	}
	if delivered {
		for client := range h.clients {
			if client != origin && client.user == message.From {
				h.enqueueLocked(client, message)
			}
		}
	}
	return delivered
}

func validDirectMessage(input realtimeInput, sender string) bool {
	target := input.Type == "group_message" && chatmodel.ValidID(input.To) ||
		input.Type == "direct_message" && chatmodel.ValidID(input.To) && input.To != sender && input.To != onlineChatRoom
	return chatmodel.ValidID(input.RequestID) && target &&
		utf8.ValidString(input.Text) && utf8.RuneCountInString(input.Text) <= maxDirectMessageCharacters && strings.TrimSpace(input.Text) != ""
}

func (a *App) publishDirectMessage(origin *directMessageClient, input realtimeInput) (directMessage, string) {
	if !validDirectMessage(input, origin.user) {
		return directMessage{}, "invalid_message"
	}
	// Presence excludes expired/disabled identities, including clients whose
	// auth-change notification has not yet been processed by their socket.
	if input.Type != "group_message" {
		online := false
		for _, person := range a.presence.List(a.store) {
			if person.ID == input.To {
				online = true
				break
			}
		}
		if !online {
			return directMessage{}, "offline"
		}
	}
	message := directMessage{
		ID: chatmodel.NewID(), RequestID: input.RequestID, From: origin.user,
		To: input.To, Text: strings.TrimSpace(input.Text), SentAt: time.Now().UnixMilli(),
		Group: input.Type == "group_message",
	}
	if message.Group {
		return message, a.directMessages.publishGroup(origin, message)
	}
	if !a.directMessages.publish(origin, message) {
		return directMessage{}, "offline"
	}
	return message, ""
}

func (m directMessage) eventType() string {
	if m.Group {
		return "group_message"
	}
	return "direct_message"
}

func (h *directMessageHub) publishGroup(origin *directMessageClient, message directMessage) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var members []string
	if message.To != onlineChatRoom {
		group, exists := h.groups[message.To]
		if !exists || !slices.Contains(group.Members, origin.user) {
			return "forbidden"
		}
		members = group.Members
	}
	for client := range h.clients {
		if client != origin && (message.To == onlineChatRoom || slices.Contains(members, client.user)) {
			h.enqueueLocked(client, message)
		}
	}
	return ""
}

func (h *directMessageHub) groupsFor(user string) []peopleChatGroup {
	h.mu.Lock()
	defer h.mu.Unlock()
	groups := []peopleChatGroup{}
	for _, group := range h.groups {
		if slices.Contains(group.Members, user) {
			group.Members = slices.Clone(group.Members)
			groups = append(groups, group)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	return groups
}

func (a *App) changePeopleChatGroup(origin *directMessageClient, input realtimeInput) (peopleChatGroup, string) {
	if !chatmodel.ValidID(input.RequestID) || len(input.Members) == 0 || len(input.Members) > 31 ||
		utf8.RuneCountInString(input.Name) > 80 {
		return peopleChatGroup{}, "invalid_group"
	}
	online := map[string]bool{}
	for _, person := range a.presence.List(a.store) {
		online[person.ID] = true
	}
	h := &a.directMessages
	h.mu.Lock()
	defer h.mu.Unlock()
	group := peopleChatGroup{ID: "group-" + chatmodel.NewID(), Name: strings.TrimSpace(input.Name), Members: []string{origin.user}, CreatedBy: origin.user}
	if input.Type == "group_invite" {
		var exists bool
		group, exists = h.groups[input.To]
		if !exists || !slices.Contains(group.Members, origin.user) {
			return peopleChatGroup{}, "forbidden"
		}
		group.Members = slices.Clone(group.Members)
	} else if len(h.groups) >= 256 {
		return peopleChatGroup{}, "group_limit"
	}
	for _, user := range input.Members {
		if !chatmodel.ValidID(user) {
			return peopleChatGroup{}, "invalid_group"
		}
		if slices.Contains(group.Members, user) {
			continue
		}
		connected := false
		for client := range h.clients {
			if client.user == user {
				connected = true
				break
			}
		}
		if !online[user] || !connected {
			return peopleChatGroup{}, "offline"
		}
		group.Members = append(group.Members, user)
	}
	if len(group.Members) < 2 || len(group.Members) > 32 {
		return peopleChatGroup{}, "invalid_group"
	}
	if group.Name == "" {
		group.Name = "Group chat"
	}
	if h.groups == nil {
		h.groups = make(map[string]peopleChatGroup)
	}
	h.groups[group.ID] = group
	for client := range h.clients {
		select {
		case client.groups <- struct{}{}:
		default:
		}
	}
	group.Members = slices.Clone(group.Members)
	return group, ""
}
