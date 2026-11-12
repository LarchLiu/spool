package relay

import (
	"errors"
	"log"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const maxActivityEvents = 1000

type activityEvent struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Action   string    `json:"action"`
	TargetID string    `json:"target_id,omitempty"`
	Name     string    `json:"name,omitempty"`
	Result   string    `json:"result"`
}

// Registration changes and their successful activity events share one atomic
// snapshot. Never put credentials, hashes or request bodies in activity records.
// Caller holds Server.mu (or is loading a new, unpublished Server).
func (s *Server) registrationActivity(next map[string]registrationRecord) []activityEvent {
	activity := slices.Clone(s.activity)
	var events []activityEvent
	now := time.Now().UTC()
	add := func(action string, record registrationRecord) {
		events = append(events, activityEvent{ID: randomID(), Time: now, Action: action, TargetID: record.ID, Name: record.Name, Result: "success"})
	}
	for id, record := range next {
		previous, exists := s.registrations[id]
		if !exists {
			if record.Automatic {
				add("auto_registered", record)
			} else {
				add("generate", record)
			}
			continue
		}
		if previous.RevokedAt == nil && record.RevokedAt != nil {
			add("revoke", record)
		}
		if previous.UnregisteredAt == nil && record.UnregisteredAt != nil {
			add("unregister", record)
		}
		if previous.ServerID == "" && record.ServerID != "" {
			add("register", record)
		}
		// Credential recovery keeps the record and identity, but replaces
		// the token. Record the operation without retaining either hash.
		if previous.TokenHash != record.TokenHash {
			add("rotate", record)
		}
		if previous.WebBlocked != record.WebBlocked {
			if record.WebBlocked {
				add("disable", record)
			} else {
				add("allow", record)
			}
		}
	}
	for id, record := range s.registrations {
		if _, exists := next[id]; !exists {
			add("purge", record)
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TargetID < events[j].TargetID })
	activity = append(activity, events...)
	if len(activity) > maxActivityEvents {
		activity = activity[len(activity)-maxActivityEvents:]
	}
	return activity
}

type adminResponse struct {
	http.ResponseWriter
	status int
}

func (w *adminResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Well-formed authenticated operations that fail are also retained. A storage
// outage may prevent recording the failure; it never turns a failure into success.
func (s *Server) auditFailure(w *adminResponse, action string, ids []string) {
	if w.status < 400 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	activity := slices.Clone(s.activity)
	if len(ids) == 0 {
		ids = []string{""}
	}
	for _, id := range ids {
		activity = append(activity, activityEvent{ID: randomID(), Time: time.Now().UTC(), Action: action, TargetID: id, Name: s.registrations[id].Name, Result: "failed"})
	}
	if len(activity) > maxActivityEvents {
		activity = activity[len(activity)-maxActivityEvents:]
	}
	if err := s.saveRegistrySnapshot(s.registrations, activity); err != nil {
		log.Printf("relay activity storage unavailable: %v", err)
	}
}

type registrationSearch struct {
	Page       int      `json:"page"`
	PageSize   int      `json:"page_size"`
	Group      string   `json:"group"`
	Query      string   `json:"query"`
	Connection string   `json:"connection"`
	Web        string   `json:"web"`
	Selected   []string `json:"selected"`
	DetailID   string   `json:"detail_id"`
}

func registrationGroup(record Registration) string {
	if record.RevokedAt != nil || record.UnregisteredAt != nil {
		return "revoked"
	}
	if record.ServerID == "" {
		return "unused"
	}
	return "registered"
}

func (s *Server) searchRegistrations(input registrationSearch) (any, error) {
	if input.Page < 1 || input.PageSize < 1 || input.PageSize > 100 || len(input.Query) > 256 || !utf8.ValidString(input.Query) ||
		!slices.Contains([]string{"all", "registered", "unused", "revoked"}, input.Group) ||
		!slices.Contains([]string{"all", "online", "offline"}, input.Connection) ||
		!slices.Contains([]string{"all", "published", "unpublished", "blocked"}, input.Web) ||
		len(input.Selected) > 256 || slices.ContainsFunc(input.Selected, func(id string) bool { return !validToken(id) }) ||
		(input.DetailID != "" && !validToken(input.DetailID)) {
		return nil, errors.New("invalid registration search")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.registrationStatusesLocked("")
	counts := map[string]int{"all": len(list), "registered": 0, "unused": 0, "revoked": 0}
	metrics := map[string]int{"online": 0, "active": 0, "connections": 0}
	filtered := make([]registrationStatus, 0)
	selected := make([]registrationStatus, 0, len(input.Selected))
	selectedIDs := make(map[string]bool, len(input.Selected))
	for _, id := range input.Selected {
		selectedIDs[id] = true
	}
	var detail *registrationStatus
	query := strings.ToLower(strings.TrimSpace(input.Query))
	// The legacy list is oldest first. Page results are newest first, with a
	// stable ID tie-breaker so polling does not reshuffle equal timestamps.
	slices.Reverse(list)
	for _, entry := range list {
		group := registrationGroup(entry.Registration)
		counts[group]++
		if entry.Connected {
			metrics["online"]++
		}
		if group != "revoked" {
			metrics["active"]++
		}
		metrics["connections"] += entry.Connections
		if selectedIDs[entry.ID] {
			selected = append(selected, entry)
		}
		if entry.ID == input.DetailID {
			copy := entry
			detail = &copy
		}
		if input.Group != "all" && group != input.Group ||
			query != "" && !strings.Contains(strings.ToLower(entry.Name), query) && !strings.Contains(entry.ServerID, query) && !strings.Contains(strings.ToLower(entry.RegistrationIP), query) ||
			input.Connection == "online" && !entry.Connected || input.Connection == "offline" && (group != "registered" || entry.Connected) ||
			input.Web == "published" && entry.WebURL == "" || input.Web == "unpublished" && entry.WebURL != "" || input.Web == "blocked" && !entry.WebBlocked {
			continue
		}
		filtered = append(filtered, entry)
	}
	total := len(filtered)
	pages := max(1, (total+input.PageSize-1)/input.PageSize)
	page := min(input.Page, pages)
	start := (page - 1) * input.PageSize
	return map[string]any{"registrations": filtered[start:min(start+input.PageSize, total)], "page": page, "pages": pages, "total": total, "counts": counts, "metrics": metrics, "selected": selected, "detail": detail}, nil
}

func (s *Server) activityPage(page, size int) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	pages := max(1, (len(s.activity)+size-1)/size)
	page = min(page, pages)
	list := make([]activityEvent, len(s.activity))
	copy(list, s.activity)
	slices.Reverse(list)
	start := (page - 1) * size
	return map[string]any{"events": list[start:min(start+size, len(list))], "page": page, "pages": pages, "total": len(list)}
}
